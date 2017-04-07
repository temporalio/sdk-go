package cadence

import (
	"errors"
	"fmt"

	"github.com/uber-go/cadence-client/.gen/go/shared"
)

type (
	// Error to return from Workflow and activity implementations.
	Error interface {
		error
		Reason() string
		Details() []byte
	}

	// TimeoutError returned when activity or child workflow timed out
	TimeoutError interface {
		error
		TimeoutType() shared.TimeoutType
	}

	// CanceledError returned when operation was canceled
	CanceledError interface {
		error
		Details() []byte
	}

	// PanicError contains information about panicked workflow
	PanicError interface {
		error
		Value() interface{} // Value passed to panic call
		StackTrace() string // Stack trace of a panicked coroutine
	}
)

var _ Error = (*errorImpl)(nil)
var _ CanceledError = (*canceledError)(nil)
var _ TimeoutError = (*timeoutError)(nil)
var _ PanicError = (*panicError)(nil)

// ErrActivityResultPending is returned from activity's Execute method to indicate the activity is not completed when
// Execute method returns. activity will be completed asynchronously when Client.CompleteActivity() is called.
var ErrActivityResultPending = errors.New("not error: do not autocomplete, " +
	"using Client.CompleteActivity() to complete")

// NewErrorWithDetails creates Error instance
// Create standard error through errors.New or fmt.Errorf if no details are provided
func NewErrorWithDetails(reason string, details []byte) Error {
	return &errorImpl{reason: reason, details: details}
}

// NewTimeoutError creates TimeoutError instance
func NewTimeoutError(timeoutType shared.TimeoutType) TimeoutError {
	return &timeoutError{timeoutType: timeoutType}
}

// NewCanceledErrorWithDetails creates CanceledError instance
func NewCanceledErrorWithDetails(details []byte) CanceledError {
	return &canceledError{details: details}
}

// NewCanceledError creates CanceledError instance
func NewCanceledError() CanceledError {
	return NewCanceledErrorWithDetails([]byte{})
}

// errorImpl implements Error
type errorImpl struct {
	reason  string
	details []byte
}

func (e *errorImpl) Error() string {
	return e.reason
}

// Reason is from Error interface
func (e *errorImpl) Reason() string {
	return e.reason
}

// Details is from Error interface
func (e *errorImpl) Details() []byte {
	return e.details
}

// timeoutError implements TimeoutError
type timeoutError struct {
	timeoutType shared.TimeoutType
}

// Error from error.Error
func (e *timeoutError) Error() string {
	return fmt.Sprintf("TimeoutType: %v", e.timeoutType)
}

func (e *timeoutError) TimeoutType() shared.TimeoutType {
	return e.timeoutType
}

type canceledError struct {
	details []byte
}

// Error from error.Error
func (e *canceledError) Error() string {
	return fmt.Sprintf("Details: %s", e.details)
}

// Details of the error
func (e *canceledError) Details() []byte {
	return e.details
}
