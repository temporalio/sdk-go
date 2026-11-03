package internal

import (
	"sync"
	"sync/atomic"

	workerpb "go.temporal.io/api/worker/v1"
)

// workflowTaskHeartbeatNotifications routes notifications without taking workflow execution locks.
// The handler owns registration lifetimes; notification acceptance and retirement are serialized.
type workflowTaskHeartbeatNotifications struct {
	mu sync.Mutex
	// WFT token bytes as a string -> notification channel and dedup state; guarded by mu.
	tasks map[string]*workflowTaskHeartbeatNotification
	// Owned by sharedNamespaceWorker for this client and namespace. Workflow pollers
	// and eager dispatch read it to advertise WHN only while command reception is enabled.
	commandWorkerEnabled atomic.Bool
}

type workflowTaskHeartbeatNotification struct {
	ready    chan struct{}
	received bool
}

func (r *workflowTaskHeartbeatNotifications) canReceiveNotifications() bool {
	return r != nil && r.commandWorkerEnabled.Load()
}

func (r *workflowTaskHeartbeatNotifications) register(token []byte) (<-chan struct{}, func()) {
	if r == nil || len(token) == 0 {
		return nil, func() {}
	}
	key := string(token)
	notification := &workflowTaskHeartbeatNotification{ready: make(chan struct{}, 1)}
	r.mu.Lock()
	if r.tasks == nil {
		r.tasks = make(map[string]*workflowTaskHeartbeatNotification)
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

func (r *workflowTaskHeartbeatNotifications) notify(token []byte) workerpb.WorkflowTaskHeartbeatNotificationResult_Outcome {
	if r == nil {
		return workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tasks) == 0 {
		return workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK
	}
	notification := r.tasks[string(token)]
	if notification == nil {
		return workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH
	}
	if notification.received {
		return workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_ALREADY_RECEIVED
	}
	notification.received = true
	notification.ready <- struct{}{}
	return workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED // TODO: API should rename to OUTCOME_ACCEPTED
}
