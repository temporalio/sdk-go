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
	"errors"
	"fmt"
	"log"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	consistentQuerySignalCh = "consistent-query-signal-chan"
)

type Workflows struct{}

func (w *Workflows) Basic(ctx workflow.Context) ([]string, error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	var ans1 string
	workflow.GetLogger(ctx).Info("calling ExecuteActivity")
	err := workflow.ExecuteActivity(ctx, "Prefix_ToUpperWithDelay", "hello", time.Second).Get(ctx, &ans1)
	if err != nil {
		return nil, err
	}
	var ans2 string
	if err := workflow.ExecuteActivity(ctx, "Prefix_ToUpper", ans1).Get(ctx, &ans2); err != nil {
		return nil, err
	}
	if ans2 != "HELLO" {
		return nil, fmt.Errorf("incorrect return value from activity: expected=%v,got=%v", "HELLO", ans2)
	}
	return []string{"toUpperWithDelay", "toUpper"}, nil
}

func (w *Workflows) Deadlocked(ctx workflow.Context) ([]string, error) {
	// Simulates deadlock. Never call time.Sleep in production code!
	time.Sleep(2 * time.Second)
	return []string{}, nil
}

var isDeadlockedWithLocalActivityFirstAttempt bool = true

func (w *Workflows) DeadlockedWithLocalActivity(ctx workflow.Context) ([]string, error) {
	laCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	})

	_ = workflow.ExecuteLocalActivity(laCtx, LocalSleep, time.Second*2).Get(laCtx, nil)

	if isDeadlockedWithLocalActivityFirstAttempt {
		// Simulates deadlock. Never call time.Sleep in production code!
		time.Sleep(2 * time.Second)
		isDeadlockedWithLocalActivityFirstAttempt = false
	}

	return []string{}, nil
}

func (w *Workflows) Panicked(ctx workflow.Context) ([]string, error) {
	panic("simulated")
}

func (w *Workflows) PanickedActivity(ctx workflow.Context, maxAttempts int32) (ret []string, err error) {
	// Only retry limited number of times on activities
	oneRetry := &temporal.RetryPolicy{InitialInterval: 1 * time.Nanosecond, MaximumAttempts: maxAttempts}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         oneRetry,
	})
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         oneRetry,
	})

	var a Activities

	// Panic activity
	err = workflow.ExecuteActivity(ctx, a.Panicked).Get(ctx, nil)
	var actPanicErr *temporal.PanicError
	if !errors.As(err, &actPanicErr) {
		return nil, fmt.Errorf("no activity panic error, got: %v", err)
	}

	// Panic local activity
	err = workflow.ExecuteLocalActivity(ctx, a.Panicked).Get(ctx, nil)
	var localActPanicErr *temporal.PanicError
	if !errors.As(err, &localActPanicErr) {
		return nil, fmt.Errorf("no local activity error, got: %v", err)
	}

	return []string{
		"act err: " + actPanicErr.Error(),
		"local act err: " + localActPanicErr.Error(),
	}, nil
}

func (w *Workflows) ActivityRetryOnError(ctx workflow.Context) ([]string, error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptionsWithRetry())
	startTime := workflow.Now(ctx)
	err := workflow.ExecuteActivity(ctx, "Fail").Get(ctx, nil)
	if err == nil {
		return nil, fmt.Errorf("expected activity to fail but succeeded")
	}

	elapsed := workflow.Now(ctx).Sub(startTime)
	if elapsed < 2*time.Second {
		return nil, fmt.Errorf("expected activity to be retried on failure, but it was not")
	}

	var applicationErr *temporal.ApplicationError
	ok := errors.As(err, &applicationErr)
	if !ok {
		return nil, fmt.Errorf("activity failed with unexpected error: %v", err)
	}
	if applicationErr.Error() != errFailOnPurpose.Error() {
		return nil, fmt.Errorf("activity failed with unexpected error reason: %v", applicationErr.Error())
	}

	return []string{"fail", "fail", "fail"}, nil
}

func (w *Workflows) CallUnregisteredActivityRetry(ctx workflow.Context) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptionsWithRetry())
	startTime := workflow.Now(ctx)
	err := workflow.ExecuteActivity(ctx, "Unknown").Get(ctx, nil)
	if err == nil {
		return "", fmt.Errorf("expected activity to fail but succeeded")
	}

	elapsed := workflow.Now(ctx).Sub(startTime)
	if elapsed < 2*time.Second {
		return "", fmt.Errorf("expected activity to be retried on failure, but it was not")
	}

	var applicationErr *temporal.ApplicationError
	ok := errors.As(err, &applicationErr)
	if !ok {
		return "", fmt.Errorf("activity failed with unexpected error: %v", err)
	}
	if !strings.HasPrefix(applicationErr.Error(), "unable to find activityType=Unknown") {
		return "", fmt.Errorf("unexpected error type")
	}
	return "done", nil
}

func (w *Workflows) ActivityRetryOptionsChange(ctx workflow.Context) ([]string, error) {
	opts := w.defaultActivityOptionsWithRetry()
	opts.RetryPolicy.MaximumAttempts = 2
	if workflow.IsReplaying(ctx) {
		opts.RetryPolicy.MaximumAttempts = 3
	}
	ctx = workflow.WithActivityOptions(ctx, opts)
	err := workflow.ExecuteActivity(ctx, "Fail").Get(ctx, nil)
	if err == nil {
		return nil, fmt.Errorf("expected activity to fail but succeeded")
	}
	return []string{"fail", "fail"}, nil
}

func (w *Workflows) ActivityRetryOnTimeout(ctx workflow.Context, timeoutType enumspb.TimeoutType) ([]string, error) {
	opts := w.defaultActivityOptionsWithRetry()
	switch timeoutType {
	case enumspb.TIMEOUT_TYPE_SCHEDULE_TO_CLOSE:
		opts.ScheduleToCloseTimeout = time.Second
	case enumspb.TIMEOUT_TYPE_START_TO_CLOSE:
		opts.StartToCloseTimeout = time.Second
	}

	ctx = workflow.WithActivityOptions(ctx, opts)

	startTime := workflow.Now(ctx)
	err := workflow.ExecuteActivity(ctx, "Sleep", 2*time.Second).Get(ctx, nil)
	if err == nil {
		return nil, fmt.Errorf("expected activity to fail but succeeded")
	}

	elapsed := workflow.Now(ctx).Sub(startTime)
	if elapsed < 5*time.Second {
		return nil, fmt.Errorf("expected activity to be retried on failure, but it was not: %v", elapsed)
	}

	var timeoutErr *temporal.TimeoutError
	ok := errors.As(err, &timeoutErr)
	if !ok {
		return nil, fmt.Errorf("activity failed with unexpected error: %v", err)
	}

	if timeoutErr.TimeoutType() != timeoutType {
		return nil, fmt.Errorf("activity failed due to unexpected timeout %v", timeoutErr.TimeoutType())
	}

	return []string{"sleep", "sleep", "sleep"}, nil
}

func (w *Workflows) LongRunningActivityWithHB(ctx workflow.Context) ([]string, error) {
	opts := w.defaultActivityOptionsWithRetry()
	opts.HeartbeatTimeout = 2 * time.Second
	opts.ScheduleToCloseTimeout = time.Second * 12
	opts.StartToCloseTimeout = time.Second * 12
	opts.RetryPolicy = &internal.RetryPolicy{
		MaximumAttempts: 1,
	}
	ctx = workflow.WithActivityOptions(ctx, opts)

	err := workflow.ExecuteActivity(ctx, "LongRunningHeartbeat", 8*time.Second, 200*time.Millisecond).Get(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("expected activity to succeed but it failed: %v", err)
	}

	return []string{"longRunningHeartbeat"}, nil
}

func (w *Workflows) ActivityRetryOnHBTimeout(ctx workflow.Context) ([]string, error) {
	opts := workflow.ActivityOptions{
		ScheduleToStartTimeout: 5 * time.Second,
		ScheduleToCloseTimeout: 15 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
		HeartbeatTimeout:       3 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 1.0,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, opts)

	var result int
	// Activity "HeartbeatAndSleep" will heartbeat once and then sleep 5s. The first heartbeat will be reported
	// immediately without delay/buffer. With the settings we have, below is timeline:
	// 0s  activity starts (attempt 1).
	// 0s  activity reports heartbeat.
	// 3s  activity timeout due to missing heartbeat.
	// 4s  activity retry attempt 2.
	// 4s  activity reports heartbeat.
	// 7s  activity timeout due to missing heartbeat.
	// 8s  activity retry attempt 3.
	// 8s  activity reports heartbeat.
	// 11s activity timeout due to missing heartbeat.
	// 11s activity close with heartbeat timeout error.
	err := workflow.ExecuteActivity(ctx, "HeartbeatAndSleep", 0, 5*time.Second).Get(ctx, &result)
	if err == nil {
		return nil, fmt.Errorf("expected activity to fail but succeeded")
	}

	var timeoutErr *temporal.TimeoutError
	ok := errors.As(err, &timeoutErr)
	if !ok {
		return nil, fmt.Errorf("activity failed with unexpected error: %v", err)
	}

	if timeoutErr.TimeoutType() != enumspb.TIMEOUT_TYPE_HEARTBEAT {
		return nil, fmt.Errorf("activity failed due to unexpected timeout %v", timeoutErr.TimeoutType())
	}

	if !timeoutErr.HasLastHeartbeatDetails() {
		return nil, fmt.Errorf("timeout missing last heartbeat details")
	}

	if err := timeoutErr.LastHeartbeatDetails(&result); err != nil {
		return nil, err
	}

	if result != 3 {
		return nil, fmt.Errorf("invalid heartbeat details: %v", result)
	}

	return []string{"heartbeatAndSleep", "heartbeatAndSleep", "heartbeatAndSleep"}, nil
}

