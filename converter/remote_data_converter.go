// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"

	"github.com/gogo/protobuf/jsonpb"
	commonpb "go.temporal.io/api/common/v1"
)

const ENCODE_PATH = "/encode"
const DECODE_PATH = "/decode"

// RemoteEncoderDataConverterOptions are options for NewRemoteEncoderDataConverter.
// Client and Converter are optional and default to http.Client{} and the default SDK data converter.
type RemoteEncoderDataConverterOptions struct {
	Endpoint  string
	Client    http.Client
	Converter *DataConverter
}

type remotePayloadConverter struct {
	options RemoteEncoderDataConverterOptions
}

// NewRemoteEncoderDataConverter creates a DataConverter that uses a remote endpoint to encode/decode.
// The encoded/decoded payloads are (de)serialized by the Converter provided in the options, or
// the default SDK data converter if the option is not set.
func NewRemoteEncoderDataConverter(options RemoteEncoderDataConverterOptions) *EncodingDataConverter {
	converter := options.Converter
	if converter == nil {
		converter = &defaultDataConverter
	}

	return NewEncodingDataConverter(
		*converter,
		&remotePayloadConverter{options},
	)
}

func (rdc *remotePayloadConverter) sendHTTP(endpoint string, p *commonpb.Payload) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("unable to marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
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
		err = jsonpb.Unmarshal(response.Body, p)
		if err != nil {
			return fmt.Errorf("unable to unmarshal payload: %w", err)
		}
		return nil
	}

	message, _ := io.ReadAll(response.Body)
	return fmt.Errorf("%s: %s", http.StatusText(response.StatusCode), message)
}

// Encode sends a payload to remote data converter server and returns the encoded payload.
func (rdc *remotePayloadConverter) Encode(p *commonpb.Payload) error {
	return rdc.sendHTTP(path.Join(rdc.options.Endpoint, ENCODE_PATH), p)
}

// Decode sends a payload to a remote data converter server and returns the decoded payload.
func (rdc *remotePayloadConverter) Decode(p *commonpb.Payload) error {
	return rdc.sendHTTP(path.Join(rdc.options.Endpoint, DECODE_PATH), p)
}
