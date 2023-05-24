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
	"fmt"
	"math/rand"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
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

func LocalActivityWorkflow(ctx workflow.Context, name string) error {
	ao := workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: time.Minute,
	}

	ctx = workflow.WithLocalActivityOptions(ctx, ao)
	var helloworldResult string
	return workflow.ExecuteLocalActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
}

func ContinueAsNewWorkflow(ctx workflow.Context, continueAsNew bool) error {
	if continueAsNew {
		return workflow.NewContinueAsNewError(ctx, ContinueAsNewWorkflow, false)
	}
	return nil
}

func UpsertMemoWorkflow(ctx workflow.Context, memo string) error {
	err := workflow.UpsertMemo(ctx, map[string]interface{}{
		"Test key": memo,
	})
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	err = workflow.ExecuteActivity(ctx, helloworldActivity, memo).Get(ctx, &memo)
	if err != nil {
		return err
	}

	return workflow.UpsertMemo(ctx, map[string]interface{}{
		"Test key": memo,
	})
}

func UpsertSearchAttributesWorkflow(ctx workflow.Context, field string) error {
	err := workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"CustomStringField": field,
	})
	if err != nil {
		return err
	}

	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	err = workflow.ExecuteActivity(ctx, helloworldActivity, field).Get(ctx, &field)
	if err != nil {
		return err
	}

	return workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"CustomStringField": field,
	})
}

func SideEffectWorkflow(ctx workflow.Context, field string) error {
	encodedRandom := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return rand.Intn(100)
	})

	var random int
	err := encodedRandom.Get(&random)
	if err != nil {
		return err
	}

	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	err = workflow.ExecuteActivity(ctx, helloworldActivity, field).Get(ctx, &field)
	if err != nil {
		return err
	}

	return encodedRandom.Get(&random)
}

func EmptyWorkflow(ctx workflow.Context, _ string) error {
	return nil
}

func MutableSideEffectWorkflow(ctx workflow.Context) ([]int, error) {
	f := func(retVal int) (newVal int) {
		err := workflow.MutableSideEffect(
			ctx,
			"side-effect-1",
			func(ctx workflow.Context) interface{} { return retVal },
			func(a, b interface{}) bool { return a.(int) == b.(int) },
		).Get(&newVal)
		if err != nil {
			panic(err)
		}
		return
	}
	results := []int{f(0)}
	results = append(results, f(0))
	results = append(results, f(0))
	results = append(results, f(1))
	results = append(results, f(1))
	results = append(results, f(2))
	err := workflow.Sleep(ctx, time.Second)
	if err != nil {
		return nil, err
	}
	results = append(results, f(3))
	results = append(results, f(3))
	results = append(results, f(4))
	err = workflow.Sleep(ctx, time.Second)
	if err != nil {
		return nil, err
	}
	results = append(results, f(4))
	results = append(results, f(5))

	return results, nil
}

func VersionLoopWorkflow(ctx workflow.Context, changeID string, iterations int) error {
	for i := 0; i < iterations; i++ {
		workflow.GetVersion(ctx, fmt.Sprintf("%s:%d", changeID, i), workflow.DefaultVersion, 1)
	}
	return workflow.Sleep(ctx, time.Second)
}

func VersionLoopWorkflowMultipleTasks(ctx workflow.Context, changeID string, iterations int) error {
	for i := 0; i < iterations; i++ {
		workflow.GetVersion(ctx, fmt.Sprintf("%s:%d", changeID, i), workflow.DefaultVersion, 1)
		err := workflow.Sleep(ctx, time.Millisecond)
		if err != nil {
			return err
		}
	}
	return nil
}

func ChildWorkflowWaitOnSignal(ctx workflow.Context) error {
	workflow.GetSignalChannel(ctx, "unblock").Receive(ctx, nil)
	return nil
}

func DuplicateChildWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: "ABC-SIMPLE-CHILD-WORKFLOW-ID",
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)

	child1 := workflow.ExecuteChildWorkflow(childCtx, ChildWorkflowWaitOnSignal)
	var childWE workflow.Execution
	err := child1.GetChildWorkflowExecution().Get(ctx, &childWE)
	if err != nil {
		return err
	}

	duplicateChildWFFuture := workflow.ExecuteChildWorkflow(childCtx, ChildWorkflowWaitOnSignal)
	selector := workflow.NewSelector(ctx)
	selector.AddFuture(duplicateChildWFFuture, func(f workflow.Future) {
		logger.Info("child workflow is ready")
		err = f.Get(ctx, nil)
		if _, ok := err.(*temporal.ChildWorkflowExecutionAlreadyStartedError); !ok {
			panic("Second child must fail to start as duplicate")
		}
		err = workflow.Sleep(ctx, time.Second)
	}).AddFuture(duplicateChildWFFuture.GetChildWorkflowExecution(), func(f workflow.Future) {
		logger.Info("child workflow execution is ready")
		err = f.Get(ctx, nil)
		if _, ok := err.(*temporal.ChildWorkflowExecutionAlreadyStartedError); !ok {
			panic("Second child must fail to start as duplicate")
		}
		ao := workflow.ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		field := "hello"
		err = workflow.ExecuteActivity(ctx, helloworldActivity, field).Get(ctx, &field)
	}).AddDefault(func() {
		err = workflow.Sleep(ctx, time.Second)
	})
	for i := 0; i < 2; i++ {
		selector.Select(ctx)
		if err != nil {
			return err
		}
	}

	workflow.SignalExternalWorkflow(ctx, childWE.ID, childWE.RunID, "unblock", nil)
	if err != nil {
		return err
	}
	err = child1.Get(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func UpdateWorkflow(ctx workflow.Context) error {
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context, d time.Duration) error {
			return workflow.Sleep(ctx, d)
		}); err != nil {
		return err
	}
	workflow.GetSignalChannel(ctx, "shutdown").Receive(ctx, nil)
	return nil
}

func UpdateAndExit(ctx workflow.Context) error {
	ch := workflow.NewChannel(ctx)
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context, d time.Duration) error {
			// passing a non-zero duration here controls whether the update is
			// accepted+completed in the same WFT or accepted in one WFT and
			// completed in a subsquent task.
			if d != time.Duration(0) {
				_ = workflow.Sleep(ctx, d)
			}
			ch.Close()
			return nil
		}); err != nil {
		return err
	}

	// by waiting on a channel that is closed by a call to update we ensure that
	// the update completion and workflow completion commands occur on the same
	// WFT completion.
	ch.Receive(ctx, nil)
	return ctx.Err()
}

func NonDeterministicUpdate(ctx workflow.Context) error {
	ch := workflow.NewChannel(ctx)
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context) error {
			// The workflow.Sleep below was not commented out when the json
			// history was generated. By commenting it out we make the update
			// code non-deterministic.
			//
			//_ = workflow.Sleep(ctx, 1*time.Second)

			ch.Close()
			return nil
		}); err != nil {
		return err
	}
	ch.Receive(ctx, nil)
	return ctx.Err()
}
