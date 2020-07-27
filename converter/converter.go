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

// Package converter contains wrappers that are used for binary payloads deserialization.
package converter

import (
	"go.temporal.io/sdk/internal/converter"
)

type (

	// Value is used to encapsulate/extract encoded value from workflow/activity.
	Value = converter.Value

	// Values is used to encapsulate/extract encoded one or more values from workflow/activity.
	Values = converter.Values

	// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
	// that need to be sent over the wire.
	// To encode/decode workflow arguments, set DataConverter in client, through client.Options.
	// To override DataConverter for specific activity or child workflow use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	DataConverter = converter.DataConverter

	// CompositeDataConverter applies PayloadConverters in specified order.
	CompositeDataConverter = converter.CompositeDataConverter

	// PayloadConverter is an interface to convert a single payload.
	PayloadConverter = converter.PayloadConverter
	// ByteSlicePayloadConverter pass through []byte to Data field in payload.
	ByteSlicePayloadConverter = converter.ByteSlicePayloadConverter
	// JSONPayloadConverter converts to/from JSON.
	JSONPayloadConverter = converter.JSONPayloadConverter
	// ProtoJSONPayloadConverter converts proto objects to/from JSON.
	ProtoJSONPayloadConverter = converter.ProtoJSONPayloadConverter
	// ProtoPayloadConverter converts proto objects to protobuf binary format.
	ProtoPayloadConverter = converter.ProtoPayloadConverter
	// NilPayloadConverter doesn't set Data field in payload.
	NilPayloadConverter = converter.NilPayloadConverter
)

// GetDefaultDataConverter return default data converter used by Temporal worker.
func GetDefaultDataConverter() DataConverter {
	return converter.DefaultDataConverter
}

// NewCompositeDataConverter creates new instance of CompositeDataConverter from ordered list of PayloadConverters.
// Order is important here because during serialization DataConverter will try PayloadsConverters in
// that order until PayloadConverter returns non nil payload.
// Last PayloadConverter should always serialize the value (JSONPayloadConverter is good candidate for it),
func NewCompositeDataConverter(payloadConverters ...PayloadConverter) *CompositeDataConverter {
	return converter.NewCompositeDataConverter(payloadConverters...)
}

// NewByteSlicePayloadConverter creates new instance of ByteSlicePayloadConverter.
func NewByteSlicePayloadConverter() *ByteSlicePayloadConverter {
	return converter.NewByteSlicePayloadConverter()
}

// NewJSONPayloadConverter creates new instance of JSONPayloadConverter.
func NewJSONPayloadConverter() *JSONPayloadConverter {
	return converter.NewJSONPayloadConverter()
}

// NewProtoJSONPayloadConverter creates new instance of ProtoJSONPayloadConverter.
func NewProtoJSONPayloadConverter() *ProtoJSONPayloadConverter {
	return converter.NewProtoJSONPayloadConverter()
}

// NewProtoPayloadConverter creates new instance of ProtoPayloadConverter.
func NewProtoPayloadConverter() *ProtoPayloadConverter {
	return converter.NewProtoPayloadConverter()
}

// NewNilPayloadConverter creates new instance of NilPayloadConverter.
func NewNilPayloadConverter() *NilPayloadConverter {
	return converter.NewNilPayloadConverter()
}
