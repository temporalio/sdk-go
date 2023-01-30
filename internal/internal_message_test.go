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

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	protocolpb "go.temporal.io/api/protocol/v1"
)

func TestEventMessageIndex(t *testing.T) {
	newMsg := func(id string, eventID int64) *protocolpb.Message {
		return &protocolpb.Message{
			Id:           id,
			SequencingId: &protocolpb.Message_EventId{EventId: eventID},
		}
	}
	messages := []*protocolpb.Message{
		newMsg("00", 0),
		newMsg("01", 2),
		newMsg("02", 2),
		newMsg("03", 101),
		newMsg("04", 100),
		newMsg("05", 50),
		newMsg("06", 3),
		newMsg("07", 0),
		newMsg("08", 100),
	}

	emi := indexMessagesByEventID(messages)

	batch := emi.takeLTE(2)
	require.Len(t, batch, 4)
	for i := 0; i < len(batch)-1; i++ {
		if batch[i].GetEventId() == batch[i+1].GetEventId() {
			require.Less(t, batch[i].Id, batch[i+1].Id)
		}
	}

	batch = emi.takeLTE(2)
	require.Empty(t, batch)

	batch = emi.takeLTE(3)
	require.Len(t, batch, 1)

	batch = emi.takeLTE(100)
	require.Len(t, batch, 3)
	for i := 0; i < len(batch)-1; i++ {
		if batch[i].GetEventId() == batch[i+1].GetEventId() {
			require.Less(t, batch[i].Id, batch[i+1].Id)
		}
	}

	emi = indexMessagesByEventID(messages)
	batch = emi.takeLTE(9000)
	require.Len(t, batch, len(messages))
	require.Empty(t, emi)
}
