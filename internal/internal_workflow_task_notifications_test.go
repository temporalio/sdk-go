package internal

import (
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func TestWorkflowTaskNotificationsMatchActiveTask(t *testing.T) {
	n := newWorkflowTaskNotifications()
	ch := make(chan struct{}, 1)
	task := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		TaskToken:         []byte("task-1"),
	}
	n.register(task, ch)
	command := &workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: []byte("task-1")}

	n.notify(command)
	n.notify(command)
	if len(ch) != 1 {
		t.Fatalf("notifications = %d, want 1", len(ch))
	}

	replacement := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		TaskToken:         []byte("task-2"),
	}
	n.register(replacement, ch)
	if len(ch) != 0 {
		t.Fatal("notification for old task survived replacement")
	}
	n.notify(command)
	if len(ch) != 0 {
		t.Fatal("stale task notification woke replacement")
	}
	n.notify(&workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: replacement.TaskToken})
	if len(ch) != 1 {
		t.Fatal("matching notification did not wake replacement")
	}
	n.unregister("run", ch)
	<-ch
	n.notify(&workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: replacement.TaskToken})
	if len(ch) != 0 {
		t.Fatal("unregistered task received a notification")
	}
}

func TestWorkflowTaskNotificationsArriveBeforeTask(t *testing.T) {
	n := newWorkflowTaskNotifications()
	ch := make(chan struct{}, 1)
	task := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		TaskToken:         []byte("task-2"),
	}
	command := &workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: task.TaskToken}

	n.notify(command)
	n.notify(command)
	n.register(task, ch)
	if len(ch) != 1 {
		t.Fatalf("early notifications = %d, want 1", len(ch))
	}
	<-ch
	n.unregister("run", ch)

	oldTask := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		TaskToken:         []byte("task-1"),
	}
	n.register(oldTask, ch)
	n.notify(command)
	if len(ch) != 0 {
		t.Fatal("new task notification woke old task")
	}
	n.register(task, ch)
	if len(ch) != 1 {
		t.Fatal("notification was lost while old task was registered")
	}
	<-ch

	n.unregister("run", ch)
	n.notify(&workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: []byte("other-task")})
	n.register(task, ch)
	if len(ch) != 0 {
		t.Fatal("notification for a different task woke this task")
	}
}
