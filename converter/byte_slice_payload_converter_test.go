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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestByteSliceConverter(t *testing.T) {
	bc := NewByteSlicePayloadConverter()

	assert.Equal(t, bc.Encoding(), MetadataEncodingBinary)
	payload, err := bc.ToPayload(nil)
	assert.Nil(t, err)
	assert.Nil(t, payload)

	b := []byte("hello world")
	payload, err = bc.ToPayload(b)
	require.NoError(t, err)
	assert.Equal(t, string(payload.Metadata[MetadataEncoding]), MetadataEncodingBinary)
	assert.Equal(t, payload.Data, b)

	var gotBytes []byte
	var gotInterface interface{}

	err = bc.FromPayload(payload, &gotBytes)
	require.NoError(t, err)
	assert.Equal(t, b, gotBytes)

	err = bc.FromPayload(payload, &gotInterface)
	require.NoError(t, err)
	assert.Equal(t, b, gotInterface)

	gotString := bc.ToString(payload)
	// base64 unpadded encodeing of "hello world"
	assert.Equal(t, "aGVsbG8gd29ybGQ", gotString)

	// error branches
	err = bc.FromPayload(payload, nil)
	assert.ErrorIs(t, err, ErrValuePtrIsNotPointer)

	var s string
	err = bc.FromPayload(payload, s)
	assert.ErrorIs(t, err, ErrValuePtrIsNotPointer)

	err = bc.FromPayload(payload, &s)
	assert.ErrorIs(t, err, ErrTypeIsNotByteSlice)
}
