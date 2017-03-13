package cadence

import (
	"context"

	"code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
)

type (
	// ActivityType identifies a activity type.
	ActivityType struct {
		Name string
	}

	// Activity is an interface of an activity implementation.
	Activity interface {
		Execute(ctx context.Context, input []byte) ([]byte, error)
		ActivityType() ActivityType
	}

	// ActivityInfo contains information about currently executing activity.
	ActivityInfo struct {
		TaskToken         []byte
		WorkflowExecution WorkflowExecution
		ActivityID        string
		ActivityType      ActivityType
	}
)

// GetActivityInfo returns information about currently executing activity.
func GetActivityInfo(ctx context.Context) ActivityInfo {
	env := getActivityEnv(ctx)
	return ActivityInfo{
		ActivityID:        env.activityID,
		ActivityType:      env.activityType,
		TaskToken:         env.taskToken,
		WorkflowExecution: env.workflowExecution,
	}
}

// RecordActivityHeartbeat sends heartbeat for the currently executing activity
// TODO: Implement automatic heartbeating with cancellation through ctx.
func RecordActivityHeartbeat(ctx context.Context, details []byte) error {
	env := getActivityEnv(ctx)
	return env.serviceInvoker.Heartbeat(details)
}

// ServiceInvoker abstracts calls to the Cadence service from an Activity implementation.
// Implement to unit test activities.
type ServiceInvoker interface {
	// Returns ActivityTaskCanceledError if activity is cancelled
	Heartbeat(details []byte) error
}

// WithActivityTask adds activity specific information into context.
// Use this method to unit test activity implementations that use context extractor methodshared.
func WithActivityTask(
	ctx context.Context,
	task *shared.PollForActivityTaskResponse,
	invoker ServiceInvoker,
) context.Context {
	// TODO: Add activity start to close timeout to activity task and use it as the deadline
	return context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		taskToken:      task.TaskToken,
		serviceInvoker: invoker,
		activityType:   ActivityType{Name: *task.ActivityType.Name},
		activityID:     *task.ActivityId,
		workflowExecution: WorkflowExecution{
			RunID: *task.WorkflowExecution.RunId,
			ID:    *task.WorkflowExecution.WorkflowId},
	})
}
