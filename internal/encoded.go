// Copyright (c) 2017 Uber Technologies, Inc.
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
	encodingMetadata     = "encoding"
	encodingMetadataRaw  = "raw"
	encodingMetadataJson = "json"
	encodingMetadataGob  = "gob"
	nameMetadata         = "name"
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
		ToData(value ...interface{}) ([]byte, error)
		// FromData implements conversion of an array of values of different types.
		// Useful for deserializing arguments of function invocations.
		FromData(input []byte, valuePtr ...interface{}) error

		ToDataP(value ...interface{}) (*commonpb.Payload, error)
		FromDataP(input *commonpb.Payload, valuePtr ...interface{}) error
	}

	// defaultDataConverter uses JSON.
	defaultDataConverter struct{}
)

// DefaultDataConverter is default data converter used by Temporal worker
var (
	DefaultDataConverter = &defaultDataConverter{}

	ErrEncodingIsNotSet       = errors.New("payload encoding metadata is not set")
	ErrEncodingIsNotSupported = errors.New("payload encoding metadata is not supported")
	ErrUnableToJsonEncode     = errors.New("unable to encode to JSON")
	ErrUnableToJsonDecode     = errors.New("unable to decode from JSON")
)

// getDefaultDataConverter return default data converter used by Temporal worker
func getDefaultDataConverter() DataConverter {
	return DefaultDataConverter
}

func (dc *defaultDataConverter) ToData(r ...interface{}) ([]byte, error) {
	if len(r) == 1 && isTypeByteSlice(reflect.TypeOf(r[0])) {
		return r[0].([]byte), nil
	}

	encoder := &jsonEncoding{}

	data, err := encoder.Marshal(r)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (dc *defaultDataConverter) FromData(data []byte, to ...interface{}) error {
	if len(to) == 1 && isTypeByteSlice(reflect.TypeOf(to[0])) {
		reflect.ValueOf(to[0]).Elem().SetBytes(data)
		return nil
	}

	encoder := &jsonEncoding{}

	return encoder.Unmarshal(data, to)
}

func (dc *defaultDataConverter) ToDataP(args ...interface{}) (*commonpb.Payload, error) {
	payload := &commonpb.Payload{}

	for i, arg := range args {
		var payloadItem *commonpb.PayloadItem
		if isTypeByteSlice(reflect.TypeOf(arg)) {
			payloadItem = &commonpb.PayloadItem{
				Metadata: map[string][]byte{
					encodingMetadata: []byte(encodingMetadataRaw),
					nameMetadata:     []byte(fmt.Sprintf("args[%d]", i)),
				},
				Data: arg.([]byte),
			}
		} else {
			data, err := json.Marshal(arg)
			if err != nil {
				return nil, fmt.Errorf("args[%d]: %w: %v", i, ErrUnableToJsonEncode, err)
			}
			payloadItem = &commonpb.PayloadItem{
				Metadata: map[string][]byte{
					encodingMetadata: []byte(encodingMetadataJson),
					nameMetadata:     []byte(fmt.Sprintf("args[%d]", i)),
				},
				Data: data,
			}
		}
		payload.Items = append(payload.Items, payloadItem)
	}

	return payload, nil
}

func (dc *defaultDataConverter) FromDataP(payload *commonpb.Payload, to ...interface{}) error {
	for i, payloadItem := range payload.GetItems() {
		encoding, ok := payloadItem.GetMetadata()[encodingMetadata]

		if !ok {
			return fmt.Errorf("args[%d]: %w", i, ErrEncodingIsNotSet)
		}

		e := string(encoding)
		if e == encodingMetadataRaw {
			reflect.ValueOf(to[i]).Elem().SetBytes(payloadItem.GetData())
			return nil
		} else if e == encodingMetadataJson {
			err := json.Unmarshal(payloadItem.GetData(), to[i])
			if err != nil {
				return fmt.Errorf("args[%d]: %w: %v", i, ErrUnableToJsonDecode, err)
			}
		} else {
			return fmt.Errorf("args[%d], encoding %q: %w", i, e, ErrEncodingIsNotSupported)
		}
	}

	return nil
}
