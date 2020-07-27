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
		value.Set(newProtoValue)                                         // Set newly created value back to passed valuePtr
	}

	err := protoUnmarshaler.Unmarshal(payload.GetData())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnableToDecode, err)
	}

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
