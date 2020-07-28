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
	"bytes"
	"encoding/gob"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
)

const (
	metadataEncodingGob = "binary/gob"
)

// TestDataConverter implements DataConverter using gob.
type TestDataConverter struct{}

// NewTestDataConverter created new instance of TestDataConverter.
func NewTestDataConverter() converter.DataConverter {
	return &TestDataConverter{}
}

// ToPayloads converts a list of values.
func (dc *TestDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
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

// FromPayloads converts to a list of values of different types.
func (dc *TestDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	for i, payload := range payloads.GetPayloads() {
		err := dc.FromPayload(payload, valuePtrs[i])

		if err != nil {
			return fmt.Errorf("args[%d]: %w", i, err)
		}
	}

	return nil
}

// ToPayload converts single value to payload.
func (dc *TestDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("%w: %v", converter.ErrUnableToEncode, err)
	}

	payload := &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte(metadataEncodingGob),
		},
		Data: buf.Bytes(),
	}

	return payload, nil
}

// FromPayload converts single value from payload.
func (dc *TestDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	enc, ok := payload.GetMetadata()[converter.MetadataEncoding]

	if !ok {
		return converter.ErrEncodingIsNotSet
	}

	e := string(enc)
	if e == metadataEncodingGob {
		dec := gob.NewDecoder(bytes.NewBuffer(payload.GetData()))
		if err := dec.Decode(valuePtr); err != nil {
			return fmt.Errorf("%w: %v", converter.ErrUnableToDecode, err)
		}
	} else {
		return fmt.Errorf("encoding %q: %w", e, converter.ErrEncodingIsNotSupported)
	}

	return nil
}

// ToStrings converts payloads object into human readable strings.
func (dc *TestDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	var result []string
	for _, payload := range payloads.GetPayloads() {
		result = append(result, dc.ToString(payload))
	}

	return result
}

// ToString converts payload object into human readable string.
func (dc *TestDataConverter) ToString(payload *commonpb.Payload) string {
	enc, ok := payload.GetMetadata()[converter.MetadataEncoding]

	if !ok {
		return converter.ErrEncodingIsNotSet.Error()
	}

	e := string(enc)
	if e != metadataEncodingGob {
		return fmt.Errorf("encoding %q: %w", e, converter.ErrEncodingIsNotSupported).Error()
	}

	var byteSlice []byte
	dec := gob.NewDecoder(bytes.NewBuffer(payload.GetData()))
	if err := dec.Decode(&byteSlice); err != nil {
		return fmt.Errorf("%w: %v", converter.ErrUnableToDecode, err).Error()
	}
	return string(byteSlice)
}
