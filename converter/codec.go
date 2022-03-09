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

// PayloadCodec is an codec that encodes or decodes the given payloads.
//
// For example, NewZlibCodec returns a PayloadCodec that can be used for
// compression.
// These can be used (and even chained) in NewCodecDataConverter.
type PayloadCodec interface {
	// Encode optionally encodes the given payloads which are guaranteed to never
	// be nil. The byte slices of the payload's metadata or data should never be
	// mutated directly, but they can be referenced or replaced.
	Encode([]*commonpb.Payload) error

	// Decode optionally decodes the given payloads which are guaranteed to never
	// be nil. The byte slices of the payload's metadata or data should never be
	// mutated directly, but they can be referenced or replaced.
	//
	// For compatibility reasons, implementers should take care not to decode
	// payloads that were not previously encoded.
	Decode([]*commonpb.Payload) error
}

// ZlibCodecOptions are options for NewZlibCodec. All fields are optional.
type ZlibCodecOptions struct {
	// If true, the zlib codec will encode the contents even if there is no size
	// benefit. Otherwise, the zlib codec will only use the encoded value if it
	// is smaller.
	AlwaysEncode bool
}

type zlibCodec struct{ options ZlibCodecOptions }

// NewZlibCodec creates a PayloadCodec for use in NewCodecDataConverter
// to support zlib payload compression.
//
// While this serves as a reasonable example of a compression encoder, callers
// may prefer alternative compression algorithms for lots of small payloads.
func NewZlibCodec(options ZlibCodecOptions) PayloadCodec { return &zlibCodec{options} }

func (z *zlibCodec) Encode(ps []*commonpb.Payload) error {
	for _, p := range ps {
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
	}
	return nil
}

func (*zlibCodec) Decode(ps []*commonpb.Payload) error {
	for _, p := range ps {
		// Only if it's our encoding
		if string(p.Metadata[MetadataEncoding]) != "binary/zlib" {
			continue
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
		err = proto.Unmarshal(b, p)
		if err != nil {
			return err
		}
	}
	return nil
}

// CodecDataConverter is a DataConverter that wraps an underlying data
// converter and supports chained encoding of just the payload without regard
// for serialization to/from actual types.
type CodecDataConverter struct {
	parent DataConverter
	codecs []PayloadCodec
}

// NewCodecDataConverter wraps the given parent DataConverter and performs
// encoding/decoding on the payload via the given codecs. When encoding for
// ToPayload(s), the codecs are applied last to first meaning the earlier
// encoders wrap the later ones. When decoding for FromPayload(s) and
// ToString(s), the decoders are applied first to last to reverse the effect.
func NewCodecDataConverter(parent DataConverter, codecs ...PayloadCodec) DataConverter {
	return &CodecDataConverter{parent, codecs}
}

func (e *CodecDataConverter) encode(payloads []*commonpb.Payload) error {
	// Iterate backwards encoding
	for i := len(e.codecs) - 1; i >= 0; i-- {
		if err := e.codecs[i].Encode(payloads); err != nil {
			return err
		}
	}
	return nil
}

func (e *CodecDataConverter) decode(payloads []*commonpb.Payload) error {
	// Iterate forwards decoding
	for _, codec := range e.codecs {
		if err := codec.Decode(payloads); err != nil {
			return err
		}
	}
	return nil
}

// ToPayload implements DataConverter.ToPayload performing encoding on the
// result of the parent's ToPayload call.
func (e *CodecDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := e.parent.ToPayload(value)
	if payload == nil || err != nil {
		return payload, err
	}
	err = e.encode([]*commonpb.Payload{payload})
	return payload, err
}

// ToPayloads implements DataConverter.ToPayloads performing encoding on the
// result of the parent's ToPayloads call.
func (e *CodecDataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := e.parent.ToPayloads(value...)
	if payloads == nil || err != nil {
		return payloads, err
	}
	err = e.encode(payloads.Payloads)
	return payloads, err
}

// FromPayload implements DataConverter.FromPayload performing decoding on the
// given payload before sending to the parent FromPayload.
func (e *CodecDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	if payload == nil {
		return nil
	}
	// Clone to not affect caller
	payload = partiallyClonePayload(payload)
	err := e.decode([]*commonpb.Payload{payload})
	if err != nil {
		return err
	}
	return e.parent.FromPayload(payload, valuePtr)
}

// FromPayloads implements DataConverter.FromPayloads performing decoding on the
// given payloads before sending to the parent FromPayloads.
func (e *CodecDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return e.parent.FromPayloads(payloads, valuePtrs...)
	}
	// Clone to not affect caller
	newPayloads := partiallyClonePayloads(payloads.Payloads)
	err := e.decode(newPayloads)
	if err != nil {
		return err
	}
	return e.parent.FromPayloads(&commonpb.Payloads{Payloads: newPayloads}, valuePtrs...)
}

// ToString implements DataConverter.ToString performing decoding on the given
// payload before sending to the parent ToString.
func (e *CodecDataConverter) ToString(payload *commonpb.Payload) string {
	// Clone to not affect caller
	payload = partiallyClonePayload(payload)
	err := e.decode([]*commonpb.Payload{payload})
	if err != nil {
		return err.Error()
	}
	return e.parent.ToString(payload)
}

