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

package replaytests

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

// Workflow1 test workflow
func Workflow1(ctx workflow.Context, name string) error {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("helloworld workflow started")
	var helloworldResult string
	v := workflow.GetVersion(ctx, "test-change", workflow.DefaultVersion, 1)
	if v == workflow.DefaultVersion {
		err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", "Error", err)
			return err
		}
	} else {
		err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", "Error", err)
			return err
		}

		err = workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", "Error", err)
			return err
		}
	}

	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
	if err != nil {
		logger.Error("Activity failed.", "Error", err)
		return err
	}

	logger.Info("Workflow completed.", "Result", helloworldResult)

	return nil
}

// Workflow2 test workflow
func Workflow2(ctx workflow.Context, name string) error {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("helloworld workflow started")
	var helloworldResult string

	workflow.GetVersion(ctx, "test-change", workflow.DefaultVersion, 1)

	_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{"CustomKeywordField": "testkey"})

	workflow.GetVersion(ctx, "test-change-2", workflow.DefaultVersion, 1)

	workflow.GetVersion(ctx, "test-change-3", workflow.DefaultVersion, 1)

	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
	if err != nil {
		logger.Error("Activity failed.", "Error", err)
		return err
	}

	logger.Info("Workflow completed.", "Result", helloworldResult)

	return nil
}

func helloworldActivity(ctx context.Context, name string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("helloworld activity started")
	return "Hello " + name + "!", nil
}

// TimerWf starts a timer and always starts another timer at workflow end, even if cancelled
func TimerWf(ctx workflow.Context) error {
	defer func() {
		// Produce another timer after being cancelled
		newCtx, _ := workflow.NewDisconnectedContext(ctx)
		_ = workflow.NewTimer(newCtx, 10*time.Minute).Get(ctx, nil)
	}()
	return workflow.NewTimer(ctx, time.Minute*10).Get(ctx, nil)
}