func (w *Workflows) UpdateInfoWorkflow(ctx workflow.Context) error {
	err := workflow.SetUpdateHandlerWithOptions(ctx, "update", func(ctx workflow.Context) (string, error) {
		return workflow.GetUpdateInfo(ctx).ID, nil
	}, workflow.UpdateHandlerOptions{
		Validator: func(ctx workflow.Context) error {
			if workflow.GetUpdateInfo(ctx).ID != "testID" {
				return errors.New("invalid update ID")
			}
			return nil
		},
	})
	if err != nil {
		return errors.New("failed to register update handler")
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return nil
}

func (w *Workflows) ActivityHeartbeatWithRetry(ctx workflow.Context) (heartbeatCounts int, err error) {
	// Make retries fast
	opts := w.defaultActivityOptions()
	opts.RetryPolicy = &temporal.RetryPolicy{InitialInterval: 5 * time.Millisecond, BackoffCoefficient: 1}
	ctx = workflow.WithActivityOptions(ctx, opts)

	// Fail twice then succeed
	err = workflow.ExecuteActivity(ctx, "HeartbeatTwiceAndFailNTimes", 2,
		"activity-heartbeat-"+workflow.GetInfo(ctx).WorkflowExecution.ID).Get(ctx, &heartbeatCounts)
	return
}

func (w *Workflows) ContinueAsNew(ctx workflow.Context, count int, taskQueue string) (int, error) {
	tq := workflow.GetInfo(ctx).TaskQueueName
	if tq != taskQueue {
		return -1, fmt.Errorf("invalid taskQueueName name, expected=%v, got=%v", taskQueue, tq)
	}
	if count == 0 {
		return 999, nil
	}
	ctx = workflow.WithTaskQueue(ctx, taskQueue)
	return -1, workflow.NewContinueAsNewError(ctx, w.ContinueAsNew, count-1, taskQueue)
}

func (w *Workflows) ContinueAsNewWithOptions(ctx workflow.Context, count int, taskQueue string) (string, error) {
	info := workflow.GetInfo(ctx)
	tq := info.TaskQueueName
	if tq != taskQueue {
		return "", fmt.Errorf("invalid taskQueueName name, expected=%v, got=%v", taskQueue, tq)
	}

	if info.Memo == nil || info.SearchAttributes == nil || info.RetryPolicy == nil {
		return "", errors.New("memo, search attributes, and/or retry policy are not carried over")
	}
	var memoVal string
	err := converter.GetDefaultDataConverter().FromPayload(info.Memo.Fields["memoKey"], &memoVal)
	if err != nil {
		return "", errors.New("error when get memo value")
	}

	var searchAttrVal string
	err = converter.GetDefaultDataConverter().FromPayload(info.SearchAttributes.IndexedFields["CustomKeywordField"], &searchAttrVal)
	if err != nil {
		return "", errors.New("error when get search attribute value")
	}

	if count == 0 {
		return fmt.Sprintf("%v,%v,%v", memoVal, searchAttrVal, info.RetryPolicy.MaximumAttempts), nil
	}
	ctx = workflow.WithTaskQueue(ctx, taskQueue)

	return "", workflow.NewContinueAsNewError(ctx, w.ContinueAsNewWithOptions, count-1, taskQueue)
}

func (w *Workflows) IDReusePolicy(
	ctx workflow.Context,
	childWFID string,
	policy enumspb.WorkflowIdReusePolicy,
	parallel bool,
	failFirstChild bool,
) (string, error) {
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:               childWFID,
		WorkflowExecutionTimeout: 9 * time.Second,
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowIDReusePolicy:    policy,
	})

	var ans1 string
	child1 := workflow.ExecuteChildWorkflow(ctx, w.child, "hello", failFirstChild)
	if !parallel {
		err := child1.Get(ctx, &ans1)
		if failFirstChild && err == nil {
			return "", fmt.Errorf("child1 succeeded when it was expected to fail")
		}
		if !failFirstChild && err != nil {
			return "", fmt.Errorf("child1 failed when it was expected to succeed")
		}
	}

	var ans2 string
	if err := workflow.ExecuteChildWorkflow(ctx, w.child, "world", false).Get(ctx, &ans2); err != nil {
		// Expect it is a execution-already-started
		if temporal.IsWorkflowExecutionAlreadyStartedError(err) {
			return "", err
		}
		return "", fmt.Errorf("unexpected child workflow error: %w", err)
	}

	if parallel {
		err := child1.Get(ctx, &ans1)
		if failFirstChild && err == nil {
			return "", fmt.Errorf("child1 succeeded when it was expected to fail")
		}
		if !failFirstChild && err != nil {
			return "", fmt.Errorf("child1 failed when it was expected to succeed")
		}
	}

	return ans1 + ans2, nil
}

func (w *Workflows) ChildWorkflowRetryOnError(ctx workflow.Context) error {
	opts := workflow.ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 9 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithChildOptions(ctx, opts)
	var result string
	return workflow.ExecuteChildWorkflow(ctx, w.child, "hello", true).Get(ctx, &result)
}

func (w *Workflows) ChildWorkflowRetryOnTimeout(ctx workflow.Context) error {
	opts := workflow.ChildWorkflowOptions{
		WorkflowTaskTimeout:      time.Second,
		WorkflowRunTimeout:       time.Second,
		WorkflowExecutionTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithChildOptions(ctx, opts)
	return workflow.ExecuteChildWorkflow(ctx, w.sleep, 2*time.Second).Get(ctx, nil)
}

func (w *Workflows) ChildWorkflowSuccess(ctx workflow.Context) (result string, err error) {
	opts := workflow.ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 10 * time.Second,
		Memo:                     map[string]interface{}{"memoKey": "memoVal"},
		SearchAttributes:         map[string]interface{}{"CustomKeywordField": "searchAttrVal"},
	}
	ctx = workflow.WithChildOptions(ctx, opts)
	err = workflow.ExecuteChildWorkflow(ctx, w.childForMemoAndSearchAttr).Get(ctx, &result)
	return
}

func (w *Workflows) CascadingCancellation(ctx workflow.Context) (err error) {
	opts := workflow.ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 30 * time.Second,
		WorkflowID:               workflow.GetInfo(ctx).WorkflowExecution.ID + "-child",
	}
	ctx = workflow.WithChildOptions(ctx, opts)
	ft := workflow.ExecuteChildWorkflow(ctx, w.timer, 10*time.Second)
	return ft.Get(ctx, nil)
}

func (w *Workflows) ChildWorkflowSuccessWithParentClosePolicyTerminate(ctx workflow.Context) (result string, err error) {
	opts := workflow.ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 30 * time.Second,
	}
	ctx = workflow.WithChildOptions(ctx, opts)
	ft := workflow.ExecuteChildWorkflow(ctx, w.sleep, 20*time.Second)
	var childWE internal.WorkflowExecution
	err = ft.GetChildWorkflowExecution().Get(ctx, &childWE)
	return childWE.ID, err
}

func (w *Workflows) ChildWorkflowSuccessWithParentClosePolicyAbandon(ctx workflow.Context) (result string, err error) {
	opts := workflow.ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 10 * time.Second,
		ParentClosePolicy:        enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}
	ctx = workflow.WithChildOptions(ctx, opts)
	ft := workflow.ExecuteChildWorkflow(ctx, w.sleep, 5*time.Second)
	var childWE internal.WorkflowExecution
	err = ft.GetChildWorkflowExecution().Get(ctx, &childWE)
	return childWE.ID, err
}

func (w *Workflows) childWorkflowWaitOnContextCancel(ctx workflow.Context) error {
	var err error
	// Wait for the workflow to be cancelled
	ctx.Done().Receive(ctx, &err)
	var canceledError *temporal.CanceledError
	if errors.As(ctx.Err(), &canceledError) {
		return ctx.Err()
	} else {
		return errors.New("childWorkflowWaitOnContextCancel was not cancelled")
	}
}

func (w *Workflows) ChildWorkflowAndParentCancel(ctx workflow.Context) error {
	var childWorkflowID string
	err := workflow.SetQueryHandler(ctx, "child-and-parent-cancel-child-workflow-id", func(input []byte) (string, error) {
		return childWorkflowID, nil
	})
	if err != nil {
		return err
	}

	cwo := workflow.ChildWorkflowOptions{
		WorkflowRunTimeout: time.Second * 2,
	}
	ctx = workflow.WithChildOptions(ctx, cwo)

	childWorkflowFuture := workflow.ExecuteChildWorkflow(ctx, w.childWorkflowWaitOnContextCancel)

	var childWorkflowExecution workflow.Execution
	err = childWorkflowFuture.GetChildWorkflowExecution().Get(ctx, &childWorkflowExecution)
	if err != nil {
		return err
	}
	childWorkflowID = childWorkflowExecution.ID

	// Wait for the workflow to be cancelled
	ctx.Done().Receive(ctx, &err)
	return nil
}

func (w *Workflows) childWorkflowWaitOnSignal(ctx workflow.Context) error {
	workflow.GetSignalChannel(ctx, "unblock").Receive(ctx, nil)
	return nil
}

func (w *Workflows) ChildWorkflowCancelUnusualTransitionsRepro(ctx workflow.Context) error {
	var childWorkflowID string
	err := workflow.SetQueryHandler(ctx, "child-workflow-id", func(input []byte) (string, error) {
		return childWorkflowID, nil
	})
	if err != nil {
		return err
	}

	cwo := workflow.ChildWorkflowOptions{WorkflowRunTimeout: time.Second * 2}
	ctx = workflow.WithChildOptions(ctx, cwo)

	childWorkflowFuture := workflow.ExecuteChildWorkflow(ctx, w.childWorkflowWaitOnSignal)

	var childWorkflowExecution workflow.Execution
	err = childWorkflowFuture.GetChildWorkflowExecution().Get(ctx, &childWorkflowExecution)
	if err != nil {
		return err
	}
	childWorkflowID = childWorkflowExecution.ID

	var result string
	err = childWorkflowFuture.Get(ctx, &result)
	if err != nil {
		return err
	}
	return nil
}

func (w *Workflows) ChildWorkflowDuplicatePanicRepro(ctx workflow.Context) error {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: "ABC-SIMPLE-CHILD-WORKFLOW-ID",
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)

	child1 := workflow.ExecuteChildWorkflow(childCtx, w.childWorkflowWaitOnSignal)
	var childWE workflow.Execution
	err := child1.GetChildWorkflowExecution().Get(ctx, &childWE)
	if err != nil {
		return err
	}
	workflow.SignalExternalWorkflow(ctx, childWE.ID, childWE.RunID, "unblock", nil)
	if err != nil {
		return err
	}
	err = workflow.ExecuteChildWorkflow(childCtx, w.childWorkflowWaitOnSignal).Get(ctx, nil)
	if _, ok := err.(*temporal.ChildWorkflowExecutionAlreadyStartedError); !ok {
		panic("Second child must fail to start as duplicate")
	}
	return nil
}

func (w *Workflows) ChildWorkflowDuplicateGetExecutionStuckRepro(ctx workflow.Context) error {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: "ABC-SIMPLE-CHILD-WORKFLOW-ID",
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)

	child1 := workflow.ExecuteChildWorkflow(childCtx, w.childWorkflowWaitOnSignal)
	var childWE workflow.Execution
	err := child1.GetChildWorkflowExecution().Get(ctx, &childWE)
	if err != nil {
		return err
	}
	workflow.SignalExternalWorkflow(ctx, childWE.ID, childWE.RunID, "unblock", nil)
	if err != nil {
		return err
	}
	err = workflow.ExecuteChildWorkflow(childCtx, w.childWorkflowWaitOnSignal).GetChildWorkflowExecution().Get(ctx, nil)
	if _, ok := err.(*temporal.ChildWorkflowExecutionAlreadyStartedError); !ok {
		panic("Second child must fail to start as duplicate")
	}
	return nil
}

