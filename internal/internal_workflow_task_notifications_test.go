package internal

import (
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWorkflowTaskNotificationsMatchActiveTask(t *testing.T) {
	n := newWorkflowTaskNotifications()
	ch := make(chan struct{}, 1)
	started := timestamppb.New(time.Unix(100, 0))
	task := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		ScheduledEventId:  10,
		StartedEventId:    11,
		Attempt:           2,
		StartedTime:       started,
	}
	n.register(task, ch)
	command := &workerpb.WorkflowTaskHeartbeatNotificationCommand{
		RunId: "run", ScheduledEventId: 10, StartedEventId: 11,
		Attempt: 2, StartedTime: started,
	}

	n.notify(command)
	n.notify(command) // Duplicates coalesce into one wakeup.
	if len(ch) != 1 {
		t.Fatalf("notifications = %d, want 1", len(ch))
	}

	replacement := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		ScheduledEventId:  12,
		StartedEventId:    13,
		Attempt:           1,
		StartedTime:       timestamppb.New(time.Unix(101, 0)),
	}
	n.register(replacement, ch)
	if len(ch) != 0 {
		t.Fatal("notification for old task survived replacement")
	}
	n.notify(command)
	if len(ch) != 0 {
		t.Fatal("stale task notification woke replacement")
	}
	command.ScheduledEventId = 12
	command.StartedEventId = 13
	command.Attempt = 1
	// The timestamp also distinguishes recreated speculative tasks.
	n.notify(command)
	if len(ch) != 0 {
		t.Fatal("wrong start time woke replacement")
	}
	command.StartedTime = replacement.StartedTime
	n.notify(command)
	if len(ch) != 1 {
		t.Fatal("matching notification did not wake replacement")
	}
	n.unregister("run", ch)
	<-ch
	n.notify(command)
	if len(ch) != 0 {
		t.Fatal("unregistered task received a notification")
	}
}

func TestWorkflowTaskNotificationsArriveBeforeTask(t *testing.T) {
	n := newWorkflowTaskNotifications()
	ch := make(chan struct{}, 1)
	started := timestamppb.New(time.Unix(100, 0))
	task := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		ScheduledEventId:  10,
		StartedEventId:    11,
		Attempt:           2,
		StartedTime:       started,
	}
	command := &workerpb.WorkflowTaskHeartbeatNotificationCommand{
		RunId: "run", ScheduledEventId: 10, StartedEventId: 11,
		Attempt: 2, StartedTime: started,
	}

	n.notify(command)
	n.notify(command)
	n.register(task, ch)
	if len(ch) != 1 {
		t.Fatalf("early notifications = %d, want 1", len(ch))
	}
	<-ch
	n.unregister("run", ch)

	// A notification for a different task must not wake this task, even when
	// the previous task is still registered when that notification arrives.
	oldTask := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution: &commonpb.WorkflowExecution{RunId: "run"},
		ScheduledEventId:  8,
		StartedEventId:    9,
		Attempt:           1,
		StartedTime:       timestamppb.New(time.Unix(99, 0)),
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

	// A different speculative task may reuse the event IDs and attempt.
	n.unregister("run", ch)
	command.StartedTime = timestamppb.New(time.Unix(101, 0))
	n.notify(command)
	n.register(task, ch)
	if len(ch) != 0 {
		t.Fatal("notification for a different task woke this task")
	}
}
