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

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/api/common/v1"
)

const (
	metadataEncoding          = "encoding"
	metadataEncodingRaw       = "raw"
	metadataEncodingJSON      = "json"
	metadataEncodingNil       = "null"
	metadataEncodingProtoJSON = "protobuf-json"
	metadataEncodingProto     = "protobuf"
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
	// To encode/decode workflow arguments, one should set DataConverter in for Client, through client.Options.
	// To encode/decode Activity/ChildWorkflow arguments, one should set DataConverter in two places:
	//   1. Inside workflow code, use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	//   2. Activity/Workflow worker that run these activity/childWorkflow, through cleint.Options.
	DataConverter interface {
		// ToPayload single value to payload.
		ToPayload(value interface{}) (*commonpb.Payload, error)
		// FromPayload single value from payload.
		FromPayload(payload *commonpb.Payload, valuePtr interface{}) error

		// ToPayloads implements conversion of a list of values.
		ToPayloads(value ...interface{}) (*commonpb.Payloads, error)
		// FromPayloads implements conversion of an array of values of different types.
		// Useful for deserializing arguments of function invocations.
		FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error

		// ToString converts payload object into human readable string.
		ToString(input *commonpb.Payload) (string, error)
		// ToStrings converts payloads object into human readable strings.
		ToStrings(input *commonpb.Payloads) ([]string, error)
	}

	defaultDataConverter struct {
		payloadConverters map[string]PayloadConverter
		orderedEncodings  []string
	}
)

var (
	// DefaultDataConverter is default data converter used by Temporal worker.
	DefaultDataConverter = &defaultDataConverter{
		payloadConverters: map[string]PayloadConverter{
			metadataEncodingNil: &NilPayloadConverter{},
			metadataEncodingRaw: &ByteSlicePayloadConverter{},
			metadataEncodingProtoJSON: &ProtoJsonPayloadConverter{
				marshaler:   jsonpb.Marshaler{},
				unmarshaler: jsonpb.Unmarshaler{},
			},
			metadataEncodingJSON: &JsonPayloadConverter{},
		},
		orderedEncodings: []string{
			metadataEncodingNil,
			metadataEncodingRaw,
			metadataEncodingProtoJSON,
			metadataEncodingJSON,
		},
	}

	// ErrMetadataIsNotSet is returned when metadata is not set.
	ErrMetadataIsNotSet = errors.New("metadata is not set")
	// ErrEncodingIsNotSet is returned when payload encoding metadata is not set.
	ErrEncodingIsNotSet = errors.New("payload encoding metadata is not set")
	// ErrEncodingIsNotSupported is returned when payload encoding is not supported.
	ErrEncodingIsNotSupported = errors.New("payload encoding is not supported")
	// ErrUnableToEncodeJSON is returned when "unable to encode to JSON.
	ErrUnableToEncodeJSON = errors.New("unable to encode to JSON")
	// ErrUnableToDecodeJSON is returned when unable to decode JSON.
	ErrUnableToDecodeJSON = errors.New("unable to decode JSON")
	// ErrUnableToSetValue is returned when unable to set value.
	ErrUnableToSetValue = errors.New("unable to set value")
	// ErrUnableToFindConverter is returned when unable to find converter.
	ErrUnableToFindConverter = errors.New("unable to find converter")
	// ErrValueDoesntImplementProtoMessage is returned when value doesn't implement proto.Message.
	ErrValueDoesntImplementProtoMessage = errors.New("value doesn't implement proto.Message")
)

// getDefaultDataConverter return default data converter used by Temporal worker.
func getDefaultDataConverter() DataConverter {
	return DefaultDataConverter
}

