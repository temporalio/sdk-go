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
	commonpb "go.temporal.io/temporal-proto/common"
)

var (
	ErrUnableToGobEncode = errors.New("unable to encode to gob")
	ErrUnableToGobDecode = errors.New("unable to encode from gob")
)

func testDataConverterFunction(t *testing.T, dc DataConverter, f interface{}, args ...interface{}) string {
	input, err := dc.ToData(args...)
	require.NoError(t, err, err)

	var result []interface{}
	for _, v := range args {
		arg := reflect.New(reflect.TypeOf(v)).Interface()
		result = append(result, arg)
	}
	err = dc.FromData(input, result...)
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

// testDataConverter implements encoded.DataConverter using gob
type testDataConverter struct{}

func newTestDataConverter() DataConverter {
	return &testDataConverter{}
}

func (tdc *testDataConverter) ToData(value ...interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	for i, obj := range value {
		if err := enc.Encode(obj); err != nil {
			return nil, fmt.Errorf(
				"unable to encode argument: %d, %v, with gob error: %v", i, reflect.TypeOf(obj), err)
		}
	}
	return buf.Bytes(), nil
}

func (tdc *testDataConverter) FromData(input []byte, valuePtr ...interface{}) error {
	if len(input) == 0 {
		return nil
	}
	dec := gob.NewDecoder(bytes.NewBuffer(input))
	for i, obj := range valuePtr {
		if err := dec.Decode(obj); err != nil {
			return fmt.Errorf(
				"unable to decode argument: %d, %v, with gob error: %v", i, reflect.TypeOf(obj), err)
		}
	}
	return nil
}

func (dc *defaultDataConverter) ToDataP(args ...interface{}) (*commonpb.Payload, error) {
	payload := &commonpb.Payload{}

	for i, arg := range args {
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(arg); err != nil {
			return nil, fmt.Errorf("args[%d]: %w: %v", i, ErrUnableToGobEncode, err)
		}

		payloadItem := &commonpb.PayloadItem{
			Metadata: map[string][]byte{
				encodingMetadata: []byte(encodingMetadataGob),
				nameMetadata:     []byte(fmt.Sprintf("args[%d]", i)),
			},
			Data: buf.Bytes(),
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
		if e == encodingMetadataGob {
			dec := gob.NewDecoder(bytes.NewBuffer(payloadItem.GetData()))
			if err := dec.Decode(to[i]); err != nil {
				return fmt.Errorf("args[%d]: %w: %v", i, ErrUnableToGobDecode, err)
			}
		} else {
			return fmt.Errorf("args[%d], encoding %q: %w", i, e, ErrEncodingIsNotSupported)
		}
	}

	return nil
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
