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

	s "go.uber.org/cadence/.gen/go/shared"
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
func HistoryEventToString(e *s.HistoryEvent) string {
	var data interface{}
	switch e.GetEventType() {
	case s.EventTypeWorkflowExecutionStarted:
		data = e.WorkflowExecutionStartedEventAttributes

	case s.EventTypeWorkflowExecutionCompleted:
		data = e.WorkflowExecutionCompletedEventAttributes

	case s.EventTypeWorkflowExecutionFailed:
		data = e.WorkflowExecutionFailedEventAttributes

	case s.EventTypeWorkflowExecutionTimedOut:
		data = e.WorkflowExecutionTimedOutEventAttributes

	case s.EventTypeDecisionTaskScheduled:
		data = e.DecisionTaskScheduledEventAttributes

	case s.EventTypeDecisionTaskStarted:
		data = e.DecisionTaskStartedEventAttributes

	case s.EventTypeDecisionTaskCompleted:
		data = e.DecisionTaskCompletedEventAttributes

	case s.EventTypeDecisionTaskTimedOut:
		data = e.DecisionTaskTimedOutEventAttributes

	case s.EventTypeActivityTaskScheduled:
		data = e.ActivityTaskScheduledEventAttributes

	case s.EventTypeActivityTaskStarted:
		data = e.ActivityTaskStartedEventAttributes

	case s.EventTypeActivityTaskCompleted:
		data = e.ActivityTaskCompletedEventAttributes

	case s.EventTypeActivityTaskFailed:
		data = e.ActivityTaskFailedEventAttributes

	case s.EventTypeActivityTaskTimedOut:
		data = e.ActivityTaskTimedOutEventAttributes

	case s.EventTypeActivityTaskCancelRequested:
		data = e.ActivityTaskCancelRequestedEventAttributes

	case s.EventTypeRequestCancelActivityTaskFailed:
		data = e.RequestCancelActivityTaskFailedEventAttributes

	case s.EventTypeActivityTaskCanceled:
		data = e.ActivityTaskCanceledEventAttributes

	case s.EventTypeTimerStarted:
		data = e.TimerStartedEventAttributes

	case s.EventTypeTimerFired:
		data = e.TimerFiredEventAttributes

	case s.EventTypeCancelTimerFailed:
		data = e.CancelTimerFailedEventAttributes

	case s.EventTypeTimerCanceled:
		data = e.TimerCanceledEventAttributes

	case s.EventTypeMarkerRecorded:
		data = e.MarkerRecordedEventAttributes

	case s.EventTypeWorkflowExecutionTerminated:
		data = e.WorkflowExecutionTerminatedEventAttributes

	default:
		data = e
	}

	return e.GetEventType().String() + ": " + anyToString(data)
}

// DecisionToString convert Decision to string
func DecisionToString(d *s.Decision) string {
	var data interface{}
	switch d.GetDecisionType() {
	case s.DecisionTypeScheduleActivityTask:
		data = d.ScheduleActivityTaskDecisionAttributes

	case s.DecisionTypeRequestCancelActivityTask:
		data = d.RequestCancelActivityTaskDecisionAttributes

	case s.DecisionTypeStartTimer:
		data = d.StartTimerDecisionAttributes

	case s.DecisionTypeCancelTimer:
		data = d.CancelTimerDecisionAttributes

	case s.DecisionTypeCompleteWorkflowExecution:
		data = d.CompleteWorkflowExecutionDecisionAttributes

	case s.DecisionTypeFailWorkflowExecution:
		data = d.FailWorkflowExecutionDecisionAttributes

	case s.DecisionTypeRecordMarker:
		data = d.RecordMarkerDecisionAttributes

	default:
		data = d
	}

	return d.GetDecisionType().String() + ": " + anyToString(data)
}
