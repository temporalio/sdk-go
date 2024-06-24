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

	"github.com/google/uuid"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
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

func DeadlockedWorkflow(ctx workflow.Context, _ string) error {
	// Sleep for just over 1 second to trigger deadlock detection
	time.Sleep(1100 * time.Millisecond)
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

func VersionAndMutableSideEffectWorkflow(ctx workflow.Context, name string) (string, error) {
	uid := ""
	logger := workflow.GetLogger(ctx)

	v := workflow.GetVersion(ctx, "mutable-side-effect-bug", workflow.DefaultVersion, 1)
	if v == 1 {
		var err error
		uid, err = generateUUID(ctx)
		if err != nil {
			logger.Error("failed to generated uuid", "Error", err)
			return "", err
		}

		logger.Info("generated uuid", "uuid-val", uid)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result string
	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &result)
	if err != nil {
		logger.Error("Activity failed.", "Error", err)
		return "", err
	}
	return uid, nil
}

func generateUUID(ctx workflow.Context) (string, error) {
	var generatedUUID string

	err := workflow.MutableSideEffect(ctx, "generate-random-uuid", func(ctx workflow.Context) interface{} {
		return uuid.NewString()
	}, func(a, b interface{}) bool {
		return a.(string) == b.(string)
	}).Get(&generatedUUID)
	if err != nil {
		return "", err
	}

	return generatedUUID, nil
}

func CancelOrderSelectWorkflow(ctx workflow.Context) error {
	timerf := workflow.NewTimer(ctx, 5*time.Minute)

	var err error
	disCtx, _ := workflow.NewDisconnectedContext(ctx)
	selector := workflow.NewSelector(ctx)

	selector.AddFuture(timerf, func(f workflow.Future) {
		err = timerf.Get(ctx, nil)
		// do something different on cancel error
		if !temporal.IsCanceledError(err) {
			_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{"CustomKeywordField": "testkey"})
		} else {
			var result string
			ao := workflow.ActivityOptions{
				ScheduleToStartTimeout: time.Minute,
				StartToCloseTimeout:    time.Minute,
				HeartbeatTimeout:       time.Second * 20,
			}
			disCtx = workflow.WithActivityOptions(disCtx, ao)
			err = workflow.ExecuteActivity(disCtx, helloworldActivity, "world").Get(ctx, &result)
		}

	})
	selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, nil)
		err = workflow.Sleep(disCtx, 1*time.Second)

	})
	selector.Select(ctx)
	return err
}

func ChildWorkflowCancelWithUpdate(ctx workflow.Context) error {
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context) error {
			activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: 5 * time.Second,
			})

			return workflow.ExecuteActivity(activityCtx, helloworldActivity, "world", time.Second).Get(ctx, nil)
		}); err != nil {
		return err
	}
	childCtx, cancel := workflow.WithCancel(ctx)

	childCtx = workflow.WithChildOptions(childCtx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})
	childFut := workflow.ExecuteChildWorkflow(childCtx, ChildWorkflowWaitOnSignal)
	cancel()
	_ = childFut.GetChildWorkflowExecution().Get(ctx, nil)

	workflow.GetSignalChannel(ctx, "shutdown").Receive(ctx, nil)
	return nil
}

func MultipleUpdateWorkflow(ctx workflow.Context) (int, error) {
	inflightUpdates := 0
	updatesRan := 0
	sleepHandle := func(ctx workflow.Context) error {
		inflightUpdates++
		updatesRan++
		defer func() {
			inflightUpdates--
		}()
		return workflow.Sleep(ctx, time.Second)
	}
	echoHandle := func(ctx workflow.Context) error {
		inflightUpdates++
		updatesRan++
		defer func() {
			inflightUpdates--
		}()

		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			ScheduleToStartTimeout: 5 * time.Second,
			ScheduleToCloseTimeout: 5 * time.Second,
			StartToCloseTimeout:    9 * time.Second,
		})
		return workflow.ExecuteActivity(ctx, "Echo", 1, 1).Get(ctx, nil)
	}
	emptyHandle := func(ctx workflow.Context) error {
		inflightUpdates++
		updatesRan++
		defer func() {
			inflightUpdates--
		}()
		return ctx.Err()
	}
	// Register multiple update handles in the first workflow task to make sure we process an
	// update only when its handle is registered, not when any handle is registered
	workflow.SetUpdateHandler(ctx, "echo", echoHandle)
	workflow.SetUpdateHandler(ctx, "sleep", sleepHandle)
	workflow.SetUpdateHandler(ctx, "empty", emptyHandle)
	err := workflow.Await(ctx, func() bool { return inflightUpdates == 0 })
	if err != nil {
		return 0, err
	}
	return updatesRan, nil
}

func CounterWorkflow(ctx workflow.Context) (int, error) {
	counter := 0

	if err := workflow.SetUpdateHandlerWithOptions(
		ctx,
		"fetch_and_add",
		func(ctx workflow.Context, i int) (int, error) {
			tmp := counter
			counter += i
			_ = workflow.Sleep(ctx, 1*time.Second)
			return tmp, nil
		},
		workflow.UpdateHandlerOptions{Validator: nonNegative},
	); err != nil {
		return 0, err
	}

	_ = workflow.GetSignalChannel(ctx, "done").Receive(ctx, nil)
	return counter, ctx.Err()
}

func nonNegative(ctx workflow.Context, i int) error {
	if i < 0 {
		return fmt.Errorf("addend must be non-negative (%v)", i)
	}
	return nil
}

func ListAndDescribeWorkflow(ctx workflow.Context) (int, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result workflowservice.ListWorkflowExecutionsResponse
	err := workflow.ExecuteActivity(ctx, "ListWorkflow").Get(ctx, &result)
	if err != nil {
		return 0, err
	}
	for _, execution := range result.Executions {
		if execution.Status == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			err := workflow.Sleep(ctx, 1*time.Second)
			if err != nil {
				return 0, err
			}
		} else {
			var wf workflowservice.DescribeWorkflowExecutionResponse
			err := workflow.ExecuteActivity(ctx, "DescribeWorkflowExecution", execution.GetExecution().WorkflowId).Get(ctx, &wf)
			if err != nil {
				return 0, err
			}
			if wf.ExecutionConfig.WorkflowExecutionTimeout != nil {
				err = workflow.Sleep(ctx, time.Second)
				if err != nil {
					return 0, err
				}
			}
		}
	}
	return len(result.Executions), nil
}
