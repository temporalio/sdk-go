package converter

import commonpb "go.temporal.io/api/common/v1"

// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
// that need to be sent over the wire.
//
// Exposed as: [go.temporal.io/sdk/converter.DataConverter]
type DataConverter interface {
	// ToPayload converts single value to payload.
	//
	// Note: When value is of RawValue type, encoding should occur, but data conversion must be skipped.
	ToPayload(value any) (*commonpb.Payload, error)
	// FromPayload converts single value from payload.
	//
	// Note, values should not be reused for extraction here because merging on
	// top of existing values may result in unexpected behavior similar to
	// json.Unmarshal.
	//
	// Note: When valuePtr is of RawValue type, decryption should occur but data conversion must be skipped.
	FromPayload(payload *commonpb.Payload, valuePtr any) error
	// ToPayloads converts a list of values.
	ToPayloads(value ...any) (*commonpb.Payloads, error)
	// FromPayloads converts to a list of values of different types.
	// Useful for deserializing arguments of function invocations.
	//
	// Note, values should not be reused for extraction here because merging on
	// top of existing values may result in unexpected behavior similar to
	// json.Unmarshal.
	FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error
	// ToString converts payload object into human readable string.
	ToString(input *commonpb.Payload) string
	// ToStrings converts payloads object into human readable strings.
	ToStrings(input *commonpb.Payloads) []string
}

// PayloadCodec is exposed as [go.temporal.io/sdk/converter.PayloadCodec].
type PayloadCodec interface {
	// Encode optionally encodes non-nil payloads without mutating them.
	Encode([]*commonpb.Payload) ([]*commonpb.Payload, error)
	// Decode optionally decodes non-nil payloads without mutating them.
	// For compatibility, implementations should not decode unencoded payloads.
	Decode([]*commonpb.Payload) ([]*commonpb.Payload, error)
}

// PayloadCodecProvider is exposed as [go.temporal.io/sdk/converter.PayloadCodecProvider].
type PayloadCodecProvider interface {
	Codec() PayloadCodec
}
