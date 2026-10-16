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

// WorkflowTaskFailureError can be returned from a PayloadCodec's Encode or
// Decode to fail the current Workflow Task instead of the Workflow Execution, so
// the Temporal Service can retry the task. It behaves the same under both
// WorkflowPanicPolicy values.
//
// During Workflow Task processing, it fails the task when returned for workflow
// input, the workflow result, Continue-As-New input, failure details, update
// results, marker data, summaries, and the input of activities, child workflows,
// Nexus operations, and external signals. Results of activities, child
// workflows, and Nexus operations fail the task from Future.Get, even if the
// workflow handles the error.
//
// Other workflow APIs that decode payloads, such as EncodedValues,
// GetLastCompletionResult, and ApplicationError.Details, return it to the
// workflow as an ordinary error. Signal, update argument, query, and memo
// handling are unchanged. Outside Workflow Task processing it is an ordinary
// error.
type WorkflowTaskFailureError struct {
	cause error
}

// Error returns the cause's message.
func (e *WorkflowTaskFailureError) Error() string {
	if e.cause == nil {
		return "workflow task failure requested by payload codec"
	}
	return e.cause.Error()
}

// Unwrap returns the cause, which may be nil.
func (e *WorkflowTaskFailureError) Unwrap() error {
	return e.cause
}

// NewWorkflowTaskFailureError wraps cause in a WorkflowTaskFailureError.
//
// Codec calls count against the Workflow Task timeout, so retry transient
// failures locally before returning this error.
func NewWorkflowTaskFailureError(cause error) error {
	return &WorkflowTaskFailureError{cause: cause}
}

// tagCodecRequestedTaskFailure tags markers at the codec boundary so one returned
// directly by workflow code is not honored.
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
