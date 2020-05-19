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

package util

import (
	"bytes"
	"fmt"
	"reflect"

	decisionpb "go.temporal.io/temporal-proto/decision"
	eventpb "go.temporal.io/temporal-proto/event"
)

func anyToString(d interface{}) string {
	v := reflect.ValueOf(d)
	switch v.Kind() {
	case reflect.Ptr:
		return anyToString(v.Elem().Interface())
	case reflect.Struct:
		var buf bytes.Buffer
		t := reflect.TypeOf(d)
		buf.WriteString("(")
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.Kind() == reflect.Invalid {
				continue
			}
			fieldValue := valueToString(f)
			if len(fieldValue) == 0 {
				continue
			}
			if buf.Len() > 1 {
				buf.WriteString(", ")
			}
			buf.WriteString(fmt.Sprintf("%s:%s", t.Field(i).Name, fieldValue))
		}
		buf.WriteString(")")
		return buf.String()
	default:
		return fmt.Sprint(d)
	}
}

func valueToString(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Ptr:
		return valueToString(v.Elem())
	case reflect.Struct:
		return anyToString(v.Interface())
	case reflect.Invalid:
		return ""
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return fmt.Sprintf("[%v]", string(v.Bytes()))
		}
		return fmt.Sprintf("[len=%d]", v.Len())
	default:
		return fmt.Sprint(v.Interface())
	}
}

// HistoryEventToString convert HistoryEvent to string
func HistoryEventToString(e *eventpb.HistoryEvent) string {
	var data interface{}
	switch e.GetEventType() {
	case eventpb.EventType_WorkflowExecutionStarted:
		data = e.GetWorkflowExecutionStartedEventAttributes()

	case eventpb.EventType_WorkflowExecutionCompleted:
		data = e.GetWorkflowExecutionCompletedEventAttributes()

	case eventpb.EventType_WorkflowExecutionFailed:
		data = e.GetWorkflowExecutionFailedEventAttributes()

	case eventpb.EventType_WorkflowExecutionTimedOut:
		data = e.GetWorkflowExecutionTimedOutEventAttributes()

	case eventpb.EventType_DecisionTaskScheduled:
		data = e.GetDecisionTaskScheduledEventAttributes()

	case eventpb.EventType_DecisionTaskStarted:
		data = e.GetDecisionTaskStartedEventAttributes()

	case eventpb.EventType_DecisionTaskCompleted:
		data = e.GetDecisionTaskCompletedEventAttributes()

	case eventpb.EventType_DecisionTaskTimedOut:
		data = e.GetDecisionTaskTimedOutEventAttributes()

	case eventpb.EventType_ActivityTaskScheduled:
		data = e.GetActivityTaskScheduledEventAttributes()

	case eventpb.EventType_ActivityTaskStarted:
		data = e.GetActivityTaskStartedEventAttributes()

	case eventpb.EventType_ActivityTaskCompleted:
		data = e.GetActivityTaskCompletedEventAttributes()

	case eventpb.EventType_ActivityTaskFailed:
		data = e.GetActivityTaskFailedEventAttributes()

	case eventpb.EventType_ActivityTaskTimedOut:
		data = e.GetActivityTaskTimedOutEventAttributes()

	case eventpb.EventType_ActivityTaskCancelRequested:
		data = e.GetActivityTaskCancelRequestedEventAttributes()

	case eventpb.EventType_ActivityTaskCanceled:
		data = e.GetActivityTaskCanceledEventAttributes()

	case eventpb.EventType_TimerStarted:
		data = e.GetTimerStartedEventAttributes()

	case eventpb.EventType_TimerFired:
		data = e.GetTimerFiredEventAttributes()

	case eventpb.EventType_CancelTimerFailed:
		data = e.GetCancelTimerFailedEventAttributes()

	case eventpb.EventType_TimerCanceled:
		data = e.GetTimerCanceledEventAttributes()

	case eventpb.EventType_MarkerRecorded:
		data = e.GetMarkerRecordedEventAttributes()

	case eventpb.EventType_WorkflowExecutionTerminated:
		data = e.GetWorkflowExecutionTerminatedEventAttributes()

	default:
		data = e
	}

	return e.GetEventType().String() + ": " + anyToString(data)
}

// DecisionToString convert Decision to string
func DecisionToString(d *decisionpb.Decision) string {
	var data interface{}
	switch d.GetDecisionType() {
	case decisionpb.DecisionType_ScheduleActivityTask:
		data = d.GetScheduleActivityTaskDecisionAttributes()

	case decisionpb.DecisionType_RequestCancelActivityTask:
		data = d.GetRequestCancelActivityTaskDecisionAttributes()

	case decisionpb.DecisionType_StartTimer:
		data = d.GetStartTimerDecisionAttributes()

	case decisionpb.DecisionType_CancelTimer:
		data = d.GetCancelTimerDecisionAttributes()

	case decisionpb.DecisionType_CompleteWorkflowExecution:
		data = d.GetCompleteWorkflowExecutionDecisionAttributes()

	case decisionpb.DecisionType_FailWorkflowExecution:
		data = d.GetFailWorkflowExecutionDecisionAttributes()

	case decisionpb.DecisionType_RecordMarker:
		data = d.GetRecordMarkerDecisionAttributes()

	default:
		data = d
	}

	return d.GetDecisionType().String() + ": " + anyToString(data)
}
