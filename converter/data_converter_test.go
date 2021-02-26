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

	"github.com/stretchr/testify/require"
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
	t.Run("result", func(t *testing.T) {
		t.Parallel()
		f1 := func(ctx context.Context, r []byte) string {
			return "result"
		}
		r1 := testDataConverterFunction(t, defaultDataConverter, f1, context.Background(), []byte("test"))
		require.Equal(t, r1, "result")
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		// No parameters.
		f2 := func() string {
			return "empty-result"
		}
		r2 := testDataConverterFunction(t, defaultDataConverter, f2)
		require.Equal(t, r2, "empty-result")
	})
	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		// Nil parameter.
		f3 := func(r []byte) string {
			return "nil-result"
		}
		r3 := testDataConverterFunction(t, defaultDataConverter, f3, []byte(""))
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

	got := testToStringsFunction(t, defaultDataConverter,
		[]byte("test"),
		[]string{"hello", "world"},
		"hello world",
		42,
		testStruct)

	want := []string{
		"dGVzdA",
		`["hello","world"]`,
		`"hello world"`,
		"42",
		`{"A":"hi","B":3}`,
	}

	require.Equal(t, want, got)
}
