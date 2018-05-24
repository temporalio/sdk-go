// Copyright (c) 2017 Uber Technologies, Inc.
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
	"strings"

	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
)

/*
Below are the possible cases that activity could fail:
1) *CustomError: (this should be the most common one)
	If activity implementation returns *CustomError by using NewCustomError() API, workflow code would receive *CustomError.
	The err would contain a Reason and Details. The reason is what activity specified to NewCustomError(), which workflow
	code could check to determine what kind of error it was and take actions based on the reason. The details is encoded
	[]byte which workflow code could extract strong typed data. Workflow code needs to know what the types of the encoded
	details are before extracting them.
2) *GenericError:
	If activity implementation returns errors other than from NewCustomError() API, workflow code would receive *GenericError.
	Use err.Error() to get the string representation of the actual error.
3) *CanceledError:
	If activity was canceled, workflow code will receive instance of *CanceledError. When activity cancels itself by
	returning NewCancelError() it would supply optional details which could be extracted by workflow code.
4) *TimeoutError:
	If activity was timed out (several timeout types), workflow code will receive instance of *TimeoutError. The err contains
	details about what type of timeout it was.
5) *PanicError:
	If activity code panic while executing, cadence activity worker will report it as activity failure to cadence server.
	The cadence client library will present that failure as *PanicError to workflow code. The err contains a string
	representation of the panic message and the call stack when panic was happen.

Workflow code could handle errors based on different types of error. Below is sample code of how error handling looks like.

_, err := workflow.ExecuteActivity(ctx, MyActivity, ...).Get(nil)
if err != nil {
	switch err := err.(type) {
	case *workflow.CustomError:
		// handle activity errors (created via NewCustomError() API)
		switch err.Reason() {
		case CustomErrReasonA: // assume CustomErrReasonA is constant defined by activity implementation
			var detailMsg string // assuming activity return error by NewCustomError(CustomErrReasonA, "string details")
			err.Details(&detailMsg) // extract strong typed details (corresponding to CustomErrReasonA)
			// handle CustomErrReasonA
		case CustomErrReasonB:
			// handle CustomErrReasonB
		default:
			// newer version of activity could return new errors that workflow was not aware of.
		}
	case *workflow.GenericError:
		// handle generic error (errors created other than using NewCustomError() API)
	case *workflow.CanceledError:
		// handle cancellation
	case *workflow.TimeoutError:
		// handle timeout, could check timeout type by err.TimeoutType()
	case *workflow.PanicError:
		// handle panic
	}
}

Errors from child workflow should be handled in a similar way, except that there should be no *PanicError from child workflow.
When panic happen in workflow implementation code, cadence client library catches that panic and causing the decision timeout.
That decision task will be retried at a later time (with exponential backoff retry intervals).
*/

type (
	// CustomError returned from workflow and activity implementations with reason and optional details.
	CustomError struct {
		reason  string
		details encoded.Values
	}

	// GenericError returned from workflow/workflow when the implementations return errors other than from NewCustomError() API.
	GenericError struct {
		err string
	}

	// TimeoutError returned when activity or child workflow timed out.
	TimeoutError struct {
		timeoutType shared.TimeoutType
		details     encoded.Values
	}

	// CanceledError returned when operation was canceled.
	CanceledError struct {
		details encoded.Values
	}

	// TerminatedError returned when workflow was terminated.
	TerminatedError struct {
	}

	// PanicError contains information about panicked workflow/activity.
	PanicError struct {
		value      string
		stackTrace string
	}

	// ContinueAsNewError contains information about how to continue the workflow as new.
	ContinueAsNewError struct {
		wfn    interface{}
		args   []interface{}
		params *executeWorkflowParams
	}
)

const (
	errReasonPanic    = "cadenceInternal:Panic"
	errReasonGeneric  = "cadenceInternal:Generic"
	errReasonCanceled = "cadenceInternal:Canceled"
	errReasonTimeout  = "cadenceInternal:Timeout"
)

// ErrNoData is returned when trying to extract strong typed data while there is no data available.
var ErrNoData = errors.New("no data available")

// ErrTooManyArg is returned when trying to extract strong typed data with more arguments than available data.
var ErrTooManyArg = errors.New("too many arguments")

// ErrActivityResultPending is returned from activity's implementation to indicate the activity is not completed when
// activity method returns. Activity needs to be completed by Client.CompleteActivity() separately. For example, if an
// activity require human interaction (like approve an expense report), the activity could return activity.ErrResultPending
// which indicate the activity is not done yet. Then, when the waited human action happened, it needs to trigger something
// that could report the activity completed event to cadence server via Client.CompleteActivity() API.
var ErrActivityResultPending = errors.New("not error: do not autocomplete, using Client.CompleteActivity() to complete")

