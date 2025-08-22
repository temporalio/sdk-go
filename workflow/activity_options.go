package workflow

import (
	"time"

	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/temporal"
)

// ActivityOptions stores all activity-specific invocation parameters that will be stored inside of a context.
type ActivityOptions = internal.ActivityOptions

// LocalActivityOptions doc
type LocalActivityOptions = internal.LocalActivityOptions

// WithActivityOptions makes a copy of the context and adds the
// passed in options to the context. If an activity options exists,
// it will be overwritten by the passed in value as a whole.
// So specify all the values in the options as necessary, as values
// in the existing context options will not be carried over.
func WithActivityOptions(ctx Context, options ActivityOptions) Context {
	return internal.WithActivityOptions(ctx, options)
}

// WithLocalActivityOptions makes a copy of the context and adds the
// passed in options to the context. If a local activity options exists,
// it will be overwritten by the passed in value.
func WithLocalActivityOptions(ctx Context, options LocalActivityOptions) Context {
	return internal.WithLocalActivityOptions(ctx, options)
}

// WithTaskQueue makes a copy of the current context and update the taskQueue
// field in its activity options. An empty activity options will be created
// if it does not exist in the original context.
func WithTaskQueue(ctx Context, name string) Context {
	return internal.WithTaskQueue(ctx, name)
}

// WithScheduleToCloseTimeout makes a copy of the current context and update
// the ScheduleToCloseTimeout field in its activity options. An empty activity
// options will be created if it does not exist in the original context.
//
// Temporal time resolution is in seconds and the library uses math.Ceil(d.Seconds())
// to calculate the final value. This is subject to change in the future.
func WithScheduleToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithScheduleToCloseTimeout(ctx, d)
}

// WithScheduleToStartTimeout makes a copy of the current context and update
// the ScheduleToStartTimeout field in its activity options. An empty activity
// options will be created if it does not exist in the original context.
//
// Temporal time resolution is in seconds and the library uses math.Ceil(d.Seconds())
// to calculate the final value. This is subject to change in the future.
func WithScheduleToStartTimeout(ctx Context, d time.Duration) Context {
	return internal.WithScheduleToStartTimeout(ctx, d)
}

// WithStartToCloseTimeout makes a copy of the current context and update
// the StartToCloseTimeout field in its activity options. An empty activity
// options will be created if it does not exist in the original context.
//
// Temporal time resolution is in seconds and the library uses math.Ceil(d.Seconds())
// to calculate the final value. This is subject to change in the future.
func WithStartToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithStartToCloseTimeout(ctx, d)
}

// WithHeartbeatTimeout makes a copy of the current context and update
// the HeartbeatTimeout field in its activity options. An empty activity
// options will be created if it does not exist in the original context.
//
// Temporal time resolution is in seconds and the library uses math.Ceil(d.Seconds())
// to calculate the final value. This is subject to change in the future.
func WithHeartbeatTimeout(ctx Context, d time.Duration) Context {
	return internal.WithHeartbeatTimeout(ctx, d)
}

// WithWaitForCancellation makes a copy of the current context and update
// the WaitForCancellation field in its activity options. An empty activity
// options will be created if it does not exist in the original context.
func WithWaitForCancellation(ctx Context, wait bool) Context {
	return internal.WithWaitForCancellation(ctx, wait)
}

// WithRetryPolicy makes a copy of the current context and update
// the RetryPolicy field in its activity options. An empty activity
// options will be created if it does not exist in the original context.
func WithRetryPolicy(ctx Context, retryPolicy temporal.RetryPolicy) Context {
	return internal.WithRetryPolicy(ctx, retryPolicy)
}

// WithPriority makes a copy of the current context and updates
// the Priority field in its activity options. An empty activity
// options will be created if it does not exist in the original context.
//
// WARNING: Task queue priority is currently experimental.
func WithPriority(ctx Context, priority temporal.Priority) Context {
	return internal.WithPriority(ctx, priority)
}

// GetActivityOptions returns all activity options present on the context.
func GetActivityOptions(ctx Context) ActivityOptions {
	return internal.GetActivityOptions(ctx)
}

// GetLocalActivityOptions returns all local activity options present on the context.
func GetLocalActivityOptions(ctx Context) LocalActivityOptions {
	return internal.GetLocalActivityOptions(ctx)
}
