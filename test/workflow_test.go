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
	"time"

	enumspb "go.temporal.io/temporal-proto/enums/v1"

	"go.temporal.io/temporal"
	"go.temporal.io/temporal/client"
	"go.temporal.io/temporal/encoded"
	"go.temporal.io/temporal/internal"
	"go.temporal.io/temporal/worker"
	"go.temporal.io/temporal/workflow"
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

func (w *Workflows) ActivityRetryOnHBTimeout(ctx workflow.Context) ([]string, error) {
	opts := w.defaultActivityOptionsWithRetry()
	opts.HeartbeatTimeout = time.Second
	ctx = workflow.WithActivityOptions(ctx, opts)

	var result int
	startTime := workflow.Now(ctx)
	err := workflow.ExecuteActivity(ctx, "HeartbeatAndSleep", 0, 2*time.Second).Get(ctx, &result)
	if err == nil {
		return nil, fmt.Errorf("expected activity to fail but succeeded")
	}

	elapsed := workflow.Now(ctx).Sub(startTime)
	if elapsed < 5*time.Second {
		return nil, fmt.Errorf("expected activity to be retried on failure, but it was not. Elapsed time: %d", elapsed.Milliseconds())
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

func (w *Workflows) ContinueAsNew(ctx workflow.Context, count int, taskList string) (int, error) {
	tl := workflow.GetInfo(ctx).TaskListName
	if tl != taskList {
		return -1, fmt.Errorf("invalid taskListName name, expected=%v, got=%v", taskList, tl)
	}
	if count == 0 {
		return 999, nil
	}
	ctx = workflow.WithTaskList(ctx, taskList)
	return -1, workflow.NewContinueAsNewError(ctx, w.ContinueAsNew, count-1, taskList)
}

func (w *Workflows) ContinueAsNewWithOptions(ctx workflow.Context, count int, taskList string) (string, error) {
	info := workflow.GetInfo(ctx)
	tl := info.TaskListName
	if tl != taskList {
		return "", fmt.Errorf("invalid taskListName name, expected=%v, got=%v", taskList, tl)
	}

	if info.Memo == nil || info.SearchAttributes == nil {
		return "", errors.New("memo or search attributes are not carried over")
	}
	var memoVal string
	err := encoded.GetDefaultDataConverter().FromPayload(info.Memo.Fields["memoKey"], &memoVal)
	if err != nil {
		return "", errors.New("error when get memo value")
	}

	var searchAttrVal string
	err = encoded.GetDefaultDataConverter().FromPayload(info.SearchAttributes.IndexedFields["CustomKeywordField"], &searchAttrVal)
	if err != nil {
		return "", errors.New("error when get search attribute value")
	}

	if count == 0 {
		return memoVal + "," + searchAttrVal, nil
	}
	ctx = workflow.WithTaskList(ctx, taskList)

	return "", workflow.NewContinueAsNewError(ctx, w.ContinueAsNewWithOptions, count-1, taskList)
}

func (w *Workflows) IDReusePolicy(
	ctx workflow.Context,
	childWFID string,
	policy client.WorkflowIDReusePolicy,
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
		WorkflowExecutionTimeout: time.Second,
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
		ParentClosePolicy:        client.ParentClosePolicyAbandon,
	}
	ctx = workflow.WithChildOptions(ctx, opts)
	ft := workflow.ExecuteChildWorkflow(ctx, w.sleep, 5*time.Second)
	var childWE internal.WorkflowExecution
	err = ft.GetChildWorkflowExecution().Get(ctx, &childWE)
	return childWE.ID, err
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
			workflow.GetLogger(activityCtx).Sugar().Infof("Activity Failed: Err: %v", err)
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
			TaskList:               "bad_tl",
		})

		activityF := workflow.ExecuteActivity(activityCtx, "Prefix_ToUpper", "hello")
		var ans string
		err := activityF.Get(activityCtx, &ans)
		if err != nil {
			workflow.GetLogger(activityCtx).Sugar().Infof("Activity Failed: Err: %v", err)
		}
	})

	// Third go-routine which get blocked on ActivitySchedule and not started
	workflow.Go(ctx, func(ctx1 workflow.Context) {
		activityCtx := workflow.WithActivityOptions(ctx1, workflow.ActivityOptions{
			ScheduleToStartTimeout: 10 * time.Second,
			ScheduleToCloseTimeout: 10 * time.Second,
			StartToCloseTimeout:    1 * time.Second,
			TaskList:               "bad_tl",
		})

		activityF := workflow.ExecuteActivity(activityCtx, "Prefix_ToUpper", "hello")
		var ans string
		err := activityF.Get(activityCtx, &ans)
		if err != nil {
			workflow.GetLogger(activityCtx).Sugar().Infof("Activity Failed: Err: %v", err)
		}
	})

	// Cause the workflow to block on sleep
	_ = workflow.Sleep(ctx, 10*time.Second)

	return []string{"toUpperWithDelay"}, nil
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
	err = encoded.GetDefaultDataConverter().FromPayload(info.Memo.Fields["memoKey"], &memo)
	if err != nil {
		return
	}
	var searchAttrVal string
	err = encoded.GetDefaultDataConverter().FromPayload(info.SearchAttributes.IndexedFields["CustomKeywordField"], &searchAttrVal)
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

