package converter

import (
	"errors"

	"go.temporal.io/sdk/internal/codecerror"
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
// on a workflow-side payload path the SDK routes through Workflow Task
// completion, requests that the current Workflow Task fail rather than the
// Workflow Execution, so the server retries the task while the execution stays
// open and a transient codec failure can recover. The honored paths are decoding
// workflow input, decoding an activity or child-workflow result delivered through
// a Future, encoding activity or child-workflow arguments, and encoding a side
// effect's summary. It is honored identically under both WorkflowPanicPolicy
// values and is not logged as a workflow panic.
//
// Other codec paths keep their existing behavior: signal decoding drops the
// signal, update-argument decoding rejects the update, and query decoding fails
// the query. On the client or in an activity worker the marker is an ordinary
// error. The wrapped cause is preserved through errors.Is and errors.As.
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

// tagCodecRequestedTaskFailure tags a *WorkflowTaskFailureError with the codec
// origin marker (see internal/codecerror), applied only at the PayloadCodec
// boundary so a marker returned directly from workflow code is never tagged.
func tagCodecRequestedTaskFailure(err error) error {
	if err == nil {
		return nil
	}
	var marker *WorkflowTaskFailureError
	if errors.As(err, &marker) {
		return codecerror.Tag(err)
	}
	return err
}