func (w *Workflows) ActivityCancelRepro(ctx workflow.Context) ([]string, error) {
	ctx, cancelFunc := workflow.WithCancel(ctx)

	// First go-routine which triggers cancellation on completion of first activity
	workflow.Go(ctx, func(ctx1 workflow.Context) {
		activityCtx := workflow.WithActivityOptions(ctx1, workflow.ActivityOptions{
			ScheduleToStartTimeout: 10 * time.Second,
			ScheduleToCloseTimeout: 10 * time.Second,
			StartToCloseTimeout:    9 * time.Second,
		})

		activityF := workflow.ExecuteActivity(activityCtx, "Prefix_ToUpperWithDelay", "hello", 1*time.Second)
		var ans string
		err := activityF.Get(activityCtx, &ans)
		if err != nil {
			workflow.GetLogger(activityCtx).Info("Activity A Failed.", "Error", err)
			return
		}

		// Trigger cancellation of root context
		cancelFunc()
	})

	// Second go-routine which get blocked on ActivitySchedule and not started
	workflow.Go(ctx, func(ctx1 workflow.Context) {
		activityCtx := workflow.WithActivityOptions(ctx1, workflow.ActivityOptions{
			ScheduleToStartTimeout: 10 * time.Second,
			ScheduleToCloseTimeout: 10 * time.Second,
			StartToCloseTimeout:    1 * time.Second,
			TaskQueue:              "bad_tq",
		})

		activityF := workflow.ExecuteActivity(activityCtx, "Prefix_ToUpper", "hello")
		var ans string
		err := activityF.Get(activityCtx, &ans)
		if err != nil {
			workflow.GetLogger(activityCtx).Info("Activity B Failed.", "Error", err)
		}
	})

	// Third go-routine which get blocked on ActivitySchedule and not started
	workflow.Go(ctx, func(ctx1 workflow.Context) {
		activityCtx := workflow.WithActivityOptions(ctx1, workflow.ActivityOptions{
			ScheduleToStartTimeout: 10 * time.Second,
			ScheduleToCloseTimeout: 10 * time.Second,
			StartToCloseTimeout:    1 * time.Second,
			TaskQueue:              "bad_tq",
		})

		activityF := workflow.ExecuteActivity(activityCtx, "Prefix_ToUpper", "hello")
		var ans string
		err := activityF.Get(activityCtx, &ans)
		if err != nil {
			workflow.GetLogger(activityCtx).Info("Activity C Failed.", "Error", err)
		}
	})

	// Cause the workflow to block on sleep
	_ = workflow.Sleep(ctx, 10*time.Second)

	return []string{"toUpperWithDelay"}, nil
}

func (w *Workflows) CancelActivity(ctx workflow.Context) ([]string, error) {
	activityCtx1, cancelFunc1 := workflow.WithCancel(ctx)
	activityCtx1 = workflow.WithActivityOptions(activityCtx1, workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    3 * time.Second,
	})

	_ = workflow.ExecuteActivity(activityCtx1, "Prefix_ToUpperWithDelay", "hello", 2*time.Second)
	// Sleep to send commands to the server.
	_ = workflow.Sleep(ctx, 1*time.Second)
	cancelFunc1()

	activityCtx2 := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    1 * time.Second,
	})
	_ = workflow.ExecuteActivity(activityCtx2, "Prefix_ToUpper", "hello").Get(activityCtx2, nil)

	return []string{"toUpperWithDelay", "toUpper"}, nil
}

func (w *Workflows) CancelTimer(ctx workflow.Context) ([]string, error) {
	timerCtx1, cancelFunc1 := workflow.WithCancel(ctx)

	_ = workflow.NewTimer(timerCtx1, 3*time.Second)
	// Sleep to send commands to the server.
	_ = workflow.Sleep(ctx, 1*time.Second)
	cancelFunc1()

	activityCtx2 := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	})
	_ = workflow.ExecuteActivity(activityCtx2, "Prefix_ToUpper", "hello").Get(activityCtx2, nil)

	return []string{"toUpper"}, nil
}

func (w *Workflows) CancelTimerAfterActivity(ctx workflow.Context) (string, error) {
	timerCtx1, cancelFunc1 := workflow.WithCancel(ctx)

	_ = workflow.NewTimer(timerCtx1, 3*time.Second)
	// Start an activity
	activityCtx2 := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	})
	var res string
	fut := workflow.ExecuteActivity(activityCtx2, "Prefix_ToUpper", "hello")
	// Cancel timer
	cancelFunc1()

	err := fut.Get(activityCtx2, &res)
	return res, err
}

var cancelTimerDeferCount uint32

func (w *Workflows) CancelTimerViaDeferAfterWFTFailure(ctx workflow.Context) error {
	timerCtx, canceller := workflow.WithCancel(ctx)
	defer func() {
		if atomic.AddUint32(&cancelTimerDeferCount, 1) == 1 {
			panic("Intentional panic to trigger WFT failure")
		}
		canceller()
	}()

	_ = workflow.NewTimer(timerCtx, time.Second).Get(timerCtx, nil)

	return nil
}

func (w *Workflows) CancelChildWorkflow(ctx workflow.Context) ([]string, error) {
	childCtx1, cancelFunc1 := workflow.WithCancel(ctx)
	opts := workflow.ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 10 * time.Second,
	}
	childCtx1 = workflow.WithChildOptions(childCtx1, opts)
	_ = workflow.ExecuteChildWorkflow(childCtx1, w.sleep, 3*time.Second)
	// Sleep to send commands to the server.
	_ = workflow.Sleep(ctx, 1*time.Second)
	cancelFunc1()

	activityCtx2 := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	})
	_ = workflow.ExecuteActivity(activityCtx2, "Prefix_ToUpper", "hello").Get(activityCtx2, nil)

	return []string{"sleep", "toUpper"}, nil
}

func (w *Workflows) StartingChildAfterBeingCanceled(ctx workflow.Context) (bool, error) {
	// schedule a timer, which will be cancelled, but ignore that cancel
	_ = workflow.Sleep(ctx, 5*time.Minute)

	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})
	childErr := workflow.ExecuteChildWorkflow(ctx, w.sleep, time.Second).Get(ctx, nil)
	if childErr != nil {
		return false, childErr
	}

	return true, nil
}

func (w *Workflows) CancelActivityImmediately(ctx workflow.Context) ([]string, error) {
	activityCtx1, cancelFunc1 := workflow.WithCancel(ctx)
	activityCtx1 = workflow.WithActivityOptions(activityCtx1, workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    3 * time.Second,
	})

	_ = workflow.ExecuteActivity(activityCtx1, "Prefix_ToUpperWithDelay", "hello", 2*time.Second)
	cancelFunc1()

	activityCtx2 := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    1 * time.Second,
	})
	_ = workflow.ExecuteActivity(activityCtx2, "Prefix_ToUpper", "hello").Get(activityCtx2, nil)

	return []string{"toUpper"}, nil
}

func (w *Workflows) RaceOnCacheEviction(ctx workflow.Context, testCase string) error {
	// When we evict a workflow from the cache we call runtime.Goexit() from each Go routine the workflow
	// launched. Calling runtime.Goexit() terminates those Go routines, but also calls their respective defers.
	// Since we clean up all the workflows Go routines in a loop, and don't do any synchronization between them,
	// we are running all their defers in parallel. If more than one of those defers does something that generates
	// a command there is a race on multiple data structure in the SDK state machine.
	re := func(ctx workflow.Context) {
		ctx, _ = workflow.NewDisconnectedContext(ctx)
		ctx, cancel := workflow.WithCancel(ctx)
		if testCase == "child_workflow" {
			ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WaitForCancellation: true})
			child := workflow.ExecuteChildWorkflow(ctx, w.SleepForDuration, 10*time.Minute)
			if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
				panic(err)
			}
		} else if testCase == "activity" {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				ScheduleToStartTimeout: 30 * time.Second,
				StartToCloseTimeout:    30 * time.Second,
			})
			_ = workflow.ExecuteActivity(ctx, "Prefix_ToUpperWithDelay", "hello", 10*time.Second)
		} else if testCase == "timer" {
			_ = workflow.NewTimer(ctx, 10*time.Second)
		}

		defer func() {
			time.Sleep(time.Second)
			cancel()
		}()
		// Since the main workflow function returns before this timer finishes the code will never run past it
		_ = workflow.Sleep(ctx, time.Hour)
	}
	for i := 0; i < 10; i++ {
		workflow.Go(ctx, re)
	}
	// Returning will eventually cause all the other go routines to clean up and call goexit in parallel
	return workflow.Sleep(ctx, 5*time.Second)
}

func (w *Workflows) CancelMultipleCommandsOverMultipleTasks(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	// We want this "cleanup" activity to be run when the whole workflow is cancelled
	defer func() {
		// When workflow is canceled, it has to get a new disconnected context to execute any activities
		newCtx, _ := workflow.NewDisconnectedContext(ctx)
		err := workflow.ExecuteActivity(newCtx, "Prefix_ToUpper", "hello").Get(newCtx, nil)
		if err != nil {
			panic("Cleanup activity error")
		}
	}()

	// Start a timer that will be canceled when the workflow is
	_ = workflow.NewTimer(ctx, time.Minute*10)
	// Throw in a side effect for fun
	_ = workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return "hi!"
	})
	// Include a timer we cancel across the wf task
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	_ = workflow.NewTimer(timerCtx, time.Second*3)
	// Actually wait on a real timer to trigger a wf task
	_ = workflow.Sleep(ctx, time.Millisecond*500)
	cancelTimer()
	// Another timers we expect to get cancelled
	_ = workflow.NewTimer(ctx, time.Minute*10)

	// Include a timer we cancel immediately
	timerCtx2, cancelTimer2 := workflow.WithCancel(ctx)
	_ = workflow.NewTimer(timerCtx2, time.Second*3)
	cancelTimer2()

	// We need to be cancelled by test runner here
	_ = workflow.Sleep(ctx, time.Minute*10)

	return nil
}

func (w *Workflows) SimplestWorkflow(_ workflow.Context) (string, error) {
	return "hello", nil
}

func (w *Workflows) TwoParameterWorkflow(_ workflow.Context, _ string, _ string) (string, error) {
	return "TwoParameterWorkflow", nil
}

func (w *Workflows) ThreeParameterWorkflow(_ workflow.Context, _ string, _ string, _ string) (string, error) {
	return "ThreeParameterWorkflow", nil
}

func (w *Workflows) LargeQueryResultWorkflow(ctx workflow.Context) (string, error) {
	err := workflow.SetQueryHandler(ctx, "large_query", func() ([]byte, error) {
		result := make([]byte, 3000000)
		rand.Read(result)
		return result, nil
	})
	if err != nil {
		return "", errors.New("failed to register query handler")
	}

	return "hello", nil
}

func (w *Workflows) MutatingQueryWorkflow(ctx workflow.Context) (string, error) {
	err := workflow.SetQueryHandler(ctx, "mutating_query", func() (string, error) {
		_ = workflow.Sleep(ctx, time.Second)
		return "failed", nil
	})
	if err != nil {
		return "", errors.New("failed to register query handler")
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return "hello", nil
}

func (w *Workflows) MutatingUpdateValidatorWorkflow(ctx workflow.Context) (string, error) {
	err := workflow.SetUpdateHandlerWithOptions(ctx, "mutating_update", func(ctx workflow.Context) (string, error) {
		_ = workflow.Sleep(ctx, time.Second)
		return "failed", nil
	}, workflow.UpdateHandlerOptions{
		Validator: func(ctx workflow.Context) error {
			return workflow.Sleep(ctx, time.Second)
		},
	})
	if err != nil {
		return "", errors.New("failed to register query handler")
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return "hello", nil
}

func (w *Workflows) MutatingSideEffectWorkflow(ctx workflow.Context) (string, error) {
	encodedValue := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		_ = workflow.Sleep(ctx, 45*time.Second)
		return "fail"
	})
	var sideEffectValue string
	err := encodedValue.Get(&sideEffectValue)
	return sideEffectValue, err
}

