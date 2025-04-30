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
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestRawValueCompositeDataConverter(t *testing.T) {
	require := require.New(t)

	defaultConv := GetDefaultDataConverter()
	origPayload, err := defaultConv.ToPayload("test raw value")
	require.NoError(err)
	raw := NewRawValue(origPayload)

	// To/FromPayload
	payload, err := defaultConv.ToPayload(raw)
	require.NoError(err)
	require.True(proto.Equal(raw.Payload(), payload))

	var decodedRV RawValue
	err = defaultConv.FromPayload(payload, &decodedRV)
	require.NoError(err)

	require.True(proto.Equal(origPayload, decodedRV.Payload()))

	// To/FromPayloads
	payloads, err := defaultConv.ToPayloads(raw)
	require.NoError(err)
	require.Len(payloads.Payloads, 1)
	require.True(proto.Equal(origPayload, payloads.Payloads[0]))

	err = defaultConv.FromPayloads(payloads, &decodedRV)
	require.NoError(err)

	// Confirm the payload inside RawValue matches original
	require.True(proto.Equal(origPayload, decodedRV.Payload()))
}

func TestCompositeDataConverter_MixedValues(t *testing.T) {
	require := require.New(t)
	defaultConv := GetDefaultDataConverter()

	s := "test string"
	i := 42
	f := 3.14
	b := []byte("raw bytes")
	origPayload, err := defaultConv.ToPayload("test raw value")
	require.NoError(err)
	raw := NewRawValue(origPayload)

	payloads, err := defaultConv.ToPayloads(s, i, f, b, raw)
	require.NoError(err)
	require.Equal(5, len(payloads.Payloads))

	var outString string
	var outInt int
	var outFloat float64
	var outBytes []byte
	var outRaw RawValue

	err = defaultConv.FromPayloads(payloads, &outString, &outInt, &outFloat, &outBytes, &outRaw)
	require.NoError(err)

	require.Equal(s, outString)
	require.Equal(i, outInt)
	require.Equal(f, outFloat)
	require.Equal(b, outBytes)
	require.True(proto.Equal(origPayload, outRaw.Payload()))
}
