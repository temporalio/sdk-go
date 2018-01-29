// Copyright (c) 2017 Uber Technologies, Inc.
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

	"go.uber.org/cadence/internal"
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
