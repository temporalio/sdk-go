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

package cadence

import (
	"errors"
	"fmt"
	"reflect"

	"go.uber.org/cadence/.gen/go/shared"
)

type (
	// Marker functions are used to ensure that interfaces never implement each other.
	// For example without marker an implementation of ErrorWithDetails matches
	// CanceledError interface as well.

	// ErrorWithDetails to return from Workflow and activity implementations.
	ErrorWithDetails interface {
		error
		Reason() string
		Details(d ...interface{}) // Extracts details into passed pointers
		errorWithDetails()        // interface marker
	}

	// TimeoutError returned when activity or child workflow timed out
	TimeoutError interface {
		error
		TimeoutType() shared.TimeoutType
		Details(d ...interface{}) // Present only for HEARTBEAT TimeoutType
		timeoutError()            // interface marker
	}

	// CanceledError returned when operation was canceled
	CanceledError interface {
		error
		Details(d ...interface{}) // Extracts details into passed pointers
		canceledError()           // interface marker
	}

	// PanicError contains information about panicked workflow
	PanicError interface {
		error
		Value(v interface{}) // Value passed to panic call
		StackTrace() string  // Stack trace of a panicked coroutine
		panicError()         // interface marker
	}

	// ContinueAsNewError contains information about how to continue the
	// current workflow as a fresh one.
	ContinueAsNewError interface {
		error
		continueAsNewError() // interface marker
	}
)

var _ ErrorWithDetails = (*errorWithDetails)(nil)
var _ CanceledError = (*canceledError)(nil)
var _ TimeoutError = (*timeoutError)(nil)
var _ PanicError = (*panicError)(nil)
var _ ContinueAsNewError = (*continueAsNewError)(nil)

// ErrActivityResultPending is returned from activity's Execute method to indicate the activity is not completed when
// Execute method returns. activity will be completed asynchronously when Client.CompleteActivity() is called.
var ErrActivityResultPending = errors.New("not error: do not autocomplete, " +
	"using Client.CompleteActivity() to complete")

// NewErrorWithDetails creates ErrorWithDetails instance
// Create standard error through errors.New or fmt.Errorf() if no details are provided
func NewErrorWithDetails(reason string, details ...interface{}) ErrorWithDetails {
	data, err := getHostEnvironment().encodeArgs(details)
	if err != nil {
		panic(err)
	}
	return &errorWithDetails{reason: reason, details: data}
}

// NewTimeoutError creates TimeoutError instance.
// Use NewHeartbeatTimeoutError to create heartbeat TimeoutError
// WARNING: This function is public only to support unit testing of workflows.
// It shouldn't be used by application level code.
func NewTimeoutError(timeoutType shared.TimeoutType) TimeoutError {
	return &timeoutError{timeoutType: timeoutType}
}

// NewHeartbeatTimeoutError creates TimeoutError instance
// WARNING: This function is public only to support unit testing of workflows.
// It shouldn't be used by application level code.
func NewHeartbeatTimeoutError(details ...interface{}) TimeoutError {
	data, err := getHostEnvironment().encodeArgs(details)
	if err != nil {
		panic(err)
	}
	return &timeoutError{timeoutType: shared.TimeoutType_HEARTBEAT, details: data}
}

// NewCanceledError creates CanceledError instance
func NewCanceledError(details ...interface{}) CanceledError {
	data, err := getHostEnvironment().encodeArgs(details)
	if err != nil {
		panic(err)
	}
	return &canceledError{details: data}
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
func NewContinueAsNewError(ctx Context, wfn interface{}, args ...interface{}) ContinueAsNewError {
	// Validate type and its arguments.
	workflowType, input, err := getValidatedWorkerFunction(wfn, args)
	if err != nil {
		panic(err)
	}
	options := getWorkflowEnvOptions(ctx)
	if options == nil {
		panic("context is missing required options for continue as new")
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

	options.workflowType = workflowType
	options.input = input
	return &continueAsNewError{wfn: wfn, args: args, options: options}
}

// errorWithDetails implements ErrorWithDetails
type errorWithDetails struct {
	reason  string
	details []byte
}

// Error from error interface
func (e *errorWithDetails) Error() string {
	return e.reason
}

// Reason is from ErrorWithDetails interface
func (e *errorWithDetails) Reason() string {
	return e.reason
}

// Details is from ErrorWithDetails interface
func (e *errorWithDetails) Details(d ...interface{}) {
	if err := getHostEnvironment().decode(e.details, d); err != nil {
		panic(err)
	}
}

// errorWithDetails is from ErrorWithDetails interface
func (e *errorWithDetails) errorWithDetails() {}

// timeoutError implements TimeoutError
type timeoutError struct {
	timeoutType shared.TimeoutType
	details     []byte
}

// Error from error interface
func (e *timeoutError) Error() string {
	return fmt.Sprintf("TimeoutType: %v", e.timeoutType)
}

func (e *timeoutError) TimeoutType() shared.TimeoutType {
	return e.timeoutType
}

// Details is from TimeoutError interface
func (e *timeoutError) Details(d ...interface{}) {
	if err := getHostEnvironment().decode(e.details, d); err != nil {
		panic(err)
	}

}

func (e *timeoutError) timeoutError() {}

type canceledError struct {
	details []byte
}

// Error from error interface
func (e *canceledError) Error() string {
	return "CanceledError"
}

// Details is from CanceledError interface
func (e *canceledError) Details(d ...interface{}) {
	if err := getHostEnvironment().decode(e.details, d); err != nil {
		panic(err)
	}
}

func (e *canceledError) canceledError() {}

type panicError struct {
	value      interface{}
	stackTrace string
}

func newPanicError(value interface{}, stackTrace string) PanicError {
	return &panicError{value: value, stackTrace: stackTrace}
}

func (e *panicError) Error() string {
	return fmt.Sprintf("%v", e.value)
}

func (e *panicError) Value(v interface{}) {
	reflect.ValueOf(v).Elem().Set(reflect.ValueOf(e.value))
}

func (e *panicError) StackTrace() string {
	return e.stackTrace
}

func (e *panicError) panicError() {}

type continueAsNewError struct {
	wfn     interface{}
	args    []interface{}
	options *workflowOptions
}

// Error from error interface
func (e *continueAsNewError) Error() string {
	return "ContinueAsNew"
}

// continueAsNewError is from continueAsNewError interface
func (e *continueAsNewError) continueAsNewError() {}
