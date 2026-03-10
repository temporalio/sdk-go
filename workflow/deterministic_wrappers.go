package workflow

import (
	"time"

	"go.temporal.io/sdk/internal"
)

type (

	// Channel must be used instead of a native go channel by workflow code.
	// Use [workflow.NewChannel] to create a Channel instance.
	// Channel extends both [ReceiveChannel] and [SendChannel]. Prefer using one of these interfaces
	// to share a Channel with consumers or producers.
	Channel = internal.Channel

	// ReceiveChannel is a read-only view of the Channel
	ReceiveChannel = internal.ReceiveChannel

	// SendChannel is a write-only view of the Channel
	SendChannel = internal.SendChannel

	// Selector must be used instead of native go select by workflow code.
	// Use [workflow.NewSelector] method to create a Selector instance.
	Selector = internal.Selector

	// Future represents the result of an asynchronous computation.
	Future = internal.Future

	// Settable is used to set value or error on a future.
	// See more: [workflow.NewFuture].
	Settable = internal.Settable

	// WaitGroup is used to wait for a collection of
	// coroutines to finish
	WaitGroup = internal.WaitGroup

	// Mutex is a mutual exclusion lock.
	// Mutex must be used instead of native go mutex by workflow code.
	// Use [workflow.NewMutex] method to create a Mutex instance.
	Mutex = internal.Mutex

	// Semaphore is a counting semaphore.
	// Use [workflow.NewSemaphore] method to create a Semaphore instance.
	Semaphore = internal.Semaphore

	// TimerOptions are options for [NewTimerWithOptions]
	//
	// NOTE: Experimental
	TimerOptions = internal.TimerOptions

	// AwaitOptions are options for [AwaitWithOptions]
	//
	// NOTE: Experimental
	AwaitOptions = internal.AwaitOptions
)

// Await blocks the calling thread until condition() returns true.
// Do not mutate values or trigger side effects inside condition.
// Returns CanceledError if the ctx is canceled.
// The following code will block until the captured count
// variable is set to 5:
//
//	workflow.Await(ctx, func() bool {
//	    return count == 5
//	})
//
// The trigger is evaluated on every workflow state transition.
// Note that conditions that wait for time can be error-prone as nothing might cause evaluation.
// For example:
//
//	workflow.Await(ctx, func() bool {
//	    return workflow.Now() > someTime
//	})
//
// might never return true unless some other event like a Signal or activity completion forces the condition evaluation.
// For a time-based wait use workflow.AwaitWithTimeout function.
func Await(ctx Context, condition func() bool) error {
	return internal.Await(ctx, condition)
}

// AwaitWithTimeout blocks the calling thread until condition() returns true
// or blocking time exceeds the passed timeout value.
// Returns ok=false if timed out, and err CanceledError if the ctx is canceled.
// The following code will block until the captured count
// variable is set to 5, or one hour passes.
//
//	workflow.AwaitWithTimeout(ctx, time.Hour, func() bool {
//	  return count == 5
//	})
func AwaitWithTimeout(ctx Context, timeout time.Duration, condition func() bool) (ok bool, err error) {
	return internal.AwaitWithTimeout(ctx, timeout, condition)
}

// AwaitWithOptions blocks the calling thread until condition() returns true
// or blocking time exceeds the passed timeout value.
// Returns ok=false if timed out, and err CanceledError if the ctx is canceled.
// The following code will block until the captured count
// variable is set to 5, or one hour passes.
//
//	workflow.AwaitWithOptions(ctx, AwaitOptions{Timeout: time.Hour, TimerOptions: TimerOptions{Summary:"Example"}}, func() bool {
//	  return count == 5
//	})
//
// NOTE: Experimental
func AwaitWithOptions(ctx Context, options AwaitOptions, condition func() bool) (ok bool, err error) {
	return internal.AwaitWithOptions(ctx, options, condition)
}

// NewChannel creates a new Channel instance
func NewChannel(ctx Context) Channel {
	return internal.NewChannel(ctx)
}

// NewNamedChannel creates a new Channel instance with a given human-readable name.
// The name appears in stack traces that are blocked on this channel.
func NewNamedChannel(ctx Context, name string) Channel {
	return internal.NewNamedChannel(ctx, name)
}

