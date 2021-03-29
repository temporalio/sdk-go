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

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
)

// WithChildOptions adds all workflow options to the context.
func WithChildOptions(ctx Context, cwo ChildWorkflowOptions) Context {
	return internal.WithChildWorkflowOptions(ctx, cwo)
}

// WithWorkflowNamespace adds a namespace to the context.
func WithWorkflowNamespace(ctx Context, name string) Context {
	return internal.WithWorkflowNamespace(ctx, name)
}

// WithWorkflowTaskQueue adds a task queue to the context.
func WithWorkflowTaskQueue(ctx Context, name string) Context {
	return internal.WithWorkflowTaskQueue(ctx, name)
}

// WithWorkflowID adds a workflowID to the context.
func WithWorkflowID(ctx Context, workflowID string) Context {
	return internal.WithWorkflowID(ctx, workflowID)
}

// WithWorkflowRunTimeout adds a run timeout to the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithWorkflowRunTimeout(ctx Context, d time.Duration) Context {
	return internal.WithWorkflowRunTimeout(ctx, d)
}

// WithWorkflowTaskTimeout adds a workflow task timeout to the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithWorkflowTaskTimeout(ctx Context, d time.Duration) Context {
	return internal.WithWorkflowTaskTimeout(ctx, d)
}

// WithDataConverter adds DataConverter to the context.
func WithDataConverter(ctx Context, dc converter.DataConverter) Context {
	return internal.WithDataConverter(ctx, dc)
}

// GetChildWorkflowOptions returns all workflow options present on the context.
func GetChildWorkflowOptions(ctx Context) ChildWorkflowOptions {
	return internal.GetChildWorkflowOptions(ctx)
}
