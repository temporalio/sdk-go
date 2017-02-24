package cadence

import (
	"context"

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
