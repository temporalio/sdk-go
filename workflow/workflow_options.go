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

	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/internal"
)

// WithChildOptions adds all workflow options to the context.
func WithChildOptions(ctx Context, cwo ChildWorkflowOptions) Context {
	return internal.WithChildWorkflowOptions(ctx, cwo)
}

// WithWorkflowDomain adds a domain to the context.
func WithWorkflowDomain(ctx Context, name string) Context {
	return internal.WithWorkflowDomain(ctx, name)
}

// WithWorkflowTaskList adds a task list to the context.
func WithWorkflowTaskList(ctx Context, name string) Context {
	return internal.WithWorkflowTaskList(ctx, name)
}

// WithWorkflowID adds a workflowID to the context.
func WithWorkflowID(ctx Context, workflowID string) Context {
	return internal.WithWorkflowID(ctx, workflowID)
}

// WithChildPolicy adds a ChildWorkflowPolicy to the context.
func WithChildPolicy(ctx Context, childPolicy ChildWorkflowPolicy) Context {
	return internal.WithChildPolicy(ctx, childPolicy)
}

// WithExecutionStartToCloseTimeout adds a workflow execution timeout to the context.
func WithExecutionStartToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithExecutionStartToCloseTimeout(ctx, d)
}

// WithWorkflowTaskStartToCloseTimeout adds a decision timeout to the context.
func WithWorkflowTaskStartToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithWorkflowTaskStartToCloseTimeout(ctx, d)
}

// WithDataConverter adds DataConverter to the context.
func WithDataConverter(ctx Context, dc encoded.DataConverter) Context {
	return internal.WithDataConverter(ctx, dc)
}
