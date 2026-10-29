package converter

import commonconverter "go.temporal.io/sdk/internal/common/converter"

// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
// that need to be sent over the wire.
// To encode/decode workflow arguments, set DataConverter in client, through client.Options.
// To override DataConverter for specific activity or child workflow use workflow.WithDataConverter to create new Context,
// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
// For advanced data converters that may exceed the deadlock detection timeout
// for a workflow, such as ones making remote calls, use
// workflow.DataConverterWithoutDeadlockDetection.
type DataConverter = commonconverter.DataConverter