// NewBufferedChannel creates a new buffered Channel instance
func NewBufferedChannel(ctx Context, size int) Channel {
	return internal.NewBufferedChannel(ctx, size)
}

// NewNamedBufferedChannel creates a new BufferedChannel instance with a given human-readable name.
// The name appears in stack traces that are blocked on this Channel.
func NewNamedBufferedChannel(ctx Context, name string, size int) Channel {
	return internal.NewNamedBufferedChannel(ctx, name, size)
}

// NewSelector creates a new Selector instance.
func NewSelector(ctx Context) Selector {
	return internal.NewSelector(ctx)
}

// NewNamedSelector creates a new Selector instance with a given human-readable name.
// The name appears in stack traces that are blocked on this Selector.
func NewNamedSelector(ctx Context, name string) Selector {
	return internal.NewNamedSelector(ctx, name)
}

// NewWaitGroup creates a new WaitGroup instance.
func NewWaitGroup(ctx Context) WaitGroup {
	return internal.NewWaitGroup(ctx)
}

// NewMutex creates a new Mutex instance. A mutex can be used
// when you want to ensure only one coroutine in a workflow is executing a
// critical section of code at a time.
//
// Note: In a workflow, only one coroutine is ever executing at a time. So
// a mutex is not needed to simply protect shared data.
func NewMutex(ctx Context) Mutex {
	return internal.NewMutex(ctx)
}

// NewSemaphore creates a new Semaphore instance.
func NewSemaphore(ctx Context, n int64) Semaphore {
	return internal.NewSemaphore(ctx, n)
}

// Go creates a new coroutine. It has similar semantics to a goroutine, but in the context of the workflow.
func Go(ctx Context, f func(ctx Context)) {
	internal.Go(ctx, f)
}

// GoNamed creates a new coroutine with a given human-readable name.
// It has similar semantics to a goroutine, but in the context of the workflow.
// The name appears in stack traces that include this coroutine.
func GoNamed(ctx Context, name string, f func(ctx Context)) {
	internal.GoNamed(ctx, name, f)
}

// NewFuture creates a new future as well as an associated Settable that is used to set its value.
func NewFuture(ctx Context) (Future, Settable) {
	return internal.NewFuture(ctx)
}

// Now returns the time when the workflow task was first started, even during replay.
// Workflows must use this Now() to get the wall clock time, instead of Go's time.Now().
func Now(ctx Context) time.Time {
	return internal.Now(ctx)
}

// NewTimer returns immediately and the future becomes ready after the specified duration d. Workflows must use
// this NewTimer() to get the timer, instead of Go's timer.NewTimer(). You can cancel the pending
// timer by canceling the Context (using the context from workflow.WithCancel(ctx)) and that will cancel the timer. After the timer
// is canceled, the returned Future becomes ready, and Future.Get() will return *CanceledError.
//
// To be able to set options like timer summary, use [NewTimerWithOptions].
func NewTimer(ctx Context, d time.Duration) Future {
	return internal.NewTimer(ctx, d)
}

// NewTimerWithOptions returns immediately and the future becomes ready after the specified duration d. Workflows must
// use this NewTimerWithOptions() to get the timer, instead of Go's timer.NewTimer(). You can cancel the pending timer
// by canceling the Context (using the context from workflow.WithCancel(ctx)) and that will cancel the timer. After the
// timer is canceled, the returned Future becomes ready, and Future.Get() will return *CanceledError.
//
// NOTE: Experimental
func NewTimerWithOptions(ctx Context, d time.Duration, options TimerOptions) Future {
	return internal.NewTimerWithOptions(ctx, d, options)
}

// Sleep pauses the current workflow for at least the duration d. A negative or zero duration causes Sleep to return
// immediately. Workflow code must use this Sleep() to sleep, instead of Go's timer.Sleep().
// You can cancel the pending sleep by canceling the Context (using the context from workflow.WithCancel(ctx)).
// Sleep() returns nil if the duration d is passed, or *CanceledError if the ctx is canceled. There are two
// reasons the ctx might be canceled: 1) your workflow code canceled the ctx (with workflow.WithCancel(ctx));
// 2) your workflow itself was canceled by external request.
//
// To be able to set options like timer summary, use [NewTimerWithOptions] and wait on the future.
func Sleep(ctx Context, d time.Duration) (err error) {
	return internal.Sleep(ctx, d)
}
