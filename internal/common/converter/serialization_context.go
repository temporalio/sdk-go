package converter

// SerializationContext provides metadata about where serialization is occurring.
//
// Exposed as: [go.temporal.io/sdk/converter.SerializationContext]
type SerializationContext interface {
	isSerializationContext()
}

// WorkflowSerializationContext is the serialization context for workflow-level payloads.
//
// Exposed as: [go.temporal.io/sdk/converter.WorkflowSerializationContext]
type WorkflowSerializationContext struct {
	Namespace  string
	WorkflowID string
}

func (WorkflowSerializationContext) isSerializationContext() {}

// ActivitySerializationContext is the serialization context for activity-level payloads.
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
// Exposed as: [go.temporal.io/sdk/converter.DataConverterWithSerializationContext]
type DataConverterWithSerializationContext interface {
	WithSerializationContext(SerializationContext) DataConverter
}

// WithDataConverterSerializationContext returns a DataConverter that is aware of the given
// serialization context.
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
