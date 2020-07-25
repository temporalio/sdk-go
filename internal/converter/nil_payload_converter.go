package converter

import (
	"fmt"
	"reflect"

	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/internal/common/util"
)

// NilPayloadConverter doesn't set Data field in payload.
type NilPayloadConverter struct {
}

// NewNilPayloadConverter creates new instance of NilPayloadConverter.
func NewNilPayloadConverter() *NilPayloadConverter {
	return &NilPayloadConverter{}
}

// ToPayload converts single nil value to payload.
func (c *NilPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if util.IsInterfaceNil(value) {
		return newPayload(nil, c), nil
	}
	return nil, nil
}

// FromPayload converts single nil value from payload.
func (c *NilPayloadConverter) FromPayload(_ *commonpb.Payload, valuePtr interface{}) error {
	value := reflect.ValueOf(valuePtr).Elem()
	if !value.CanSet() {
		return fmt.Errorf("type: %T: %w", valuePtr, ErrUnableToSetValue)
	}
	value.Set(reflect.Zero(value.Type()))
	return nil
}

// ToString converts payload object into human readable string.
func (c *NilPayloadConverter) ToString(*commonpb.Payload) string {
	return "nil"
}

// Encoding returns metadataEncodingNil.
func (c *NilPayloadConverter) Encoding() string {
	return metadataEncodingNil
}