func (w *Workflows) MutatingMutableSideEffectWorkflow(ctx workflow.Context) (string, error) {
	encodedValue := workflow.MutableSideEffect(ctx, "test-id", func(ctx workflow.Context) interface{} {
		_ = workflow.Sleep(ctx, 45*time.Second)
		return "fail"
	}, func(a, b interface{}) bool {
		return false
	})
	var sideEffectValue string
	err := encodedValue.Get(&sideEffectValue)
	return sideEffectValue, err
}

func (w *Workflows) ConsistentQueryWorkflow(ctx workflow.Context, delay time.Duration) error {
	queryResult := "starting-value"
	err := workflow.SetQueryHandler(ctx, "consistent_query", func() (string, error) {
		return queryResult, nil
	})
	if err != nil {
		return errors.New("failed to register query handler")
	}
	ch := workflow.GetSignalChannel(ctx, consistentQuerySignalCh)
	var signalData string
	ch.Receive(ctx, &signalData)
	laCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	})

	_ = workflow.ExecuteLocalActivity(laCtx, LocalSleep, delay).Get(laCtx, nil)
	queryResult = signalData
	return nil
}

func (w *Workflows) ActivityTimeoutsWorkflow(ctx workflow.Context, activityOptions workflow.ActivityOptions) error {
	activityCtx := workflow.WithActivityOptions(ctx, activityOptions)
	return workflow.ExecuteActivity(activityCtx, "Sleep", time.Second).Get(ctx, nil)
}
func (w *Workflows) SignalWorkflow(ctx workflow.Context) (*commonpb.WorkflowType, error) {
	s := workflow.NewSelector(ctx)

	stringSignalChan := workflow.GetSignalChannel(ctx, "string-signal")
	var stringSignalValue string
	s.AddReceive(stringSignalChan, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &stringSignalValue)
		workflow.GetLogger(ctx).Info("Received signal", "signal", "string-signal", "value", stringSignalValue)
	})
	s.Select(ctx)

	protoSignalChan := workflow.GetSignalChannel(ctx, "proto-signal")
	var protoSignalValue *commonpb.WorkflowType
	s.AddReceive(protoSignalChan, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &protoSignalValue)
		workflow.GetLogger(ctx).Info("Received signal", "signal", "proto-signal", "value", protoSignalValue)
	})
	s.Select(ctx)

	protoSignalValue.Name = stringSignalValue

	return protoSignalValue, nil
}

func (w *Workflows) RetryTimeoutStableErrorWorkflow(ctx workflow.Context) ([]string, error) {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Second,
		StartToCloseTimeout:    1 * time.Second,
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 1.0,
			MaximumInterval:    1 * time.Second,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	// Test calling activity by method pointer
	// As Go allows nil receiver pointers it works fine
	var a *Activities
	err := workflow.ExecuteActivity(ctx, a.RetryTimeoutStableErrorActivity).Get(ctx, nil)

	var timeoutErr *temporal.TimeoutError
	ok := errors.As(err, &timeoutErr)
	if !ok {
		return []string{}, fmt.Errorf("activity failed with unexpected error: %v", err)
	}

	if timeoutErr.TimeoutType() != enumspb.TIMEOUT_TYPE_SCHEDULE_TO_CLOSE {
		return []string{}, fmt.Errorf("activity timed out with unexpected timeout type: %v", timeoutErr.TimeoutType())
	}

	err = errors.Unwrap(timeoutErr)
	var previousTimeoutErr *temporal.TimeoutError
	if !errors.As(err, &previousTimeoutErr) {
		return []string{}, fmt.Errorf("activity timed out with unexpected last error %v", err)
	}

	if previousTimeoutErr.TimeoutType() != enumspb.TIMEOUT_TYPE_START_TO_CLOSE {
		return []string{}, fmt.Errorf("activity timed out with unexpected timeout type of last timeout: %v", previousTimeoutErr.TimeoutType())
	}

	return []string{}, nil
}

func (w *Workflows) child(ctx workflow.Context, arg string, mustFail bool) (string, error) {
	var result string
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	err := workflow.ExecuteActivity(ctx, "Prefix_ToUpper", arg).Get(ctx, &result)
	if mustFail {
		return "", fmt.Errorf("failing-on-purpose")
	}
	return result, err
}

func (w *Workflows) childForMemoAndSearchAttr(ctx workflow.Context) (result string, err error) {
	info := workflow.GetInfo(ctx)
	var memo string
	err = converter.GetDefaultDataConverter().FromPayload(info.Memo.Fields["memoKey"], &memo)
	if err != nil {
		return
	}
	var searchAttrVal string
	err = converter.GetDefaultDataConverter().FromPayload(info.SearchAttributes.IndexedFields["CustomKeywordField"], &searchAttrVal)
	if err != nil {
		return
	}
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	err = workflow.ExecuteActivity(ctx, "GetMemoAndSearchAttr", memo, searchAttrVal).Get(ctx, &result)
	return
}

func (w *Workflows) sleep(ctx workflow.Context, d time.Duration) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToStartTimeout: 5 * time.Second,
		ScheduleToCloseTimeout: 5*time.Second + d,
		StartToCloseTimeout:    time.Second + d,
	})
	return workflow.ExecuteActivity(ctx, "Sleep", d).Get(ctx, nil)
}

func (w *Workflows) timer(ctx workflow.Context, d time.Duration) error {
	return workflow.NewTimer(ctx, d).Get(ctx, nil)
}

func (w *Workflows) InspectActivityInfo(ctx workflow.Context) error {
	info := workflow.GetInfo(ctx)
	namespace := info.Namespace
	wfType := info.WorkflowType.Name
	taskQueue := info.TaskQueueName
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	return workflow.ExecuteActivity(ctx, "inspectActivityInfo", namespace, taskQueue, wfType, false).Get(ctx, nil)
}

func (w *Workflows) InspectLocalActivityInfo(ctx workflow.Context) error {
	info := workflow.GetInfo(ctx)
	namespace := info.Namespace
	wfType := info.WorkflowType.Name
	taskQueue := info.TaskQueueName
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	var activities *Activities
	return workflow.ExecuteLocalActivity(
		ctx, activities.InspectActivityInfo, namespace, taskQueue, wfType, true).Get(ctx, nil)
}

func (w *Workflows) WorkflowWithLocalActivityCtxPropagation(ctx workflow.Context) (string, error) {
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	ctx = workflow.WithValue(ctx, contextKey(testContextKey1), "test-data-in-context")
	var activities *Activities
	var result string
	err := workflow.ExecuteLocalActivity(ctx, activities.DuplicateStringInContext).Get(ctx, &result)
	if err != nil {
		return "", err
	}
	return result, nil
}

func (w *Workflows) WorkflowWithParallelLocalActivities(ctx workflow.Context) (string, error) {
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	var activities *Activities
	var futures []workflow.Future

	for i := 0; i < 10; i++ {
		futures = append(futures, workflow.ExecuteLocalActivity(ctx, activities.Echo, 0, i))
	}

	for i, future := range futures {
		var activityResult int
		if err := future.Get(ctx, &activityResult); err != nil {
			return "", err
		}

		if activityResult != i {
			return "", fmt.Errorf("Expected %v, Got %v", i, activityResult)
		}
	}

	return "", nil
}

