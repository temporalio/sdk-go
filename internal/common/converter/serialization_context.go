package converter

// SerializationContext provides metadata about where serialization is occurring,
// with the concrete type depending on the context:
//
//   - [WorkflowSerializationContext] for workflow-level payloads.
//   - [ActivitySerializationContext] for activity-level payloads.
//   - [NexusSerializationContext] for Nexus-level payloads.
//
// Exposed as: [go.temporal.io/sdk/converter.SerializationContext]
type SerializationContext interface {
	isSerializationContext()
}

// WorkflowSerializationContext is the serialization context for workflow-level payloads.
// This includes: workflow input/result, child workflow input/result, signal input,
// query input/result, update input/result, memo, continue-as-new args, and
// external signal payloads.
//
// For child workflows, WorkflowID is the child's ID, not the parent's.
// For external signals, WorkflowID is the target workflow's ID.
//
// Exposed as: [go.temporal.io/sdk/converter.WorkflowSerializationContext]
type WorkflowSerializationContext struct {
	Namespace  string
	WorkflowID string
}

func (WorkflowSerializationContext) isSerializationContext() {}

// ActivitySerializationContext is the serialization context for activity-level payloads.
// This includes: activity input/result, heartbeat details, and activity failure details.
//
// Exposed as: [go.temporal.io/sdk/converter.ActivitySerializationContext]
type ActivitySerializationContext struct {
	Namespace string
	// Empty for a standalone activity.
	WorkflowID string
	// Empty for a standalone activity.
	WorkflowType string
	ActivityType string
	TaskQueue    string
	IsLocal      bool
}

func (ActivitySerializationContext) isSerializationContext() {}

// NexusSerializationContext is used when serializing Nexus operation payloads.
// Callers receive it when encoding inputs and decoding results or failures.
// Handlers receive it when decoding inputs, encoding synchronous results, and
// encoding failures produced while handling a Nexus task.
//
// Operation is the resolved operation name. The context is not propagated to
// the eventual result of an asynchronous operation. Standalone operation handles
// use the context of their start request, including when an existing operation is
// returned, while handles created without starting an operation do not receive it.
//
// For failures, callers receive this context in [FailureConverter.FailureToError],
// while handlers receive it in [FailureConverter.ErrorToFailure]. Implementations
// must not assume symmetric failure conversion. Context-dependent encodings should
// be self-describing and support legacy payloads without context.
//
// NOTE: Experimental
//
// Exposed as: [go.temporal.io/sdk/converter.NexusSerializationContext]
type NexusSerializationContext struct {
	// Endpoint is the Nexus endpoint name.
	Endpoint string
	// Service is the Nexus service name.
	Service string
	// Operation is the resolved Nexus operation name.
	Operation string
}

func (NexusSerializationContext) isSerializationContext() {}

// DataConverterWithSerializationContext is an optional interface that [DataConverter]
// implementations can implement to receive serialization context.
//
// When implemented, the SDK calls WithSerializationContext before serializing/deserializing
// payloads. The returned DataConverter should use the context to vary its behavior
// (e.g. using workflow ID as associated data for encryption).
//
// Implementations must work correctly without context — the SDK and user code may use
// the DataConverter directly without calling WithSerializationContext first.
//
// This method should be cheap and fast. The SDK does not cache returned instances
// and may call this method frequently. Avoid recreating expensive objects on every call.
//
// Exposed as: [go.temporal.io/sdk/converter.DataConverterWithSerializationContext]
type DataConverterWithSerializationContext interface {
	WithSerializationContext(SerializationContext) DataConverter
}

// WithDataConverterSerializationContext returns a DataConverter that is aware of the given
// serialization context. If the DataConverter implements
// [DataConverterWithSerializationContext], it delegates to that implementation;
// otherwise it returns the original DataConverter unchanged.
//
// Exposed as: [go.temporal.io/sdk/converter.WithDataConverterSerializationContext]
func WithDataConverterSerializationContext(dc DataConverter, ctx SerializationContext) DataConverter {
	if sc, ok := dc.(DataConverterWithSerializationContext); ok {
		result := sc.WithSerializationContext(ctx)
		if result == nil {
			panic("DataConverterWithSerializationContext.WithSerializationContext must not return nil")
		}
		return result
	}
	return dc
}
