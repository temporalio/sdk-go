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
	"bytes"
	"encoding/gob"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
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

func testDataConverterHelper(t *testing.T, dc DataConverter) {
	f1 := func(ctx Context, r []byte) string {
		return "result"
	}
	r1 := testDataConverterFunction(t, dc, f1, new(emptyCtx), []byte("test"))
	require.Equal(t, r1, "result")
	// No parameters.
	f2 := func() string {
		return "empty-result"
	}
	r2 := testDataConverterFunction(t, dc, f2)
	require.Equal(t, r2, "empty-result")
	// Nil parameter.
	f3 := func(r []byte) string {
		return "nil-result"
	}
	r3 := testDataConverterFunction(t, dc, f3, []byte(""))
	require.Equal(t, r3, "nil-result")
}

func TestDefaultDataConverter(t *testing.T) {
	dc := getDefaultDataConverter()
	testDataConverterHelper(t, dc)
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
	dec := gob.NewDecoder(bytes.NewBuffer(input))
	for i, obj := range valuePtr {
		if err := dec.Decode(obj); err != nil {
			return fmt.Errorf(
				"unable to decode argument: %d, %v, with gob error: %v", i, reflect.TypeOf(obj), err)
		}
	}
	return nil
}

func TestTestDataConverter(t *testing.T) {
	dc := newTestDataConverter()
	testDataConverterHelper(t, dc)
}

func TestDecodeArg(t *testing.T) {
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