func (w *Workflows) WorkflowWithLocalActivityStartWhenTimerCancel(ctx workflow.Context) (bool, error) {
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	var activities *Activities
	// Start a timer
	_ = workflow.NewTimer(timerCtx, time.Second*3)

	// On signal, start local activity and cancel timer simultaneously
	sigChan := workflow.GetSignalChannel(ctx, "signal")
	var signal string
	if channelActive := sigChan.Receive(ctx, &signal); channelActive {
		localActivityFut := workflow.ExecuteActivity(ctx, activities.Echo, 0, 0)
		cancelTimer()
		err := localActivityFut.Get(ctx, nil)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

func (w *Workflows) WorkflowWithParallelLongLocalActivityAndHeartbeat(ctx workflow.Context) error {
	ao := w.defaultLocalActivityOptions()
	ao.ScheduleToCloseTimeout = 10 * time.Second
	ctx = workflow.WithLocalActivityOptions(ctx, ao)
	// Intentionally instantiating to test legacy path of non nil receiver.
	activities := Activities{}
	var futures []workflow.Future

	for i := 0; i < 10; i++ {
		futures = append(futures, workflow.ExecuteLocalActivity(ctx, activities.Echo, 5, i))
	}

	for i, future := range futures {
		var activityResult int
		if err := future.Get(ctx, &activityResult); err != nil {
			return err
		}

		if activityResult != i {
			return fmt.Errorf("Expected %v, Got %v", i, activityResult)
		}
	}

	return nil
}

func (w *Workflows) WorkflowWithLocalActivityStartToCloseTimeout(ctx workflow.Context) error {
	// Validate that local activities respect StartToCloseTimeout and retry correctly
	ao := w.defaultLocalActivityOptions()
	ao.ScheduleToCloseTimeout = 10 * time.Second
	ao.StartToCloseTimeout = 1 * time.Second
	ao.RetryPolicy = &temporal.RetryPolicy{
		MaximumInterval: time.Second,
		MaximumAttempts: 5,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, ao)

	var activities *Activities
	future := workflow.ExecuteLocalActivity(ctx, activities.SleepN, 3*time.Second, 3)
	var count int32
	err := future.Get(ctx, &count)
	if err != nil {
		return err
	}
	if count != 3 {
		return fmt.Errorf("expected 3, got %v", count)
	}
	// Validate the correct timeout error is returned
	ao.StartToCloseTimeout = 1 * time.Second
	ao.RetryPolicy = &temporal.RetryPolicy{
		MaximumInterval: time.Second,
		MaximumAttempts: 1,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, ao)
	future = workflow.ExecuteLocalActivity(ctx, activities.SleepN, 3*time.Second, 3)
	err = future.Get(ctx, nil)
	var timeoutErr *temporal.TimeoutError
	if errors.As(err, &timeoutErr) {
		if timeoutErr.TimeoutType() != enumspb.TIMEOUT_TYPE_START_TO_CLOSE {
			return fmt.Errorf("expected start to close timeout, got %v", timeoutErr.TimeoutType())
		}
		return nil
	}
	return errors.New("expected timeout error")
}

func (w *Workflows) WorkflowWithLocalActivityRetries(ctx workflow.Context) error {
	laOpts := w.defaultLocalActivityOptions()
	laOpts.RetryPolicy = &internal.RetryPolicy{
		InitialInterval:    50 * time.Millisecond,
		BackoffCoefficient: 1.1,
		MaximumInterval:    time.Second * 5,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, laOpts)
	var activities *Activities

	var futures []workflow.Future
	for i := 1; i <= 10; i++ {
		la := workflow.ExecuteLocalActivity(ctx, activities.failNTimes, 2, i)
		futures = append(futures, la)
	}

	for _, fut := range futures {
		err := fut.Get(ctx, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Workflows) WorkflowWithLocalActivityRetriesAndHeartbeat(ctx workflow.Context) error {
	laOpts := w.defaultLocalActivityOptions()
	laOpts.RetryPolicy = &internal.RetryPolicy{
		InitialInterval:    510 * time.Millisecond,
		BackoffCoefficient: 1.0,
		MaximumAttempts:    20,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, laOpts)
	var activities *Activities

	err := workflow.ExecuteLocalActivity(ctx, activities.failNTimes, 7, 1).Get(ctx, nil)
	if err != nil {
		return err
	}
	err = workflow.ExecuteLocalActivity(ctx, activities.failNTimes, 7, 2).Get(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func (w *Workflows) WorkflowWithLocalActivityRetriesAndDefaultRetryPolicy(ctx workflow.Context) error {
	laOpts := w.defaultLocalActivityOptions()
	// Don't set any retry policy
	ctx = workflow.WithLocalActivityOptions(ctx, laOpts)
	var activities *Activities

	var futures []workflow.Future
	for i := 1; i <= 10; i++ {
		la := workflow.ExecuteLocalActivity(ctx, activities.failNTimes, 2, i)
		futures = append(futures, la)
	}

	for _, fut := range futures {
		err := fut.Get(ctx, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Workflows) WorkflowWithLocalActivityRetriesAndPartialRetryPolicy(ctx workflow.Context) error {
	laOpts := w.defaultLocalActivityOptions()
	// Set only max attempts and use defaults for other parameters.
	laOpts.RetryPolicy = &internal.RetryPolicy{
		MaximumAttempts: 3,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, laOpts)
	var activities *Activities

	var futures []workflow.Future
	for i := 1; i <= 10; i++ {
		la := workflow.ExecuteLocalActivity(ctx, activities.failNTimes, 2, i)
		futures = append(futures, la)
	}

	for _, fut := range futures {
		err := fut.Get(ctx, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Workflows) WorkflowWithParallelSideEffects(ctx workflow.Context) (string, error) {
	var futures []workflow.Future

	for i := 0; i < 10; i++ {
		valueToSet := i
		future, setter := workflow.NewFuture(ctx)
		futures = append(futures, future)

		workflow.Go(ctx, func(ctx workflow.Context) {
			encodedValue := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
				return valueToSet
			})
			var sideEffectValue int
			err := encodedValue.Get(&sideEffectValue)
			setter.Set(sideEffectValue, err)
		})
	}

	for i, future := range futures {
		var sideEffectValue int
		if err := future.Get(ctx, &sideEffectValue); err != nil {
			return "", err
		}

		if i != sideEffectValue {
			return "", fmt.Errorf("Expected %v, Got %v", i, sideEffectValue)
		}
	}

	return "", nil
}

func (w *Workflows) WorkflowWithParallelMutableSideEffects(ctx workflow.Context) (string, error) {
	var futures []workflow.Future

	for i := 0; i < 10; i++ {
		valueToSet := i
		future, setter := workflow.NewFuture(ctx)
		futures = append(futures, future)

		workflow.Go(ctx, func(ctx workflow.Context) {
			encodedValue := workflow.MutableSideEffect(
				ctx,
				strconv.Itoa(valueToSet),
				func(ctx workflow.Context) interface{} {
					return valueToSet
				},
				func(a interface{}, b interface{}) bool {
					return a == b
				},
			)

			var sideEffectValue int
			err := encodedValue.Get(&sideEffectValue)
			setter.Set(sideEffectValue, err)
		})
	}

	for i, future := range futures {
		var sideEffectValue int
		if err := future.Get(ctx, &sideEffectValue); err != nil {
			return "", err
		}

		if i != sideEffectValue {
			return "", fmt.Errorf("Expected %v, Got %v", i, sideEffectValue)
		}
	}

	return "", nil
}

func (w *Workflows) BasicSession(ctx workflow.Context) ([]string, error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())

	so := &workflow.SessionOptions{
		CreationTimeout:  time.Minute,
		ExecutionTimeout: time.Minute,
	}
	ctx, err := workflow.CreateSession(ctx, so)
	if err != nil {
		return nil, err
	}
	defer workflow.CompleteSession(ctx)

	var ans1 string
	workflow.GetLogger(ctx).Info("calling ExecuteActivity")
	if err = workflow.ExecuteActivity(ctx, "Prefix_ToUpper", "hello").Get(ctx, &ans1); err != nil {
		return nil, err
	}
	if ans1 != "HELLO" {
		return nil, fmt.Errorf("incorrect return value from activity: expected=%v,got=%v", "HELLO", ans1)
	}
	return []string{"toUpper"}, nil
}

type AdvancedSessionParams struct {
	SessionCount           int
	SessionCreationTimeout time.Duration
	// Just a single index to do recreation. Ignored if 0.
	RecreateAtIndex int
}

func (w Workflows) AdvancedSession(ctx workflow.Context, params *AdvancedSessionParams) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		// No retry on activities
		RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	// Create a query to know sessions pending or started
	var sessionCreatePending bool
	var sessionsCreated int
	err := workflow.SetQueryHandler(ctx, "sessions-created-equals", func(expected int) (bool, error) {
		return sessionsCreated == expected, nil
	})
	if err != nil {
		return err
	}
	err = workflow.SetQueryHandler(ctx, "sessions-created-equals-and-pending", func(expected int) (bool, error) {
		return sessionsCreated == expected && sessionCreatePending, nil
	})
	if err != nil {
		return err
	}

	// Create the sessions
	sel := workflow.NewSelector(ctx)
	var actErr error
	var act Activities
	var lastSessionCtx workflow.Context
	for i := 0; i < params.SessionCount; i++ {
		i := i
		var sessionCtx workflow.Context
		var err error
		opts := &workflow.SessionOptions{
			CreationTimeout:  params.SessionCreationTimeout,
			ExecutionTimeout: 20 * time.Second,
			HeartbeatTimeout: 2 * time.Second,
		}
		// Do a create unless at recreate index
		verb := "creating"
		sessionCreatePending = true
		if params.RecreateAtIndex > 0 && i == params.RecreateAtIndex {
			sessionCtx, err = workflow.RecreateSession(ctx, workflow.GetSessionInfo(lastSessionCtx).GetRecreateToken(), opts)
			verb = "recreating"
		} else {
			sessionCtx, err = workflow.CreateSession(ctx, opts)
		}
		sessionCreatePending = false
		if err != nil {
			// We use the error message instead of wrapping the error itself
			// because unfortunately the error converter unwraps some like
			// cancellation
			return fmt.Errorf("failed %v session #%v: %v", verb, i+1, err.Error())
		}
		sessionsCreated++
		defer workflow.CompleteSession(sessionCtx)
		lastSessionCtx = sessionCtx

		// Run activity in session
		sel.AddFuture(workflow.ExecuteActivity(sessionCtx, act.WaitForManualStop), func(f workflow.Future) {
			if err := f.Get(sessionCtx, nil); err != nil {
				// We use the error message instead of wrapping the error itself
				// because unfortunately the error converter unwraps some like
				// cancellation
				actErr = fmt.Errorf("activity on session #%v failed: %v", i+1, err.Error())
			}
		})
	}

	// Wait for all activities
	for i := 0; i < params.SessionCount; i++ {
		sel.Select(ctx)
		if actErr != nil {
			return actErr
		}
	}
	return nil
}

func (w Workflows) SessionFailedStateWorkflow(ctx workflow.Context, params *AdvancedSessionParams) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		// No retry on activities
		RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	// Create a query to know sessions pending or started
	var sessionsCreated int
	err := workflow.SetQueryHandler(ctx, "sessions-created-equals", func(expected int) (bool, error) {
		return sessionsCreated == expected, nil
	})
	if err != nil {
		return err
	}

	opts := &workflow.SessionOptions{
		CreationTimeout:  params.SessionCreationTimeout,
		ExecutionTimeout: 20 * time.Second,
		// Note the heartbeat timeout is less then half the activity timeout.
		HeartbeatTimeout: 1 * time.Second,
	}
	sessionCtx, err := workflow.CreateSession(ctx, opts)
	if err != nil {
		return err
	}
	sessionsCreated += 1
	var act Activities
	// The test should kill the worker and the session should fail.
	err = workflow.ExecuteActivity(sessionCtx, act.WaitForManualStop).Get(sessionCtx, nil)
	var canceledErr *temporal.CanceledError
	if !errors.As(err, &canceledErr) {
		return errors.New("Expected activity to be canceled")
	}
	if workflow.GetSessionInfo(sessionCtx).SessionState != workflow.SessionStateFailed {
		return errors.New("Session not in correct state")
	}
	return nil
}

func (w *Workflows) ActivityCompletionUsingID(ctx workflow.Context) ([]string, error) {
	activityAOptions := workflow.ActivityOptions{
		ActivityID:             "A",
		ScheduleToStartTimeout: 5 * time.Second,
		ScheduleToCloseTimeout: 5 * time.Second,
		StartToCloseTimeout:    9 * time.Second,
	}
	activityACtx := workflow.WithActivityOptions(ctx, activityAOptions)
	activityAFuture := workflow.ExecuteActivity(activityACtx, "AsyncComplete", "activityA called")

	activityBOptions := workflow.ActivityOptions{
		ActivityID:             "B",
		ScheduleToStartTimeout: 5 * time.Second,
		ScheduleToCloseTimeout: 5 * time.Second,
		StartToCloseTimeout:    9 * time.Second,
	}
	activityBCtx := workflow.WithActivityOptions(ctx, activityBOptions)
	activityBFuture := workflow.ExecuteActivity(activityBCtx, "AsyncComplete", "activityB called")

	var activityAResult string
	if err := activityAFuture.Get(ctx, &activityAResult); err != nil {
		return nil, err
	}

	var activityBResult string
	if err := activityBFuture.Get(ctx, &activityBResult); err != nil {
		return nil, err
	}

	return []string{activityAResult, activityBResult}, nil
}

func (w *Workflows) ContextPropagator(ctx workflow.Context, startChild bool) ([]string, error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	var result []string

	if val1 := ctx.Value(contextKey(testContextKey1)); val1 != nil {
		if val1s, ok := val1.(string); ok {
			result = append(result, val1s)
		} else {
			return nil, fmt.Errorf("%s key is not propagated to workflow", testContextKey1)
		}
	}

	if val2 := ctx.Value(contextKey(testContextKey2)); val2 != nil {
		if val2s, ok := val2.(string); ok {
			result = append(result, val2s)
		} else {
			return nil, fmt.Errorf("%s key is not propagated to workflow", testContextKey2)
		}
	}

	var a Activities
	var activityResult []string
	if err := workflow.ExecuteActivity(ctx, a.PropagateActivity).Get(ctx, &activityResult); err != nil {
		return nil, err
	}
	result = append(result, activityResult...)

	// Now test child workflow also.
	if startChild {
		ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowExecutionTimeout: 9 * time.Second,
			WorkflowTaskTimeout:      5 * time.Second,
		})
		var childResult []string
		if err := workflow.ExecuteChildWorkflow(ctx, w.ContextPropagator, false).Get(ctx, &childResult); err != nil {
			return nil, err
		}
		for _, cr := range childResult {
			result = append(result, "child_"+cr)
		}
	}

	return result, nil
}

const CronFailMsg = "dying on purpose"

func (w *Workflows) CronWorkflow(ctx workflow.Context) (int, error) {
	retme := 0

	if workflow.HasLastCompletionResult(ctx) {
		var lastres int
		if err := workflow.GetLastCompletionResult(ctx, &lastres); err == nil {
			retme = lastres + 1
		}
	}

	lastfail := workflow.GetLastError(ctx)
	if retme == 2 && lastfail != nil {
		if lastfail.Error() != CronFailMsg {
			return -3, errors.New("incorrect message in latest failure")
		}
		return 3, temporal.NewCanceledError("finished OK")
	}
	if retme == 2 {
		return -1, errors.New(CronFailMsg)
	}

	return retme, nil
}

func (w *Workflows) WaitForCancelWithDisconnectedContextWorkflow(ctx workflow.Context) (err error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		HeartbeatTimeout:    5 * time.Second,
		WaitForCancellation: true,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var activities *Activities
	defer func() {
		if !errors.Is(ctx.Err(), workflow.ErrCanceled) {
			return
		}
		// When the Workflow is canceled, it has to get a new disconnected context to execute any Activities
		newCtx, _ := workflow.NewDisconnectedContext(ctx)
		err = workflow.ExecuteActivity(newCtx, activities.EmptyActivity).Get(newCtx, nil)
	}()

	s := workflow.NewSelector(ctx)

	newCtx, _ := workflow.NewDisconnectedContext(ctx)
	newCtx, cancel := workflow.WithCancel(newCtx)

	timer1 := workflow.NewTimer(newCtx, 5*time.Minute)

	err = workflow.SetQueryHandler(newCtx, "timer-created", func() (bool, error) {
		return true, nil
	})
	if err != nil {
		return err
	}

	s.AddFuture(timer1, func(f workflow.Future) {
		err = f.Get(newCtx, nil)
		if !errors.Is(ctx.Err(), workflow.ErrCanceled) {
			panic("error is not canceled error")
		}
	})

	s.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, nil)
		cancel()
		s.Select(ctx)
	})

	s.Select(ctx)

	var result string
	err = workflow.ExecuteActivity(ctx, activities.EmptyActivity).Get(ctx, &result)
	return
}

