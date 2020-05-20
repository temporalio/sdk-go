// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"errors"
	"fmt"
	"reflect"

	commonpb "go.temporal.io/temporal-proto/common"
	failurepb "go.temporal.io/temporal-proto/failure"
)

/*
Below are the possible cases that activity could fail:
1) *ApplicationError: (this should be the most common one)
	If activity implementation returns *ApplicationError by using NewApplicationError() API, workflow code would receive *ApplicationError.
	The err would contain a Reason and Details. The reason is what activity specified to NewApplicationError(), which workflow
	code could check to determine what kind of error it was and take actions based on the reason. The details is encoded
	[]byte which workflow code could extract strong typed data. Workflow code needs to know what the types of the encoded
	details are before extracting them.
2) *GenericError:
	If activity implementation returns errors other than from NewApplicationError() API, workflow code would receive *GenericError.
	Use err.Error() to get the string representation of the actual error.
3) *CanceledError:
	If activity was canceled, workflow code will receive instance of *CanceledError. When activity cancels itself by
	returning NewCancelError() it would supply optional details which could be extracted by workflow code.
4) *TimeoutError:
	If activity was timed out (several timeout types), workflow code will receive instance of *TimeoutError. The err contains
	details about what type of timeout it was.
5) *PanicError:
	If activity code panic while executing, temporal activity worker will report it as activity failure to temporal server.
	The temporal client library will present that failure as *PanicError to workflow code. The err contains a string
	representation of the panic message and the call stack when panic was happen.

Workflow code could handle errors based on different types of error. Below is sample code of how error handling looks like.

_, err := workflow.ExecuteActivity(ctx, MyActivity, ...).Get(nil)
if err != nil {
	switch err := err.(type) {
	case *workflow.ApplicationError:
		// handle activity errors (created via NewApplicationError() API)
		switch err.Reason() {
		case CustomErrReasonA: // assume CustomErrReasonA is constant defined by activity implementation
			var detailMsg string // assuming activity return error by NewApplicationError(CustomErrReasonA, "string details")
			err.Details(&detailMsg) // extract strong typed details (corresponding to CustomErrReasonA)
			// handle CustomErrReasonA
		case CustomErrReasonB:
			// handle CustomErrReasonB
		default:
			// newer version of activity could return new errors that workflow was not aware of.
		}
	case *workflow.GenericError:
		// handle generic error (errors created other than using NewApplicationError() API)
	case *workflow.CanceledError:
		// handle cancellation
	case *workflow.TimeoutError:
		// handle timeout, could check timeout type by err.TimeoutType()
	case *workflow.PanicError:
		// handle panic
	}
}

Errors from child workflow should be handled in a similar way, except that there should be no *PanicError from child workflow.
When panic happen in workflow implementation code, temporal client library catches that panic and causing the decision timeout.
That decision task will be retried at a later time (with exponential backoff retry intervals).
*/

type (
	// ApplicationError returned from activity implementations with message and optional details.
	ApplicationError struct {
		message      string
		nonRetryable bool
		details      Values
	}

	// TimeoutError returned when activity or child workflow timed out.
	TimeoutError struct {
		timeoutType          commonpb.TimeoutType
		lastErr              error
		lastHeartbeatDetails Values
	}

	// CanceledError returned when operation was canceled.
	CanceledError struct {
		details Values
	}

	// TerminatedError returned when workflow was terminated.
	TerminatedError struct {
	}

	// PanicError contains information about panicked workflow/activity.
	PanicError struct {
		value      interface{}
		stackTrace string
	}

	// workflowPanicError contains information about panicked workflow.
	// Used to distinguish go panic in the workflow code from a PanicError returned from a workflow function.
	workflowPanicError struct {
		value      interface{}
		stackTrace string
	}

	// ContinueAsNewError contains information about how to continue the workflow as new.
	ContinueAsNewError struct {
		wfn    interface{}
		args   []interface{}
		params *executeWorkflowParams
	}

	// UnknownExternalWorkflowExecutionError can be returned when external workflow doesn't exist
	UnknownExternalWorkflowExecutionError struct{}

	// ActivityTaskError returned from workflow when activity returned an error.
	ActivityTaskError struct {
		scheduledEventID int64
		startedEventID   int64
		identity         string
		cause            error
	}

	// ChildWorkflowExecutionError returned from workflow when child workflow returned an error.
	ChildWorkflowExecutionError struct {
		namespace         string
		workflowExecution *commonpb.WorkflowExecution
		workflowType      *commonpb.WorkflowType
		initiatedEventID  int64
		startedEventID    int64
		cause             error
	}
)

