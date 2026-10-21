package internal

import (
	"sync"
	"time"

	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
)

// workflowTaskNotifications routes best-effort notifications to the active task.
type workflowTaskNotifications struct {
	mu        sync.Mutex
	tasks     map[string]workflowTaskNotificationTarget
	runTokens map[string]string
	pending   map[string]time.Time
}

const (
	maxPendingWorkflowTaskNotifications = 10000
	pendingWorkflowTaskNotificationTTL  = time.Minute
)

type workflowTaskNotificationTarget struct {
	ch chan struct{}
}

func newWorkflowTaskNotifications() *workflowTaskNotifications {
	return &workflowTaskNotifications{
		tasks:     make(map[string]workflowTaskNotificationTarget),
		runTokens: make(map[string]string),
		pending:   make(map[string]time.Time),
	}
}

func (n *workflowTaskNotifications) register(task *workflowservice.PollWorkflowTaskQueueResponse, ch chan struct{}) {
	if n == nil || task == nil || task.GetWorkflowExecution() == nil || len(task.GetTaskToken()) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// A buffered notification for the previous task must not affect this task.
	select {
	case <-ch:
	default:
	}
	runID := task.GetWorkflowExecution().GetRunId()
	if oldToken, ok := n.runTokens[runID]; ok {
		delete(n.tasks, oldToken)
	}
	token := string(task.GetTaskToken())
	n.runTokens[runID] = token
	n.tasks[token] = workflowTaskNotificationTarget{ch: ch}
	if receivedAt, ok := n.pending[token]; ok {
		delete(n.pending, token)
		if time.Since(receivedAt) <= pendingWorkflowTaskNotificationTTL {
			ch <- struct{}{}
		}
	}
}

func (n *workflowTaskNotifications) unregister(runID string, ch chan struct{}) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if token, ok := n.runTokens[runID]; ok && n.tasks[token].ch == ch {
		delete(n.runTokens, runID)
		delete(n.tasks, token)
	}
}

func (n *workflowTaskNotifications) notify(command *workerpb.WorkflowTaskHeartbeatNotificationCommand) {
	if n == nil || command == nil || len(command.GetTaskToken()) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	token := string(command.GetTaskToken())
	if target, ok := n.tasks[token]; ok {
		select {
		case target.ch <- struct{}{}:
		default:
		}
		return
	}
	if len(n.pending) >= maxPendingWorkflowTaskNotifications {
		for token, receivedAt := range n.pending {
			if time.Since(receivedAt) > pendingWorkflowTaskNotificationTTL {
				delete(n.pending, token)
			}
		}
		if len(n.pending) >= maxPendingWorkflowTaskNotifications {
			return
		}
	}
	n.pending[token] = time.Now()
}
