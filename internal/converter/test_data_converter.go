package converter

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
)

const (
	metadataEncodingGob = "binary/gob"
)

var (
	ErrUnableToEncodeGob = errors.New("unable to encode to gob")
	ErrUnableToDecodeGob = errors.New("unable to encode from gob")
)

// TestDataConverter implements encoded.DataConverter using gob
type TestDataConverter struct{}

func NewTestDataConverter() DataConverter {
	return &TestDataConverter{}
}

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

func (dc *TestDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	for i, payload := range payloads.GetPayloads() {
		err := dc.FromPayload(payload, valuePtrs[i])

		if err != nil {
			return fmt.Errorf("args[%d]: %w", i, err)
		}
	}

	return nil
}

func (dc *TestDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnableToEncodeGob, err)
	}

	payload := &commonpb.Payload{
		Metadata: map[string][]byte{
			metadataEncoding: []byte(metadataEncodingGob),
		},
		Data: buf.Bytes(),
	}

	return payload, nil
}

func (dc *TestDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	encoding, ok := payload.GetMetadata()[metadataEncoding]

	if !ok {
		return ErrEncodingIsNotSet
	}

	e := string(encoding)
	if e == metadataEncodingGob {
		dec := gob.NewDecoder(bytes.NewBuffer(payload.GetData()))
		if err := dec.Decode(valuePtr); err != nil {
			return fmt.Errorf("%w: %v", ErrUnableToDecodeGob, err)
		}
	} else {
		return fmt.Errorf("encoding %q: %w", e, ErrEncodingIsNotSupported)
	}

	return nil
}

func (dc *TestDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	var result []string
	for _, payload := range payloads.GetPayloads() {
		result = append(result, dc.ToString(payload))
	}

	return result
}

func (dc *TestDataConverter) ToString(payload *commonpb.Payload) string {
	encoding, ok := payload.GetMetadata()[metadataEncoding]

	if !ok {
		return ErrEncodingIsNotSet.Error()
	}

	e := string(encoding)
	if e != metadataEncodingGob {
		return fmt.Errorf("encoding %q: %w", e, ErrEncodingIsNotSupported).Error()
	}

	var byteSlice []byte
	dec := gob.NewDecoder(bytes.NewBuffer(payload.GetData()))
	if err := dec.Decode(&byteSlice); err != nil {
		return fmt.Errorf("%w: %v", ErrUnableToDecodeGob, err).Error()
	}
	return string(byteSlice)
}