// ErrNoData is returned when trying to extract strong typed data while there is no data available.
var ErrNoData = errors.New("no data available")

// ErrTooManyArg is returned when trying to extract strong typed data with more arguments than available data.
var ErrTooManyArg = errors.New("too many arguments")

// ErrActivityResultPending is returned from activity's implementation to indicate the activity is not completed when
// activity method returns. Activity needs to be completed by Client.CompleteActivity() separately. For example, if an
// activity require human interaction (like approve an expense report), the activity could return activity.ErrResultPending
// which indicate the activity is not done yet. Then, when the waited human action happened, it needs to trigger something
// that could report the activity completed event to temporal server via Client.CompleteActivity() API.
var ErrActivityResultPending = errors.New("not error: do not autocomplete, using Client.CompleteActivity() to complete")

// NewApplicationError create new instance of *ApplicationError with reason and optional details.
func NewApplicationError(message string, nonRetryable bool, details ...interface{}) *ApplicationError {
	// When return error to user, use EncodedValues as details and data is ready to be decoded by calling Get
	if len(details) == 1 {
		if d, ok := details[0].(*EncodedValues); ok {
			return &ApplicationError{message: message, nonRetryable: nonRetryable, details: d}
		}
	}
	// When create error for server, use ErrorDetailsValues as details to hold values and encode later
	return &ApplicationError{message: message, nonRetryable: nonRetryable, details: ErrorDetailsValues(details)}
}

// NewTimeoutError creates TimeoutError instance.
// Use NewHeartbeatTimeoutError to create heartbeat TimeoutError.
func NewTimeoutError(timeoutType commonpb.TimeoutType, lastErr error, lastHeatbeatDetails ...interface{}) *TimeoutError {
	timeoutErr := &TimeoutError{
		timeoutType: timeoutType,
		lastErr:     lastErr,
	}

	if len(lastHeatbeatDetails) == 1 {
		if d, ok := lastHeatbeatDetails[0].(*EncodedValues); ok {
			timeoutErr.lastHeartbeatDetails = d
			return timeoutErr
		}
	}
	timeoutErr.lastHeartbeatDetails = ErrorDetailsValues(lastHeatbeatDetails)
	return timeoutErr
}

// NewHeartbeatTimeoutError creates TimeoutError instance.
func NewHeartbeatTimeoutError(details ...interface{}) *TimeoutError {
	return NewTimeoutError(commonpb.TimeoutType_Heartbeat, nil, details...)
}

// NewCanceledError creates CanceledError instance.
func NewCanceledError(details ...interface{}) *CanceledError {
	if len(details) == 1 {
		if d, ok := details[0].(*EncodedValues); ok {
			return &CanceledError{details: d}
		}
	}
	return &CanceledError{details: ErrorDetailsValues(details)}
}

// NewActivityTaskError creates ActivityTaskError instance.
func NewActivityTaskError(
	scheduledEventID int64,
	startedEventID int64,
	identity string,
	cause error,
) *ActivityTaskError {
	return &ActivityTaskError{
		scheduledEventID: scheduledEventID,
		startedEventID:   startedEventID,
		identity:         identity,
		cause:            cause,
	}
}

// NewChildWorkflowExecutionError creates ChildWorkflowExecutionError instance.
func NewChildWorkflowExecutionError(
	namespace string,
	workflowExecution *commonpb.WorkflowExecution,
	workflowType *commonpb.WorkflowType,
	initiatedEventID int64,
	startedEventID int64,
	cause error,
) *ChildWorkflowExecutionError {
	return &ChildWorkflowExecutionError{
		namespace:         namespace,
		workflowExecution: workflowExecution,
		workflowType:      workflowType,
		initiatedEventID:  initiatedEventID,
		startedEventID:    startedEventID,
		cause:             cause,
	}
}

// IsCanceledError returns whether error in CanceledError.
func IsCanceledError(err error) bool {
	var canceledErr *CanceledError
	return errors.As(err, &canceledErr)
}

