package internal

import (
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/encoding/protowire"
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

func TestWorkflowTaskNotificationsIgnoreTokenClock(t *testing.T) {
	// Matching adds field 9 (the shard clock) after History creates the
	// notification token. The other task identity fields must still match.
	prefix := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), "namespace")
	prefix = protowire.AppendString(protowire.AppendTag(prefix, 2, protowire.BytesType), "workflow")
	prefix = protowire.AppendString(protowire.AppendTag(prefix, 3, protowire.BytesType), "run")
	prefix = protowire.AppendVarint(protowire.AppendTag(prefix, 4, protowire.VarintType), 2)
	withoutClock := protowire.AppendVarint(protowire.AppendTag(append([]byte(nil), prefix...), 10, protowire.VarintType), 3)
	withClock := protowire.AppendBytes(protowire.AppendTag(append([]byte(nil), prefix...), 9, protowire.BytesType), []byte{8, 1})
	withClock = protowire.AppendVarint(protowire.AppendTag(withClock, 10, protowire.VarintType), 3)
	if string(withClock) == string(withoutClock) {
		t.Fatal("test tokens must differ")
	}

	n := newWorkflowTaskNotifications()
	ch := make(chan struct{}, 1)
	n.register(&workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		TaskToken:         withClock,
	}, ch)
	n.notify(&workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: withoutClock})
	if len(ch) != 1 {
		t.Fatal("notification did not match token with a shard clock")
	}

	otherTask := protowire.AppendVarint(protowire.AppendTag(append([]byte(nil), prefix...), 10, protowire.VarintType), 4)
	<-ch
	n.notify(&workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: otherTask})
	if len(ch) != 0 {
		t.Fatal("notification for a different task matched")
	}

	early := newWorkflowTaskNotifications()
	early.notify(&workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: withoutClock})
	early.register(&workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		TaskToken:         withClock,
	}, ch)
	if len(ch) != 1 {
		t.Fatal("early notification did not match token with a shard clock")
	}
}
