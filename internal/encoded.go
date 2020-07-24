// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/api/common/v1"
)

const (
	metadataEncoding          = "encoding"
	metadataEncodingRaw       = "binary/raw"
	metadataEncodingJSON      = "json/plain"
	metadataEncodingNil       = "binary/null"
	metadataEncodingProtoJSON = "json/protobuf"
	metadataEncodingProto     = "binary/protobuf"
)

type (
	// Value is used to encapsulate/extract encoded value from workflow/activity.
	Value interface {
		// HasValue return whether there is value encoded.
		HasValue() bool
		// Get extract the encoded value into strong typed value pointer.
		Get(valuePtr interface{}) error
	}

	// Values is used to encapsulate/extract encoded one or more values from workflow/activity.
	Values interface {
		// HasValues return whether there are values encoded.
		HasValues() bool
		// Get extract the encoded values into strong typed value pointers.
		Get(valuePtr ...interface{}) error
	}

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
		ToString(input *commonpb.Payload) (string, error)
		// ToStrings converts payloads object into human readable strings.
		ToStrings(input *commonpb.Payloads) ([]string, error)
	}

	// CompositeDataConverter applies PayloadConverters in specified order.
	CompositeDataConverter struct {
		payloadConverters map[string]PayloadConverter
		orderedEncodings  []string
	}
)

var (
	// DefaultDataConverter is default data converter used by Temporal worker.
	DefaultDataConverter = NewCompositeDataConverter(
		NewNilPayloadConverter(),
		NewByteSlicePayloadConverter(),
		// Only one proto converter should be used.
		// Although they check for different interfaces (proto.Message and proto.Marshaler) all proto messages implements both interfaces.
		NewProtoJSONPayloadConverter(),
		// NewProtoPayloadConverter(),
		NewJSONPayloadConverter(),
	)

	// ErrMetadataIsNotSet is returned when metadata is not set.
	ErrMetadataIsNotSet = errors.New("metadata is not set")
	// ErrEncodingIsNotSet is returned when payload encoding metadata is not set.
	ErrEncodingIsNotSet = errors.New("payload encoding metadata is not set")
	// ErrEncodingIsNotSupported is returned when payload encoding is not supported.
	ErrEncodingIsNotSupported = errors.New("payload encoding is not supported")
	// ErrUnableToEncode is returned when unable to encode.
	ErrUnableToEncode = errors.New("unable to encode")
	// ErrUnableToDecode is returned when unable to decode.
	ErrUnableToDecode = errors.New("unable to decode")
	// ErrUnableToSetValue is returned when unable to set value.
	ErrUnableToSetValue = errors.New("unable to set value")
	// ErrUnableToFindConverter is returned when unable to find converter.
	ErrUnableToFindConverter = errors.New("unable to find converter")
	// ErrValueDoesntImplementProtoMessage is returned when value doesn't implement proto.Message.
	ErrValueDoesntImplementProtoMessage = errors.New("value doesn't implement proto.Message")
	// ErrValueDoesntImplementProtoUnmarshaler is returned when value doesn't implement proto.Unmarshaler.
	ErrValueDoesntImplementProtoUnmarshaler = errors.New("value doesn't implement proto.Unmarshaler")
)

// getDefaultDataConverter return default data converter used by Temporal worker.
// TODO: remove this func
func getDefaultDataConverter() DataConverter {
	return DefaultDataConverter
}

// NewCompositeDataConverter creates new instance of CompositeDataConverter from ordered list of PayloadConverters.
// Order is important here because during serialization DataConverter will try PayloadsConverters in
// that order until PayloadConverter returns non nil payload.
// Last PayloadConverter should always serialize the value (JSONPayloadConverter is good candidate for it),
func NewCompositeDataConverter(payloadConverters ...PayloadConverter) *CompositeDataConverter {
	dc := &CompositeDataConverter{
		payloadConverters: make(map[string]PayloadConverter, len(payloadConverters)),
		orderedEncodings:  make([]string, len(payloadConverters)),
	}

	for i, payloadConverter := range payloadConverters {
		dc.payloadConverters[payloadConverter.Encoding()] = payloadConverter
		dc.orderedEncodings[i] = payloadConverter.Encoding()
	}

	return dc
}

// ToPayloads converts a list of values.
func (dc *CompositeDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	if len(values) == 0 {
		return nil, nil
	}

	result := &commonpb.Payloads{}
	for i, value := range values {
		payload, err := dc.ToPayload(value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}

		result.Payloads = append(result.Payloads, payload)
	}

	return result, nil
}

// FromPayloads converts to a list of values of different types.
func (dc *CompositeDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return nil
	}

	for i, payload := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}

		err := dc.FromPayload(payload, valuePtrs[i])
		if err != nil {
			return fmt.Errorf("payload item %d: %w", i, err)
		}
	}

	return nil
}