func (w *Workflows) InspectActivityInfo(ctx workflow.Context) error {
	info := workflow.GetInfo(ctx)
	namespace := info.Namespace
	wfType := info.WorkflowType.Name
	taskList := info.TaskListName
	ctx = workflow.WithActivityOptions(ctx, w.defaultActivityOptions())
	return workflow.ExecuteActivity(ctx, "inspectActivityInfo", namespace, taskList, wfType).Get(ctx, nil)
}

func (w *Workflows) InspectLocalActivityInfo(ctx workflow.Context) error {
	info := workflow.GetInfo(ctx)
	namespace := info.Namespace
	wfType := info.WorkflowType.Name
	taskList := info.TaskListName
	ctx = workflow.WithLocalActivityOptions(ctx, w.defaultLocalActivityOptions())
	activites := Activities{}
	return workflow.ExecuteLocalActivity(
		ctx, activites.InspectActivityInfo, namespace, taskList, wfType).Get(ctx, nil)
}

func (w *Workflows) register(worker worker.Worker) {
	worker.RegisterWorkflow(w.Basic)
	worker.RegisterWorkflow(w.ActivityRetryOnError)
	worker.RegisterWorkflow(w.ActivityRetryOnHBTimeout)
	worker.RegisterWorkflow(w.ActivityRetryOnTimeout)
	worker.RegisterWorkflow(w.ActivityRetryOptionsChange)
	worker.RegisterWorkflow(w.ContinueAsNew)
	worker.RegisterWorkflow(w.ContinueAsNewWithOptions)
	worker.RegisterWorkflow(w.IDReusePolicy)
	worker.RegisterWorkflow(w.ChildWorkflowRetryOnError)
	worker.RegisterWorkflow(w.ChildWorkflowRetryOnTimeout)
	worker.RegisterWorkflow(w.ChildWorkflowSuccess)
	worker.RegisterWorkflow(w.ChildWorkflowSuccessWithParentClosePolicyTerminate)
	worker.RegisterWorkflow(w.ChildWorkflowSuccessWithParentClosePolicyAbandon)
	worker.RegisterWorkflow(w.InspectActivityInfo)
	worker.RegisterWorkflow(w.InspectLocalActivityInfo)
	worker.RegisterWorkflow(w.sleep)
	worker.RegisterWorkflow(w.child)
	worker.RegisterWorkflow(w.childForMemoAndSearchAttr)
	worker.RegisterWorkflow(w.ActivityCancelRepro)
	worker.RegisterWorkflow(w.SimplestWorkflow)
	worker.RegisterWorkflow(w.LargeQueryResultWorkflow)
	worker.RegisterWorkflow(w.RetryTimeoutStableErrorWorkflow)
	worker.RegisterWorkflow(w.ConsistentQueryWorkflow)
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