func (w *Workflows) CancelTimerConcurrentWithOtherCommandWorkflow(ctx workflow.Context) (int, error) {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("CancelTimerConcurrentWithOtherCommandWorkflow workflow started")

	childCtx, cancelHandler := workflow.WithCancel(ctx)
	selector := workflow.NewSelector(ctx)

	var result int
	var err error
	var a Activities
	selector.AddReceive(workflow.GetSignalChannel(childCtx, "signal"), func(c workflow.ReceiveChannel, more bool) {
		var signal string
		if channelActive := c.Receive(ctx, &signal); channelActive {
			cancelHandler() // in this case the timer will be canceled
			err = workflow.ExecuteActivity(ctx, a.Echo, 0, 1).Get(ctx, &result)
		}
	})
	selector.AddFuture(workflow.NewTimer(childCtx, time.Second*5), func(future workflow.Future) {
		err = fmt.Errorf("timeout reached, no signal within allowed duration")
	})
	// Block until finished
	selector.Select(ctx)

	if err != nil {
		logger.Error("Activity failed.", "Error", err)
		return 0, err
	}

	logger.Info("HelloWorld workflow completed.", "result", result)

	return result, nil
}

func (w *Workflows) WaitSignalReturnParam(ctx workflow.Context, v interface{}) (interface{}, error) {
	// Wait for signal before returning
	s := workflow.NewSelector(ctx)
	signalCh := workflow.GetSignalChannel(ctx, "done-signal")
	s.AddReceive(signalCh, func(c workflow.ReceiveChannel, more bool) {
		var ignore bool
		c.Receive(ctx, &ignore)
		workflow.GetLogger(ctx).Info("Received signal")
	})
	s.Select(ctx)
	return v, nil
}

func (w *Workflows) ActivityWaitForWorkerStop(ctx workflow.Context, timeout time.Duration) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	var s string
	err := workflow.ExecuteActivity(ctx, "WaitForWorkerStop", timeout).Get(ctx, &s)
	return s, err
}

func (w *Workflows) ActivityHeartbeatUntilSignal(ctx workflow.Context) error {
	ch := workflow.GetSignalChannel(ctx, "cancel")
	actCtx, actCancel := workflow.WithCancel(workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Hour,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		WaitForCancellation: true,
		HeartbeatTimeout:    1 * time.Second,
	}))
	var a *Activities
	actFut := workflow.ExecuteActivity(actCtx, a.HeartbeatUntilCanceled, 100*time.Millisecond)

	// Wait for signal then cancel
	ch.Receive(ctx, nil)
	actCancel()
	return actFut.Get(ctx, nil)
}

func (w *Workflows) CancelChildAndExecuteActivityRace(ctx workflow.Context) error {
	// This workflow replicates an issue where cancel was reported out of order
	// with when it occurs. Specifically, this workflow creates a long-running
	// child then signals its cancellation from a simulated goroutine and
	// immediately starts an activity. Previously, the SDK would put the cancel
	// command before the execute command since the child workflow started first.

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 2 * time.Minute})

	// Start long-running child workflow
	childCtx, childCancel := workflow.WithCancel(ctx)
	childCtx = workflow.WithChildOptions(childCtx, workflow.ChildWorkflowOptions{WaitForCancellation: true})
	child := workflow.ExecuteChildWorkflow(childCtx, w.SleepForDuration, 3*time.Minute)
	if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return err
	}

	// Start "goroutine" to send to channel and immediately start activity
	ch := workflow.NewChannel(ctx)
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch.Send(ctx, nil)
		if err := workflow.ExecuteActivity(ctx, new(Activities).Sleep, 1*time.Millisecond).Get(ctx, nil); err != nil {
			panic(err)
		}
	})

	// Wait for channel and cancel child
	ch.Receive(ctx, nil)
	childCancel()
	_ = child.Get(ctx, nil)
	return nil
}

func (w *Workflows) SleepForDuration(ctx workflow.Context, d time.Duration) error {
	return workflow.Sleep(ctx, d)
}

func (w *Workflows) InterceptorCalls(ctx workflow.Context, someVal string) (string, error) {
	someVal = "workflow(" + someVal + ")"

	// Handle queries
	err := workflow.SetQueryHandler(ctx, "query", func(arg string) (string, error) {
		return "queryresult(" + arg + ")", nil
	})
	if err != nil {
		return "", err
	}

	// Exec activity
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	var a Activities
	if err := workflow.ExecuteActivity(ctx, a.InterceptorCalls, someVal).Get(ctx, &someVal); err != nil {
		return "", err
	}

	// Exec local activity
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	if err := workflow.ExecuteLocalActivity(ctx, a.Echo, 0, 0).Get(ctx, nil); err != nil {
		return "", err
	}

	// Do a bunch of other calls ignoring failure
	workflow.Go(ctx, func(workflow.Context) {})
	workflow.ExecuteChildWorkflow(ctx, "badworkflow")
	workflow.GetInfo(ctx)
	workflow.GetLogger(ctx)
	workflow.GetMetricsHandler(ctx)
	workflow.Now(ctx)
	workflow.NewTimer(ctx, 1*time.Millisecond)
	_ = workflow.Sleep(ctx, 1*time.Millisecond)
	_ = workflow.RequestCancelExternalWorkflow(ctx, "badid", "").Get(ctx, nil)
	_ = workflow.SignalExternalWorkflow(ctx, "badid", "", "badsignal", nil).Get(ctx, nil)
	_ = workflow.UpsertSearchAttributes(ctx, nil)
	_ = workflow.UpsertMemo(ctx, nil)
	workflow.SideEffect(ctx, func(workflow.Context) interface{} { return "sideeffect" })
	workflow.MutableSideEffect(ctx, "badid",
		func(workflow.Context) interface{} { return "mutablesideeffect" }, reflect.DeepEqual)
	workflow.GetVersion(ctx, "badchangeid", 2, 3)
	workflow.IsReplaying(ctx)
	workflow.HasLastCompletionResult(ctx)
	_ = workflow.GetLastCompletionResult(ctx)
	_ = workflow.GetLastError(ctx)
	_ = workflow.NewContinueAsNewError(ctx, "badworkflow")

	// Wait for signal
	finishCh := workflow.GetSignalChannel(ctx, "finish")
	var finishStr string
	finishCh.Receive(ctx, &finishStr)
	someVal = finishStr + "(" + someVal + ")"

	return someVal, nil
}

func (w *Workflows) WaitSignalToStart(ctx workflow.Context) (string, error) {
	var value string
	workflow.GetSignalChannel(ctx, "start-signal").Receive(ctx, &value)
	return value, nil
}

func (w *Workflows) SignalsAndQueries(ctx workflow.Context, execChild, execActivity bool) error {
	// Add query handler
	err := workflow.SetQueryHandler(ctx, "workflow-query", func() (string, error) { return "query-response", nil })
	if err != nil {
		return fmt.Errorf("failed setting query handler: %w", err)
	}

	// Wait for signal on start
	workflow.GetSignalChannel(ctx, "start-signal").Receive(ctx, nil)

	// Run child if requested
	if execChild {
		fut := workflow.ExecuteChildWorkflow(ctx, w.SignalsAndQueries, false, true)
		// Signal child twice
		if err := fut.SignalChildWorkflow(ctx, "start-signal", nil).Get(ctx, nil); err != nil {
			return fmt.Errorf("failed signaling child with start: %w", err)
		} else if err = fut.SignalChildWorkflow(ctx, "finish-signal", nil).Get(ctx, nil); err != nil {
			return fmt.Errorf("failed signaling child with finish: %w", err)
		}
		// Wait for done
		if err := fut.Get(ctx, nil); err != nil {
			return fmt.Errorf("child failed: %w", err)
		}
	}

	// Run activity if requested
	if execActivity {
		ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
		var a Activities
		if err := workflow.ExecuteActivity(ctx, a.ExternalSignalsAndQueries).Get(ctx, nil); err != nil {
			return fmt.Errorf("activity failed: %w", err)
		}
	}

	// Wait for finish signal
	workflow.GetSignalChannel(ctx, "finish-signal").Receive(ctx, nil)
	return nil
}

type AdvancedPostCancellationInput struct {
	PreCancelActivity  bool
	PostCancelActivity bool
	PreCancelTimer     bool
	PostCancelTimer    bool
}

