// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
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
	"compress/zlib"
	"io/ioutil"

	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/api/common/v1"
)

// PayloadEncoder is an encoder that encodes or decodes the given payload.
//
// These can be used (and even chained) in NewEncodingDataConverter. For
// example, NewZlibEncoder returns a PayloadEncoder that can be used for
// compression.
type PayloadEncoder interface {
	// Encode optionally encodes the given payload which is guaranteed to never
	// be nil. The byte slices of the payload's metadata or data should never be
	// mutated directly, but they can be referenced or replaced.
	Encode(*commonpb.Payload) error

	// Decode optionally decodes the given payload which is guaranteed to never
	// be nil. The byte slices of the payload's metadata or data should never be
	// mutated directly, but they can be referenced or replaced.
	//
	// For compatibility reasons, implementers should take care not to decode
	// payloads that were not previously encoded.
	Decode(*commonpb.Payload) error
}

// ZlibEncoderOptions are options for NewZlibEncoder. All fields are optional.
type ZlibEncoderOptions struct {
	// If true, the zlib encoder will encode the contents even if there is no size
	// benefit. Otherwise, the zlib encoder will only use the encoded value if it
	// is smaller.
	AlwaysEncode bool
}

type zlibEncoder struct{ options ZlibEncoderOptions }

// NewZlibEncoder creates a PayloadEncoder for use in NewEncodingDataConverter
// to support zlib payload compression.
//
// While this serves as a reasonable example of a compression encoder, callers
// may prefer alternative compression algorithms for lots of small payloads.
func NewZlibEncoder(options ZlibEncoderOptions) PayloadEncoder { return &zlibEncoder{options} }

func (z *zlibEncoder) Encode(p *commonpb.Payload) error {
	// Marshal and write
	b, err := proto.Marshal(p)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, err = w.Write(b)
	if closeErr := w.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Only set if smaller than original amount or has option to always encode
	if buf.Len() < len(b) || z.options.AlwaysEncode {
		p.Metadata = map[string][]byte{MetadataEncoding: []byte("binary/zlib")}
		p.Data = buf.Bytes()
	}
	return nil
}

func (*zlibEncoder) Decode(p *commonpb.Payload) error {
	// Only if it's our encoding
	if string(p.Metadata[MetadataEncoding]) != "binary/zlib" {
		return nil
	}
	r, err := zlib.NewReader(bytes.NewReader(p.Data))
	if err != nil {
		return err
	}
	// Read all and unmarshal
	b, err := ioutil.ReadAll(r)
	if closeErr := r.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	p.Reset()
	return proto.Unmarshal(b, p)
}

// EncodingDataConverter is a DataConverter that wraps an underlying data
// converter and supports chained encoding of just the payload without regard
// for serialization to/from actual types.
type EncodingDataConverter struct {
	parent   DataConverter
	encoders []PayloadEncoder
}

var _ DataConverter = &EncodingDataConverter{}

// NewEncodingDataConverter wraps the given parent DataConverter and performs
// encoding/decoding on the payload via the given encoders. When encoding for
// ToPayload(s), the encoders are applied last to first meaning the earlier
// encoders wrap the later ones. When decoding for FromPayload(s) and
// ToString(s), the encoders are applied first to last to reverse the effect.
func NewEncodingDataConverter(parent DataConverter, encoders ...PayloadEncoder) *EncodingDataConverter {
	return &EncodingDataConverter{parent, encoders}
}

// ToPayload implements DataConverter.ToPayload performing encoding on the
// result of the parent's ToPayload call.
func (e *EncodingDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := e.parent.ToPayload(value)
	if payload == nil || err != nil {
		return payload, err
	}
	return e.toPayload(payload)
}

// ToPayloads implements DataConverter.ToPayloads performing encoding on the
// result of the parent's ToPayloads call.
func (e *EncodingDataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := e.parent.ToPayloads(value...)
	if payloads == nil || err != nil {
		return payloads, err
	}
	newPayloads := &commonpb.Payloads{Payloads: make([]*commonpb.Payload, len(payloads.Payloads))}
	for i, payload := range payloads.Payloads {
		if newPayloads.Payloads[i], err = e.toPayload(payload); err != nil {
			return nil, err
		}
	}
	return newPayloads, nil
}

func (e *EncodingDataConverter) toPayload(payload *commonpb.Payload) (*commonpb.Payload, error) {
	if payload == nil {
		return nil, nil
	}
	// Clone to not affect caller
	payload = partiallyClonePayload(payload)
	// Iterate backwards converting
	for i := len(e.encoders) - 1; i >= 0; i-- {
		if err := e.encoders[i].Encode(payload); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

// FromPayload implements DataConverter.FromPayload performing decoding on the
// given payload before sending to the parent FromPayload.
func (e *EncodingDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	payload, err := e.fromPayload(payload)
	if err != nil {
		return err
	}
	return e.parent.FromPayload(payload, valuePtr)
}

// FromPayloads implements DataConverter.FromPayloads performing decoding on the
// given payloads before sending to the parent FromPayloads.
func (e *EncodingDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return e.parent.FromPayloads(payloads, valuePtrs...)
	}
	newPayloads := &commonpb.Payloads{Payloads: make([]*commonpb.Payload, len(payloads.Payloads))}
	for i, payload := range payloads.Payloads {
		var err error
		if newPayloads.Payloads[i], err = e.fromPayload(payload); err != nil {
			return err
		}
	}
	return e.parent.FromPayloads(newPayloads, valuePtrs...)
}

func (e *EncodingDataConverter) fromPayload(payload *commonpb.Payload) (*commonpb.Payload, error) {
	// If the payload is not the expected encoding, do not convert it
	if payload == nil {
		return nil, nil
	}
	// Clone to not affect caller
	payload = partiallyClonePayload(payload)
	// Iterate forwards converting
	for _, encoder := range e.encoders {
		if err := encoder.Decode(payload); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

// ToString implements DataConverter.ToString performing decoding on the given
// payload before sending to the parent ToString.
func (e *EncodingDataConverter) ToString(input *commonpb.Payload) string {
	input, err := e.fromPayload(input)
	if err != nil {
		return err.Error()
	}
	return e.parent.ToString(input)
}

// ToStrings implements DataConverter.ToStrings using ToString for each value.
func (e *EncodingDataConverter) ToStrings(input *commonpb.Payloads) []string {
	if input == nil {
		return nil
	}
	strs := make([]string, len(input.Payloads))
	for i, payload := range input.Payloads {
		strs[i] = e.ToString(payload)
	}
	return strs
}

// Only copies metadata in shallow way, not byte slice
func partiallyClonePayload(p *commonpb.Payload) *commonpb.Payload {
	ret := &commonpb.Payload{Metadata: make(map[string][]byte, len(p.Metadata)), Data: p.Data}
	for k, v := range p.Metadata {
		ret.Metadata[k] = v
	}
	return ret
}
