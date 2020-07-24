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

// Package encoded contains wrappers that are used for binary payloads deserialization.
package encoded

import "go.temporal.io/sdk/internal"

type (

	// Value is used to encapsulate/extract encoded value from workflow/activity.
	Value = internal.Value

	// Values is used to encapsulate/extract encoded one or more values from workflow/activity.
	Values = internal.Values

	// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
	// that need to be sent over the wire.
	// To encode/decode workflow arguments, set DataConverter in client, through client.Options.
	// To override DataConverter for specific activity or child workflow use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	DataConverter = internal.DataConverter

	// PayloadConverter is an interface to convert a single payload.
	PayloadConverter = internal.PayloadConverter
	// ByteSlicePayloadConverter pass through []byte to Data field in payload.
	ByteSlicePayloadConverter = internal.ByteSlicePayloadConverter
	// JSONPayloadConverter converts to/from JSON.
	JSONPayloadConverter = internal.JSONPayloadConverter
	// ProtoJSONPayloadConverter converts proto objects to/from JSON.
	ProtoJSONPayloadConverter = internal.ProtoJSONPayloadConverter
	// ProtoPayloadConverter converts proto objects to binary format.
	ProtoPayloadConverter = internal.ProtoPayloadConverter
	// NilPayloadConverter doesn't set Data field in payload.
	NilPayloadConverter = internal.NilPayloadConverter
)

// GetDefaultDataConverter return default data converter used by Temporal worker.
func GetDefaultDataConverter() DataConverter {
	return internal.DefaultDataConverter
}

// NewDataConverter creates new instance of DataConverter from ordered list of PayloadConverters.
// Order is important here because during serialization DataConverter will try PayloadsConverters in
// that order until PayloadConverter returns non nil payload.
// Last PayloadConverter should always serialize the value (JSONPayloadConverter is good candidate for it),
func NewDataConverter(payloadConverters ...PayloadConverter) DataConverter {
	return internal.NewDataConverter(payloadConverters...)
}
