// Package encoded contains wrappers that are used for binary payloads deserialization.
package encoded

import "go.temporal.io/temporal/internal"

type (

	// Value is used to encapsulate/extract encoded value from workflow/activity.
	Value = internal.Value

	// Values is used to encapsulate/extract encoded one or more values from workflow/activity.
	Values = internal.Values

	// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
	// that need to be sent over the wire.
	// To encode/decode workflow arguments, one should set DataConverter in two places:
	//   1. Workflow worker, through worker.Options
	//   2. Client, through client.Options
	// To encode/decode Activity/ChildWorkflow arguments, one should set DataConverter in two places:
	//   1. Inside workflow code, use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	//   2. Activity/Workflow worker that run these activity/childWorkflow, through worker.Options.
	DataConverter = internal.DataConverter
)

// GetDefaultDataConverter return default data converter used by Temporal worker
func GetDefaultDataConverter() DataConverter {
	return internal.DefaultDataConverter
}
