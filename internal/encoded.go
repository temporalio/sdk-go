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

	commonpb "go.temporal.io/temporal-proto/common"
)

const (
	metadataEncoding     = "encoding"
	metadataEncodingRaw  = "raw"
	metadataEncodingJSON = "json"

	metadataName = "name"
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
		// ToData implements conversion of a list of values.
		ToData(value ...interface{}) (*commonpb.Payloads, error)
		// FromData implements conversion of an array of values of different types.
		// Useful for deserializing arguments of function invocations.
		FromData(input *commonpb.Payloads, valuePtr ...interface{}) error
	}

	// defaultDataConverter uses JSON.
	defaultDataConverter struct{}

	// NameValuePair represent named value.
	NameValuePair struct {
		Name  string
		Value interface{}
	}
)

var (
	// DefaultDataConverter is default data converter used by Temporal worker
	DefaultDataConverter = &defaultDataConverter{}

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
	// ErrUnableToSetBytes is returned when unable to set []byte value.
	ErrUnableToSetBytes = errors.New("unable to set []byte value")
)

// getDefaultDataConverter return default data converter used by Temporal worker
func getDefaultDataConverter() DataConverter {
	return DefaultDataConverter
}

func (dc *defaultDataConverter) ToData(values ...interface{}) (*commonpb.Payloads, error) {
	if len(values) == 0 {
		return nil, nil
	}

	result := &commonpb.Payloads{}
	for i, value := range values {
		nvp, ok := value.(NameValuePair)
		if !ok {
			nvp.Name = fmt.Sprintf("values[%d]", i)
			nvp.Value = value
		}

		var payload *commonpb.Payload
		if bytes, isByteSlice := nvp.Value.([]byte); isByteSlice {
			payload = &commonpb.Payload{
				Metadata: map[string][]byte{
					metadataEncoding: []byte(metadataEncodingRaw),
					metadataName:     []byte(nvp.Name),
				},
				Data: bytes,
			}
		} else {
			data, err := json.Marshal(nvp.Value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w: %v", nvp.Name, ErrUnableToEncodeJSON, err)
			}
			payload = &commonpb.Payload{
				Metadata: map[string][]byte{
					metadataEncoding: []byte(metadataEncodingJSON),
					metadataName:     []byte(nvp.Name),
				},
				Data: data,
			}
		}
		result.Payloads = append(result.Payloads, payload)
	}

	return result, nil
}

func (dc *defaultDataConverter) FromData(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return nil
	}

	for i, payload := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}

		metadata := payload.GetMetadata()
		if metadata == nil {
			return fmt.Errorf("payload item %d: %w", i, ErrMetadataIsNotSet)
		}

		var name string
		if n, ok := metadata[metadataName]; ok {
			name = string(n)
		} else {
			name = fmt.Sprintf("values[%d]", i)
		}

		var encoding string
		if e, ok := metadata[metadataEncoding]; ok {
			encoding = string(e)
		} else {
			return fmt.Errorf("%s: %w", name, ErrEncodingIsNotSet)
		}

		switch encoding {
		case metadataEncodingRaw:
			valueBytes := reflect.ValueOf(valuePtrs[i]).Elem()
			if !valueBytes.CanSet() {
				return fmt.Errorf("%s: %w", name, ErrUnableToSetBytes)
			}
			valueBytes.SetBytes(payload.GetData())
		case metadataEncodingJSON:
			err := json.Unmarshal(payload.GetData(), valuePtrs[i])
			if err != nil {
				return fmt.Errorf("%s: %w: %v", name, ErrUnableToDecodeJSON, err)
			}
		default:
			return fmt.Errorf("%s, encoding %s: %w", name, encoding, ErrEncodingIsNotSupported)
		}
	}

	return nil
}
