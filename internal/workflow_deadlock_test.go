// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

package internal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func TestDeadlockDetector(t *testing.T) {
	// Create a 500ms ticker and confirm it pauses/resumes properly. We have
	// chosen to use real time instead of an abstract clock here for simplicity.
	d := newDeadlockDetector()
	ticker := d.begin(500 * time.Millisecond)
	defer ticker.end()
	d.pause()
	// Confirm never reached while paused
	select {
	case <-time.After(600 * time.Millisecond):
	case <-ticker.reached():
		t.Fatal("unexpectedly reached deadlock")
	}
	// Resume and confirm reached
	d.resume()
	select {
	case <-time.After(600 * time.Millisecond):
		t.Fatal("unexpectedly didn't deadlock")
	case <-ticker.reached():
	}
}

func TestDataConverterWithoutDeadlockDetection(t *testing.T) {
	conv := converter.GetDefaultDataConverter()

	runWorkflow := func() {
		var suite WorkflowTestSuite
		activityFn := func(ctx context.Context, arg string) error {
			return nil
		}
		workflowFn := func(ctx Context) error {
			ctx = WithDataConverter(ctx, conv)
			ctx = WithActivityOptions(ctx, ActivityOptions{ScheduleToCloseTimeout: 10 * time.Second})
			return ExecuteActivity(ctx, activityFn, "some arg").Get(ctx, nil)
		}
		env := suite.NewTestWorkflowEnvironment()
		env.SetWorkerOptions(WorkerOptions{DeadlockDetectionTimeout: 400 * time.Millisecond})
		env.RegisterWorkflow(workflowFn)
		env.RegisterActivity(activityFn)
		env.ExecuteWorkflow(workflowFn)
		require.True(t, env.IsWorkflowCompleted())
	}

	// Run with a slow converter and confirm it panics
	conv = &slowToPayloadsConverter{conv}
	require.Panics(t, runWorkflow)

	// Run with that same payload converter without deadlock detection
	conv = DataConverterWithoutDeadlockDetection(conv)
	require.NotPanics(t, runWorkflow)
}

type slowToPayloadsConverter struct{ converter.DataConverter }

func (s *slowToPayloadsConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	time.Sleep(600 * time.Millisecond)
	return s.DataConverter.ToPayloads(value...)
}