func (w *Workflows) AdvancedPostCancellation(ctx workflow.Context, in *AdvancedPostCancellationInput) error {
	// Setup query to tell caller we're waiting for cancel
	waitingForCancel := false
	err := workflow.SetQueryHandler(ctx, "waiting-for-cancel", func() (bool, error) {
		return waitingForCancel, nil
	})
	if err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    5 * time.Second,
		WaitForCancellation: true,
	})
	var a *Activities

	// Start pre-cancel pieces
	var actFut, timerFut workflow.Future
	if in.PreCancelActivity {
		actFut = workflow.ExecuteActivity(ctx, a.HeartbeatUntilCanceled, 1*time.Second)
	}
	if in.PreCancelTimer {
		timerFut = workflow.NewTimer(ctx, 10*time.Minute)
	}

	// Set as waiting and wait for futures
	waitingForCancel = true
	if actFut != nil {
		if err := actFut.Get(ctx, nil); err != nil {
			return fmt.Errorf("activity did not gracefully cancel: %w", err)
		}
	}
	if timerFut != nil {
		if err := timerFut.Get(ctx, nil); !temporal.IsCanceledError(err) {
			return fmt.Errorf("timer did not get canceled error, got: %w", err)
		}
	}

	// Run post-cancel pieces with context not considered cancel
	ctx, _ = workflow.NewDisconnectedContext(ctx)
	if in.PostCancelActivity {
		if err := workflow.ExecuteActivity(ctx, a.Sleep, 1*time.Millisecond).Get(ctx, nil); err != nil {
			return fmt.Errorf("failed post-cancel activity: %w", err)
		}
	}
	if in.PostCancelTimer {
		if err := workflow.NewTimer(ctx, 1*time.Millisecond).Get(ctx, nil); err != nil {
			return fmt.Errorf("failed post-cancel timer: %w", err)
		}
	}
	return nil
}

func (w *Workflows) AdvancedPostCancellationChildWithDone(ctx workflow.Context) error {
	// Setup query to tell caller we're waiting for cancel
	waitingForCancel := false
	err := workflow.SetQueryHandler(ctx, "waiting-for-cancel", func() (bool, error) {
		return waitingForCancel, nil
	})
	if err != nil {
		return fmt.Errorf("failed setting query handler: %w", err)
	}

	// Start child but ignore future result
	workflow.ExecuteChildWorkflow(ctx, w.SleepForDuration, 5*time.Hour)

	// Mark as waiting for cancel and receive from done channel
	waitingForCancel = true
	ctx.Done().Receive(ctx, nil)

	// Run after-cancel activity
	ctx, _ = workflow.NewDisconnectedContext(ctx)
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	var a *Activities
	if err := workflow.ExecuteActivity(ctx, a.Sleep, 1*time.Millisecond).Get(ctx, nil); err != nil {
		return fmt.Errorf("failed post-cancel activity: %w", err)
	}
	return nil
}

type ParamsValue struct {
	Param1 string
	Param2 int
	Param3 bool
	Param4 struct{ SomeField string }
	Param5 *ParamsValue
	Param6 []byte
	Child  *ParamsValue
}

func (w *Workflows) TooFewParams(
	ctx workflow.Context,
	param1 string,
	param2 int,
	param3 bool,
	param4 struct{ SomeField string },
	param5 *ParamsValue,
	param6 []byte,
) (*ParamsValue, error) {
	ret := &ParamsValue{Param1: param1, Param2: param2, Param3: param3, Param4: param4, Param5: param5, Param6: param6}
	// Execute activity with only the first param
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{ScheduleToCloseTimeout: 1 * time.Minute})
	var a *Activities
	return ret, workflow.ExecuteActivity(ctx, a.TooFewParams, param1).Get(ctx, &ret.Child)
}

func (w *Workflows) ExecuteRemoteActivityToUpper(ctx workflow.Context, taskQueue, str string) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:              taskQueue,
		ScheduleToCloseTimeout: 5 * time.Second,
	})
	var resp string
	err := workflow.ExecuteActivity(ctx, (*Activities2).ToUpper, str).Get(ctx, &resp)
	return resp, err
}

func (w *Workflows) ReturnCancelError(
	ctx workflow.Context,
	fromActivity bool,
	rawActivityError bool,
	waitForCancel bool,
	goCancelError bool,
) error {
	// Use activity if requested
	if fromActivity {
		actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			ScheduleToCloseTimeout: 5 * time.Second,
			// No retry
			RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		actCtx, actCancel := workflow.WithCancel(actCtx)
		var a *Activities
		actFut := workflow.ExecuteActivity(actCtx, a.ReturnCancelError, waitForCancel, goCancelError)
		// If waiting for cancel, sleep a bit then cancel
		if waitForCancel {
			_ = workflow.Sleep(ctx, 100*time.Millisecond)
			actCancel()
		}
		if err := actFut.Get(ctx, nil); err != nil {
			// If requested, we use the raw activity error. Otherwise we just use the
			// string.
			if rawActivityError {
				return err
			}
			return errors.New(err.Error())
		}
		return nil
	}

	// Wait for cancel if requested
	if waitForCancel {
		ctx.Done().Receive(ctx, nil)
	}

	// Return canceled
	if goCancelError {
		return context.Canceled
	}
	return temporal.NewCanceledError("some details")
}

func (w *Workflows) LocalActivityByStringName(ctx workflow.Context) error {
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	return workflow.ExecuteLocalActivity(ctx, "Prefix_ToUpper", "somestring").Get(ctx, nil)
}

func (w *Workflows) SignalCounter(ctx workflow.Context) error {
	var signalCount int
	err := workflow.SetQueryHandler(ctx, "has-signal-count", func(expected int) (bool, error) {
		return signalCount == expected, nil
	})
	if err != nil {
		return err
	}
	signalCh := workflow.GetSignalChannel(ctx, "signal")
	for {
		signalCh.Receive(ctx, nil)
		signalCount++
	}
}

func (w *Workflows) PanicOnSignal(ctx workflow.Context) error {
	// Wait for signal then panic
	workflow.GetSignalChannel(ctx, "panic-signal").Receive(ctx, nil)
	panic("intentional panic")
}

var forcedNonDeterminismCounter int

func (w *Workflows) ForcedNonDeterminism(ctx workflow.Context, sameCommandButDiffName bool) (err error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	var a Activities
	var waitTickCount int
	tickCh := workflow.GetSignalChannel(ctx, "tick")
	err = workflow.SetQueryHandler(
		ctx,
		"is-wait-tick-count",
		func(v int) (bool, error) { return waitTickCount == v, nil },
	)
	for err == nil {
		waitTickCount++
		tickCh.Receive(ctx, nil)
		// Exec activity at first, then either exec diff activity or timer next
		if forcedNonDeterminismCounter == 0 {
			err = workflow.ExecuteActivity(ctx, a.Sleep, 1*time.Millisecond).Get(ctx, nil)
		} else if sameCommandButDiffName {
			err = workflow.ExecuteActivity(ctx, a.Echo, 1, 1).Get(ctx, nil)
		} else {
			err = workflow.Sleep(ctx, 1*time.Millisecond)
		}
	}
	return
}

func (w *Workflows) UpsertSearchAttributesConditional(ctx workflow.Context, maxTicks int) error {
	var waitTickCount int
	tickCh := workflow.GetSignalChannel(ctx, "tick")
	err := workflow.SetQueryHandler(
		ctx,
		"is-wait-tick-count",
		func(v int) (bool, error) { return waitTickCount == v, nil },
	)
	if err != nil {
		return err
	}
	currentPayload, exists := workflow.GetInfo(ctx).SearchAttributes.GetIndexedFields()["CustomKeywordField"]
	if !exists {
		return errors.New("search attribute not present")
	}
	var searchAttr string
	err = converter.GetDefaultDataConverter().FromPayload(currentPayload, &searchAttr)
	log.Printf("Search attribute: %s. Replaying? %v.", searchAttr, workflow.IsReplaying(ctx))
	if err != nil {
		return errors.New("error when get search attribute")
	}
	// Search attribute should always be "unset".
	if searchAttr == "set" {
		err = workflow.Sleep(ctx, 100*time.Millisecond)
	} else if searchAttr == "unset" {
		err = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{"CustomKeywordField": "set"})
	} else {
		return errors.New("unkown search attribute value")
	}
	if err != nil {
		return err
	}
	// Now just wait for signals over and over
	for {
		waitTickCount++
		if waitTickCount >= maxTicks {
			return nil
		}
		tickCh.Receive(ctx, nil)
		log.Printf("Signal received (replaying? %v)", workflow.IsReplaying(ctx))
	}
}

func (w *Workflows) UpsertMemoConditional(ctx workflow.Context, maxTicks int) error {
	var waitTickCount int
	tickCh := workflow.GetSignalChannel(ctx, "tick")
	err := workflow.SetQueryHandler(
		ctx,
		"is-wait-tick-count",
		func(v int) (bool, error) { return waitTickCount == v, nil },
	)
	if err != nil {
		return err
	}
	// Get current memo value
	currentPayload, ok := workflow.GetInfo(ctx).Memo.GetFields()["TestMemo"]
	if !ok {
		return errors.New("no memo value")
	}
	var memoValue string
	err = converter.GetDefaultDataConverter().FromPayload(currentPayload, &memoValue)
	if err != nil {
		return err
	}
	// Memo should always be "unset".
	log.Printf("Memo value %s, Replaying? %v.", memoValue, workflow.IsReplaying(ctx))
	if memoValue == "set" {
		err = workflow.Sleep(ctx, 100*time.Millisecond)
	} else if memoValue == "unset" {
		err = workflow.UpsertMemo(ctx, map[string]interface{}{"TestMemo": "set"})
	} else {
		return errors.New("memo unknown value")
	}
	if err != nil {
		return err
	}
	// Now just wait for signals over and over
	for {
		waitTickCount++
		if waitTickCount >= maxTicks {
			return nil
		}
		tickCh.Receive(ctx, nil)
		log.Printf("Signal received (replaying? %v)", workflow.IsReplaying(ctx))
	}
}

func (w *Workflows) LocalActivityStaleCache(ctx workflow.Context, maxTicks int) error {
	var waitTickCount int
	tickCh := workflow.GetSignalChannel(ctx, "tick")
	err := workflow.SetQueryHandler(
		ctx,
		"is-wait-tick-count",
		func(v int) (bool, error) { return waitTickCount == v, nil },
	)
	if err != nil {
		return err
	}
	oneRetry := &temporal.RetryPolicy{InitialInterval: 1 * time.Nanosecond, MaximumAttempts: 1}

	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         oneRetry,
	})

	// Now just wait for signals over and over
	for {
		waitTickCount++
		if waitTickCount >= maxTicks {
			return nil
		}
		tickCh.Receive(ctx, nil)
		err = workflow.ExecuteLocalActivity(ctx, func(tickCount int) error {
			log.Printf("Running local activity on tickCount %d", waitTickCount)
			return nil
		}, waitTickCount).Get(ctx, nil)
		if err != nil {
			return err
		}

		log.Printf("Signal received (replaying? %v)", workflow.IsReplaying(ctx))
	}
}

