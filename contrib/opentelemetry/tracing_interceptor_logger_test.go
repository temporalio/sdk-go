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

//go:build go1.21

package opentelemetry_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestLogFields(t *testing.T) {
	var rec tracetest.SpanRecorder

	tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
		Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(&rec)).Tracer(""),
	})
	require.NoError(t, err)

	var suite testsuite.WorkflowTestSuite

	buf := bytes.Buffer{}
	slogger := slog.New(slog.NewTextHandler(&buf, nil))
	suite.SetLogger(slogger)

	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testActivity)
	env.RegisterWorkflow(testWorkflow)

	// Set tracer interceptor
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{interceptor.NewTracingInterceptor(tracer)},
	})

	var testWorkflowStartTime = time.Date(1969, 7, 20, 20, 17, 0, 0, time.UTC)
	env.SetStartTime(testWorkflowStartTime)

	// Exec
	env.ExecuteWorkflow(testWorkflow)

	// Ensure it doesn't introduce panic or else
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Validate that the fields are present and properly set.
	span := rec.Ended()[0]
	assert.Contains(t, buf.String(), "TraceID="+span.Parent().TraceID().String())
	assert.Contains(t, buf.String(), "SpanID="+span.Parent().SpanID().String())
	assert.Contains(t, buf.String(), "TraceFlags="+span.Parent().TraceFlags().String())
}

func testWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("inside a worflow")

	var temp []string

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	return workflow.ExecuteActivity(ctx, testActivity).Get(ctx, &temp)
}

func testActivity(ctx context.Context) ([]string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("inside an activity")

	return []string{"act"}, nil
}
