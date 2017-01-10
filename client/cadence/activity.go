package cadence

import (
	"golang.org/x/net/context"

	"github.com/uber/tchannel-go/thrift"

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
		Execute(ctx context.Context, input []byte) ([]byte, Error)
		ActivityType() ActivityType
	}

	// ActivityInfo contains information about currently executing activity.
	ActivityInfo struct {
		taskToken         []byte
		workflowExecution WorkflowExecution
		activityID        string
		activityType      ActivityType
		identity          string
	}
)

// GetActivityInfo returns information about currently executing activity.
func GetActivityInfo(ctx context.Context) ActivityInfo {
	env := getActivityEnv(ctx)
	return ActivityInfo{
		activityID:        env.activityID,
		activityType:      env.activityType,
		identity:          env.identity,
		taskToken:         env.taskToken,
		workflowExecution: env.workflowExecution,
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
			ctx, cancel := thrift.NewContext(serviceTimeOut)
			defer cancel()

			// TODO: Handle the propagation of Cancel to activity.
			_, err2 := env.service.RecordActivityTaskHeartbeat(ctx, request)
			return err2
		}, serviceOperationRetryPolicy, isServiceTransientError)
	return err
}
