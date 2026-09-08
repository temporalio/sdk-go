package workflow

import (
	"go.temporal.io/sdk/internal"
)

// Context is a clone of context.Context with Done() returning Channel instead
// of native channel.
// A Context carries a deadline, a cancellation signal, and other values across
// API boundaries.
//
// Context's methods may be called by multiple goroutines simultaneously.
type Context = internal.Context

// LocalVar is a handle to a value local to a single workflow run. The handle can
// be passed by value between workflow code and workflow interceptors while
// retaining access to the same run-local value.
//
// Create a LocalVar with NewLocalVar from within a workflow or workflow
// interceptor. Get returns the zero value of T until Set is called. LocalVar
// values are in-memory workflow state: they are rebuilt by replay and are not
// propagated to activities, child workflows, or continue-as-new runs. The zero
// LocalVar is invalid.
//
// LocalVar is a handle. Copies of a LocalVar share the same identity and access
// the same per-run value. Calls to Set must follow the same determinism
// requirements as other workflow state mutations and are not allowed in
// read-only workflow code such as queries, update validators, or side-effect
// callbacks.
//
// Example:
//
//	func MyWorkflow(ctx workflow.Context) error {
//		currentUser := workflow.NewLocalVar[string](ctx)
//		currentUser.Set(ctx, "alice")
//		return useCurrentUser(currentUser.Get(ctx))
//	}
type LocalVar[T any] = internal.LocalVar[T]

// NewLocalVar creates a LocalVar bound to the workflow run for ctx. Copies of
// the returned handle access the same value in that run.
func NewLocalVar[T any](ctx Context) LocalVar[T] {
	return internal.NewLocalVar[T](ctx)
}

// ContextAware is an optional interface that can be implemented alongside
// DataConverter. This interface allows Temporal to pass Workflow/Activity
// contexts to the DataConverter so that it may tailor its behavior.
//
// Note that data converters may be called in non-context-aware situations to
// convert payloads that may not be customized per context. Data converter
// implementers should not expect or require contextual data be present.
type ContextAware = internal.ContextAware

// ErrCanceled is the error returned by Context.Err when the context is canceled.
var ErrCanceled = internal.ErrCanceled

// ErrDeadlineExceeded is the error returned by Context.Err when the context's
// deadline passes.
var ErrDeadlineExceeded = internal.ErrDeadlineExceeded

// CancelFunc tells an operation to abandon its work.
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
func WithValue(parent Context, key any, val any) Context {
	return internal.WithValue(parent, key, val)
}

// NewDisconnectedContext returns a new context that won't propagate parent's cancellation to the new child context.
// One common use case is to do cleanup work after workflow is canceled.
//
//	err := workflow.ExecuteActivity(ctx, ActivityFoo).Get(ctx, &activityFooResult)
//	if err != nil && temporal.IsCanceledError(ctx.Err()) {
//	  // activity failed, and workflow context is canceled
//	  disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx);
//	  workflow.ExecuteActivity(disconnectedCtx, handleCancellationActivity).Get(disconnectedCtx, nil)
//	  return err // workflow return CanceledError
//	}
func NewDisconnectedContext(parent Context) (ctx Context, cancel CancelFunc) {
	return internal.NewDisconnectedContext(parent)
}
