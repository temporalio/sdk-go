package internal

import (
	"sync"
	"time"

	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/encoding/protowire"
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
	token := workflowTaskNotificationKey(task.GetTaskToken())
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
	token := workflowTaskNotificationKey(command.GetTaskToken())
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

// workflowTaskNotificationKey excludes the token's shard vector clock. History
// creates the notification before it can see the clock added to the worker's
// task token by RecordWorkflowTaskStarted. The clock is not task identity.
func workflowTaskNotificationKey(taskToken []byte) string {
	original := taskToken
	key := make([]byte, 0, len(taskToken))
	for len(taskToken) > 0 {
		fieldNumber, wireType, tagSize := protowire.ConsumeTag(taskToken)
		if tagSize < 0 {
			return string(original)
		}
		valueSize := protowire.ConsumeFieldValue(fieldNumber, wireType, taskToken[tagSize:])
		if valueSize < 0 {
			return string(original)
		}
		fieldSize := tagSize + valueSize
		if fieldNumber != 9 || wireType != protowire.BytesType {
			key = append(key, taskToken[:fieldSize]...)
		}
		taskToken = taskToken[fieldSize:]
	}
	return string(key)
}
