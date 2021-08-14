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
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
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
	opts.HeartbeatTimeout = 3 * time.Second
	opts.ScheduleToCloseTimeout = time.Second * 12
	opts.StartToCloseTimeout = time.Second * 12
	opts.RetryPolicy = &internal.RetryPolicy{
		MaximumAttempts: 1,
	}
	ctx = workflow.WithActivityOptions(ctx, opts)

	err := workflow.ExecuteActivity(ctx, "LongRunningHeartbeat", 8*time.Second, 300*time.Millisecond).Get(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("expected activity to succeed but it failed: %v", err)
	}

	return []string{"longRunningHeartbeat"}, nil
}

func (w *Workflows) ActivityRetryOnHBTimeout(ctx workflow.Context) ([]string, error) {
	opts := workflow.ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		ScheduleToCloseTimeout: 10 * time.Second,
		StartToCloseTimeout:    3 * time.Second,
		HeartbeatTimeout:       time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 1.0,
			MaximumAttempts:    3,
		},
	}
	opts.HeartbeatTimeout = time.Second
	ctx = workflow.WithActivityOptions(ctx, opts)

	var result int
	err := workflow.ExecuteActivity(ctx, "HeartbeatAndSleep", 0, 3*time.Second).Get(ctx, &result)
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

	if info.Memo == nil || info.SearchAttributes == nil {
		return "", errors.New("memo or search attributes are not carried over")
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
		return memoVal + "," + searchAttrVal, nil
	}
	ctx = workflow.WithTaskQueue(ctx, taskQueue)

	return "", workflow.NewContinueAsNewError(ctx, w.ContinueAsNewWithOptions, count-1, taskQueue)
}

func (w *Workflows) IDReusePolicy(
	ctx workflow.Context,
	childWFID string,
	policy enumspb.WorkflowIdReusePolicy,
	parallel bool,
	failFirstChild bool) (string, error) {

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
		return "", err
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
	return workflow.ExecuteActivity(ctx, "inspectActivityInfo", namespace, taskQueue, wfType).Get(ctx, nil)
}

func (w *Workflows) InspectLocalActivityInfo(ctx workflow.Context) error {
	info := workflow.GetInfo(ctx)
	namespace := info.Namespace
	wfType := info.WorkflowType.Name
	taskQueue := info.TaskQueueName
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	var activities *Activities
	return workflow.ExecuteLocalActivity(
		ctx, activities.InspectActivityInfo, namespace, taskQueue, wfType).Get(ctx, nil)
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

func (w *Workflows) register(worker worker.Worker) {
	worker.RegisterWorkflow(w.ActivityCancelRepro)
	worker.RegisterWorkflow(w.ActivityCompletionUsingID)
	worker.RegisterWorkflow(w.ActivityRetryOnError)
	worker.RegisterWorkflow(w.CallUnregisteredActivityRetry)
	worker.RegisterWorkflow(w.ActivityRetryOnHBTimeout)
	worker.RegisterWorkflow(w.ActivityRetryOnTimeout)
	worker.RegisterWorkflow(w.ActivityRetryOptionsChange)
	worker.RegisterWorkflow(w.Basic)
	worker.RegisterWorkflow(w.Deadlocked)
	worker.RegisterWorkflow(w.DeadlockedWithLocalActivity)
	worker.RegisterWorkflow(w.Panicked)
	worker.RegisterWorkflow(w.BasicSession)
	worker.RegisterWorkflow(w.CancelActivity)
	worker.RegisterWorkflow(w.CancelActivityImmediately)
	worker.RegisterWorkflow(w.CancelChildWorkflow)
	worker.RegisterWorkflow(w.StartingChildAfterBeingCanceled)
	worker.RegisterWorkflow(w.CancelTimer)
	worker.RegisterWorkflow(w.CancelTimerAfterActivity)
	worker.RegisterWorkflow(w.CascadingCancellation)
	worker.RegisterWorkflow(w.ChildWorkflowRetryOnError)
	worker.RegisterWorkflow(w.ChildWorkflowRetryOnTimeout)
	worker.RegisterWorkflow(w.ChildWorkflowSuccess)
	worker.RegisterWorkflow(w.ChildWorkflowSuccessWithParentClosePolicyTerminate)
	worker.RegisterWorkflow(w.ChildWorkflowSuccessWithParentClosePolicyAbandon)
	worker.RegisterWorkflow(w.ChildWorkflowCancelUnusualTransitionsRepro)
	worker.RegisterWorkflow(w.ConsistentQueryWorkflow)
	worker.RegisterWorkflow(w.ContextPropagator)
	worker.RegisterWorkflow(w.ContinueAsNew)
	worker.RegisterWorkflow(w.ContinueAsNewWithOptions)
	worker.RegisterWorkflow(w.IDReusePolicy)
	worker.RegisterWorkflow(w.InspectActivityInfo)
	worker.RegisterWorkflow(w.InspectLocalActivityInfo)
	worker.RegisterWorkflow(w.LargeQueryResultWorkflow)
	worker.RegisterWorkflow(w.LongRunningActivityWithHB)
	worker.RegisterWorkflow(w.RetryTimeoutStableErrorWorkflow)
	worker.RegisterWorkflow(w.SimplestWorkflow)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityCtxPropagation)
	worker.RegisterWorkflow(w.WorkflowWithParallelLongLocalActivityAndHeartbeat)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityRetries)
	worker.RegisterWorkflow(w.WorkflowWithParallelLocalActivities)
	worker.RegisterWorkflow(w.WorkflowWithLocalActivityStartWhenTimerCancel)
	worker.RegisterWorkflow(w.WorkflowWithParallelSideEffects)
	worker.RegisterWorkflow(w.WorkflowWithParallelMutableSideEffects)
	worker.RegisterWorkflow(w.SignalWorkflow)
	worker.RegisterWorkflow(w.CronWorkflow)
	worker.RegisterWorkflow(w.CancelTimerConcurrentWithOtherCommandWorkflow)
	worker.RegisterWorkflow(w.CancelMultipleCommandsOverMultipleTasks)

	worker.RegisterWorkflow(w.child)
	worker.RegisterWorkflow(w.childForMemoAndSearchAttr)
	worker.RegisterWorkflow(w.childWorkflowWaitOnSignal)
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