func (dc *defaultDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
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

func (dc *defaultDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
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

func (dc *defaultDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
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

func (dc *defaultDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	if payload == nil {
		return nil
	}

	encoding, err := getEncoding(payload)
	if err != nil {
		return err
	}

	payloadConverter, ok := dc.payloadConverters[encoding]
	if !ok {
		return fmt.Errorf("encoding %s: %w", encoding, ErrEncodingIsNotSupported)
	}

	return payloadConverter.FromPayload(payload, valuePtr)
}

func (dc *defaultDataConverter) ToString(payload *commonpb.Payload) (string, error) {
	result := ""

	if payload == nil {
		return result, nil
	}

	encoding, err := getEncoding(payload)
	if err != nil {
		return result, err
	}

	payloadConverter, ok := dc.payloadConverters[encoding]
	if !ok {
		return "", fmt.Errorf("encoding %s: %w", encoding, ErrEncodingIsNotSupported)
	}

	return payloadConverter.ToString(payload), nil
}

func (dc *defaultDataConverter) ToStrings(payloads *commonpb.Payloads) ([]string, error) {
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

func getEncoding(payload *commonpb.Payload) (string, error) {
	metadata := payload.GetMetadata()
	if metadata == nil {
		return "", ErrMetadataIsNotSet
	}

	if e, ok := metadata[metadataEncoding]; ok {
		return string(e), nil
	}

	return "", ErrEncodingIsNotSet
}

type PayloadConverter interface {
	// ToPayload single value to payload.
	ToPayload(value interface{}) (*commonpb.Payload, error)
	// FromPayload single value from payload.
	FromPayload(payload *commonpb.Payload, valuePtr interface{}) error
	// ToString converts payloads object into human readable string.
	ToString(*commonpb.Payload) string
}

type ByteSlicePayloadConverter struct {
}

func (c *ByteSlicePayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if bytes, isByteSlice := value.([]byte); isByteSlice {
		payload := &commonpb.Payload{
			Metadata: map[string][]byte{
				metadataEncoding: []byte(metadataEncodingRaw),
			},
			Data: bytes,
		}

		return payload, nil
	}

	return nil, nil
}

func (c *ByteSlicePayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	valueBytes := reflect.ValueOf(valuePtr).Elem()
	if !valueBytes.CanSet() {
		return ErrUnableToSetValue
	}
	valueBytes.SetBytes(payload.GetData())
	return nil
}

func (c *ByteSlicePayloadConverter) ToString(payload *commonpb.Payload) string {
	var byteSlice []byte
	err := c.FromPayload(payload, &byteSlice)
	if err != nil {
		return err.Error()
	}
	return base64.RawStdEncoding.EncodeToString(byteSlice)
}

type JsonPayloadConverter struct {
}

func (c *JsonPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnableToEncodeJSON, err)
	}
	payload := &commonpb.Payload{
		Metadata: map[string][]byte{
			metadataEncoding: []byte(metadataEncodingJSON),
		},
		Data: data,
	}
	return payload, nil
}

func (c *JsonPayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	err := json.Unmarshal(payload.GetData(), valuePtr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnableToDecodeJSON, err)
	}
	return nil
}

func (c *JsonPayloadConverter) ToString(payload *commonpb.Payload) string {
	var value interface{}
	err := c.FromPayload(payload, &value)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%+v", value)
}

type NilPayloadConverter struct {
}

func (c *NilPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if isInterfaceNil(value) {
		payload := &commonpb.Payload{
			Metadata: map[string][]byte{
				metadataEncoding: []byte(metadataEncodingNil),
			},
			Data: nil,
		}
		return payload, nil
	}
	return nil, nil
}

func (c *NilPayloadConverter) FromPayload(_ *commonpb.Payload, valuePtr interface{}) error {
	value := reflect.ValueOf(valuePtr).Elem()
	if !value.CanSet() {
		return ErrUnableToSetValue
	}
	value.Set(reflect.Zero(value.Type()))
	return nil
}

func (c *NilPayloadConverter) ToString(*commonpb.Payload) string {
	return "nil"
}

type ProtoJsonPayloadConverter struct {
	marshaler   jsonpb.Marshaler
	unmarshaler jsonpb.Unmarshaler
}

func (c *ProtoJsonPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if valueProto, ok := value.(proto.Message); ok {
		var buf bytes.Buffer
		err := c.marshaler.Marshal(&buf, valueProto)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnableToEncodeJSON, err)
		}
		payload := &commonpb.Payload{
			Metadata: map[string][]byte{
				metadataEncoding: []byte(metadataEncodingProtoJSON),
			},
			Data: buf.Bytes(),
		}
		return payload, nil
	}
	return nil, nil
}

func (c *ProtoJsonPayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	value := reflect.ValueOf(valuePtr).Elem()
	if !value.CanSet() {
		return ErrUnableToSetValue
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
		return fmt.Errorf("%w: %v", ErrUnableToDecodeJSON, err)
	}

	value.Set(reflect.ValueOf(protoMessage))
	return nil
}

func (c *ProtoJsonPayloadConverter) ToString(payload *commonpb.Payload) string {
	var value interface{}
	err := c.FromPayload(payload, &value)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%+v", value)
}
