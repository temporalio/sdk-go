package activity

import (
	"context"

	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
)

type (
	// Type identifies an activity type.
	Type = internal.ActivityType

	// Info contains information about a currently executing activity.
	Info = internal.ActivityInfo

	// RegisterOptions consists of options for registering an activity.
	RegisterOptions = internal.RegisterActivityOptions

	// DynamicRegisterOptions consists of options for registering a dynamic activity.
	DynamicRegisterOptions = internal.DynamicRegisterActivityOptions
)

// ErrResultPending is returned from activity's implementation to indicate the activity is not completed when the
// activity method returns. Activity needs to be completed by Client.CompleteActivity() separately. For example, if an
// activity requires human interaction (like approving an expense report), the activity could return ErrResultPending,
// which indicates the activity is not done yet. Then, when the waited human action happened, it needs to trigger something
// that could report the activity completed event to the temporal server via the Client.CompleteActivity() API.
var ErrResultPending = internal.ErrActivityResultPending

// ErrActivityPaused is returned from an activity heartbeat or the cause of an activity's context to indicate that the activity is paused.
//
// WARNING: Activity pause is currently experimental
var ErrActivityPaused = internal.ErrActivityPaused

// ErrActivityReset is returned from an activity heartbeat or the cause of an activity's context to indicate that the activity has been reset.
//
// WARNING: Activity reset is currently experimental
var ErrActivityReset = internal.ErrActivityReset

// GetInfo returns information about the currently executing activity.
func GetInfo(ctx context.Context) Info {
	return internal.GetActivityInfo(ctx)
}

// GetLogger returns a logger that can be used in the activity.
func GetLogger(ctx context.Context) log.Logger {
	return internal.GetActivityLogger(ctx)
}

// GetMetricsHandler returns a metrics handler that can be used in the activity.
func GetMetricsHandler(ctx context.Context) metrics.Handler {
	return internal.GetActivityMetricsHandler(ctx)
}

// RecordHeartbeat sends a heartbeat for the currently executing activity.
// If the activity is either canceled or the workflow/activity doesn't exist, then we would cancel
// the context with error [context.Canceled]. The [context.Cause] will be set based on the reason
// for the cancellation.
//
// For example, if the activity is requested to be paused by the Server:
//
//		func MyActivity(ctx context.Context) error {
//			activity.RecordHeartbeat(ctx, "")
//			// assume the activity is paused by the server
//			activity.RecordHeartbeat(ctx, "some details")
//			context.Cause(ctx) // Will return activity.ErrActivityPaused
//	     	return ctx.Err() // Will return context.Canceled
//		}
//
// details - The details that you provide here can be seen in the workflow when it receives TimeoutError. You
// can check error with TimeoutType()/Details().
//
// Note: If using asynchronous activity completion,
// after returning [ErrResultPending] users should heartbeat with [go.temporal.io/sdk/client.Client.RecordActivityHeartbeat]
func RecordHeartbeat(ctx context.Context, details ...interface{}) {
	internal.RecordActivityHeartbeat(ctx, details...)
}

// HasHeartbeatDetails checks if there are heartbeat details from the last attempt.
func HasHeartbeatDetails(ctx context.Context) bool {
	return internal.HasHeartbeatDetails(ctx)
}

// GetHeartbeatDetails extracts heartbeat details from the last failed attempt. This is used in combination with the retry policy.
// An activity could be scheduled with an optional retry policy on ActivityOptions. If the activity failed, then server
// would attempt to dispatch another activity task to retry according to the retry policy. If there were heartbeat
// details reported by activity from the failed attempt, the details would be delivered along with the activity task for
// the retry attempt. An activity can extract the details from GetHeartbeatDetails() and resume progress from there.
// See TestActivityEnvironment.SetHeartbeatDetails() for unit test support.
//
// Note: Values should not be reused for extraction here because merging on top
// of existing values may result in unexpected behavior similar to json.Unmarshal.
func GetHeartbeatDetails(ctx context.Context, d ...interface{}) error {
	return internal.GetHeartbeatDetails(ctx, d...)
}

// GetWorkerStopChannel returns a read-only channel. The closure of this channel indicates the activity worker is stopping.
// When the worker is stopping, it will close this channel and wait until the worker stop timeout finishes. After the timeout
// hits, the worker will cancel the activity context and then exit. The timeout can be defined by worker option: WorkerStopTimeout.
// Use this channel to handle a graceful activity exit when the activity worker stops.
func GetWorkerStopChannel(ctx context.Context) <-chan struct{} {
	return internal.GetWorkerStopChannel(ctx)
}

// IsActivity checks if the context is an activity context from a normal or local activity.
func IsActivity(ctx context.Context) bool {
	return internal.IsActivity(ctx)
}

// GetClient returns a client that can be used to interact with the Temporal
// service from an activity. Return type internal.Client is the same underlying
// type as client.Client.
func GetClient(ctx context.Context) internal.Client {
	return internal.GetClient(ctx)
}
