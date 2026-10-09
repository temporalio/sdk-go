package converter

// SerializationContext identifies where payload serialization is occurring.
type SerializationContext interface {
	isSerializationContext()
}

// WorkflowSerializationContext identifies workflow-level payload serialization.
type WorkflowSerializationContext struct {
	Namespace  string
	WorkflowID string
}

func (WorkflowSerializationContext) isSerializationContext() {}

// ActivitySerializationContext identifies activity-level payload serialization.
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

// NexusSerializationContext identifies Nexus operation payload serialization.
type NexusSerializationContext struct {
	// Endpoint is the Nexus endpoint name.
	Endpoint string
	// Service is the Nexus service name.
	Service string
	// Operation is the resolved Nexus operation name.
	Operation string
}

func (NexusSerializationContext) isSerializationContext() {}

// DataConverterWithSerializationContext optionally binds a data converter to a
// serialization context.
type DataConverterWithSerializationContext interface {
	WithSerializationContext(SerializationContext) DataConverter
}

// WithDataConverterSerializationContext applies ctx when dc supports it.
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
