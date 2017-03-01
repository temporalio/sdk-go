package cadence

import (
	"context"

	"fmt"

	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/backoff"
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
		Identity          string
	}

	// ActivityTaskFailedError wraps the details of the failure of activity
	ActivityTaskFailedError struct {
		reason  string
		details []byte
	}

	// ActivityTaskTimeoutError wraps the details of the timeout of activity
	ActivityTaskTimeoutError struct {
		TimeoutType s.TimeoutType
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
		Identity:          env.identity,
		TaskToken:         env.taskToken,
		WorkflowExecution: env.workflowExecution,
	}
}

// RecordActivityHeartbeat sends heartbeat for the currently executing activity
// TODO: Implement automatic heartbeating with cancellation through ctx.
func RecordActivityHeartbeat(ctx context.Context, details []byte) error {
	env := getActivityEnv(ctx)
	request := &s.RecordActivityTaskHeartbeatRequest{
		TaskToken: env.taskToken,
		Details:   details,
		Identity:  common.StringPtr(env.identity)}

	err := backoff.Retry(
		func() error {
			ctx, cancel := common.NewTChannelContext(respondTaskServiceTimeOut, common.RetryDefaultOptions)
			defer cancel()

			// TODO: Handle the propagation of Cancel to activity.
			_, err2 := env.service.RecordActivityTaskHeartbeat(ctx, request)
			return err2
		}, serviceOperationRetryPolicy, isServiceTransientError)
	return err
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
