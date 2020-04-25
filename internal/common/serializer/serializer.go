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

package serializer

import (
	"encoding/json"
	"fmt"

	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/temporal-proto/common"
	eventpb "go.temporal.io/temporal-proto/event"
	filterpb "go.temporal.io/temporal-proto/filter"
	"go.temporal.io/temporal-proto/serviceerror"
)

// Data encoding types
const (
	// todo: Deprecate and use protoEncodingEnum.ToString()
	EncodingTypeJSON    EncodingType = "json"
	EncodingTypeGob     EncodingType = "gob"
	EncodingTypeUnknown EncodingType = "unknow"
	EncodingTypeEmpty   EncodingType = ""
	EncodingTypeProto3  EncodingType = "proto3"
)

func (e EncodingType) String() string {
	return string(e)
}

type (
	// EncodingType is an enum that represents various data encoding types
	EncodingType string
)

type (

	// SerializationError is an error type for serialization
	SerializationError struct {
		msg string
	}

	// DeserializationError is an error type for deserialization
	DeserializationError struct {
		msg string
	}

	// UnknownEncodingTypeError is an error type for unknown or unsupported encoding type
	UnknownEncodingTypeError struct {
		encodingType commonpb.EncodingType
	}
)

// SerializeBatchEvents serializes batch events into a datablob proto
func SerializeBatchEvents(events []*eventpb.HistoryEvent, encodingType commonpb.EncodingType) (*commonpb.DataBlob, error) {
	return serialize(&eventpb.History{Events: events}, encodingType)
}

func serializeProto(p proto.Marshaler, encodingType commonpb.EncodingType) (*commonpb.DataBlob, error) {
	if p == nil {
		return nil, nil
	}

	var data []byte
	var err error

	switch encodingType {
	case commonpb.EncodingType_Proto3:
		data, err = p.Marshal()
	case commonpb.EncodingType_JSON:
		encodingType = commonpb.EncodingType_JSON
		pb, ok := p.(proto.Message)
		if !ok {
			return nil, NewSerializationError("could not cast protomarshal interface to proto.message")
		}
		data, err = NewJSONPBEncoder().Encode(pb)
	default:
		return nil, NewUnknownEncodingTypeError(encodingType)
	}

	if err != nil {
		return nil, NewSerializationError(err.Error())
	}

	// Shouldn't happen, but keeping
	if data == nil {
		return nil, nil
	}

	return NewDataBlob(data, encodingType), nil
}

// DeserializeBatchEvents deserializes batch events from a datablob proto
func DeserializeBatchEvents(data *commonpb.DataBlob) ([]*eventpb.HistoryEvent, error) {
	if data == nil {
		return nil, nil
	}
	if len(data.Data) == 0 {
		return nil, nil
	}

	events := &eventpb.History{}
	var err error
	switch data.EncodingType {
	case commonpb.EncodingType_JSON:
		err = NewJSONPBEncoder().Decode(data.Data, events)
	case commonpb.EncodingType_Proto3:
		err = proto.Unmarshal(data.Data, events)
	default:
		return nil, NewDeserializationError("DeserializeBatchEvents invalid encoding")
	}
	if err != nil {
		return nil, err
	}
	return events.Events, nil
}

func serialize(input interface{}, encodingType commonpb.EncodingType) (*commonpb.DataBlob, error) {
	if input == nil {
		return nil, nil
	}

	if p, ok := input.(proto.Marshaler); ok {
		return serializeProto(p, encodingType)
	}

	var data []byte
	var err error

	switch encodingType {
	case commonpb.EncodingType_JSON: // For backward-compatibility
		data, err = json.Marshal(input)
	default:
		return nil, NewUnknownEncodingTypeError(encodingType)
	}

	if err != nil {
		return nil, NewSerializationError(err.Error())
	}

	return NewDataBlob(data, encodingType), nil
}

// NewUnknownEncodingTypeError returns a new instance of encoding type error
func NewUnknownEncodingTypeError(encodingType commonpb.EncodingType) error {
	return &UnknownEncodingTypeError{encodingType: encodingType}
}

func (e *UnknownEncodingTypeError) Error() string {
	return fmt.Sprintf("unknown or unsupported encoding type %v", e.encodingType)
}

// NewSerializationError returns a SerializationError
func NewSerializationError(msg string) *SerializationError {
	return &SerializationError{msg: msg}
}

func (e *SerializationError) Error() string {
	return fmt.Sprintf("cadence serialization error: %v", e.msg)
}

// NewDeserializationError returns a DeserializationError
func NewDeserializationError(msg string) *DeserializationError {
	return &DeserializationError{msg: msg}
}

func (e *DeserializationError) Error() string {
	return fmt.Sprintf("cadence deserialization error: %v", e.msg)
}

// NewDataBlob creates new blob data
func NewDataBlob(data []byte, encodingType commonpb.EncodingType) *commonpb.DataBlob {
	if len(data) == 0 {
		return nil
	}

	return &commonpb.DataBlob{
		Data:         data,
		EncodingType: encodingType,
	}
}

// DeserializeBlobDataToHistoryEvents deserialize the blob data to history event data
func DeserializeBlobDataToHistoryEvents(
	dataBlobs []*commonpb.DataBlob, filterType filterpb.HistoryEventFilterType,
) (*eventpb.History, error) {

	var historyEvents []*eventpb.HistoryEvent

	for _, batch := range dataBlobs {
		events, err := DeserializeBatchEvents(batch)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			return nil, &serviceerror.Internal{
				Message: "corrupted history event batch, empty events",
			}
		}

		historyEvents = append(historyEvents, events...)
	}

	if filterType == filterpb.HistoryEventFilterType_CloseEvent {
		historyEvents = []*eventpb.HistoryEvent{historyEvents[len(historyEvents)-1]}
	}
	return &eventpb.History{Events: historyEvents}, nil
}
