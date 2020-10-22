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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
)

type testStruct struct {
	Name string
	Age  int
}

func TestProtoJsonPayloadConverter_Gogo(t *testing.T) {
	pc := NewProtoJSONPayloadConverter()

	wt := &historypb.HistoryEvent{
		EventId:   1978,
		EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_TIMED_OUT,
		Attributes: &historypb.HistoryEvent_WorkflowTaskTimedOutEventAttributes{WorkflowTaskTimedOutEventAttributes: &historypb.WorkflowTaskTimedOutEventAttributes{
			ScheduledEventId: 2,
			TimeoutType:      enumspb.TIMEOUT_TYPE_SCHEDULE_TO_START,
		}}}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := &historypb.HistoryEvent{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, int64(1978), wt2.EventId)

	var wt3 *historypb.HistoryEvent
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, int64(1978), wt3.EventId)

	s := pc.ToString(payload)
	assert.Equal(t, `{"eventId":"1978","eventType":"WorkflowTaskTimedOut","workflowTaskTimedOutEventAttributes":{"scheduledEventId":"2","timeoutType":"ScheduleToStart"}}`, s)
}

func TestProtoJsonPayloadConverter_Google(t *testing.T) {
	pc := NewProtoJSONPayloadConverter()

	wt := &GoV2{
		Name:     "qwe",
		BirthDay: 12,
		Type:     TypeV2_TYPEV2_R,
		Values:   &GoV2_ValueS{ValueS: "asd"},
	}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := &GoV2{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt2.Name)
	assert.Equal(t, int64(12), wt2.BirthDay)

	var wt3 *GoV2
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt3.Name)
	assert.Equal(t, int64(12), wt3.BirthDay)

	s := pc.ToString(payload)
	assert.Equal(t, `{"name":"qwe","birthDay":"12","type":"TYPEV2_R","valueS":"asd"}`, strings.Replace(s, " ", "", -1))
}

func TestProtoPayloadConverter_Gogo(t *testing.T) {
	pc := NewProtoPayloadConverter()

	wt := &historypb.HistoryEvent{
		EventId:   1978,
		EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_TIMED_OUT,
		Attributes: &historypb.HistoryEvent_WorkflowTaskTimedOutEventAttributes{WorkflowTaskTimedOutEventAttributes: &historypb.WorkflowTaskTimedOutEventAttributes{
			ScheduledEventId: 2,
			TimeoutType:      enumspb.TIMEOUT_TYPE_SCHEDULE_TO_START,
		}}}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := &historypb.HistoryEvent{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, int64(1978), wt2.EventId)

	var wt3 *historypb.HistoryEvent
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, int64(1978), wt3.EventId)

	s := pc.ToString(payload)
	assert.Equal(t, "CLoPGAhqBAgCGAI", s)
}

func TestProtoPayloadConverter_Google(t *testing.T) {
	pc := NewProtoPayloadConverter()

	wt := &GoV2{
		Name:     "qwe",
		BirthDay: 12,
		Type:     TypeV2_TYPEV2_R,
		Values:   &GoV2_ValueS{ValueS: "asd"},
	}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := &GoV2{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt2.Name)

	var wt3 *GoV2
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt3.Name)

	s := pc.ToString(payload)
	assert.Equal(t, "CgNxd2UQDDgBQgNhc2Q", s)
}

func TestJsonPayloadConverter(t *testing.T) {
	pc := NewJSONPayloadConverter()

	wt := testStruct{Name: "qwe"}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)
	wt2 := testStruct{}
	err = pc.FromPayload(payload, &wt2)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt2.Name)

	var wt3 *testStruct
	err = pc.FromPayload(payload, &wt3)
	require.NoError(t, err)
	assert.Equal(t, "qwe", wt3.Name)

	s := pc.ToString(payload)
	assert.Equal(t, "{Age:0 Name:qwe}", s)
}

func TestProtoJsonPayloadConverter_NotPointer(t *testing.T) {
	pc := NewProtoJSONPayloadConverter()

	wt := commonpb.WorkflowType{Name: "qwe"}
	payload, err := pc.ToPayload(wt)
	require.NoError(t, err)

	wt2 := commonpb.WorkflowType{} // Note: there is no &
	err = pc.FromPayload(payload, &wt2)
	assert.Equal(t, "qwe", wt2.Name)
}
