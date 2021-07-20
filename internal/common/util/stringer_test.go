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

package util

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_byteSliceToString(t *testing.T) {
	data := []byte("blob-data")
	v := reflect.ValueOf(data)
	strVal := valueToString(v)

	require.Equal(t, "[blob-data]", strVal)

	intBlob := []int32{1, 2, 3}
	v2 := reflect.ValueOf(intBlob)
	strVal2 := valueToString(v2)

	require.Equal(t, "[len=3]", strVal2)
}

func TestValueToString(t *testing.T) {

	testValue := "test"
	testSlice := []string{"a", "b", "c"}
	var emptyInter interface{}
	var testFloat64 float64 = 5.55

	tempStruct := struct {
		Test string
	}{
		"test",
	}

	tempUnexportedStruct := struct {
		test string
	}{
		"test",
	}

	tests := []struct {
		name     string
		value    reflect.Value
		expected string
	}{
		{
			name:     "pointer",
			value:    reflect.ValueOf(&testValue),
			expected: testValue,
		},
		{
			name:     "invalid",
			value:    reflect.ValueOf(emptyInter),
			expected: "",
		},
		{
			name:     "struct",
			value:    reflect.ValueOf(tempStruct),
			expected: "(Test:test)",
		},
		{
			name:     "unexported struct",
			value:    reflect.ValueOf(tempUnexportedStruct),
			expected: "()",
		},
		{
			name:     "slice",
			value:    reflect.ValueOf(testSlice),
			expected: "[len=3]",
		},
		{
			name:     "default",
			value:    reflect.ValueOf(testFloat64),
			expected: "5.55",
		},
	}

	for _, d := range tests {
		t.Logf("testing test name %s", d.name)
		str := valueToString(d.value)

		if str != d.expected {
			t.Fatalf("%s: expected %s, received %s", d.name, d.expected, str)
		}

	}

}
