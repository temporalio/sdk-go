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

package serializer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/thriftrw/protocol"
	"go.uber.org/thriftrw/wire"
)

type (
	// ThriftObject represents a thrift object
	ThriftObject interface {
		FromWire(w wire.Value) error
		ToWire() (wire.Value, error)
	}
)

const (
	// used by thriftrw binary codec
	preambleVersion0 byte = 0x59
)

var (
	// MissingBinaryEncodingVersion indicate that the encoding version is missing
	MissingBinaryEncodingVersion = &shared.BadRequestError{Message: "Missing binary encoding version."}
	// InvalidBinaryEncodingVersion indicate that the encoding version is incorrect
	InvalidBinaryEncodingVersion = &shared.BadRequestError{Message: "Invalid binary encoding version."}
	// MsgPayloadNotThriftEncoded indicate message is not thrift encoded
	MsgPayloadNotThriftEncoded = &shared.BadRequestError{Message: "Message payload is not thrift encoded."}
)

// Decode decode the object
func Decode(binary []byte, val ThriftObject) error {
	if len(binary) < 1 {
		return MissingBinaryEncodingVersion
	}

	version := binary[0]
	if version != preambleVersion0 {
		return InvalidBinaryEncodingVersion
	}

	reader := bytes.NewReader(binary[1:])
	wireVal, err := protocol.Binary.Decode(reader, wire.TStruct)
	if err != nil {
		return err
	}

	return val.FromWire(wireVal)
}

// Encode encode the object
func Encode(obj ThriftObject) ([]byte, error) {
	if obj == nil {
		return nil, MsgPayloadNotThriftEncoded
	}
	var writer bytes.Buffer
	// use the first byte to version the serialization
	err := writer.WriteByte(preambleVersion0)
	if err != nil {
		return nil, err
	}
	val, err := obj.ToWire()
	if err != nil {
		return nil, err
	}
	err = protocol.Binary.Encode(val, &writer)
	if err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

// SerializeBatchEvents will serialize history event data to blob data
func SerializeBatchEvents(events []*shared.HistoryEvent, encodingType shared.EncodingType) (*shared.DataBlob, error) {
	return serialize(events, encodingType)
}

// DeserializeBatchEvents will deserialize blob data to history event data
func DeserializeBatchEvents(data *shared.DataBlob) ([]*shared.HistoryEvent, error) {
	if data == nil {
		return nil, nil
	}
	var events []*shared.HistoryEvent
	if data != nil && len(data.Data) == 0 {
		return events, nil
	}
	err := deserialize(data, &events)
	return events, err
}

func serialize(input interface{}, encodingType shared.EncodingType) (*shared.DataBlob, error) {
	if input == nil {
		return nil, nil
	}

	var data []byte
	var err error

	switch encodingType {
	case shared.EncodingTypeThriftRW:
		data, err = thriftrwEncode(input)
	case shared.EncodingTypeJSON: // For backward-compatibility
		encodingType = shared.EncodingTypeJSON
		data, err = json.Marshal(input)
	default:
		return nil, fmt.Errorf("unknown or unsupported encoding type %v", encodingType)
	}

	if err != nil {
		return nil, fmt.Errorf("cadence serialization error: %v", err.Error())
	}
	return NewDataBlob(data, encodingType), nil
}

func thriftrwEncode(input interface{}) ([]byte, error) {
	switch input.(type) {
	case []*shared.HistoryEvent:
		return Encode(&shared.History{Events: input.([]*shared.HistoryEvent)})
	case *shared.HistoryEvent:
		return Encode(input.(*shared.HistoryEvent))
	case *shared.Memo:
		return Encode(input.(*shared.Memo))
	case *shared.ResetPoints:
		return Encode(input.(*shared.ResetPoints))
	case *shared.BadBinaries:
		return Encode(input.(*shared.BadBinaries))
	case *shared.VersionHistories:
		return Encode(input.(*shared.VersionHistories))
	default:
		return nil, nil
	}
}

func deserialize(data *shared.DataBlob, target interface{}) error {
	if data == nil {
		return nil
	}
	if len(data.Data) == 0 {
		return errors.New("DeserializeEvent empty data")
	}
	var err error

	switch *(data.EncodingType) {
	case shared.EncodingTypeThriftRW:
		err = thriftrwDecode(data.Data, target)
	case shared.EncodingTypeJSON: // For backward-compatibility
		err = json.Unmarshal(data.Data, target)

	}

	if err != nil {
		return fmt.Errorf("DeserializeBatchEvents encoding: \"%v\", error: %v", data.EncodingType, err.Error())
	}
	return nil
}

func thriftrwDecode(data []byte, target interface{}) error {
	switch target := target.(type) {
	case *[]*shared.HistoryEvent:
		history := shared.History{Events: *target}
		if err := Decode(data, &history); err != nil {
			return err
		}
		*target = history.Events
		return nil
	case *shared.HistoryEvent:
		return Decode(data, target)
	case *shared.Memo:
		return Decode(data, target)
	case *shared.ResetPoints:
		return Decode(data, target)
	case *shared.BadBinaries:
		return Decode(data, target)
	case *shared.VersionHistories:
		return Decode(data, target)
	default:
		return nil
	}
}

// NewDataBlob creates new blob data
func NewDataBlob(data []byte, encodingType shared.EncodingType) *shared.DataBlob {
	if data == nil || len(data) == 0 {
		return nil
	}

	return &shared.DataBlob{
		Data:         data,
		EncodingType: &encodingType,
	}
}

// DeserializeBlobDataToHistoryEvents deserialize the blob data to history event data
func DeserializeBlobDataToHistoryEvents(
	dataBlobs []*shared.DataBlob, filterType shared.HistoryEventFilterType,
) (*shared.History, error) {

	var historyEvents []*shared.HistoryEvent

	for _, batch := range dataBlobs {
		events, err := DeserializeBatchEvents(batch)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			return nil, &shared.InternalServiceError{
				Message: fmt.Sprintf("corrupted history event batch, empty events"),
			}
		}

		historyEvents = append(historyEvents, events...)
	}

	if filterType == shared.HistoryEventFilterTypeCloseEvent {
		historyEvents = []*shared.HistoryEvent{historyEvents[len(historyEvents)-1]}
	}
	return &shared.History{Events: historyEvents}, nil
}
