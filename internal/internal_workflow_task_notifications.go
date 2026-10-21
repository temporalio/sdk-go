package internal

import (
	"sync"

	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
)

// workflowTaskNotifications routes best-effort notifications to the active task.
// The task identity is checked under the lock so stale commands cannot wake a
// replacement task in the same run.
type workflowTaskNotifications struct {
	mu    sync.Mutex
	tasks map[string]workflowTaskNotificationTarget
}

type workflowTaskNotificationTarget struct {
	task *workflowservice.PollWorkflowTaskQueueResponse
	ch   chan struct{}
}

func newWorkflowTaskNotifications() *workflowTaskNotifications {
	return &workflowTaskNotifications{tasks: make(map[string]workflowTaskNotificationTarget)}
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
	target, ok := n.tasks[command.GetRunId()]
	if !ok || target.task.GetScheduledEventId() != command.GetScheduledEventId() ||
		target.task.GetStartedEventId() != command.GetStartedEventId() ||
		target.task.GetAttempt() != command.GetAttempt() ||
		!proto.Equal(target.task.GetStartedTime(), command.GetStartedTime()) {
		return
	}
	select {
	case target.ch <- struct{}{}:
	default:
	}
}
