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
	"testing"

	"github.com/stretchr/testify/assert"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
)

func TestHeaderWriter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		initial  *commonpb.Header
		expected *commonpb.Header
		vals     map[string]*commonpb.Payload
	}{
		{
			"no values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{},
			},
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{},
			},
			map[string]*commonpb.Payload{},
		},
		{
			"add values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{},
			},
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]*commonpb.Payload{
				"key1": encodeString(t, "val1"),
				"key2": encodeString(t, "val2"),
			},
		},
		{
			"overwrite values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "unexpected"),
				},
			},
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]*commonpb.Payload{
				"key1": encodeString(t, "val1"),
				"key2": encodeString(t, "val2"),
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := NewHeaderWriter(test.initial)
			for key, val := range test.vals {
				writer.Set(key, val)
			}
			assert.Equal(t, test.expected, test.initial)
		})
	}
}

func encodeString(t *testing.T, s string) *commonpb.Payload {
	p, err := converter.GetDefaultDataConverter().ToPayload(s)
	assert.NoError(t, err)
	return p
}

func TestHeaderReader_ForEachKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		header  *commonpb.Header
		keys    map[string]struct{}
		isError bool
	}{
		{
			"valid values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]struct{}{"key1": {}, "key2": {}},
			false,
		},
		{
			"invalid values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]struct{}{"key2": {}},
			true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := NewHeaderReader(test.header)
			err := reader.ForEachKey(func(key string, _ *commonpb.Payload) error {
				if _, ok := test.keys[key]; !ok {
					return assert.AnError
				}
				return nil
			})
			if test.isError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHeaderReader_Get(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		header       *commonpb.Header
		key          string
		headerExists bool
	}{
		{
			"valid key",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			"key1",
			true,
		},
		{
			"invalid key",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			"key3",
			false,
		},
		{
			"nil fields",
			&commonpb.Header{},
			"key1",
			false,
		},
		{
			"nil headers",
			nil,
			"key1",
			false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := NewHeaderReader(test.header)
			_, headerExist := reader.Get(test.key)
			if test.headerExists {
				assert.True(t, headerExist)
			} else {
				assert.False(t, headerExist)
			}
		})
	}
}
