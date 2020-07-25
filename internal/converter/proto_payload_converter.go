package converter

import (
	"encoding/base64"
	"fmt"
	"reflect"

	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/internal/common/util"
)

// ProtoPayloadConverter converts proto objects to protobuf binary format.
type ProtoPayloadConverter struct {
}

// NewProtoPayloadConverter creates new instance of ProtoPayloadConverter.
func NewProtoPayloadConverter() *ProtoPayloadConverter {
	return &ProtoPayloadConverter{}
}

// ToPayload converts single proto value to payload.
func (c *ProtoPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if valueProto, ok := value.(proto.Marshaler); ok {
		data, err := valueProto.Marshal()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnableToEncode, err)
		}
		return newPayload(data, c), nil
	}
	return nil, nil
}

// FromPayload converts single proto value from payload.
func (c *ProtoPayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	value := reflect.ValueOf(valuePtr).Elem()
	if !value.CanSet() {
		return fmt.Errorf("type: %T: %w", valuePtr, ErrUnableToSetValue)
	}

	protoValue := value.Interface() // protoValue is of type i.e. *commonpb.WorkflowType
	protoUnmarshaler, ok := protoValue.(proto.Unmarshaler)
	if !ok {
		return fmt.Errorf("value: %v of type: %T: %w", value, value, ErrValueDoesntImplementProtoUnmarshaler)
	}

	// If nil is passed create new instance
	if util.IsInterfaceNil(protoValue) {
		protoType := value.Type().Elem()                                 // i.e. commonpb.WorkflowType
		newProtoValue := reflect.New(protoType)                          // is of type i.e. *commonpb.WorkflowType
		protoUnmarshaler = newProtoValue.Interface().(proto.Unmarshaler) // type assertion will always succeed
	}

	err := protoUnmarshaler.Unmarshal(payload.GetData())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnableToDecode, err)
	}

	value.Set(reflect.ValueOf(protoUnmarshaler))
	return nil
}

// ToString converts payload object into human readable string.
func (c *ProtoPayloadConverter) ToString(payload *commonpb.Payload) string {
	// We can't do anything beter here.
	return base64.RawStdEncoding.EncodeToString(payload.GetData())
}

// Encoding returns metadataEncodingProto.
func (c *ProtoPayloadConverter) Encoding() string {
	return metadataEncodingProto
}