func (w *Workflows) MutableSideEffect(ctx workflow.Context, startVal int) (currVal int, err error) {
	// Make some mutable side effect calls with timers in between
	sideEffector := func(retVal int) (newVal int, err error) {
		err = workflow.MutableSideEffect(
			ctx,
			"side-effect-1",
			func(ctx workflow.Context) interface{} { return retVal },
			func(a, b interface{}) bool { return a.(int) == b.(int) },
		).Get(&newVal)
		if retVal != newVal {
			log.Panicf("MutableSideEffect did not return expected value %d == %d", retVal, newVal)
		}
		return
	}
	// Make several mutable side effect calls, some that change the data, some
	// that don't. And then sleep and do again. This checks that multiple
	// mutable side effects of the same ID can happen at the same time, and that
	// replay properly distinguishes between which ones were recorded and which
	// weren't for command counting purposes
	if currVal, err = sideEffector(startVal); err != nil {
		panic(err)
	} else if currVal, err = sideEffector(currVal); err != nil {
		panic(err)
	} else if currVal, err = sideEffector(currVal + 1); err != nil {
		panic(err)
	} else if currVal, err = sideEffector(currVal); err != nil {
		panic(err)
	} else if err = workflow.Sleep(ctx, 1*time.Millisecond); err != nil {
		panic(err)
	} else if currVal, err = sideEffector(currVal); err != nil {
		panic(err)
	} else if currVal, err = sideEffector(currVal); err != nil {
		panic(err)
	} else if currVal, err = sideEffector(currVal + 1); err != nil {
		panic(err)
	} else if err = workflow.Sleep(ctx, 1*time.Millisecond); err != nil {
		panic(err)
	} else if currVal, err = sideEffector(currVal + 1); err != nil {
		panic(err)
	}
	err = workflow.Sleep(ctx, 1*time.Millisecond)
	return
}

func (w *Workflows) VersionLoopWorkflow(ctx workflow.Context, changeIDs []string, iterations int) error {
	for _, changeID := range changeIDs {
		for i := 0; i < iterations; i++ {
			workflow.GetVersion(ctx, fmt.Sprintf("%s:%d", changeID, i), workflow.DefaultVersion, 1)
		}
		err := workflow.Sleep(ctx, time.Second)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Workflows) HistoryLengths(ctx workflow.Context, activityCount int) (lengths []int, err error) {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	var a Activities
	for i := 0; i < activityCount && err == nil; i++ {
		// Every other one we'll do local activities
		lengths = append(lengths, workflow.GetInfo(ctx).GetCurrentHistoryLength())
		if i%2 == 0 {
			err = workflow.ExecuteActivity(ctx, a.Sleep, 1*time.Millisecond).Get(ctx, nil)
		} else {
			err = workflow.ExecuteLocalActivity(ctx, a.Sleep, 1*time.Millisecond).Get(ctx, nil)
		}
	}
	return
}

func (w *Workflows) HeartbeatSpecificCount(ctx workflow.Context, interval time.Duration, count int) error {
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptionsWithRetry())
	var activities *Activities
	return workflow.ExecuteActivity(ctx, activities.HeartbeatSpecificCount, interval, count).Get(ctx, nil)
}

func (w *Workflows) UpsertMemo(ctx workflow.Context, memo map[string]interface{}) (*commonpb.Memo, error) {
	err := workflow.UpsertMemo(ctx, memo)
	if err != nil {
		return nil, err
	}
	return workflow.GetInfo(ctx).Memo, nil
}

func (w *Workflows) register(worker worker.Worker) {
	worker.RegisterWorkflow(w.ActivityCancelRepro)
	worker.RegisterWorkflow(w.ActivityCompletionUsingID)
	worker.RegisterWorkflow(w.ActivityHeartbeatWithRetry)
	worker.RegisterWorkflow(w.ActivityRetryOnError)
	worker.RegisterWorkflow(w.CallUnregisteredActivityRetry)
	worker.RegisterWorkflow(w.ActivityRetryOnHBTimeout)
	worker.RegisterWorkflow(w.ActivityRetryOnTimeout)
	worker.RegisterWorkflow(w.ActivityRetryOptionsChange)
	worker.RegisterWorkflow(w.ActivityWaitForWorkerStop)
	worker.RegisterWorkflow(w.ActivityHeartbeatUntilSignal)
	worker.RegisterWorkflow(w.Basic)
	worker.RegisterWorkflow(w.Deadlocked)
	worker.RegisterWorkflow(w.DeadlockedWithLocalActivity)
	worker.RegisterWorkflow(w.Panicked)
	worker.RegisterWorkflow(w.PanickedActivity)
	worker.RegisterWorkflow(w.BasicSession)
	worker.RegisterWorkflow(w.AdvancedSession)
	worker.RegisterWorkflow(w.CancelActivity)
	worker.RegisterWorkflow(w.CancelActivityImmediately)
	worker.RegisterWorkflow(w.CancelChildWorkflow)
	worker.RegisterWorkflow(w.StartingChildAfterBeingCanceled)
	worker.RegisterWorkflow(w.CancelTimer)
	worker.RegisterWorkflow(w.CancelTimerAfterActivity)
	worker.RegisterWorkflow(w.CancelTimerViaDeferAfterWFTFailure)
	worker.RegisterWorkflow(w.CascadingCancellation)
	worker.RegisterWorkflow(w.WaitForCancelWithDisconnectedContextWorkflow)
	worker.RegisterWorkflow(w.ChildWorkflowRetryOnError)
	worker.RegisterWorkflow(w.ChildWorkflowRetryOnTimeout)
	worker.RegisterWorkflow(w.ChildWorkflowSuccess)
	worker.RegisterWorkflow(w.ChildWorkflowSuccessWithParentClosePolicyTerminate)
	worker.RegisterWorkflow(w.ChildWorkflowSuccessWithParentClosePolicyAbandon)
	worker.RegisterWorkflow(w.ChildWorkflowCancelUnusualTransitionsRepro)
	worker.RegisterWorkflow(w.ChildWorkflowDuplicatePanicRepro)
	worker.RegisterWorkflow(w.ChildWorkflowDuplicateGetExecutionStuckRepro)
	worker.RegisterWorkflow(w.MutatingQueryWorkflow)
	worker.RegisterWorkflow(w.MutatingUpdateValidatorWorkflow)
	worker.RegisterWorkflow(w.MutatingSideEffectWorkflow)
	worker.RegisterWorkflow(w.MutatingMutableSideEffectWorkflow)
	worker.RegisterWorkflow(w.ConsistentQueryWorkflow)
	worker.RegisterWorkflow(w.ContextPropagator)
	worker.RegisterWorkflow(w.ContinueAsNew)
	worker.RegisterWorkflow(w.UpsertSearchAttributesConditional)
	worker.RegisterWorkflow(w.UpsertMemoConditional)
	worker.RegisterWorkflow(w.ContinueAsNewWithOptions)
	worker.RegisterWorkflow(w.IDReusePolicy)
	worker.RegisterWorkflow(w.InspectActivityInfo)
	worker.RegisterWorkflow(w.InspectLocalActivityInfo)
	worker.RegisterWorkflow(w.LargeQueryResultWorkflow)
	worker.RegisterWorkflow(w.LongRunningActivityWithHB)
	worker.RegisterWorkflow(w.RetryTimeoutStableErrorWorkflow)
	worker.RegisterWorkflow(w.SimplestWorkflow)
	worker.RegisterWorkflow(w.TwoParameterWorkflow)
	worker.RegisterWorkflow(w.ThreeParameterWorkflow)
	worker.RegisterWorkflow(w.WaitSignalReturnParam)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityCtxPropagation)
	worker.RegisterWorkflow(w.WorkflowWithParallelLongLocalActivityAndHeartbeat)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityRetries)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityRetriesAndHeartbeat)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityRetriesAndDefaultRetryPolicy)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityRetriesAndPartialRetryPolicy)
	worker.RegisterWorkflow(w.WorkflowWithParallelLocalActivities)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityStartWhenTimerCancel)
	worker.RegisterWorkflow(w.WorkflowWithParallelSideEffects)
	worker.RegisterWorkflow(w.WorkflowWithParallelMutableSideEffects)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityStartToCloseTimeout)
	worker.RegisterWorkflow(w.LocalActivityStaleCache)
	worker.RegisterWorkflow(w.UpdateInfoWorkflow)
	worker.RegisterWorkflow(w.SignalWorkflow)
	worker.RegisterWorkflow(w.CronWorkflow)
	worker.RegisterWorkflow(w.ActivityTimeoutsWorkflow)
	worker.RegisterWorkflow(w.CancelTimerConcurrentWithOtherCommandWorkflow)
	worker.RegisterWorkflow(w.CancelMultipleCommandsOverMultipleTasks)
	worker.RegisterWorkflow(w.CancelChildAndExecuteActivityRace)
	worker.RegisterWorkflow(w.SleepForDuration)
	worker.RegisterWorkflow(w.InterceptorCalls)
	worker.RegisterWorkflow(w.WaitSignalToStart)
	worker.RegisterWorkflow(w.SignalsAndQueries)
	worker.RegisterWorkflow(w.AdvancedPostCancellation)
	worker.RegisterWorkflow(w.AdvancedPostCancellationChildWithDone)
	worker.RegisterWorkflow(w.TooFewParams)
	worker.RegisterWorkflow(w.ExecuteRemoteActivityToUpper)
	worker.RegisterWorkflow(w.ReturnCancelError)
	worker.RegisterWorkflow(w.LocalActivityByStringName)
	worker.RegisterWorkflow(w.SignalCounter)
	worker.RegisterWorkflow(w.PanicOnSignal)
	worker.RegisterWorkflow(w.ForcedNonDeterminism)
	worker.RegisterWorkflow(w.MutableSideEffect)
	worker.RegisterWorkflow(w.HistoryLengths)
	worker.RegisterWorkflow(w.HeartbeatSpecificCount)
	worker.RegisterWorkflow(w.UpsertMemo)
	worker.RegisterWorkflow(w.SessionFailedStateWorkflow)
	worker.RegisterWorkflow(w.VersionLoopWorkflow)
	worker.RegisterWorkflow(w.RaceOnCacheEviction)

	worker.RegisterWorkflow(w.child)
	worker.RegisterWorkflow(w.childForMemoAndSearchAttr)
	worker.RegisterWorkflow(w.childWorkflowWaitOnSignal)
	worker.RegisterWorkflow(w.childWorkflowWaitOnContextCancel)
	worker.RegisterWorkflow(w.ChildWorkflowAndParentCancel)
	worker.RegisterWorkflow(w.sleep)
	worker.RegisterWorkflow(w.timer)
}

func (w *Workflows) defaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		ScheduleToStartTimeout: 5 * time.Second,
		ScheduleToCloseTimeout: 5 * time.Second,
		StartToCloseTimeout:    9 * time.Second,
	}
}

func (w *Workflows) defaultLocalActivityOptions() workflow.LocalActivityOptions {
	return workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	}
}

func (w *Workflows) defaultActivityOptionsWithRetry() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		ScheduleToStartTimeout: 5 * time.Second,
		ScheduleToCloseTimeout: 5 * time.Second,
		StartToCloseTimeout:    9 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Second,
			MaximumAttempts:    3,
		},
	}
}
