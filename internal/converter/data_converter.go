package converter

import (
	commonpb "go.temporal.io/api/common/v1"
)

type (
	// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
	// that need to be sent over the wire.
	// To encode/decode workflow arguments, set DataConverter in client, through client.Options.
	// To override DataConverter for specific activity or child workflow use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	DataConverter interface {
		// ToPayload converts single value to payload.
		ToPayload(value interface{}) (*commonpb.Payload, error)
		// FromPayload converts single value from payload.
		FromPayload(payload *commonpb.Payload, valuePtr interface{}) error

		// ToPayloads converts a list of values.
		ToPayloads(value ...interface{}) (*commonpb.Payloads, error)
		// FromPayloads converts to a list of values of different types.
		// Useful for deserializing arguments of function invocations.
		FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error

		// ToString converts payload object into human readable string.
		ToString(input *commonpb.Payload) string
		// ToStrings converts payloads object into human readable strings.
		ToStrings(input *commonpb.Payloads) []string
	}
)
