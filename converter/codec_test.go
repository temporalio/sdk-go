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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func ExampleCodecDataConverter_compression() {
	defaultConv := converter.GetDefaultDataConverter()
	// Create Zlib compression converter
	zlibConv := converter.NewCodecDataConverter(
		defaultConv,
		converter.NewZlibCodec(converter.ZlibCodecOptions{}),
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
	zlibConv := converter.NewCodecDataConverter(
		defaultConv,
		// Always encode
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}),
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
	zlibIgnoreMinConv := converter.NewCodecDataConverter(
		defaultConv,
		converter.NewZlibCodec(converter.ZlibCodecOptions{}),
	)
	require.NoError(t, err)
	compUnderMinPayload, err := zlibIgnoreMinConv.ToPayload(data)
	require.NoError(t, err)
	require.True(t, proto.Equal(uncompPayload, compUnderMinPayload))
}

func TestPayloadCodecHTTPHandler(t *testing.T) {
	defaultConv := converter.GetDefaultDataConverter()
	codec := converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true})
	handler := converter.NewPayloadCodecHTTPHandler(codec)

	req, err := http.NewRequest("GET", "/encode", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)

	req, err = http.NewRequest("POST", "/missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	req, err = http.NewRequest("POST", "/encode", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)

	payloads, _ := defaultConv.ToPayloads("test")
	payloadsJSON, _ := json.Marshal(payloads)

	fmt.Printf("%s", payloadsJSON)

	req, err = http.NewRequest("POST", "/encode", bytes.NewReader(payloadsJSON))
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	encodedPayloadsJSON := strings.TrimSpace(rr.Body.String())
	require.NotEqual(t, payloadsJSON, encodedPayloadsJSON)

	req, err = http.NewRequest("POST", "/decode", strings.NewReader(encodedPayloadsJSON))
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	decodedPayloadsJSON := strings.TrimSpace(rr.Body.String())
	require.Equal(t, string(payloadsJSON), decodedPayloadsJSON)
}

type testCodec struct {
	encoding   string
	encodeFrom string
}

func (e *testCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.Metadata[converter.MetadataEncoding]) != e.encodeFrom {
			return payloads, fmt.Errorf("unexpected encoding: %s", p.Metadata[converter.MetadataEncoding])
		}

		b, err := proto.Marshal(p)
		if err != nil {
			return payloads, err
		}

		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte(e.encoding)},
			Data:     b,
		}
	}

	return result, nil
}

func (e *testCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.Metadata[converter.MetadataEncoding]) != e.encoding {
			return payloads, fmt.Errorf("unexpected encoding: %s", p.Metadata[converter.MetadataEncoding])
		}

		result[i] = &commonpb.Payload{}
		err := proto.Unmarshal(p.Data, result[i])
		if err != nil {
			return payloads, err
		}
	}
	return result, nil
}

func TestRemoteDataConverter(t *testing.T) {
	defaultConv := converter.GetDefaultDataConverter()
	codecs := []converter.PayloadCodec{
		&testCodec{encoding: "encrypted", encodeFrom: "compressed"},
		&testCodec{encoding: "compressed", encodeFrom: "json/plain"},
	}
	handler := converter.NewPayloadCodecHTTPHandler(codecs...)

	server := httptest.NewServer(handler)
	defer server.Close()

	localConverter := converter.NewCodecDataConverter(
		defaultConv,
		codecs...,
	)

	remoteConverter := converter.NewRemoteDataConverter(
		defaultConv,
		converter.RemoteDataConverterOptions{Endpoint: server.URL},
	)

	unencodedPayloads, err := defaultConv.ToPayloads("test", "payloads")
	require.NoError(t, err)

	localEncodedPayloads, err := localConverter.ToPayloads("test", "payloads")
	require.NoError(t, err)
	remoteEncodedPayloads, err := remoteConverter.ToPayloads("test", "payloads")
	require.NoError(t, err)

	require.NotEqual(t, unencodedPayloads, localEncodedPayloads)
	require.Equal(t, localEncodedPayloads, remoteEncodedPayloads)

	unencodedPayload, err := defaultConv.ToPayload("test")
	require.NoError(t, err)

	localEncodedPayload, err := localConverter.ToPayload("test")
	require.NoError(t, err)
	remoteEncodedPayload, err := remoteConverter.ToPayload("test")
	require.NoError(t, err)

	require.NotEqual(t, unencodedPayload, localEncodedPayload)
	require.Equal(t, localEncodedPayload, remoteEncodedPayload)
}
