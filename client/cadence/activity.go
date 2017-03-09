package cadence

import (
	"context"
	"fmt"

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

	// ActivityTaskFailedError wraps the details of the failure of activity
	ActivityTaskFailedError struct {
		reason  string
		details []byte
	}

	// ActivityTaskTimeoutError wraps the details of the timeout of activity
	ActivityTaskTimeoutError struct {
		TimeoutType shared.TimeoutType
	}

	// ActivityTaskCanceledError wraps the details of the activity cancellation
	ActivityTaskCanceledError struct {
		details []byte
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

// Error from error.Error
func (e ActivityTaskFailedError) Error() string {
	return fmt.Sprintf("Reason: %s, Details: %s", e.reason, e.details)
}

// Details of the error
func (e ActivityTaskFailedError) Details() []byte {
	return e.details
}

// Reason of the error
func (e ActivityTaskFailedError) Reason() string {
	return e.reason
}

// Error from error.Error
func (e ActivityTaskTimeoutError) Error() string {
	return fmt.Sprintf("TimeoutType: %v", e.TimeoutType)
}

// Details of the error
func (e ActivityTaskTimeoutError) Details() []byte {
	return nil
}

// Reason of the error
func (e ActivityTaskTimeoutError) Reason() string {
	return e.Error()
}

// Error from error.Error
func (e ActivityTaskCanceledError) Error() string {
	return fmt.Sprintf("Details: %s", e.details)
}

// Details of the error
func (e ActivityTaskCanceledError) Details() []byte {
	return e.details
}

// Reason of the error
func (e ActivityTaskCanceledError) Reason() string {
	return e.Error()
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
