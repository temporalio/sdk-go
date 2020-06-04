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

package temporal

import (
	"errors"

	commonpb "go.temporal.io/temporal-proto/common"
	"go.temporal.io/temporal-proto/serviceerror"
	"go.temporal.io/temporal/internal"
)

/*
If activity fails then *ActivityTaskError is returned to the workflow code. The error has important information about activity
and actual error which caused activity failure. This internal error can be unwrapped using errors.Unwrap() or checked using errors.As().
Below are the possible types of internal error:
1) *ApplicationError: (this should be the most common one)
	*ApplicationError can be returned in two cases:
		- If activity implementation returns *ApplicationError by using NewRetryableApplicationError()/NewNonRetryableApplicationError() API.
		  The error would contain a message and optional details. Workflow code could extract details to string typed variable, determine
		  what kind of error it was, and take actions based on it. The details is encoded payload therefore, workflow code needs to know what
          the types of the encoded details are before extracting them.
		- If activity implementation returns errors other than from NewApplicationError() API. In this case GetOriginalType()
		  will return orginal type of an error represented as string. Workflow code could check this type to determine what kind of error it was
		  and take actions based on the type. These errors are retryable by default, unless error type is specified in retry policy.
2) *CanceledError:
	If activity was canceled, internal error will be an instance of *CanceledError. When activity cancels itself by
	returning NewCancelError() it would supply optional details which could be extracted by workflow code.
3) *TimeoutError:
	If activity was timed out (several timeout types), internal error will be an instance of *TimeoutError. The err contains
	details about what type of timeout it was.
4) *PanicError:
	If activity code panic while executing, temporal activity worker will report it as activity failure to temporal server.
	The SDK will present that failure as *PanicError. The err contains a string	representation of the panic message and
	the call stack when panic was happen.
Workflow code could handle errors based on different types of error. Below is sample code of how error handling looks like.

err := workflow.ExecuteActivity(ctx, MyActivity, ...).Get(ctx, nil)
if err != nil {
	var applicationErr *ApplicationError
	if errors.As(err, &applicationError) {
		// retrieve error message
		fmt.Println(applicationError.Error())

		// handle activity errors (created via NewApplicationError() API)
		var detailMsg string // assuming activity return error by NewApplicationError("message", true, "string details")
		applicationErr.Details(&detailMsg) // extract strong typed details

		// handle activity errors (errors created other than using NewApplicationError() API)
		switch err.OriginalType() {
		case "CustomErrTypeA":
			// handle CustomErrTypeA
		case CustomErrTypeB:
			// handle CustomErrTypeB
		default:
			// newer version of activity could return new errors that workflow was not aware of.
		}
	}

	var canceledErr *CanceledError
	if errors.As(err, &canceledErr) {
		// handle cancellation
	}

	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		// handle timeout, could check timeout type by timeoutErr.TimeoutType()
        switch err.TimeoutType() {
        case commonpb.ScheduleToStart:
                // Handle ScheduleToStart timeout.
        case commonpb.StartToClose:
                // Handle StartToClose timeout.
        case commonpb.Heartbeat:
                // Handle heartbeat timeout.
        default:
        }
	}

	var panicErr *PanicError
	if errors.As(err, &panicErr) {
		// handle panic, message and stack trace are available by panicErr.Error() and panicErr.StackTrace()
	}
}
Errors from child workflow should be handled in a similar way, except that instance of *ChildWorkflowExecutionError is returned to
workflow code. It might contain *ActivityTaskError in case if error comes from activity (which in turn will contain on of the errors above),
or *ApplicationError in case if error comes from child workflow itslef.

When panic happen in workflow implementation code, SDK catches that panic and causing the decision timeout.
That decision task will be retried at a later time (with exponential backoff retry intervals).
Workflow consumers will get an instance of *WorkflowExecutionError. This error will contains one of errors above.
*/

