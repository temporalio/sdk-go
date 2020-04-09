package workflow

import (
	"time"

	"go.temporal.io/temporal/internal"
)

// ActivityOptions stores all activity-specific invocation parameters that will be stored inside of a context.
type ActivityOptions = internal.ActivityOptions

// LocalActivityOptions doc
type LocalActivityOptions = internal.LocalActivityOptions

// WithActivityOptions adds all options to the copy of the context.
func WithActivityOptions(ctx Context, options ActivityOptions) Context {
	return internal.WithActivityOptions(ctx, options)
}

// WithLocalActivityOptions adds options for local activity to context
func WithLocalActivityOptions(ctx Context, options LocalActivityOptions) Context {
	return internal.WithLocalActivityOptions(ctx, options)
}

// WithTaskList adds a task list to the copy of the context.
func WithTaskList(ctx Context, name string) Context {
	return internal.WithTaskList(ctx, name)
}

// WithScheduleToCloseTimeout adds a timeout to the copy of the context.
func WithScheduleToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithScheduleToCloseTimeout(ctx, d)
}

// WithScheduleToStartTimeout adds a timeout to the copy of the context.
func WithScheduleToStartTimeout(ctx Context, d time.Duration) Context {
	return internal.WithScheduleToStartTimeout(ctx, d)
}

// WithStartToCloseTimeout adds a timeout to the copy of the context.
func WithStartToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithStartToCloseTimeout(ctx, d)
}

// WithHeartbeatTimeout adds a timeout to the copy of the context.
func WithHeartbeatTimeout(ctx Context, d time.Duration) Context {
	return internal.WithHeartbeatTimeout(ctx, d)
}

// WithWaitForCancellation adds wait for the cacellation to the copy of the context.
func WithWaitForCancellation(ctx Context, wait bool) Context {
	return internal.WithWaitForCancellation(ctx, wait)
}
