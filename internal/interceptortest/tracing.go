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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

var testWorkflowStartTime = time.Date(1969, 7, 20, 20, 17, 0, 0, time.UTC)

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

// RunTestWorkflow executes a test workflow with a tracing interceptor.
func RunTestWorkflow(t *testing.T, tracer interceptor.Tracer) {
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

	env.SetStartTime(testWorkflowStartTime)

	// Exec
	env.ExecuteWorkflow(testWorkflow)

	// Confirm result
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result []string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, []string{"work", "act", "act-local", "work-child", "act", "act-local"}, result)

	// Query workflow
	val, err := env.QueryWorkflow("my-query", nil)
	require.NoError(t, err)
	var queryResp string
	require.NoError(t, val.Get(&queryResp))
	require.Equal(t, "query-response", queryResp)
}

func AssertSpanPropagation(t *testing.T, tracer TestTracer) {
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
						Span("RunActivity:testActivityLocal")))),
			Span("SignalChildWorkflow:my-signal",
				Span("HandleSignal:my-signal"))),
		Span("HandleQuery:my-query"),
	}, tracer.FinishedSpans())
}

func testWorkflow(ctx workflow.Context) ([]string, error) {
	// Run code
	ret, err := workflowInternal(ctx, false)
	if err != nil {
		return nil, err
	}

	// Run child
	if err == nil {
		var temp []string
		fut := workflow.ExecuteChildWorkflow(ctx, testWorkflowChild)
		// Signal and get result
		if err := fut.SignalChildWorkflow(ctx, "my-signal", nil).Get(ctx, nil); err != nil {
			return nil, fmt.Errorf("failed signaling child: %w", err)
		}
		if err := fut.Get(ctx, &temp); err != nil {
			return nil, fmt.Errorf("failed running child: %w", err)
		}
		ret = append(ret, temp...)
	}

	return append([]string{"work"}, ret...), nil
}

func testWorkflowChild(ctx workflow.Context) (ret []string, err error) {
	ret, err = workflowInternal(ctx, true)
	return append([]string{"work-child"}, ret...), err
}

func workflowInternal(ctx workflow.Context, waitSignal bool) (ret []string, err error) {
	// Add signal and query handling
	if waitSignal {
		workflow.GetSignalChannel(ctx, "my-signal").Receive(ctx, nil)
	}
	err = workflow.SetQueryHandler(ctx, "my-query", func() (string, error) { return "query-response", nil })
	if err != nil {
		return
	}

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