// ToStrings implements DataConverter.ToStrings using ToString for each value.
func (e *CodecDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	if payloads == nil {
		return nil
	}
	strs := make([]string, len(payloads.Payloads))
	// Perform decoding one by one here so that we return individual errors
	for i, payload := range payloads.Payloads {
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

func partiallyClonePayloads(payloads []*commonpb.Payload) []*commonpb.Payload {
	newPayloads := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		newPayloads[i] = partiallyClonePayload(payload)
	}
	return newPayloads
}

const remotePayloadCodecEncodePath = "/encode"
const remotePayloadCodecDecodePath = "/decode"

type codecHTTPHandler struct {
	codecs []PayloadCodec
}

func (e *codecHTTPHandler) encode(payloads []*commonpb.Payload) error {
	for i := len(e.codecs) - 1; i >= 0; i-- {
		if err := e.codecs[i].Encode(payloads); err != nil {
			return err
		}
	}
	return nil
}

func (e *codecHTTPHandler) decode(payloads []*commonpb.Payload) error {
	for _, codec := range e.codecs {
		if err := codec.Decode(payloads); err != nil {
			return err
		}
	}
	return nil
}

// ServeHTTP implements the http.Handler interface.
func (e *codecHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}

	path := r.URL.Path

	if !strings.HasSuffix(path, remotePayloadCodecEncodePath) &&
		!strings.HasSuffix(path, remotePayloadCodecDecodePath) {
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
	case strings.HasSuffix(path, remotePayloadCodecEncodePath):
		if err := e.encode(payloads.Payloads); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case strings.HasSuffix(path, remotePayloadCodecDecodePath):
		if err := e.decode(payloads.Payloads); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
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

// NewPayloadCodecHTTPHandler creates a http.Handler for a PayloadCodec.
// This can be used to provide a remote data converter.
func NewPayloadCodecHTTPHandler(e ...PayloadCodec) http.Handler {
	return &codecHTTPHandler{codecs: e}
}

// RemoteDataConverterOptions are options for NewRemoteDataConverter.
// Client is optional.
type RemoteDataConverterOptions struct {
	Endpoint string
	Client   http.Client
}

// remoteDataConverter is a DataConverter that wraps an underlying data
// converter and uses a remote codec to handle encoding/decoding.
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
	encodedPayloads, err := rdc.encodePayloads([]*commonpb.Payload{payload})
	if err != nil {
		return payload, err
	}
	return encodedPayloads[0], err
}

// ToPayloads implements DataConverter.ToPayloads performing remote encoding on the
// result of the parent's ToPayloads call.
func (rdc *remoteDataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := rdc.parent.ToPayloads(value...)
	if payloads == nil || err != nil {
		return payloads, err
	}
	encodedPayloads, err := rdc.encodePayloads(payloads.Payloads)
	return &commonpb.Payloads{Payloads: encodedPayloads}, err
}

// FromPayload implements DataConverter.FromPayload performing remote decoding on the
// given payload before sending to the parent FromPayload.
func (rdc *remoteDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	decodedPayloads, err := rdc.decodePayloads([]*commonpb.Payload{payload})
	if err != nil {
		return err
	}
	return rdc.parent.FromPayload(decodedPayloads[0], valuePtr)
}

// FromPayloads implements DataConverter.FromPayloads performing remote decoding on the
// given payloads before sending to the parent FromPayloads.
func (rdc *remoteDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return rdc.parent.FromPayloads(payloads, valuePtrs...)
	}

	decodedPayloads, err := rdc.decodePayloads(payloads.Payloads)
	if err != nil {
		return err
	}
	return rdc.parent.FromPayloads(&commonpb.Payloads{Payloads: decodedPayloads}, valuePtrs...)
}

// ToString implements DataConverter.ToString performing remote decoding on the given
// payload before sending to the parent ToString.
func (rdc *remoteDataConverter) ToString(payload *commonpb.Payload) string {
	if payload == nil {
		return rdc.parent.ToString(payload)
	}

	decodedPayloads, err := rdc.decodePayloads([]*commonpb.Payload{payload})
	if err != nil {
		return err.Error()
	}
	return rdc.parent.ToString(decodedPayloads[0])
}

// ToStrings implements DataConverter.ToStrings using ToString for each value.
func (rdc *remoteDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	if payloads == nil {
		return nil
	}

	strs := make([]string, len(payloads.Payloads))
	// Perform decoding one by one here so that we return individual errors
	for i, payload := range payloads.Payloads {
		strs[i] = rdc.ToString(payload)
	}
	return strs
}

func (rdc *remoteDataConverter) encodePayloads(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return rdc.encodeOrDecodePayloads(rdc.options.Endpoint+remotePayloadCodecEncodePath, payloads)
}

func (rdc *remoteDataConverter) decodePayloads(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return rdc.encodeOrDecodePayloads(rdc.options.Endpoint+remotePayloadCodecDecodePath, payloads)
}

func (rdc *remoteDataConverter) encodeOrDecodePayloads(endpoint string, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	requestPayloads, err := json.Marshal(commonpb.Payloads{Payloads: payloads})
	if err != nil {
		return payloads, fmt.Errorf("unable to marshal payloads: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestPayloads))
	if err != nil {
		return payloads, fmt.Errorf("unable to build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	response, err := rdc.options.Client.Do(req)
	if err != nil {
		return payloads, err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == 200 {
		var resultPayloads commonpb.Payloads
		err = jsonpb.Unmarshal(response.Body, &resultPayloads)
		if err != nil {
			return payloads, fmt.Errorf("unable to unmarshal payloads: %w", err)
		}
		if len(payloads) != len(resultPayloads.Payloads) {
			return payloads, fmt.Errorf("received %d payloads from remote codec, expected %d", len(resultPayloads.Payloads), len(payloads))
		}
		return resultPayloads.Payloads, nil
	}

	message, _ := io.ReadAll(response.Body)
	return payloads, fmt.Errorf("%s: %s", http.StatusText(response.StatusCode), message)
}
