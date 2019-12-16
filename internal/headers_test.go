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
	"testing"

	"github.com/stretchr/testify/assert"

	commonproto "github.com/temporalio/temporal-proto/common"
)

func TestHeaderWriter(t *testing.T) {
	tests := []struct {
		name     string
		initial  *commonproto.Header
		expected *commonproto.Header
		vals     map[string][]byte
	}{
		{
			"no values",
			&commonproto.Header{
				Fields: map[string][]byte{},
			},
			&commonproto.Header{
				Fields: map[string][]byte{},
			},
			map[string][]byte{},
		},
		{
			"add values",
			&commonproto.Header{
				Fields: map[string][]byte{},
			},
			&commonproto.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string][]byte{
				"key1": []byte("val1"),
				"key2": []byte("val2"),
			},
		},
		{
			"overwrite values",
			&commonproto.Header{
				Fields: map[string][]byte{
					"key1": []byte("unexpected"),
				},
			},
			&commonproto.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string][]byte{
				"key1": []byte("val1"),
				"key2": []byte("val2"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := NewHeaderWriter(test.initial)
			for key, val := range test.vals {
				writer.Set(key, val)
			}
			assert.Equal(t, test.expected, test.initial)
		})
	}
}

func TestHeaderReader(t *testing.T) {
	tests := []struct {
		name    string
		header  *commonproto.Header
		keys    map[string]struct{}
		isError bool
	}{
		{
			"valid values",
			&commonproto.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string]struct{}{"key1": struct{}{}, "key2": struct{}{}},
			false,
		},
		{
			"invalid values",
			&commonproto.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string]struct{}{"key2": struct{}{}},
			true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := NewHeaderReader(test.header)
			err := reader.ForEachKey(func(key string, val []byte) error {
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