type (
	// ApplicationError returned from activity implementations with message and optional details.
	ApplicationError = internal.ApplicationError

	// CanceledError returned when operation was canceled.
	CanceledError = internal.CanceledError

	// ActivityTaskError returned from workflow when activity returned an error.
	ActivityTaskError = internal.ActivityTaskError

	// ServerError can be returned from server.
	ServerError = internal.ServerError

	// ChildWorkflowExecutionError returned from workflow when child workflow returned an error.
	ChildWorkflowExecutionError = internal.ChildWorkflowExecutionError

	// WorkflowExecutionError returned from workflow.
	WorkflowExecutionError = internal.WorkflowExecutionError

	// TimeoutError returned when activity or child workflow timed out.
	TimeoutError = internal.TimeoutError

	// TerminatedError returned when workflow was terminated.
	TerminatedError = internal.TerminatedError

	// PanicError contains information about panicked workflow/activity.
	PanicError = internal.PanicError

	// UnknownExternalWorkflowExecutionError can be returned when external workflow doesn't exist
	UnknownExternalWorkflowExecutionError = internal.UnknownExternalWorkflowExecutionError
)

// ErrNoData is returned when trying to extract strong typed data while there is no data available.
var ErrNoData = internal.ErrNoData

// NewNonRetryableApplicationError creates new instance of non-retryable *ApplicationError with reason and optional details.
// Use ApplicationError for any use case specific errors that cross activity and child workflow boundaries.
func NewNonRetryableApplicationError(reason string, details ...interface{}) *ApplicationError {
	return internal.NewApplicationError(reason, true, details...)
}

// NewRetryableApplicationError creates new instance of retryable *ApplicationError with reason and optional details.
// Use ApplicationError for any use case specific errors that cross activity and child workflow boundaries.
func NewApplicationError(reason string, details ...interface{}) *ApplicationError {
	return internal.NewApplicationError(reason, false, details...)
}

// NewCanceledError creates CanceledError instance.
// Return this error from activity or child workflow to indicate that it was successfully cancelled.
func NewCanceledError(details ...interface{}) *CanceledError {
	return internal.NewCanceledError(details...)
}

// IsApplicationError return if the err is a ApplicationError
func IsApplicationError(err error) bool {
	var applicationError *ApplicationError
	return errors.As(err, &applicationError)
}

// IsWorkflowExecutionAlreadyStartedError return if the err is a WorkflowExecutionAlreadyStartedError
func IsWorkflowExecutionAlreadyStartedError(err error) bool {
	_, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted)
	return ok
}

// IsCanceledError return if the err is a CanceledError
func IsCanceledError(err error) bool {
	var cancelError *CanceledError
	return errors.As(err, &cancelError)
}

// IsTimeoutError return if the err is a TimeoutError
func IsTimeoutError(err error) bool {
	var timeoutError *TimeoutError
	return errors.As(err, &timeoutError)
}

// IsTerminatedError return if the err is a TerminatedError
func IsTerminatedError(err error) bool {
	var terminateError *TerminatedError
	return errors.As(err, &terminateError)
}

// IsPanicError return if the err is a PanicError
func IsPanicError(err error) bool {
	var panicError *PanicError
	return errors.As(err, &panicError)
}

// NewTimeoutError creates TimeoutError instance.
// Use NewHeartbeatTimeoutError to create heartbeat TimeoutError
// WARNING: This function is public only to support unit testing of workflows.
// It shouldn't be used by application level code.
func NewTimeoutError(timeoutType commonpb.TimeoutType, lastErr error, details ...interface{}) *TimeoutError {
	return internal.NewTimeoutError(timeoutType, lastErr, details...)
}

// NewHeartbeatTimeoutError creates TimeoutError instance
// WARNING: This function is public only to support unit testing of workflows.
// It shouldn't be used by application level code.
func NewHeartbeatTimeoutError(details ...interface{}) *TimeoutError {
	return internal.NewHeartbeatTimeoutError(details...)
}
