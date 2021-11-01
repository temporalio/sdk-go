// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
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

package interceptortest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// TestTracer is an interceptor.Tracer that returns finished spans.
type TestTracer interface {
	interceptor.Tracer
	FinishedSpans() []*SpanInfo
}

// SpanInfo is information about a span.
type SpanInfo struct {
	Name     string
	Children []*SpanInfo
}

// Span creates a SpanInfo.
func Span(name string, children ...*SpanInfo) *SpanInfo {
	return &SpanInfo{Name: name, Children: children}
}

// AssertSpanPropagation runs a test tracer and asserts that it properly
// propagates spans.
func AssertSpanPropagation(t *testing.T, tracer TestTracer) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testActivity)
	env.RegisterActivity(testActivityLocal)
	env.RegisterWorkflow(testWorkflow)
	env.RegisterWorkflow(testWorkflowChild)

	// Set tracer interceptor
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{interceptor.NewTracingInterceptor(tracer)},
	})

	// Exec
	env.ExecuteWorkflow(testWorkflow)

	// Confirm result
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result []string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, []string{"work", "act", "act-local", "work-child", "act", "act-local"}, result)

	// Check span tree
	require.Equal(t, []*SpanInfo{
		Span("RunWorkflow:testWorkflow",
			Span("StartActivity:testActivity",
				Span("RunActivity:testActivity")),
			Span("StartActivity:testActivityLocal",
				Span("RunActivity:testActivityLocal")),
			Span("StartChildWorkflow:testWorkflowChild",
				Span("RunWorkflow:testWorkflowChild",
					Span("StartActivity:testActivity",
						Span("RunActivity:testActivity")),
					Span("StartActivity:testActivityLocal",
						Span("RunActivity:testActivityLocal"))))),
	}, tracer.FinishedSpans())
}

func testWorkflow(ctx workflow.Context) ([]string, error) {
	// Run code
	ret, err := workflowInternal(ctx)

	// Run child
	if err == nil {
		var temp []string
		err = workflow.ExecuteChildWorkflow(ctx, testWorkflowChild).Get(ctx, &temp)
		ret = append(ret, temp...)
	}

	return append([]string{"work"}, ret...), err
}

func testWorkflowChild(ctx workflow.Context) (ret []string, err error) {
	ret, err = workflowInternal(ctx)
	return append([]string{"work-child"}, ret...), err
}

func workflowInternal(ctx workflow.Context) (ret []string, err error) {
	// Exec normal activity
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	var temp []string
	err = workflow.ExecuteActivity(ctx, testActivity).Get(ctx, &temp)
	ret = append(ret, temp...)

	// Exec local activity
	if err == nil {
		ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{StartToCloseTimeout: 10 * time.Second})
		temp = nil
		err = workflow.ExecuteLocalActivity(ctx, testActivityLocal).Get(ctx, &temp)
		ret = append(ret, temp...)
	}

	return
}

func testActivity(ctx context.Context) ([]string, error) {
	return []string{"act"}, nil
}

func testActivityLocal(ctx context.Context) ([]string, error) {
	return []string{"act-local"}, nil
}
