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
	"encoding/gob"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
)

const (
	metadataEncodingGob = "gob"
)

var (
	ErrUnableToEncodeGob = errors.New("unable to encode to gob")
	ErrUnableToDecodeGob = errors.New("unable to encode from gob")
)

func testDataConverterFunction(t *testing.T, dc DataConverter, f interface{}, args ...interface{}) string {
	input, err := dc.ToPayloads(args...)
	require.NoError(t, err, err)

	var result []interface{}
	for _, v := range args {
		arg := reflect.New(reflect.TypeOf(v)).Interface()
		result = append(result, arg)
	}
	err = dc.FromPayloads(input, result...)
	require.NoError(t, err, err)

	var targetArgs []reflect.Value
	for _, arg := range result {
		targetArgs = append(targetArgs, reflect.ValueOf(arg).Elem())
	}
	fnValue := reflect.ValueOf(f)
	retValues := fnValue.Call(targetArgs)
	return retValues[0].Interface().(string)
}

func TestDefaultDataConverter(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	t.Run("result", func(t *testing.T) {
		t.Parallel()
		f1 := func(ctx Context, r []byte) string {
			return "result"
		}
		r1 := testDataConverterFunction(t, dc, f1, new(emptyCtx), []byte("test"))
		require.Equal(t, r1, "result")
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		// No parameters.
		f2 := func() string {
			return "empty-result"
		}
		r2 := testDataConverterFunction(t, dc, f2)
		require.Equal(t, r2, "empty-result")
	})
	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		// Nil parameter.
		f3 := func(r []byte) string {
			return "nil-result"
		}
		r3 := testDataConverterFunction(t, dc, f3, []byte(""))
		require.Equal(t, r3, "nil-result")
	})
}

func testDataStringerFunction(t *testing.T, dc DataConverter, args ...interface{}) []string {
	input, err := dc.ToPayloads(args...)
	require.NoError(t, err, err)

	prettyStrings, err := DefaultDataConverter.ToPrettyStrings(input)
	require.NoError(t, err, err)

	return prettyStrings
}

func TestDataStringer(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	testStruct := struct {
		A string
		B int
	}{
		A: "hi",
		B: 3,
	}

	r := testDataStringerFunction(t, dc,
		[]byte("test"),
		[]string{"hello", "world"},
		"hello world",
		42,
		testStruct)

	want := []string{
		"dGVzdA",
		"[hello world]",
		"hello world",
		"42",
		"map[A:hi B:3]",
	}

	require.Equal(t, want, r)
}

// testDataConverter implements encoded.DataConverter using gob
type testDataConverter struct{}

func newTestDataConverter() DataConverter {
	return &testDataConverter{}
}

func (dc *testDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
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

func (dc *testDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	for i, payload := range payloads.GetPayloads() {
		err := dc.FromPayload(payload, valuePtrs[i])

		if err != nil {
			return fmt.Errorf("args[%d]: %w", i, err)
		}
	}

	return nil
}

func (dc *testDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnableToEncodeGob, err)
	}

	payload := &commonpb.Payload{
		Metadata: map[string][]byte{
			metadataEncoding: []byte(metadataEncodingGob),
		},
		Data: buf.Bytes(),
	}

	return payload, nil
}

func (dc *testDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	encoding, ok := payload.GetMetadata()[metadataEncoding]

	if !ok {
		return ErrEncodingIsNotSet
	}

	e := string(encoding)
	if e == metadataEncodingGob {
		dec := gob.NewDecoder(bytes.NewBuffer(payload.GetData()))
		if err := dec.Decode(valuePtr); err != nil {
			return fmt.Errorf("%w: %v", ErrUnableToDecodeGob, err)
		}
	} else {
		return fmt.Errorf("encoding %q: %w", e, ErrEncodingIsNotSupported)
	}

	return nil
}

func (dc *testDataConverter) ToPrettyStrings(payloads *commonpb.Payloads) ([]string, error) {
	var result []string
	for i, payload := range payloads.GetPayloads() {
		payloadAsStr, err := toPrettyStringTestHelper(payload)

		if err != nil {
			return result, fmt.Errorf("args[%d]: %w", i, err)
		}

		result = append(result, payloadAsStr)
	}

	return result, nil
}

func toPrettyStringTestHelper(payload *commonpb.Payload) (string, error) {
	encoding, ok := payload.GetMetadata()[metadataEncoding]

	if !ok {
		return "", ErrEncodingIsNotSet
	}

	e := string(encoding)
	if e == metadataEncodingGob {
		var byteSlice []byte
		dec := gob.NewDecoder(bytes.NewBuffer(payload.GetData()))
		if err := dec.Decode(&byteSlice); err != nil {
			return "", fmt.Errorf("%w: %v", ErrUnableToDecodeGob, err)
		}
	} else {
		return "", fmt.Errorf("encoding %q: %w", e, ErrEncodingIsNotSupported)
	}

	return "", nil
}

func TestDecodeArg(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()

	b, err := encodeArg(dc, testErrorDetails3)
	require.NoError(t, err)
	var r testStruct
	err = decodeArg(dc, b, &r)
	require.NoError(t, err)
	require.Equal(t, testErrorDetails3, r)

	// test mismatch of multi arguments
	b, err = encodeArgs(dc, []interface{}{testErrorDetails1, testErrorDetails2})
	require.NoError(t, err)
	require.Error(t, decodeArg(dc, b, &r))
}
