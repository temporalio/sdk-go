// Copyright (c) 2017 Uber Technologies, Inc.
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
	case eventpb.EventTypeWorkflowExecutionStarted:
		data = e.GetWorkflowExecutionStartedEventAttributes()

	case eventpb.EventTypeWorkflowExecutionCompleted:
		data = e.GetWorkflowExecutionCompletedEventAttributes()

	case eventpb.EventTypeWorkflowExecutionFailed:
		data = e.GetWorkflowExecutionFailedEventAttributes()

	case eventpb.EventTypeWorkflowExecutionTimedOut:
		data = e.GetWorkflowExecutionTimedOutEventAttributes()

	case eventpb.EventTypeDecisionTaskScheduled:
		data = e.GetDecisionTaskScheduledEventAttributes()

	case eventpb.EventTypeDecisionTaskStarted:
		data = e.GetDecisionTaskStartedEventAttributes()

	case eventpb.EventTypeDecisionTaskCompleted:
		data = e.GetDecisionTaskCompletedEventAttributes()

	case eventpb.EventTypeDecisionTaskTimedOut:
		data = e.GetDecisionTaskTimedOutEventAttributes()

	case eventpb.EventTypeActivityTaskScheduled:
		data = e.GetActivityTaskScheduledEventAttributes()

	case eventpb.EventTypeActivityTaskStarted:
		data = e.GetActivityTaskStartedEventAttributes()

	case eventpb.EventTypeActivityTaskCompleted:
		data = e.GetActivityTaskCompletedEventAttributes()

	case eventpb.EventTypeActivityTaskFailed:
		data = e.GetActivityTaskFailedEventAttributes()

	case eventpb.EventTypeActivityTaskTimedOut:
		data = e.GetActivityTaskTimedOutEventAttributes()

	case eventpb.EventTypeActivityTaskCancelRequested:
		data = e.GetActivityTaskCancelRequestedEventAttributes()

	case eventpb.EventTypeRequestCancelActivityTaskFailed:
		data = e.GetRequestCancelActivityTaskFailedEventAttributes()

	case eventpb.EventTypeActivityTaskCanceled:
		data = e.GetActivityTaskCanceledEventAttributes()

	case eventpb.EventTypeTimerStarted:
		data = e.GetTimerStartedEventAttributes()

	case eventpb.EventTypeTimerFired:
		data = e.GetTimerFiredEventAttributes()

	case eventpb.EventTypeCancelTimerFailed:
		data = e.GetCancelTimerFailedEventAttributes()

	case eventpb.EventTypeTimerCanceled:
		data = e.GetTimerCanceledEventAttributes()

	case eventpb.EventTypeMarkerRecorded:
		data = e.GetMarkerRecordedEventAttributes()

	case eventpb.EventTypeWorkflowExecutionTerminated:
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
	case decisionpb.DecisionTypeScheduleActivityTask:
		data = d.GetScheduleActivityTaskDecisionAttributes()

	case decisionpb.DecisionTypeRequestCancelActivityTask:
		data = d.GetRequestCancelActivityTaskDecisionAttributes()

	case decisionpb.DecisionTypeStartTimer:
		data = d.GetStartTimerDecisionAttributes()

	case decisionpb.DecisionTypeCancelTimer:
		data = d.GetCancelTimerDecisionAttributes()

	case decisionpb.DecisionTypeCompleteWorkflowExecution:
		data = d.GetCompleteWorkflowExecutionDecisionAttributes()

	case decisionpb.DecisionTypeFailWorkflowExecution:
		data = d.GetFailWorkflowExecutionDecisionAttributes()

	case decisionpb.DecisionTypeRecordMarker:
		data = d.GetRecordMarkerDecisionAttributes()

	default:
		data = d
	}

	return d.GetDecisionType().String() + ": " + anyToString(data)
}
