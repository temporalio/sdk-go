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

// WorkflowTaskFailureError, when returned from a PayloadCodec's Encode or Decode
// during workflow-side payload processing (for example decoding a workflow's
// input or an activity result, or encoding activity arguments), requests that
// the current Workflow Task fail rather than the Workflow Execution. The task is
// failed and retried by the server while the execution stays open, so a
// transient codec failure can recover on a later attempt. It is honored
// independently of the worker's WorkflowPanicPolicy and is not classified or
// logged as a workflow panic.
//
// Outside workflow-side codec processing (on the client or in an activity
// worker) it behaves as an ordinary error. A codec that keeps returning it will
// keep failing the Workflow Task, so it should back a genuinely transient
// failure. The wrapped cause is preserved through errors.Is and errors.As so
// failure converters and observability tooling still see the original error.
type WorkflowTaskFailureError struct {
	cause error
}

// Error implements the error interface, delegating to the wrapped cause.
func (e *WorkflowTaskFailureError) Error() string {
	if e.cause == nil {
		return "workflow task failure requested by payload codec"
	}
	return e.cause.Error()
}

// Unwrap returns the wrapped cause so errors.Is and errors.As traverse it. It
// may be nil.
func (e *WorkflowTaskFailureError) Unwrap() error {
	return e.cause
}

// NewWorkflowTaskFailureError wraps cause in a WorkflowTaskFailureError. Return
// the result from a PayloadCodec's Encode or Decode to request that the current
// Workflow Task fail rather than the Workflow Execution. Codec calls consume the
// Workflow Task timeout budget, so a codec should retry a transient failure
// internally first and return this only once those bounded retries are
// exhausted. See WorkflowTaskFailureError for the full contract.
func NewWorkflowTaskFailureError(cause error) error {
	return &WorkflowTaskFailureError{cause: cause}
}

// codecRequestedTaskFailure wraps a WorkflowTaskFailureError that a PayloadCodec
// returned from Encode or Decode. CodecDataConverter applies it at the codec
// boundary (see tagCodecRequestedTaskFailure) so the SDK can tell a codec-originated
// request apart from a WorkflowTaskFailureError returned directly by workflow code.
// It unwraps to the original error so errors.Is/As and failure conversion are
// unaffected. The type is unexported; the SDK detects it via the exported method
// below, so nothing new appears in the public API.
type codecRequestedTaskFailure struct {
	err error
}

func (e *codecRequestedTaskFailure) Error() string { return e.err.Error() }
func (e *codecRequestedTaskFailure) Unwrap() error { return e.err }

// RequestsWorkflowTaskFailure identifies this error, to the SDK worker, as a
// PayloadCodec's request to fail the current Workflow Task. It is defined on an
// unexported type on purpose so it does not widen the public API surface.
func (e *codecRequestedTaskFailure) RequestsWorkflowTaskFailure() bool { return true }

// tagCodecRequestedTaskFailure wraps err in a codecRequestedTaskFailure when err
// is, or wraps, a *WorkflowTaskFailureError, and returns it unchanged otherwise
// (including nil). It is applied only at the PayloadCodec boundary so a marker
// returned directly from workflow code is never tagged.
func tagCodecRequestedTaskFailure(err error) error {
	if err == nil {
		return nil
	}
	var marker *WorkflowTaskFailureError
	if errors.As(err, &marker) {
		return &codecRequestedTaskFailure{err: err}
	}
	return err
}