// NewContinueAsNewError creates ContinueAsNewError instance
// If the workflow main function returns this error then the current execution is ended and
// the new execution with same workflow ID is started automatically with options
// provided to this function.
//  ctx - use context to override any options for the new workflow like run timeout, task timeout, task list.
//	  if not mentioned it would use the defaults that the current workflow is using.
//        ctx := WithWorkflowRunTimeout(ctx, 30 * time.Minute)
//        ctx := WithWorkflowTaskTimeout(ctx, 5 * time.Second)
//	  ctx := WithWorkflowTaskList(ctx, "example-group")
//  wfn - workflow function. for new execution it can be different from the currently running.
//  args - arguments for the new workflow.
//
func NewContinueAsNewError(ctx Context, wfn interface{}, args ...interface{}) *ContinueAsNewError {
	// Validate type and its arguments.
	options := getWorkflowEnvOptions(ctx)
	if options == nil {
		panic("context is missing required options for continue as new")
	}
	env := getWorkflowEnvironment(ctx)
	workflowType, input, err := getValidatedWorkflowFunction(wfn, args, options.dataConverter, env.GetRegistry())
	if err != nil {
		panic(err)
	}

	params := &executeWorkflowParams{
		workflowOptions: *options,
		workflowType:    workflowType,
		input:           input,
		header:          getWorkflowHeader(ctx, options.contextPropagators),
	}
	return &ContinueAsNewError{wfn: wfn, args: args, params: params}
}

// Error from error interface
func (e *ApplicationError) Error() string {
	return e.message
}

// HasDetails return if this error has strong typed detail data.
func (e *ApplicationError) HasDetails() bool {
	return e.details != nil && e.details.HasValues()
}

// Details extracts strong typed detail data of this custom error. If there is no details, it will return ErrNoData.
func (e *ApplicationError) Details(d ...interface{}) error {
	if !e.HasDetails() {
		return ErrNoData
	}
	return e.details.Get(d...)
}

// NonRetryable indicated if error is not retryable.
func (e *ApplicationError) NonRetryable() bool {
	return e.nonRetryable
}

// Error from error interface
func (e *TimeoutError) Error() string {
	return fmt.Sprintf("TimeoutType: %v, LastErr: %v", e.timeoutType, e.lastErr)
}

// TimeoutType return timeout type of this error
func (e *TimeoutError) TimeoutType() commonpb.TimeoutType {
	return e.timeoutType
}

// HasLastHeartbeatDetails return if this error has strong typed detail data.
func (e *TimeoutError) HasLastHeartbeatDetails() bool {
	return e.lastHeartbeatDetails != nil && e.lastHeartbeatDetails.HasValues()
}

// LastHeartbeatDetails extracts strong typed detail data of this error. If there is no details, it will return ErrNoData.
func (e *TimeoutError) LastHeartbeatDetails(d ...interface{}) error {
	if !e.HasLastHeartbeatDetails() {
		return ErrNoData
	}
	return e.lastHeartbeatDetails.Get(d...)
}

// Error from error interface
func (e *CanceledError) Error() string {
	return "CanceledError"
}

// HasDetails return if this error has strong typed detail data.
func (e *CanceledError) HasDetails() bool {
	return e.details != nil && e.details.HasValues()
}

// Details extracts strong typed detail data of this error.
func (e *CanceledError) Details(d ...interface{}) error {
	if !e.HasDetails() {
		return ErrNoData
	}
	return e.details.Get(d...)
}

func newPanicError(value interface{}, stackTrace string) *PanicError {
	return &PanicError{value: value, stackTrace: stackTrace}
}

func newWorkflowPanicError(value interface{}, stackTrace string) *workflowPanicError {
	return &workflowPanicError{value: value, stackTrace: stackTrace}
}

// Error from error interface
func (e *PanicError) Error() string {
	return fmt.Sprintf("%v", e.value)
}

// StackTrace return stack trace of the panic
func (e *PanicError) StackTrace() string {
	return e.stackTrace
}

// Error from error interface
func (e *workflowPanicError) Error() string {
	return fmt.Sprintf("%v", e.value)
}

// StackTrace return stack trace of the panic
func (e *workflowPanicError) StackTrace() string {
	return e.stackTrace
}

// Error from error interface
func (e *ContinueAsNewError) Error() string {
	return "ContinueAsNew"
}

// WorkflowType return workflowType of the new run
func (e *ContinueAsNewError) WorkflowType() *WorkflowType {
	return e.params.workflowType
}

// Args return workflow argument of the new run
func (e *ContinueAsNewError) Args() []interface{} {
	return e.args
}

