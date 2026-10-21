package internal

import (
	"sync"
	"time"

	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// workflowTaskNotifications routes best-effort notifications to the active task.
// The task identity is checked under the lock so stale commands cannot wake a
// replacement task in the same run.
type workflowTaskNotifications struct {
	mu      sync.Mutex
	tasks   map[string]workflowTaskNotificationTarget
	pending map[workflowTaskNotificationKey]time.Time
}

type workflowTaskNotificationKey struct {
	runID            string
	scheduledEventID int64
	startedEventID   int64
	attempt          int32
	startedSeconds   int64
	startedNanos     int32
	hasStartedTime   bool
}

const (
	maxPendingWorkflowTaskNotifications = 10000
	pendingWorkflowTaskNotificationTTL  = time.Minute
)

type workflowTaskNotificationTarget struct {
	task *workflowservice.PollWorkflowTaskQueueResponse
	ch   chan struct{}
}

func newWorkflowTaskNotifications() *workflowTaskNotifications {
	return &workflowTaskNotifications{
		tasks:   make(map[string]workflowTaskNotificationTarget),
		pending: make(map[workflowTaskNotificationKey]time.Time),
	}
}

func (n *workflowTaskNotifications) register(task *workflowservice.PollWorkflowTaskQueueResponse, ch chan struct{}) {
	if n == nil || task == nil || task.GetWorkflowExecution() == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// A buffered notification for the previous task must not affect this task.
	select {
	case <-ch:
	default:
	}
	n.tasks[task.GetWorkflowExecution().GetRunId()] = workflowTaskNotificationTarget{task: task, ch: ch}
	key := workflowTaskNotificationKeyForTask(task)
	if receivedAt, ok := n.pending[key]; ok {
		delete(n.pending, key)
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
	if current, ok := n.tasks[runID]; ok && current.ch == ch {
		delete(n.tasks, runID)
	}
}

func (n *workflowTaskNotifications) notify(command *workerpb.WorkflowTaskHeartbeatNotificationCommand) {
	if n == nil || command == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if target, ok := n.tasks[command.GetRunId()]; ok && workflowTaskNotificationMatches(target.task, command) {
		select {
		case target.ch <- struct{}{}:
		default:
		}
		return
	}
	if len(n.pending) >= maxPendingWorkflowTaskNotifications {
		for key, receivedAt := range n.pending {
			if time.Since(receivedAt) > pendingWorkflowTaskNotificationTTL {
				delete(n.pending, key)
			}
		}
		if len(n.pending) >= maxPendingWorkflowTaskNotifications {
			return
		}
	}
	n.pending[workflowTaskNotificationKeyForCommand(command)] = time.Now()
}

func workflowTaskNotificationMatches(task *workflowservice.PollWorkflowTaskQueueResponse, command *workerpb.WorkflowTaskHeartbeatNotificationCommand) bool {
	return task.GetWorkflowExecution().GetRunId() == command.GetRunId() &&
		task.GetScheduledEventId() == command.GetScheduledEventId() &&
		task.GetStartedEventId() == command.GetStartedEventId() &&
		task.GetAttempt() == command.GetAttempt() &&
		proto.Equal(task.GetStartedTime(), command.GetStartedTime())
}

func workflowTaskNotificationKeyForTask(task *workflowservice.PollWorkflowTaskQueueResponse) workflowTaskNotificationKey {
	return workflowTaskNotificationKeyFromFields(
		task.GetWorkflowExecution().GetRunId(), task.GetScheduledEventId(), task.GetStartedEventId(), task.GetAttempt(), task.GetStartedTime(),
	)
}

func workflowTaskNotificationKeyForCommand(command *workerpb.WorkflowTaskHeartbeatNotificationCommand) workflowTaskNotificationKey {
	return workflowTaskNotificationKeyFromFields(
		command.GetRunId(), command.GetScheduledEventId(), command.GetStartedEventId(), command.GetAttempt(), command.GetStartedTime(),
	)
}

func workflowTaskNotificationKeyFromFields(runID string, scheduledEventID, startedEventID int64, attempt int32, startedTime *timestamppb.Timestamp) workflowTaskNotificationKey {
	key := workflowTaskNotificationKey{
		runID: runID, scheduledEventID: scheduledEventID, startedEventID: startedEventID, attempt: attempt,
	}
	if startedTime != nil {
		key.hasStartedTime = true
		key.startedSeconds = startedTime.Seconds
		key.startedNanos = startedTime.Nanos
	}
	return key
}
