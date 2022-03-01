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

//go:generate go run ../internal/cmd/generateinterceptor/main.go

package converter

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/gogo/protobuf/jsonpb"
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

const remotePayloadEncoderEncodePath = "/encode"
const remotePayloadEncoderDecodePath = "/decode"

type encoderHTTPHandler struct {
	encoders []PayloadEncoder
}

// ServeHTTP implements the http.Handler interface.
func (e *encoderHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}

	path := r.URL.Path

	if !strings.HasSuffix(path, remotePayloadEncoderEncodePath) &&
		!strings.HasSuffix(path, remotePayloadEncoderDecodePath) {
		http.NotFound(w, r)
		return
	}

	var payloads commonpb.Payloads

	if r.Body == nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	err := jsonpb.Unmarshal(r.Body, &payloads)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch {
	case strings.HasSuffix(path, remotePayloadEncoderEncodePath):
		for _, payload := range payloads.Payloads {
			for i := len(e.encoders) - 1; i >= 0; i-- {
				if err := e.encoders[i].Encode(payload); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
	case strings.HasSuffix(path, remotePayloadEncoderDecodePath):
		for _, payload := range payloads.Payloads {
			for _, encoder := range e.encoders {
				if err := encoder.Decode(payload); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
	default:
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(payloads)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

// NewPayloadEncoderHTTPHandler creates a http.Handler for a PayloadEncoder.
// This can be used to provide a remote data converter.
func NewPayloadEncoderHTTPHandler(e ...PayloadEncoder) http.Handler {
	return &encoderHTTPHandler{encoders: e}
}

// RemoteDataConverterOptions are options for NewRemoteDataConverter.
// Client is optional.
type RemoteDataConverterOptions struct {
	Endpoint string
	Client   http.Client
}

// remoteDataConverter is a DataConverter that wraps an underlying data
// converter and uses a remote encoder to handle encoding/decoding.
type remoteDataConverter struct {
	parent  DataConverter
	options RemoteDataConverterOptions
}

// NewRemoteDataConverter wraps the given parent DataConverter and performs
// encoding/decoding on the payload via the remote endpoint.
func NewRemoteDataConverter(parent DataConverter, options RemoteDataConverterOptions) DataConverter {
	options.Endpoint = strings.TrimSuffix(options.Endpoint, "/")

	return &remoteDataConverter{parent, options}
}

// ToPayload implements DataConverter.ToPayload performing remote encoding on the
// result of the parent's ToPayload call.
func (rdc *remoteDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := rdc.parent.ToPayload(value)
	if payload == nil || err != nil {
		return payload, err
	}
	err = rdc.encodePayload(payload)
	return payload, err
}

// ToPayloads implements DataConverter.ToPayloads performing remote encoding on the
// result of the parent's ToPayloads call.
func (rdc *remoteDataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := rdc.parent.ToPayloads(value...)
	if payloads == nil || err != nil {
		return payloads, err
	}
	err = rdc.encodePayloads(payloads)
	return payloads, err
}

// FromPayload implements DataConverter.FromPayload performing remote decoding on the
// given payload before sending to the parent FromPayload.
func (rdc *remoteDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	err := rdc.decodePayload(payload)
	if err != nil {
		return err
	}

	return rdc.parent.FromPayload(payload, valuePtr)
}

// FromPayloads implements DataConverter.FromPayloads performing remote decoding on the
// given payloads before sending to the parent FromPayloads.
func (rdc *remoteDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return rdc.parent.FromPayloads(payloads, valuePtrs...)
	}

	newPayloads := &commonpb.Payloads{Payloads: payloads.Payloads}
	err := rdc.decodePayloads(newPayloads)
	if err != nil {
		return err
	}

	return rdc.parent.FromPayloads(newPayloads, valuePtrs...)
}

// ToString implements DataConverter.ToString performing remote decoding on the given
// payload before sending to the parent ToString.
func (rdc *remoteDataConverter) ToString(payload *commonpb.Payload) string {
	err := rdc.decodePayload(payload)
	if err != nil {
		return err.Error()
	}
	return rdc.parent.ToString(payload)
}

// ToStrings implements DataConverter.ToStrings using ToString for each value.
func (rdc *remoteDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	if payloads == nil {
		return nil
	}

	strs := make([]string, len(payloads.Payloads))
	for i, payload := range payloads.Payloads {
		strs[i] = rdc.ToString(payload)
	}
	return strs
}

func (rdc *remoteDataConverter) encodePayload(payload *commonpb.Payload) error {
	return rdc.encodeOrDecodePayload(rdc.options.Endpoint+remotePayloadEncoderEncodePath, payload)
}

func (rdc *remoteDataConverter) decodePayload(payload *commonpb.Payload) error {
	return rdc.encodeOrDecodePayload(rdc.options.Endpoint+remotePayloadEncoderDecodePath, payload)
}

func (rdc *remoteDataConverter) encodeOrDecodePayload(endpoint string, payload *commonpb.Payload) error {
	payloads := &commonpb.Payloads{Payloads: []*commonpb.Payload{payload}}

	err := rdc.encodeOrDecodePayloads(endpoint, payloads)
	if err != nil {
		return err
	}

	*payload = *payloads.Payloads[0]

	return nil
}

func (rdc *remoteDataConverter) encodePayloads(payloads *commonpb.Payloads) error {
	return rdc.encodeOrDecodePayloads(rdc.options.Endpoint+remotePayloadEncoderEncodePath, payloads)
}

func (rdc *remoteDataConverter) decodePayloads(payloads *commonpb.Payloads) error {
	return rdc.encodeOrDecodePayloads(rdc.options.Endpoint+remotePayloadEncoderDecodePath, payloads)
}

func (rdc *remoteDataConverter) encodeOrDecodePayloads(endpoint string, payloads *commonpb.Payloads) error {
	requestPayloads, err := json.Marshal(payloads)
	if err != nil {
		return fmt.Errorf("unable to marshal payloads: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestPayloads))
	if err != nil {
		return fmt.Errorf("unable to build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	response, err := rdc.options.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == 200 {
		err = jsonpb.Unmarshal(response.Body, payloads)
		if err != nil {
			return fmt.Errorf("unable to unmarshal payloads: %w", err)
		}
		return nil
	}

	message, _ := io.ReadAll(response.Body)
	return fmt.Errorf("%s: %s", http.StatusText(response.StatusCode), message)
}
