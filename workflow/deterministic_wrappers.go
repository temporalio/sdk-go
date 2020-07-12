// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package workflow

import (
	"time"

	"go.temporal.io/sdk/internal"
)

type (

	// Channel must be used instead of native go channel by workflow code.
	// Use workflow.NewChannel(ctx) method to create Channel instance.
	// Channel extends both ReadChanel and SendChannel. Prefer to use one of these interfaces
	// to share Channel with consumers or producers.
	Channel = internal.Channel

	// ReceiveChannel is a read only view of the Channel
	ReceiveChannel = internal.ReceiveChannel

	// SendChannel is a write only view of the Channel
	SendChannel = internal.SendChannel

	// Selector must be used instead of native go select by workflow code.
	// Use workflow.NewSelector(ctx) method to create a Selector instance.
	Selector = internal.Selector

	// Future represents the result of an asynchronous computation.
	Future = internal.Future

	// Settable is used to set value or error on a future.
	// See more: workflow.NewFuture(ctx).
	Settable = internal.Settable

	// WaitGroup is used to wait for a collection of
	// coroutines to finish
	WaitGroup = internal.WaitGroup
)

// Await blocks the calling thread until condition() returns true.
// Do not mutate values or trigger side effects inside condition.
// Returns CanceledError if the ctx is canceled.
// The following code is going to block until the captured count
// variable is set to 5:
//   workflow.Await(ctx, func() bool {
//       return count == 5
//   })
//
// The trigger is evaluated on every workflow state transition.
// Note that conditions that wait for time can be error-prone as nothing might cause evaluation.
// For example:
//   workflow.Await(ctx, func() bool {
//       return workflow.Now() > someTime
//   })
// might never return true unless some other event like Signal or activity completion would force the condition evaluation.
// For a time-based wait use workflow.AwaitWithTimeout function.
func Await(ctx Context, condition func() bool) error {
	return internal.Await(ctx, condition)
}

// AwaitWithTimeout blocks the calling thread until condition() returns true
// or blocking time exceeds the passed timeout value
// Returns ok equals to false if timed out and err equals to
// CanceledError if the ctx is canceled.
// The following code is going to block until the captured count
// variable is set to 5 or one hour passes.
//
// workflow.AwaitWithTimeout(ctx, time.Hour, func() bool {
//   return count == 5
// })
func AwaitWithTimeout(ctx Context, timeout time.Duration, condition func() bool) (ok bool, err error) {
	return internal.AwaitWithTimeout(ctx, timeout, condition)
}

// NewChannel create new Channel instance
func NewChannel(ctx Context) Channel {
	return internal.NewChannel(ctx)
}

// NewNamedChannel create new Channel instance with a given human readable name.
// Name appears in stack traces that are blocked on this channel.
func NewNamedChannel(ctx Context, name string) Channel {
	return internal.NewNamedChannel(ctx, name)
}

// NewBufferedChannel create new buffered Channel instance
func NewBufferedChannel(ctx Context, size int) Channel {
	return internal.NewBufferedChannel(ctx, size)
}

// NewNamedBufferedChannel create new BufferedChannel instance with a given human readable name.
// Name appears in stack traces that are blocked on this Channel.
func NewNamedBufferedChannel(ctx Context, name string, size int) Channel {
	return internal.NewNamedBufferedChannel(ctx, name, size)
}

// NewSelector creates a new Selector instance.
func NewSelector(ctx Context) Selector {
	return internal.NewSelector(ctx)
}

// NewNamedSelector creates a new Selector instance with a given human readable name.
// Name appears in stack traces that are blocked on this Selector.
func NewNamedSelector(ctx Context, name string) Selector {
	return internal.NewNamedSelector(ctx, name)
}

// NewWaitGroup creates a new WaitGroup instance.
func NewWaitGroup(ctx Context) WaitGroup {
	return internal.NewWaitGroup(ctx)
}

// Go creates a new coroutine. It has similar semantic to goroutine in a context of the workflow.
func Go(ctx Context, f func(ctx Context)) {
	internal.Go(ctx, f)
}

// GoNamed creates a new coroutine with a given human readable name.
// It has similar semantic to goroutine in a context of the workflow.
// Name appears in stack traces that include this coroutine.
func GoNamed(ctx Context, name string, f func(ctx Context)) {
	internal.GoNamed(ctx, name, f)
}

// NewFuture creates a new future as well as associated Settable that is used to set its value.
func NewFuture(ctx Context) (Future, Settable) {
	return internal.NewFuture(ctx)
}

// Now returns the current time when the workflow task is started or replayed.
// The workflow needs to use this Now() to get the wall clock time instead of the Go lang library one.
func Now(ctx Context) time.Time {
	return internal.Now(ctx)
}

// NewTimer returns immediately and the future becomes ready after the specified duration d. The workflow needs to use
// this NewTimer() to get the timer instead of the Go lang library one(timer.NewTimer()). You can cancel the pending
// timer by cancel the Context (using context from workflow.WithCancel(ctx)) and that will cancel the timer. After timer
// is canceled, the returned Future become ready, and Future.Get() will return *CanceledError.
// The current timer resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func NewTimer(ctx Context, d time.Duration) Future {
	return internal.NewTimer(ctx, d)
}

// Sleep pauses the current workflow for at least the duration d. A negative or zero duration causes Sleep to return
// immediately. Workflow code needs to use this Sleep() to sleep instead of the Go lang library one(timer.Sleep()).
// You can cancel the pending sleep by cancel the Context (using context from workflow.WithCancel(ctx)).
// Sleep() returns nil if the duration d is passed, or it returns *CanceledError if the ctx is canceled. There are 2
// reasons the ctx could be canceled: 1) your workflow code cancel the ctx (with workflow.WithCancel(ctx));
// 2) your workflow itself is canceled by external request.
// The current timer resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func Sleep(ctx Context, d time.Duration) (err error) {
	return internal.Sleep(ctx, d)
}
