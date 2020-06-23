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

package test_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"go.temporal.io/temporal"
	"go.temporal.io/temporal/client"
	"go.temporal.io/temporal/worker"
	"go.temporal.io/temporal/workflow"
)

func TestBasicSession(t *testing.T) {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}
	tl := "test-basic-session-task-list"
	// The client and worker are heavyweight objects that should be created once per process.
	c, err := client.NewClient(client.Options{
		HostPort: client.DefaultHostPort,
		Logger:   logger,
	})
	if err != nil {
		logger.Fatal("Unable to create client", zap.Error(err))
	}
	defer c.Close()

	workerOptions := worker.Options{
		EnableSessionWorker: true, // Important for a worker to participate in the session
	}
	w := worker.New(c, tl, workerOptions)

	w.RegisterWorkflow(BasicSession)
	w.RegisterActivity(ToUpperFunc)

	w.Start()

	workflowOptions := client.StartWorkflowOptions{
		ID:       "test-basic-session",
		TaskList: tl,
	}

	ctx := context.Background()
	run, err := c.ExecuteWorkflow(ctx, workflowOptions, BasicSession)

	var retVal string
	err = run.Get(ctx, &retVal)

	println("QQQQQQQQQQQQ:", retVal)
}

func BasicSession(ctx workflow.Context) (string, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		HeartbeatTimeout:    time.Second * 2000, // such a short timeout to make sample fail over very fast
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	so := &workflow.SessionOptions{
		CreationTimeout:  time.Minute,
		ExecutionTimeout: time.Minute,
	}
	ctx, err := workflow.CreateSession(ctx, so)
	if err != nil {
		return "", err
	}
	defer workflow.CompleteSession(ctx)

	workflow.GetLogger(ctx).Info("calling ExecuteActivity")
	var ans1 string
	if err := workflow.ExecuteActivity(ctx, ToUpperFunc, "hello").Get(ctx, &ans1); err != nil {
		return "", err
	}

	return ans1, nil
}

func ToUpperFunc(_ context.Context, arg string) (string, error) {
	return strings.ToUpper(arg), nil
}
