package converter

import (
	"errors"
)

var (
	// ErrMetadataIsNotSet is returned when metadata is not set.
	ErrMetadataIsNotSet = errors.New("metadata is not set")
	// ErrEncodingIsNotSet is returned when payload encoding metadata is not set.
	ErrEncodingIsNotSet = errors.New("payload encoding metadata is not set")
	// ErrEncodingIsNotSupported is returned when payload encoding is not supported.
	ErrEncodingIsNotSupported = errors.New("payload encoding is not supported")
	// ErrUnableToEncode is returned when unable to encode.
	ErrUnableToEncode = errors.New("unable to encode")
	// ErrUnableToDecode is returned when unable to decode.
	ErrUnableToDecode = errors.New("unable to decode")
	// ErrUnableToSetValue is returned when unable to set value.
	ErrUnableToSetValue = errors.New("unable to set value")
	// ErrUnableToFindConverter is returned when unable to find converter.
	ErrUnableToFindConverter = errors.New("unable to find converter")
	// ErrTypeNotImplementProtoMessage is returned when value doesn't implement proto.Message.
	ErrTypeNotImplementProtoMessage = errors.New("type doesn't implement proto.Message")
	// ErrValuePtrIsNotPointer is returned when proto value is not a pointer.
	ErrValuePtrIsNotPointer = errors.New("not a pointer type")
	// ErrValuePtrMustConcreteType is returned when proto value is of interface type.
	ErrValuePtrMustConcreteType = errors.New("must be a concrete type, not interface")
	// ErrTypeIsNotByteSlice is returned when value is not of *[]byte type.
	ErrTypeIsNotByteSlice = errors.New("type is not *[]byte")
)

// WorkflowTaskFailureError, when returned from a PayloadCodec's Encode or
// Decode while the SDK is running workflow-side code (for example decoding a
// workflow's input arguments, or decoding an activity result returned from a
// workflow Future), signals the SDK to fail the current Workflow Task instead
// of the Workflow Execution. The current Workflow Task is failed and retried by
// the server while the Workflow Execution stays open, so a transient codec
// failure can recover on a later task attempt.
//
// This is an opt-in backstop for codec authors who already retry transient
// failures inside their codec: return a WorkflowTaskFailureError only once those
// in-codec retries are exhausted, and only for failures that are genuinely
// transient. Codec calls consume the Workflow Task timeout budget, so in-codec
// retry should remain the primary recovery mechanism.
//
// The marker is honored independently of the worker's WorkflowPanicPolicy and
// is not classified or logged as a workflow panic. When returned from a codec
// running in an Activity worker or on the client, it behaves as an ordinary
// returned error and does not trigger any special routing.
//
// The wrapped Cause is preserved through errors.Is and errors.As so failure
// converters and observability tooling still see the original error.
type WorkflowTaskFailureError struct {
	// Cause is the underlying, transient error that should be treated as a
	// task-level failure. It must not be nil.
	Cause error
}

// Error implements the error interface, delegating to the wrapped Cause.
func (e *WorkflowTaskFailureError) Error() string {
	if e.Cause == nil {
		return "workflow task failure requested by payload codec"
	}
	return e.Cause.Error()
}

// Unwrap returns the wrapped Cause so errors.Is and errors.As traverse it.
func (e *WorkflowTaskFailureError) Unwrap() error {
	return e.Cause
}

// NewWorkflowTaskFailureError wraps cause in a WorkflowTaskFailureError. Return
// the result from a PayloadCodec's Encode or Decode to request that the current
// Workflow Task fail rather than the Workflow Execution. See
// WorkflowTaskFailureError for the full contract.
func NewWorkflowTaskFailureError(cause error) error {
	return &WorkflowTaskFailureError{Cause: cause}
}