// ToPayload converts single value to payload.
func (dc *CompositeDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	for _, encoding := range dc.orderedEncodings {
		payloadConverter := dc.payloadConverters[encoding]
		payload, err := payloadConverter.ToPayload(value)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			return payload, nil
		}
	}

	return nil, fmt.Errorf("value: %v of type: %T: %w", value, value, ErrUnableToFindConverter)
}

// FromPayload converts single value from payload.
func (dc *CompositeDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	if payload == nil {
		return nil
	}

	encoding, err := encoding(payload)
	if err != nil {
		return err
	}

	payloadConverter, ok := dc.payloadConverters[encoding]
	if !ok {
		return fmt.Errorf("encoding %s: %w", encoding, ErrEncodingIsNotSupported)
	}

	return payloadConverter.FromPayload(payload, valuePtr)
}

// ToString converts payload object into human readable string.
func (dc *CompositeDataConverter) ToString(payload *commonpb.Payload) (string, error) {
	result := ""

	if payload == nil {
		return result, nil
	}

	encoding, err := encoding(payload)
	if err != nil {
		return result, err
	}

	payloadConverter, ok := dc.payloadConverters[encoding]
	if !ok {
		return "", fmt.Errorf("encoding %s: %w", encoding, ErrEncodingIsNotSupported)
	}

	return payloadConverter.ToString(payload), nil
}

// ToStrings converts payloads object into human readable strings.
func (dc *CompositeDataConverter) ToStrings(payloads *commonpb.Payloads) ([]string, error) {
	if payloads == nil {
		return nil, nil
	}

	var result []string
	for i, payload := range payloads.GetPayloads() {
		payloadStr, err := dc.ToString(payload)
		if err != nil {
			return result, fmt.Errorf("payload item %d: %w", i, err)
		}
		result = append(result, payloadStr)
	}

	return result, nil
}

func encoding(payload *commonpb.Payload) (string, error) {
	metadata := payload.GetMetadata()
	if metadata == nil {
		return "", ErrMetadataIsNotSet
	}

	if e, ok := metadata[metadataEncoding]; ok {
		return string(e), nil
	}

	return "", ErrEncodingIsNotSet
}

func newPayload(data []byte, c PayloadConverter) *commonpb.Payload {
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			metadataEncoding: []byte(c.Encoding()),
		},
		Data: data,
	}
}

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

// ByteSlicePayloadConverter pass through []byte to Data field in payload.
type ByteSlicePayloadConverter struct {
}

// NewByteSlicePayloadConverter creates new instance of ByteSlicePayloadConverter.
func NewByteSlicePayloadConverter() *ByteSlicePayloadConverter {
	return &ByteSlicePayloadConverter{}
}

// ToPayload converts single []byte value to payload.
func (c *ByteSlicePayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if valueBytes, isByteSlice := value.([]byte); isByteSlice {
		return newPayload(valueBytes, c), nil
	}

	return nil, nil
}

// FromPayload converts single []byte value from payload.
func (c *ByteSlicePayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	valueBytes := reflect.ValueOf(valuePtr).Elem()
	if !valueBytes.CanSet() {
		return fmt.Errorf("type: %T: %w", valuePtr, ErrUnableToSetValue)
	}
	valueBytes.SetBytes(payload.GetData())
	return nil
}

// ToString converts payload object into human readable string.
func (c *ByteSlicePayloadConverter) ToString(payload *commonpb.Payload) string {
	var byteSlice []byte
	err := c.FromPayload(payload, &byteSlice)
	if err != nil {
		return err.Error()
	}
	return base64.RawStdEncoding.EncodeToString(byteSlice)
}

// Encoding returns metadataEncodingRaw.
func (c *ByteSlicePayloadConverter) Encoding() string {
	return metadataEncodingRaw
}

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

// NilPayloadConverter doesn't set Data field in payload.
type NilPayloadConverter struct {
}

// NewNilPayloadConverter creates new instance of NilPayloadConverter.
func NewNilPayloadConverter() *NilPayloadConverter {
	return &NilPayloadConverter{}
}

// ToPayload converts single nil value to payload.
func (c *NilPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if isInterfaceNil(value) {
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
	if isInterfaceNil(protoValue) {
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
	return string(payload.GetData())
}

// Encoding returns metadataEncodingProtoJSON.
func (c *ProtoJSONPayloadConverter) Encoding() string {
	return metadataEncodingProtoJSON
}

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
	if isInterfaceNil(protoValue) {
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
	return base64.RawStdEncoding.EncodeToString(payload.GetData())
}

// Encoding returns metadataEncodingProto.
func (c *ProtoPayloadConverter) Encoding() string {
	return metadataEncodingProto
}
