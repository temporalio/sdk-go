package converter

import (
	"encoding/json"
	"fmt"
	"strings"

	commonpb "go.temporal.io/api/common/v1"
)

// JSONPayloadConverter converts to/from JSON.
type JSONPayloadConverter struct {
}

// NewJSONPayloadConverter creates new instance of JSONPayloadConverter.
func NewJSONPayloadConverter() *JSONPayloadConverter {
	return &JSONPayloadConverter{}
}

// ToPayload converts single value to payload.
func (c *JSONPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnableToEncode, err)
	}
	return newPayload(data, c), nil
}

// FromPayload converts single value from payload.
func (c *JSONPayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	err := json.Unmarshal(payload.GetData(), valuePtr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnableToDecode, err)
	}
	return nil
}

// ToString converts payload object into human readable string.
func (c *JSONPayloadConverter) ToString(payload *commonpb.Payload) string {
	var value interface{}
	err := c.FromPayload(payload, &value)
	if err != nil {
		return err.Error()
	}
	s := fmt.Sprintf("%+v", value)
	if strings.HasPrefix(s, "map[") {
		s = strings.TrimPrefix(s, "map[")
		s = strings.TrimSuffix(s, "]")
		s = fmt.Sprintf("{%s}", s)
	}

	return s
}

// Encoding returns metadataEncodingJSON.
func (c *JSONPayloadConverter) Encoding() string {
	return metadataEncodingJSON
}
