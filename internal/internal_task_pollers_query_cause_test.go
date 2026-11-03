package internal

import (
	"errors"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	querypb "go.temporal.io/api/query/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func TestWorkflowQueryFailureCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want enums.WorkflowTaskFailedCause
	}{
		{
			name: "generic error stays unspecified",
			err:  errors.New("some handler failure"),
			want: enums.WORKFLOW_TASK_FAILED_CAUSE_UNSPECIFIED,
		},
		{
			name: "payload size error",
			err:  payloadSizeError{message: "too large", size: 100, limit: 50},
			want: enums.WORKFLOW_TASK_FAILED_CAUSE_PAYLOADS_TOO_LARGE,
		},
		{
			name: "workflow panic",
			err:  newWorkflowPanicError("boom", "stack"),
			want: enums.WORKFLOW_TASK_FAILED_CAUSE_WORKFLOW_WORKER_UNHANDLED_FAILURE,
		},
		{
			name: "returned PanicError stays unspecified",
			err:  newPanicError("boom", "stack"),
			want: enums.WORKFLOW_TASK_FAILED_CAUSE_UNSPECIFIED,
		},
		{
			name: "illegal state machine panic",
			err:  newWorkflowPanicError(stateMachineIllegalStatePanic{message: "bad state"}, "stack"),
			want: enums.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
		},
		{
			name: "history mismatch",
			err:  historyMismatchError{},
			want: enums.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
		},
		{
			name: "unknown sdk flag",
			err:  unknownSdkFlagError{},
			want: enums.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflowQueryFailureCause(tt.err); got != tt.want {
				t.Fatalf("workflowQueryFailureCause() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaskFailureCompletionQuerySetsCause(t *testing.T) {
	wtp := &workflowTaskProcessor{
		namespace:        "test-namespace",
		failureConverter: GetDefaultFailureConverter(),
	}
	task := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "w", RunId: "r"},
		WorkflowType:      &commonpb.WorkflowType{Name: "wf"},
		Query:             &querypb.WorkflowQuery{},
		TaskToken:         []byte("token"),
	}

	tests := []struct {
		name      string
		err       error
		wantCause enums.WorkflowTaskFailedCause
	}{
		{
			name:      "generic error stays unspecified",
			err:       errors.New("visit failure"),
			wantCause: enums.WORKFLOW_TASK_FAILED_CAUSE_UNSPECIFIED,
		},
		{
			name:      "payload size error",
			err:       payloadSizeError{message: "too large", size: 100, limit: 50},
			wantCause: enums.WORKFLOW_TASK_FAILED_CAUSE_PAYLOADS_TOO_LARGE,
		},
		{
			name:      "workflow panic",
			err:       newWorkflowPanicError("boom", "stack"),
			wantCause: enums.WORKFLOW_TASK_FAILED_CAUSE_WORKFLOW_WORKER_UNHANDLED_FAILURE,
		},
		{
			name:      "returned PanicError stays unspecified",
			err:       newPanicError("boom", "stack"),
			wantCause: enums.WORKFLOW_TASK_FAILED_CAUSE_UNSPECIFIED,
		},
		{
			name:      "illegal state machine panic",
			err:       newWorkflowPanicError(stateMachineIllegalStatePanic{message: "bad state"}, "stack"),
			wantCause: enums.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
		},
		{
			name:      "unknown sdk flag",
			err:       unknownSdkFlagError{},
			wantCause: enums.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			completion := wtp.taskFailureCompletion(task, tt.err)
			request, ok := completion.rawRequest.(*workflowservice.RespondQueryTaskCompletedRequest)
			if !ok {
				t.Fatalf("expected RespondQueryTaskCompletedRequest, got %T", completion.rawRequest)
			}
			if request.CompletedType != enums.QUERY_RESULT_TYPE_FAILED {
				t.Fatalf("completed type = %v, want FAILED", request.CompletedType)
			}
			if request.Cause != tt.wantCause {
				t.Fatalf("cause = %v, want %v", request.Cause, tt.wantCause)
			}
		})
	}
}
