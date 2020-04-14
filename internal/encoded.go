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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/temporal-proto/common"
)

const (
	encodingMetadata      = "encoding"
	encodingMetadataRaw   = "raw"
	encodingMetadataJson  = "json"
	encodingMetadataGob   = "gob"
	encodingMetadataProto = "proto"

	nameMetadata = "name"
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
	// To encode/decode workflow arguments, one should set DataConverter in two places:
	//   1. Workflow worker, through worker.Options
	//   2. Client, through client.Options
	// To encode/decode Activity/ChildWorkflow arguments, one should set DataConverter in two places:
	//   1. Inside workflow code, use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	//   2. Activity/Workflow worker that run these activity/childWorkflow, through worker.Options.
	DataConverter interface {
		// ToData implements conversion of a list of values.
		ToData(value ...interface{}) (*commonpb.Payload, error)
		// FromData implements conversion of an array of values of different types.
		// Useful for deserializing arguments of function invocations.
		FromData(input *commonpb.Payload, valuePtr ...interface{}) error
	}

	// defaultDataConverter uses JSON.
	defaultDataConverter struct{}

	NameValuePair struct {
		Name  string
		Value interface{}
	}
)

var (
	// DefaultDataConverter is default data converter used by Temporal worker
	DefaultDataConverter = &defaultDataConverter{}

	ErrMetadataIsNotSet       = errors.New("metadata is not set")
	ErrEncodingIsNotSet       = errors.New("payload encoding metadata is not set")
	ErrEncodingIsNotSupported = errors.New("payload encoding metadata is not supported")
	ErrUnableToEncodeJSON     = errors.New("unable to encode to JSON")
	ErrUnableToEncodeProto    = errors.New("unable to encode to protobuf")
	ErrUnableToDecodeJSON     = errors.New("unable to decode JSON")
	ErrUnableToDecodeProto    = errors.New("unable to decode protobuf")
	ErrUnableToSet            = errors.New("unable to set []byte value")
	ErrValuePtrIsNotProto     = fmt.Errorf("payload has encoding %q but value pointer doesn't implement \"proto.Unmarshaler\"", encodingMetadataProto)
)

// getDefaultDataConverter return default data converter used by Temporal worker
func getDefaultDataConverter() DataConverter {
	return DefaultDataConverter
}

func (dc *defaultDataConverter) ToData(values ...interface{}) (*commonpb.Payload, error) {
	if len(values) == 0 {
		return nil, nil
	}

	payload := &commonpb.Payload{}

	for i, value := range values {
		nvp, ok := value.(NameValuePair)
		if !ok {
			nvp.Name = fmt.Sprintf("values[%d]", i)
			nvp.Value = value
		}

		var payloadItem *commonpb.PayloadItem
		if isTypeByteSlice(reflect.TypeOf(nvp.Value)) {
			payloadItem = &commonpb.PayloadItem{
				Metadata: map[string][]byte{
					encodingMetadata: []byte(encodingMetadataRaw),
					nameMetadata:     []byte(nvp.Name),
				},
				Data: nvp.Value.([]byte),
			}
		} else if protoValue, isProto := nvp.Value.(proto.Marshaler); isProto {
			data, err := protoValue.Marshal()
			if err != nil {
				return nil, fmt.Errorf("%q: %w: %v", nvp.Name, ErrUnableToEncodeProto, err)
			}

			payloadItem = &commonpb.PayloadItem{
				Metadata: map[string][]byte{
					encodingMetadata: []byte(encodingMetadataProto),
					nameMetadata:     []byte(nvp.Name),
				},
				Data: data,
			}
		} else {
			data, err := json.Marshal(nvp.Value)
			if err != nil {
				return nil, fmt.Errorf("%q: %w: %v", nvp.Name, ErrUnableToEncodeJSON, err)
			}
			payloadItem = &commonpb.PayloadItem{
				Metadata: map[string][]byte{
					encodingMetadata: []byte(encodingMetadataJson),
					nameMetadata:     []byte(nvp.Name),
				},
				Data: data,
			}
		}
		payload.Items = append(payload.Items, payloadItem)
	}

	return payload, nil
}

func (dc *defaultDataConverter) FromData(payload *commonpb.Payload, valuePtrs ...interface{}) error {
	if payload == nil {
		return nil
	}

	for i, payloadItem := range payload.GetItems() {
		if i >= len(valuePtrs) {
			break
		}

		metadata := payloadItem.GetMetadata()
		if metadata == nil {
			return fmt.Errorf("payload item %d: %w", i, ErrMetadataIsNotSet)
		}

		var name string
		if n, ok := metadata[nameMetadata]; ok {
			name = string(n)
		} else {
			name = fmt.Sprintf("values[%d]", i)
		}

		var encoding string
		if e, ok := metadata[encodingMetadata]; ok {
			encoding = string(e)
		} else {
			return fmt.Errorf("%q: %w", name, ErrEncodingIsNotSet)
		}

		switch encoding {
		case encodingMetadataRaw:
			valueBytes := reflect.ValueOf(valuePtrs[i]).Elem()
			if !valueBytes.CanSet() {
				return fmt.Errorf("%q: %w", name, ErrUnableToSet)
			}
			valueBytes.SetBytes(payloadItem.GetData())
		case encodingMetadataJson:
			err := json.Unmarshal(payloadItem.GetData(), valuePtrs[i])
			if err != nil {
				return fmt.Errorf("%q: %w: %v", name, ErrUnableToDecodeJSON, err)
			}
		case encodingMetadataProto:
			valuePtr, isProto := valuePtrs[i].(proto.Unmarshaler)
			if !isProto {
				return fmt.Errorf("%q: %w", name, ErrValuePtrIsNotProto)
			}
			err := valuePtr.Unmarshal(payloadItem.GetData())
			if err != nil {
				return fmt.Errorf("%q: %w: %v", name, ErrUnableToDecodeProto, err)
			}
		default:
			return fmt.Errorf("%q, encoding %q: %w", name, encoding, ErrEncodingIsNotSupported)
		}
	}

	return nil
}
