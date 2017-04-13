package util

import (
	"bytes"
	"fmt"
	"reflect"

	s "github.com/uber-go/cadence-client/.gen/go/shared"
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
		// TODO: find a better way to handle this.
		return fmt.Sprintf("[len=%d]", v.Len())
	default:
		return fmt.Sprint(v.Interface())
	}
}

// HistoryEventToString convert HistoryEvent to string
func HistoryEventToString(e *s.HistoryEvent) string {
	var data interface{}
	switch e.GetEventType() {
	case s.EventType_WorkflowExecutionStarted:
		data = e.GetWorkflowExecutionStartedEventAttributes()

	case s.EventType_WorkflowExecutionCompleted:
		data = e.GetWorkflowExecutionCompletedEventAttributes()

	case s.EventType_WorkflowExecutionFailed:
		data = e.GetWorkflowExecutionFailedEventAttributes()

	case s.EventType_WorkflowExecutionTimedOut:
		data = e.GetWorkflowExecutionTimedOutEventAttributes()

	case s.EventType_DecisionTaskScheduled:
		data = e.GetDecisionTaskScheduledEventAttributes()

	case s.EventType_DecisionTaskStarted:
		data = e.GetDecisionTaskStartedEventAttributes()

	case s.EventType_DecisionTaskCompleted:
		data = e.GetDecisionTaskCompletedEventAttributes()

	case s.EventType_DecisionTaskTimedOut:
		data = e.GetDecisionTaskTimedOutEventAttributes()

	case s.EventType_ActivityTaskScheduled:
		data = e.GetActivityTaskScheduledEventAttributes()

	case s.EventType_ActivityTaskStarted:
		data = e.GetActivityTaskStartedEventAttributes()

	case s.EventType_ActivityTaskCompleted:
		data = e.GetActivityTaskCompletedEventAttributes()

	case s.EventType_ActivityTaskFailed:
		data = e.GetActivityTaskFailedEventAttributes()

	case s.EventType_ActivityTaskTimedOut:
		data = e.GetActivityTaskTimedOutEventAttributes()

	case s.EventType_ActivityTaskCancelRequested:
		data = e.GetActivityTaskCancelRequestedEventAttributes()

	case s.EventType_RequestCancelActivityTaskFailed:
		data = e.GetRequestCancelActivityTaskFailedEventAttributes()

	case s.EventType_ActivityTaskCanceled:
		data = e.GetActivityTaskCanceledEventAttributes()

	case s.EventType_TimerStarted:
		data = e.GetTimerStartedEventAttributes()

	case s.EventType_TimerFired:
		data = e.GetTimerFiredEventAttributes()

	case s.EventType_CompleteWorkflowExecutionFailed:
		data = e.GetCompleteWorkflowExecutionFailedEventAttributes()

	case s.EventType_CancelTimerFailed:
		data = e.GetCancelTimerFailedEventAttributes()

	case s.EventType_TimerCanceled:
		data = e.GetTimerCanceledEventAttributes()

	case s.EventType_MarkerRecorded:
		data = e.GetMarkerRecordedEventAttributes()

	case s.EventType_WorkflowExecutionTerminated:
		data = e.GetWorkflowExecutionTerminatedEventAttributes()

	default:
		data = e
	}

	return e.GetEventType().String() + ": " + anyToString(data)
}

// DecisionToString convert Decision to string
func DecisionToString(d *s.Decision) string {
	var data interface{}
	switch d.GetDecisionType() {
	case s.DecisionType_ScheduleActivityTask:
		data = d.GetScheduleActivityTaskDecisionAttributes()

	case s.DecisionType_RequestCancelActivityTask:
		data = d.GetRequestCancelActivityTaskDecisionAttributes()

	case s.DecisionType_StartTimer:
		data = d.GetStartTimerDecisionAttributes()

	case s.DecisionType_CancelTimer:
		data = d.GetCancelTimerDecisionAttributes()

	case s.DecisionType_CompleteWorkflowExecution:
		data = d.GetCompleteWorkflowExecutionDecisionAttributes()

	case s.DecisionType_FailWorkflowExecution:
		data = d.GetFailWorkflowExecutionDecisionAttributes()

	case s.DecisionType_RecordMarker:
		data = d.GetRecordMarkerDecisionAttributes()

	default:
		data = d
	}

	return d.GetDecisionType().String() + ": " + anyToString(data)
}