// newTerminatedError creates NewTerminatedError instance
func newTerminatedError() *TerminatedError {
	return &TerminatedError{}
}

// Error from error interface
func (e *TerminatedError) Error() string {
	return "Terminated"
}

// newUnknownExternalWorkflowExecutionError creates UnknownExternalWorkflowExecutionError instance
func newUnknownExternalWorkflowExecutionError() *UnknownExternalWorkflowExecutionError {
	return &UnknownExternalWorkflowExecutionError{}
}

// Error from error interface
func (e *UnknownExternalWorkflowExecutionError) Error() string {
	return "UnknownExternalWorkflowExecution"
}

func (e *ActivityTaskError) Error() string {
	return fmt.Sprintf("activity task error (scheduledEventID: %d, startedEventID: %d, identity: %s): %v", e.scheduledEventID, e.startedEventID, e.identity, e.cause)
}

func (e *ActivityTaskError) Unwrap() error {
	return e.cause
}

// Error from error interface
func (e *ChildWorkflowExecutionError) Error() string {
	return fmt.Sprintf("child workflow execution error (initiatedEventID: %d, startedEventID: %d, workflowType: %s): %v",
		e.initiatedEventID, e.startedEventID, e.workflowType, e.cause)
}

func (e *ChildWorkflowExecutionError) Unwrap() error {
	return e.cause
}

func convertErrDetailsToPayloads(details Values, dc DataConverter) *commonpb.Payloads {
	switch d := details.(type) {
	case ErrorDetailsValues:
		data, err := encodeArgs(dc, d)
		if err != nil {
			panic(err)
		}
		return data
	case *EncodedValues:
		return d.values
	default:
		panic(fmt.Sprintf("unknown error details type %T", details))
	}
}

// IsRetryable returns if error retryable or not.
func IsRetryable(err error, nonRetryableTypes []string) bool {
	var terminatedErr *TerminatedError
	var canceledErr *CanceledError
	var workflowPanicErr *workflowPanicError
	if errors.As(err, &terminatedErr) || errors.As(err, &canceledErr) || errors.As(err, &workflowPanicErr) {
		return false
	}

	var applicationErr *ApplicationError
	if errors.As(err, &applicationErr) {
		if applicationErr.nonRetryable {
			return false

		}
	}

	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		if timeoutErr.timeoutType != commonpb.TimeoutType_StartToClose &&
			timeoutErr.timeoutType != commonpb.TimeoutType_Heartbeat {
			return false
		}
	}

	errType := getErrorType(err)
	for _, er := range nonRetryableTypes {
		if er == errType {
			return false
		}
	}

	return true
}

func getErrorType(err error) string {
	var t reflect.Type
	for t = reflect.TypeOf(err); t.Kind() == reflect.Ptr; t = t.Elem() {
	}

	return t.Name()
}

// convertErrorToFailure converts error to failure.
func convertErrorToFailure(err error, dc DataConverter) *failurepb.Failure {
	if err == nil {
		return nil
	}

	failure := &failurepb.Failure{
		Source:  "GoSDK",
		Message: err.Error(),
	}

	switch err := err.(type) {
	case *ApplicationError:
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type:         getErrorType(err),
			NonRetryable: err.nonRetryable,
			Details:      convertErrDetailsToPayloads(err.details, dc),
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
	case *CanceledError:
		failureInfo := &failurepb.CanceledFailureInfo{
			Details: convertErrDetailsToPayloads(err.details, dc),
		}
		failure.FailureInfo = &failurepb.Failure_CanceledFailureInfo{CanceledFailureInfo: failureInfo}
	case *PanicError:
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type: getErrorType(err),
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
		failure.StackTrace = err.StackTrace()
	case *workflowPanicError:
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type:         getErrorType(&PanicError{}),
			NonRetryable: true,
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
		failure.StackTrace = err.StackTrace()
	case *TimeoutError:
		failureInfo := &failurepb.TimeoutFailureInfo{
			TimeoutType:          err.timeoutType,
			LastFailure:          convertErrorToFailure(err.lastErr, dc),
			LastHeartbeatDetails: convertErrDetailsToPayloads(err.lastHeartbeatDetails, dc),
		}
		failure.FailureInfo = &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: failureInfo}
	case *TerminatedError:
		failureInfo := &failurepb.TerminatedFailureInfo{}
		failure.FailureInfo = &failurepb.Failure_TerminatedFailureInfo{TerminatedFailureInfo: failureInfo}
	case *ActivityTaskError:
		failureInfo := &failurepb.ActivityTaskFailureInfo{
			ScheduledEventId: err.scheduledEventID,
			StartedEventId:   err.startedEventID,
			Identity:         err.identity,
		}
		failure.FailureInfo = &failurepb.Failure_ActivityTaskFailureInfo{ActivityTaskFailureInfo: failureInfo}
	case *ChildWorkflowExecutionError:
		failureInfo := &failurepb.ChildWorkflowExecutionFailureInfo{
			Namespace:         err.namespace,
			WorkflowExecution: err.workflowExecution,
			WorkflowType:      err.workflowType,
			InitiatedEventId:  err.initiatedEventID,
			StartedEventId:    err.startedEventID,
		}
		failure.FailureInfo = &failurepb.Failure_ChildWorkflowExecutionFailureInfo{ChildWorkflowExecutionFailureInfo: failureInfo}
	default: // All unknown errors are considered to be retryable ApplicationFailureInfo.
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type:         getErrorType(err),
			NonRetryable: false,
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
	}

	if innerErr := errors.Unwrap(err); innerErr != nil {
		failure.Cause = convertErrorToFailure(innerErr, dc)
	}

	return failure
}

