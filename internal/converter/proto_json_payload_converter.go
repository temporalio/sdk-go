package converter

import (
	"bytes"
	"fmt"
	"reflect"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/internal/common/util"
)

// ProtoJSONPayloadConverter converts proto objects to/from JSON.
type ProtoJSONPayloadConverter struct {
	marshaler   jsonpb.Marshaler
	unmarshaler jsonpb.Unmarshaler
}

// NewProtoJSONPayloadConverter creates new instance of ProtoJSONPayloadConverter.
func NewProtoJSONPayloadConverter() *ProtoJSONPayloadConverter {
	return &ProtoJSONPayloadConverter{
		marshaler:   jsonpb.Marshaler{},
		unmarshaler: jsonpb.Unmarshaler{},
	}
}

// ToPayload converts single proto value to payload.
func (c *ProtoJSONPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if valueProto, ok := value.(proto.Message); ok {
		var buf bytes.Buffer
		err := c.marshaler.Marshal(&buf, valueProto)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnableToEncode, err)
		}
		return newPayload(buf.Bytes(), c), nil
	}
	return nil, nil
}

// FromPayload converts single proto value from payload.
func (c *ProtoJSONPayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	value := reflect.ValueOf(valuePtr).Elem()
	if !value.CanSet() {
		return fmt.Errorf("type: %T: %w", valuePtr, ErrUnableToSetValue)
	}

	protoValue := value.Interface() // protoValue is of type i.e. *commonpb.WorkflowType
	protoMessage, ok := protoValue.(proto.Message)
	if !ok {
		return fmt.Errorf("value: %v of type: %T: %w", value, value, ErrValueDoesntImplementProtoMessage)
	}

	// If nil is passed create new instance
	if util.IsInterfaceNil(protoValue) {
		protoType := value.Type().Elem()                         // i.e. commonpb.WorkflowType
		newProtoValue := reflect.New(protoType)                  // is of type i.e. *commonpb.WorkflowType
		protoMessage = newProtoValue.Interface().(proto.Message) // type assertion will always succeed
	}

	err := c.unmarshaler.Unmarshal(bytes.NewReader(payload.GetData()), protoMessage)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnableToDecode, err)
	}

	value.Set(reflect.ValueOf(protoMessage))
	return nil
}

// ToString converts payload object into human readable string.
func (c *ProtoJSONPayloadConverter) ToString(payload *commonpb.Payload) string {
	// We can't do anything beter here.
	return string(payload.GetData())
}

// Encoding returns metadataEncodingProtoJSON.
func (c *ProtoJSONPayloadConverter) Encoding() string {
	return metadataEncodingProtoJSON
}
