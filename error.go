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

	"go.temporal.io/temporal-proto/serviceerror"

	"go.temporal.io/temporal/internal"
	"go.temporal.io/temporal/workflow"
)

type (
	// CustomError returned from workflow and activity implementations with reason and optional details.
	CustomError = internal.CustomError

	// CanceledError returned when operation was canceled.
	CanceledError = internal.CanceledError
)

// ErrNoData is returned when trying to extract strong typed data while there is no data available.
var ErrNoData = internal.ErrNoData

// NewCustomError create new instance of *CustomError with reason and optional details.
// Use CustomError for any use case specific errors that cross activity and child workflow boundaries.
func NewCustomError(reason string, nonRetryable bool, details ...interface{}) *CustomError {
	return internal.NewCustomError(reason, nonRetryable, details...)
}

// NewCanceledError creates CanceledError instance.
// Return this error from activity or child workflow to indicate that it was successfully cancelled.
func NewCanceledError(details ...interface{}) *CanceledError {
	return internal.NewCanceledError(details...)
}

// IsCustomError return if the err is a CustomError
func IsCustomError(err error) bool {
	var applicationError *CustomError
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
	var timeoutError *workflow.TimeoutError
	return errors.As(err, &timeoutError)
}

// IsTerminatedError return if the err is a TerminatedError
func IsTerminatedError(err error) bool {
	var terminateError *workflow.TerminatedError
	return errors.As(err, &terminateError)
}

// IsPanicError return if the err is a PanicError
func IsPanicError(err error) bool {
	var panicError *workflow.PanicError
	return errors.As(err, &panicError)
}
