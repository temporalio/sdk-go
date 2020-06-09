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
	case eventpb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
		data = e.GetWorkflowExecutionStartedEventAttributes()

	case eventpb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		data = e.GetWorkflowExecutionCompletedEventAttributes()

	case eventpb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		data = e.GetWorkflowExecutionFailedEventAttributes()

	case eventpb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
		data = e.GetWorkflowExecutionTimedOutEventAttributes()

	case eventpb.EVENT_TYPE_DECISION_TASK_SCHEDULED:
		data = e.GetDecisionTaskScheduledEventAttributes()

	case eventpb.EVENT_TYPE_DECISION_TASK_STARTED:
		data = e.GetDecisionTaskStartedEventAttributes()

	case eventpb.EVENT_TYPE_DECISION_TASK_COMPLETED:
		data = e.GetDecisionTaskCompletedEventAttributes()

	case eventpb.EVENT_TYPE_DECISION_TASK_TIMED_OUT:
		data = e.GetDecisionTaskTimedOutEventAttributes()

	case eventpb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
		data = e.GetActivityTaskScheduledEventAttributes()

	case eventpb.EVENT_TYPE_ACTIVITY_TASK_STARTED:
		data = e.GetActivityTaskStartedEventAttributes()

	case eventpb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
		data = e.GetActivityTaskCompletedEventAttributes()

	case eventpb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
		data = e.GetActivityTaskFailedEventAttributes()

	case eventpb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
		data = e.GetActivityTaskTimedOutEventAttributes()

	case eventpb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED:
		data = e.GetActivityTaskCancelRequestedEventAttributes()

	case eventpb.EVENT_TYPE_ACTIVITY_TASK_CANCELED:
		data = e.GetActivityTaskCanceledEventAttributes()

	case eventpb.EVENT_TYPE_TIMER_STARTED:
		data = e.GetTimerStartedEventAttributes()

	case eventpb.EVENT_TYPE_TIMER_FIRED:
		data = e.GetTimerFiredEventAttributes()

	case eventpb.EVENT_TYPE_CANCEL_TIMER_FAILED:
		data = e.GetCancelTimerFailedEventAttributes()

	case eventpb.EVENT_TYPE_TIMER_CANCELED:
		data = e.GetTimerCanceledEventAttributes()

	case eventpb.EVENT_TYPE_MARKER_RECORDED:
		data = e.GetMarkerRecordedEventAttributes()

	case eventpb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
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
	case decisionpb.DECISION_TYPE_SCHEDULE_ACTIVITY_TASK:
		data = d.GetScheduleActivityTaskDecisionAttributes()

	case decisionpb.DECISION_TYPE_REQUEST_CANCEL_ACTIVITY_TASK:
		data = d.GetRequestCancelActivityTaskDecisionAttributes()

	case decisionpb.DECISION_TYPE_START_TIMER:
		data = d.GetStartTimerDecisionAttributes()

	case decisionpb.DECISION_TYPE_CANCEL_TIMER:
		data = d.GetCancelTimerDecisionAttributes()

	case decisionpb.DECISION_TYPE_COMPLETE_WORKFLOW_EXECUTION:
		data = d.GetCompleteWorkflowExecutionDecisionAttributes()

	case decisionpb.DECISION_TYPE_FAIL_WORKFLOW_EXECUTION:
		data = d.GetFailWorkflowExecutionDecisionAttributes()

	case decisionpb.DECISION_TYPE_RECORD_MARKER:
		data = d.GetRecordMarkerDecisionAttributes()

	default:
		data = d
	}

	return d.GetDecisionType().String() + ": " + anyToString(data)
}
