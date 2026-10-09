package converter

import commonpb "go.temporal.io/api/common/v1"

// DataConverter serializes and deserializes workflow and activity payloads.
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
	// ToPayloads converts list of values.
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
	// ToStrings converts payload objects into human readable strings.
	ToStrings(input *commonpb.Payloads) []string
}
