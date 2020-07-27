package converter

import (
	commonpb "go.temporal.io/api/common/v1"
)

// PayloadConverter is an interface to convert a single payload.
type PayloadConverter interface {
	// ToPayload converts single value to payload. It should return nil if the PayloadConveter can not convert passed value (i.e. type is unknown).
	ToPayload(value interface{}) (*commonpb.Payload, error)
	// FromPayload converts single value from payload. valuePtr should be a reference to the variable of the type that is corresponding for payload encoding.
	// Otherwise it should return error.
	FromPayload(payload *commonpb.Payload, valuePtr interface{}) error
	// ToString converts payload object into human readable string.
	ToString(*commonpb.Payload) string

	// Encoding returns encoding supported by PayloadConverter.
	Encoding() string
}

func newPayload(data []byte, c PayloadConverter) *commonpb.Payload {
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			metadataEncoding: []byte(c.Encoding()),
		},
		Data: data,
	}
}
