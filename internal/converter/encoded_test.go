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

package converter

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/internal"
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
	dc := DefaultDataConverter
	t.Run("result", func(t *testing.T) {
		t.Parallel()
		f1 := func(ctx context.Context, r []byte) string {
			return "result"
		}
		r1 := testDataConverterFunction(t, dc, f1, context.Background(), []byte("test"))
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

func testToStringsFunction(
	t *testing.T,
	dc DataConverter,
	args ...interface{},
) []string {
	input, err := dc.ToPayloads(args...)
	require.NoError(t, err, err)

	strings := dc.ToStrings(input)

	return strings
}

func TestToStrings(t *testing.T) {
	t.Parallel()

	testStruct := struct {
		A string
		B int
	}{
		A: "hi",
		B: 3,
	}

	got := testToStringsFunction(t, DefaultDataConverter,
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
		"{A:hi B:3}",
	}

	require.Equal(t, want, got)
}

func TestDecodeArg(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()

	b, err := internal.encodeArg(dc, internal.testErrorDetails3)
	require.NoError(t, err)
	var r internal.testStruct
	err = internal.decodeArg(dc, b, &r)
	require.NoError(t, err)
	require.Equal(t, internal.testErrorDetails3, r)

	// test mismatch of multi arguments
	b, err = internal.encodeArgs(dc, []interface{}{internal.testErrorDetails1, internal.testErrorDetails2})
	require.NoError(t, err)
	require.Error(t, internal.decodeArg(dc, b, &r))
}

func TestProtoJsonPayloadConverter(t *testing.T) {
	pc := NewProtoJSONPayloadConverter()

	wt := &commonpb.WorkflowType{Name: "qwe"}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := &commonpb.WorkflowType{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt2.Name)

	var wt3 *commonpb.WorkflowType
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt3.Name)

	s := pc.ToString(payload)
	assert.Equal(t, `{"name":"qwe"}`, s)
}

func TestProtoPayloadConverter(t *testing.T) {
	pc := NewProtoPayloadConverter()

	wt := &commonpb.WorkflowType{Name: "qwe"}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := &commonpb.WorkflowType{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt2.Name)

	var wt3 *commonpb.WorkflowType
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt3.Name)

	s := pc.ToString(payload)
	assert.Equal(t, "CgNxd2U", s)
}

func TestJsonPayloadConverter(t *testing.T) {
	pc := NewJSONPayloadConverter()

	wt := internal.WorkflowType{Name: "qwe"}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := internal.WorkflowType{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt2.Name)

	var wt3 *internal.WorkflowType
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt3.Name)

	s := pc.ToString(payload)
	assert.Equal(t, "{Name:qwe}", s)
}
