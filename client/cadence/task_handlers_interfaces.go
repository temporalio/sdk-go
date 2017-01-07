package cadence

import (
	"golang.org/x/net/context"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
)

type (
	// workflowTaskHandler represents workflow task handlers.
	workflowTaskHandler interface {
		// Process the workflow task
		ProcessWorkflowTask(task *workflowTask, emitStack bool) (response *m.RespondDecisionTaskCompletedRequest, stackTrace string, err error)
	}

	// activityTaskHandler represents activity task handlers.
	activityTaskHandler interface {
		// Execute the activity task
		// The return interface{} can have three requests, use switch to find the type of it.
		// - RespondActivityTaskCompletedRequest
		// - RespondActivityTaskFailedRequest
		// - RespondActivityTaskCancelRequest
		Execute(context context.Context, task *activityTask) (interface{}, error)
	}

	// workflowExecutionEventHandler process a single event.
	workflowExecutionEventHandler interface {
		// Process a single event and return the assosciated decisions.
		ProcessEvent(event *m.HistoryEvent) ([]*m.Decision, error)
		StackTrace() string
		// Close for cleaning up resources on this event handler
		Close()
	}

	// workflowTask wraps a decision task.
	workflowTask struct {
		task *m.PollForDecisionTaskResponse
	}

	// activityTask wraps a activity task.
	activityTask struct {
		task *m.PollForActivityTaskResponse
	}
)