// convertFailureToError converts failure to error.
func convertFailureToError(failure *failurepb.Failure, dc DataConverter) error {
	if failure == nil {
		return nil
	}

	if failure.GetTimeoutFailureInfo() != nil {
		timeoutFailureInfo := failure.GetTimeoutFailureInfo()
		lastHeartbeatDetails := newEncodedValues(timeoutFailureInfo.GetLastHeartbeatDetails(), dc)
		return NewTimeoutError(
			timeoutFailureInfo.GetTimeoutType(),
			convertFailureToError(timeoutFailureInfo.GetLastFailure(), dc),
			lastHeartbeatDetails)
	} else if failure.GetApplicationFailureInfo() != nil {
		applicationFailureInfo := failure.GetApplicationFailureInfo()
		details := newEncodedValues(applicationFailureInfo.GetDetails(), dc)
		switch applicationFailureInfo.GetType() {
		case getErrorType(&ApplicationError{}):
			return NewApplicationError(failure.GetMessage(), applicationFailureInfo.GetNonRetryable(), details)
		case getErrorType(&PanicError{}):
			return newPanicError(failure.GetMessage(), failure.GetStackTrace())
		}
	} else if failure.GetCanceledFailureInfo() != nil {
		details := newEncodedValues(failure.GetCanceledFailureInfo().GetDetails(), dc)
		return NewCanceledError(details)
	} else if failure.GetServerFailureInfo() != nil {
		return NewApplicationError(failure.GetMessage(), failure.GetServerFailureInfo().GetNonRetryable(), nil)
	} else if failure.GetResetWorkflowFailureInfo() != nil {
		return NewApplicationError(failure.GetMessage(), true, failure.GetResetWorkflowFailureInfo().GetLastHeartbeatDetails())
	} else if failure.GetTerminatedFailureInfo() != nil {
		return newTerminatedError()
	} else if failure.GetActivityTaskFailureInfo() != nil {
		activityTaskInfoFailure := failure.GetActivityTaskFailureInfo()
		activityTaskError := NewActivityTaskError(
			activityTaskInfoFailure.GetScheduledEventId(),
			activityTaskInfoFailure.GetStartedEventId(),
			activityTaskInfoFailure.GetIdentity(),
			convertFailureToError(failure.GetCause(), dc),
		)
		return activityTaskError
	} else if failure.GetChildWorkflowExecutionFailureInfo() != nil {
		childWorkflowExecutionFailureInfo := failure.GetChildWorkflowExecutionFailureInfo()
		childWorkflowExecutionError := NewChildWorkflowExecutionError(
			childWorkflowExecutionFailureInfo.GetNamespace(),
			childWorkflowExecutionFailureInfo.GetWorkflowExecution(),
			childWorkflowExecutionFailureInfo.GetWorkflowType(),
			childWorkflowExecutionFailureInfo.GetInitiatedEventId(),
			childWorkflowExecutionFailureInfo.GetStartedEventId(),
			convertFailureToError(failure.GetCause(), dc),
		)
		return childWorkflowExecutionError
	}

	// All unknown types are considered to be retryable ApplicationError.
	return NewApplicationError(failure.GetMessage(), false, nil)
}
