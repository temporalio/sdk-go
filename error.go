package temporal

import (
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
func NewCustomError(reason string, details ...interface{}) *CustomError {
	return internal.NewCustomError(reason, details...)
}

// NewCanceledError creates CanceledError instance.
// Return this error from activity or child workflow to indicate that it was successfully cancelled.
func NewCanceledError(details ...interface{}) *CanceledError {
	return internal.NewCanceledError(details...)
}

// IsCustomError return if the err is a CustomError
func IsCustomError(err error) bool {
	_, ok := err.(*CustomError)
	return ok
}

// IsWorkflowExecutionAlreadyStartedError return if the err is a WorkflowExecutionAlreadyStartedError
func IsWorkflowExecutionAlreadyStartedError(err error) bool {
	_, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted)
	return ok
}

// IsCanceledError return if the err is a CanceledError
func IsCanceledError(err error) bool {
	_, ok := err.(*CanceledError)
	return ok
}

// IsGenericError return if the err is a GenericError
func IsGenericError(err error) bool {
	_, ok := err.(*workflow.GenericError)
	return ok
}

// IsTimeoutError return if the err is a TimeoutError
func IsTimeoutError(err error) bool {
	_, ok := err.(*workflow.TimeoutError)
	return ok
}

// IsTerminatedError return if the err is a TerminatedError
func IsTerminatedError(err error) bool {
	_, ok := err.(*workflow.TerminatedError)
	return ok
}

// IsPanicError return if the err is a PanicError
func IsPanicError(err error) bool {
	_, ok := err.(*workflow.PanicError)
	return ok
}