// NewCustomError create new instance of *CustomError with reason and optional details.
func NewCustomError(reason string, details ...interface{}) *CustomError {
	if strings.HasPrefix(reason, "cadenceInternal:") {
		panic("'cadenceInternal:' is reserved prefix, please use different reason")
	}
	// When return error to user, use EncodedValues as details and data is ready to be decoded by calling Get
	if len(details) == 1 {
		if d, ok := details[0].(*EncodedValues); ok {
			return &CustomError{reason: reason, details: d}
		}
	}
	// When create error for server, use ErrorDetailsValues as details to hold values and encode later
	return &CustomError{reason: reason, details: ErrorDetailsValues(details)}
}

// NewTimeoutError creates TimeoutError instance.
// Use NewHeartbeatTimeoutError to create heartbeat TimeoutError
func NewTimeoutError(timeoutType shared.TimeoutType) *TimeoutError {
	return &TimeoutError{timeoutType: timeoutType}
}

// NewHeartbeatTimeoutError creates TimeoutError instance
func NewHeartbeatTimeoutError(details ...interface{}) *TimeoutError {
	if len(details) == 1 {
		if d, ok := details[0].(*EncodedValues); ok {
			return &TimeoutError{timeoutType: shared.TimeoutTypeHeartbeat, details: d}
		}
	}
	return &TimeoutError{timeoutType: shared.TimeoutTypeHeartbeat, details: ErrorDetailsValues(details)}
}

// NewCanceledError creates CanceledError instance
func NewCanceledError(details ...interface{}) *CanceledError {
	if len(details) == 1 {
		if d, ok := details[0].(*EncodedValues); ok {
			return &CanceledError{details: d}
		}
	}
	return &CanceledError{details: ErrorDetailsValues(details)}
}

// NewContinueAsNewError creates ContinueAsNewError instance
// If the workflow main function returns this error then the current execution is ended and
// the new execution with same workflow ID is started automatically with options
// provided to this function.
//  ctx - use context to override any options for the new workflow like execution time out, decision task time out, task list.
//	  if not mentioned it would use the defaults that the current workflow is using.
//        ctx := WithExecutionStartToCloseTimeout(ctx, 30 * time.Minute)
//        ctx := WithWorkflowTaskStartToCloseTimeout(ctx, time.Minute)
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
	workflowType, input, err := getValidatedWorkflowFunction(wfn, args, options.dataConverter)
	if err != nil {
		panic(err)
	}
	if options.taskListName == nil || *options.taskListName == "" {
		panic("invalid task list provided")
	}
	if options.executionStartToCloseTimeoutSeconds == nil || *options.executionStartToCloseTimeoutSeconds <= 0 {
		panic("invalid executionStartToCloseTimeoutSeconds provided")
	}
	if options.taskStartToCloseTimeoutSeconds == nil || *options.taskStartToCloseTimeoutSeconds <= 0 {
		panic("invalid taskStartToCloseTimeoutSeconds provided")
	}

	params := &executeWorkflowParams{
		workflowOptions: *options,
		workflowType:    workflowType,
		input:           input,
	}
	return &ContinueAsNewError{wfn: wfn, args: args, params: params}
}

// Error from error interface
func (e *CustomError) Error() string {
	return e.reason
}

// Reason gets the reason of this custom error
func (e *CustomError) Reason() string {
	return e.reason
}

// HasDetails return if this error has strong typed detail data.
func (e *CustomError) HasDetails() bool {
	return e.details != nil && e.details.HasValues()
}

// Details extracts strong typed detail data of this custom error. If there is no details, it will return ErrNoData.
func (e *CustomError) Details(d ...interface{}) error {
	if !e.HasDetails() {
		return ErrNoData
	}
	return e.details.Get(d...)
}

// Error from error interface
func (e *GenericError) Error() string {
	return e.err
}

// Error from error interface
func (e *TimeoutError) Error() string {
	return fmt.Sprintf("TimeoutType: %v", e.timeoutType)
}

// TimeoutType return timeout type of this error
func (e *TimeoutError) TimeoutType() shared.TimeoutType {
	return e.timeoutType
}

// HasDetails return if this error has strong typed detail data.
func (e *TimeoutError) HasDetails() bool {
	return e.details != nil && e.details.HasValues()
}

// Details extracts strong typed detail data of this error. If there is no details, it will return ErrNoData.
func (e *TimeoutError) Details(d ...interface{}) error {
	if !e.HasDetails() {
		return ErrNoData
	}
	return e.details.Get(d...)
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
	return &PanicError{value: fmt.Sprintf("%v", value), stackTrace: stackTrace}
}

// Error from error interface
func (e *PanicError) Error() string {
	return e.value
}

// StackTrace return stack trace of the panic
func (e *PanicError) StackTrace() string {
	return e.stackTrace
}

// Error from error interface
func (e *ContinueAsNewError) Error() string {
	return "ContinueAsNew"
}

// newTerminatedError creates NewTerminatedError instance
func newTerminatedError() *TerminatedError {
	return &TerminatedError{}
}

// Error from error interface
func (e *TerminatedError) Error() string {
	return "Terminated"
}
