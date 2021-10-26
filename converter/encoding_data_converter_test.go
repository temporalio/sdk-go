// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
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

package converter_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func ExampleEncodingDataConverter_compression() {
	defaultConv := converter.GetDefaultDataConverter()
	// Create Zlib compression converter
	zlibConv := converter.NewEncodingDataConverter(
		defaultConv,
		converter.NewZlibEncoder(converter.ZlibEncoderOptions{}),
	)

	// Create payloads with both
	bigString := strings.Repeat("aabbcc", 200)
	uncompPayload, _ := defaultConv.ToPayload(bigString)
	compPayload, _ := zlibConv.ToPayload(bigString)

	// The zlib payload is smaller
	fmt.Printf("Uncompressed payload size: %v (encoding: %s)\n",
		len(uncompPayload.Data), uncompPayload.Metadata[converter.MetadataEncoding])
	fmt.Printf("Compressed payload size: %v (encoding: %s)\n",
		len(compPayload.Data), compPayload.Metadata[converter.MetadataEncoding])

	// Convert from payload and confirm the same string. This uses the same
	// compression converter because the converter does not do anything to
	// payloads it didn't previously convert.
	var uncompValue, compValue string
	_ = zlibConv.FromPayload(uncompPayload, &uncompValue)
	_ = zlibConv.FromPayload(compPayload, &compValue)
	fmt.Printf("Uncompressed payload back to original? %v\n", uncompValue == bigString)
	fmt.Printf("Compressed payload back to original? %v\n", compValue == bigString)

	// Output:
	// Uncompressed payload size: 1202 (encoding: json/plain)
	// Compressed payload size: 57 (encoding: binary/zlib)
	// Uncompressed payload back to original? true
	// Compressed payload back to original? true
}

type SomeStruct struct{ MyValue string }

func TestEncodingDataConverter(t *testing.T) {
	assertEncodingDataConverter(t, "foo")
	assertEncodingDataConverter(t, nil)
	assertEncodingDataConverter(t, []byte("foo"))
	assertEncodingDataConverter(t, &SomeStruct{MyValue: "somestring"})
}

func assertEncodingDataConverter(t *testing.T, data interface{}) {
	defaultConv := converter.GetDefaultDataConverter()
	zlibConv := converter.NewEncodingDataConverter(
		defaultConv,
		// Always encode
		converter.NewZlibEncoder(converter.ZlibEncoderOptions{AlwaysEncode: true}),
	)

	// To/FromPayload
	compPayload, err := zlibConv.ToPayload(data)
	require.NoError(t, err)
	require.Equal(t, "binary/zlib", string(compPayload.Metadata[converter.MetadataEncoding]))
	var newData interface{}
	if data == nil {
		newData = &newData
	} else if data != nil {
		newData = reflect.New(reflect.TypeOf(data)).Interface()
	}
	require.NoError(t, zlibConv.FromPayload(compPayload, newData))
	if data == nil {
		require.Nil(t, newData)
	} else {
		require.Equal(t, data, reflect.ValueOf(newData).Elem().Interface())
	}

	// To/FromPayloads
	compPayloads, err := zlibConv.ToPayloads(data)
	require.NoError(t, err)
	if data == nil {
		newData = &newData
	} else if data != nil {
		newData = reflect.New(reflect.TypeOf(data)).Interface()
	}
	require.NoError(t, zlibConv.FromPayloads(compPayloads, newData))
	if data == nil {
		require.Nil(t, newData)
	} else {
		require.Equal(t, data, reflect.ValueOf(newData).Elem().Interface())
	}

	// Ignored if not known encoding
	uncompPayload, err := defaultConv.ToPayload(data)
	require.NoError(t, err)
	if data == nil {
		newData = &newData
	} else if data != nil {
		newData = reflect.New(reflect.TypeOf(data)).Interface()
	}
	require.NoError(t, zlibConv.FromPayload(uncompPayload, newData))
	if data == nil {
		require.Nil(t, newData)
	} else {
		require.Equal(t, data, reflect.ValueOf(newData).Elem().Interface())
	}

	// Check that it's ignored if too small (which all params given are)
	zlibIgnoreMinConv := converter.NewEncodingDataConverter(
		defaultConv,
		converter.NewZlibEncoder(converter.ZlibEncoderOptions{}),
	)
	require.NoError(t, err)
	compUnderMinPayload, err := zlibIgnoreMinConv.ToPayload(data)
	require.NoError(t, err)
	require.True(t, proto.Equal(uncompPayload, compUnderMinPayload))
}

func TestEncodingDataConverterClone(t *testing.T) {
	captureConv := &captureToPayloadDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	zlibConv := converter.NewEncodingDataConverter(
		captureConv,
		// Always encode
		converter.NewZlibEncoder(converter.ZlibEncoderOptions{AlwaysEncode: true}),
	)
	// Convert simple payload and confirm the result of capture conv was not
	// mutated by zlib conv
	p, err := zlibConv.ToPayload("some string")
	require.NoError(t, err)
	require.NotNil(t, captureConv.lastToPayloadResult)
	require.NotEqual(t, captureConv.lastToPayloadResult.Data, p.Data)
	require.NotEqual(t, captureConv.lastToPayloadResult.Metadata, p.Metadata)
}

type captureToPayloadDataConverter struct {
	converter.DataConverter
	lastToPayloadResult *commonpb.Payload
}

func (c *captureToPayloadDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	p, err := c.DataConverter.ToPayload(value)
	c.lastToPayloadResult = p
	return p, err
}
