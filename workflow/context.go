package workflow

import (
	"go.temporal.io/temporal/internal"
)

// Context is a clone of context.Context with Done() returning Channel instead
// of native channel.
// A Context carries a deadline, a cancellation signal, and other values across
// API boundaries.
//
// Context's methods may be called by multiple goroutines simultaneously.
type Context = internal.Context

// ErrCanceled is the error returned by Context.Err when the context is canceled.
var ErrCanceled = internal.ErrCanceled

// ErrDeadlineExceeded is the error returned by Context.Err when the context's
// deadline passes.
var ErrDeadlineExceeded = internal.ErrDeadlineExceeded

// A CancelFunc tells an operation to abandon its work.
// A CancelFunc does not wait for the work to stop.
// After the first call, subsequent calls to a CancelFunc do nothing.
type CancelFunc = internal.CancelFunc

// WithCancel returns a copy of parent with a new Done channel. The returned
// context's Done channel is closed when the returned cancel function is called
// or when the parent context's Done channel is closed, whichever happens first.
//
// Canceling this context releases resources associated with it, so code should
// call cancel as soon as the operations running in this Context complete.
func WithCancel(parent Context) (ctx Context, cancel CancelFunc) {
	return internal.WithCancel(parent)
}

// WithValue returns a copy of parent in which the value associated with key is
// val.
//
// Use context Values only for request-scoped data that transits processes and
// APIs, not for passing optional parameters to functions.
func WithValue(parent Context, key interface{}, val interface{}) Context {
	return internal.WithValue(parent, key, val)
}

// NewDisconnectedContext returns a new context that won't propagate parent's cancellation to the new child context.
// One common use case is to do cleanup work after workflow is cancelled.
//  err := workflow.ExecuteActivity(ctx, ActivityFoo).Get(ctx, &activityFooResult)
//  if err != nil && temporal.IsCanceledError(ctx.Err()) {
//    // activity failed, and workflow context is canceled
//    disconnectedCtx, _ := workflow.newDisconnectedContext(ctx);
//    workflow.ExecuteActivity(disconnectedCtx, handleCancellationActivity).Get(disconnectedCtx, nil)
//    return err // workflow return CanceledError
//  }
func NewDisconnectedContext(parent Context) (ctx Context, cancel CancelFunc) {
	return internal.NewDisconnectedContext(parent)
}
