package internal

import (
	"bytes"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	workerpb "go.temporal.io/api/worker/v1"
)

// workflowTaskHeartbeatNotifications routes notifications without taking workflow execution locks.
// The handler owns registration lifetimes; notification acceptance and retirement are serialized.
type workflowTaskHeartbeatNotifications struct {
	mu sync.Mutex
	// Workflow execution -> current WFT token, notification channel, and dedup state; guarded by mu.
	tasks map[WorkflowExecution]*workflowTaskHeartbeatNotification
}

type workflowTaskHeartbeatNotification struct {
	token    []byte
	ready    chan struct{}
	received bool
}

func (r *workflowTaskHeartbeatNotifications) register(token []byte, execution *commonpb.WorkflowExecution) (<-chan struct{}, func()) {
	if r == nil || len(token) == 0 {
		return nil, func() {}
	}
	key := WorkflowExecution{ID: execution.GetWorkflowId(), RunID: execution.GetRunId()}
	notification := &workflowTaskHeartbeatNotification{token: token, ready: make(chan struct{}, 1)}
	r.mu.Lock()
	if r.tasks == nil {
		r.tasks = make(map[WorkflowExecution]*workflowTaskHeartbeatNotification)
	}
	r.tasks[key] = notification
	r.mu.Unlock()
	return notification.ready, func() {
		r.mu.Lock()
		if r.tasks[key] == notification {
			delete(r.tasks, key)
		}
		r.mu.Unlock()
	}
}

func (r *workflowTaskHeartbeatNotifications) notify(token []byte, execution *commonpb.WorkflowExecution) *workerpb.WorkflowHeartbeatNotificationResult {
	result := &workerpb.WorkflowHeartbeatNotificationResult{Outcome: workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH}
	if r == nil {
		return result
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	notification := r.tasks[WorkflowExecution{ID: execution.GetWorkflowId(), RunID: execution.GetRunId()}]
	if notification == nil {
		return result
	}
	if !bytes.Equal(notification.token, token) {
		result.CurrentTaskToken = notification.token
		return result
	}
	if !notification.received {
		notification.received = true
		notification.ready <- struct{}{}
	}
	result.Outcome = workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED
	return result
}
