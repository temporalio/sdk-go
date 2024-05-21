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
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/baggage"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
)

type Activities struct {
	client            client.Client
	mu                sync.Mutex
	invocations       []string
	activities2       *Activities2
	manualStopContext context.Context
}

type Activities2 struct {
	impl *Activities
}

var errFailOnPurpose = temporal.NewApplicationError("failing on purpose", "")

func newActivities() *Activities {
	activities2 := &Activities2{}
	result := &Activities{activities2: activities2}
	activities2.impl = result
	return result
}

func (a *Activities) RetryTimeoutStableErrorActivity() error {
	time.Sleep(1*time.Second + 100*time.Millisecond)
	return errFailOnPurpose
}

func (a *Activities) Sleep(_ context.Context, delay time.Duration) error {
	a.append("sleep")
	time.Sleep(delay)
	return nil
}

func (a *Activities) SleepN(ctx context.Context, delay time.Duration, times int) (int32, error) {
	a.append("sleepN")
	if activity.GetInfo(ctx).Attempt >= int32(times) {
		return activity.GetInfo(ctx).Attempt, nil
	}
	time.Sleep(delay)
	return activity.GetInfo(ctx).Attempt, nil
}

func LocalSleep(_ context.Context, delay time.Duration) error {
	time.Sleep(delay)
	return nil
}

func ErrorWithNextDelay(_ context.Context, delay time.Duration) error {
	return temporal.NewApplicationErrorWithOptions("error with next delay", "NextDelay", temporal.ApplicationErrorOptions{
		NextRetryDelay: delay,
	})
}

func (a *Activities) ActivityToBeCanceled(ctx context.Context) (string, error) {
	a.append("ActivityToBeCanceled")
	for {
		select {
		case <-time.After(1 * time.Second):
			activity.RecordHeartbeat(ctx, "")
		case <-ctx.Done():
			return "I am canceled by Done", nil
		}
	}
}

func (a *Activities) EmptyActivity(ctx context.Context) error {
	a.append("EmptyActivity")
	return nil
}

func (a *Activities) HeartbeatAndSleep(ctx context.Context, seq int, delay time.Duration) (int, error) {
	a.append("heartbeatAndSleep")
	activity.GetLogger(ctx).Info("Running HeartbeatAndSleep activity")
	if activity.HasHeartbeatDetails(ctx) {
		var prev int
		if err := activity.GetHeartbeatDetails(ctx, &prev); err == nil {
			seq = prev
		}
	}
	seq++
	activity.RecordHeartbeat(ctx, seq)
	time.Sleep(delay)
	return seq, nil
}

func (a *Activities) LongRunningHeartbeat(ctx context.Context, delay time.Duration, recordHeartbeatDelay time.Duration) error {
	a.append("longRunningHeartbeat")
	endTime := time.Now().Add(delay)
	for time.Now().Before(endTime) {
		activity.RecordHeartbeat(ctx)
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		time.Sleep(recordHeartbeatDelay)
	}

	return nil
}

func (a *Activities) HeartbeatSpecificCount(ctx context.Context, interval time.Duration, count int) error {
	for i := 0; i < count; i++ {
		time.Sleep(interval)
		activity.RecordHeartbeat(ctx)
	}
	return nil
}

func (a *Activities) HeartbeatTwiceAndFailNTimes(
	ctx context.Context,
	times int,
	id string,
) (heartbeatCounts int, err error) {
	// Get details
	if activity.HasHeartbeatDetails(ctx) {
		if err = activity.GetHeartbeatDetails(ctx, &heartbeatCounts); err != nil {
			return
		}
	}

	// Heartbeat twice, incrementing before each
	heartbeatCounts++
	activity.RecordHeartbeat(ctx, heartbeatCounts)
	heartbeatCounts++
	activity.RecordHeartbeat(ctx, heartbeatCounts)

	// Set error if haven't reached enough times
	a.append(id)
	if a.invokedCount(id) <= times {
		err = errFailOnPurpose
	}
	return
}

func (a *Activities) fail(_ context.Context) error {
	a.append("fail")
	return errFailOnPurpose
}

func (a *Activities) failNTimes(_ context.Context, times int, id int) error {
	invokeid := "failNTimes" + strconv.Itoa(id)
	a.append(invokeid)
	if a.invokedCount(invokeid) > times {
		return nil
	}
	return errFailOnPurpose
}

func (a *Activities) InspectActivityInfo(ctx context.Context, namespace, taskQueue, wfType string, isLocalActivity bool) error {
	a.append("inspectActivityInfo")
	if !activity.IsActivity(ctx) {
		return fmt.Errorf("expected InActivity to return %v but got %v", true, activity.IsActivity(ctx))
	}

	info := activity.GetInfo(ctx)
	if info.WorkflowNamespace != namespace {
		return fmt.Errorf("expected namespace %v but got %v", namespace, info.WorkflowNamespace)
	}
	if info.WorkflowType == nil || info.WorkflowType.Name != wfType {
		return fmt.Errorf("expected workflowType %v but got %v", wfType, info.WorkflowType)
	}
	if info.TaskQueue != taskQueue {
		return fmt.Errorf("expected taskQueue %v but got %v", taskQueue, info.TaskQueue)
	}
	if info.Deadline.IsZero() {
		return errors.New("expected non zero deadline")
	}
	if info.StartedTime.IsZero() {
		return errors.New("expected non zero started time")
	}
	if info.ScheduledTime.IsZero() {
		return errors.New("expected non zero scheduled time")
	}
	if info.IsLocalActivity != isLocalActivity {
		return fmt.Errorf("expected IsLocalActivity %v but got %v", isLocalActivity, info.IsLocalActivity)
	}
	return nil
}

func (a *Activities) DuplicateStringInContext(ctx context.Context) (string, error) {
	originalString := ctx.Value(contextKey(testContextKey1))
	if originalString == nil {
		return "", fmt.Errorf("context did not propagate to activity")
	}
	return strings.Repeat(originalString.(string), 2), nil
}

func (a *Activities) Echo(ctx context.Context, delayInSeconds int, value int) (int, error) {
	select {
	case <-time.After(time.Duration(delayInSeconds) * time.Second):
	case <-ctx.Done():
	}

	return value, nil
}

func (a *Activities) EchoString(ctx context.Context, message string) (string, error) {
	a.append("EchoString")
	return message, nil
}

func (a *Activities) WaitForWorkerStop(ctx context.Context, timeout time.Duration) (string, error) {
	stopCh := activity.GetWorkerStopChannel(ctx)
	// Mark activity as invoked then wait for it to be stopped
	a.append("wait-for-worker-stop")
	select {
	case <-stopCh:
		return "stopped", nil
	case <-time.After(timeout):
		return "timeout", nil
	}
}

func (a *Activities) WaitForManualStop(context.Context) error {
	if a.manualStopContext == nil {
		return fmt.Errorf("no manual context set")
	}
	<-a.manualStopContext.Done()
	return nil
}

func (a *Activities) HeartbeatUntilCanceled(ctx context.Context, heartbeatFreq time.Duration) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(heartbeatFreq):
			activity.RecordHeartbeat(ctx)
		}
	}
}

func (a *Activities) Panicked(ctx context.Context) ([]string, error) {
	panic(fmt.Sprintf("simulated panic on attempt %v", activity.GetInfo(ctx).Attempt))
}

func (a *Activities) append(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invocations = append(a.invocations, name)
}

func (a *Activities) invokedCount(name string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	var res = 0
	for i := range a.invocations {
		if a.invocations[i] == name {
			res++
		}
	}
	return res
}

func (a *Activities) invoked() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, len(a.invocations))
	copy(result, a.invocations)
	return result
}

func (a *Activities) clearInvoked() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invocations = []string{}
}

func (a *Activities2) ToUpper(_ context.Context, arg string) (string, error) {
	a.impl.append("toUpper")
	return strings.ToUpper(arg), nil
}

func (a *Activities2) ToUpperWithDelay(_ context.Context, arg string, delay time.Duration) (string, error) {
	a.impl.append("toUpperWithDelay")
	time.Sleep(delay)
	return strings.ToUpper(arg), nil
}

func (a *Activities) GetMemoAndSearchAttr(_ context.Context, memo, searchAttr string) (string, error) {
	a.append("getMemoAndSearchAttr")
	return memo + ", " + searchAttr, nil
}

func (a *Activities) AsyncComplete(ctx context.Context, input string) error {
	a.append("asyncComplete")
	return activity.ErrResultPending
}

func (a *Activities) PropagateActivity(ctx context.Context) ([]string, error) {
	var result []string

	if val1 := ctx.Value(contextKey(testContextKey1)); val1 != nil {
		if val1s, ok := val1.(string); ok {
			result = append(result, "activity_"+val1s)
		} else {
			return nil, fmt.Errorf("%s key is not propagated to activity", testContextKey1)
		}
	}

	if val2 := ctx.Value(contextKey(testContextKey2)); val2 != nil {
		if val2s, ok := val2.(string); ok {
			result = append(result, "activity_"+val2s)
		} else {
			return nil, fmt.Errorf("%s key is not propagated to activity", testContextKey2)
		}
	}

	return result, nil
}

func (a *Activities) InterceptorCalls(ctx context.Context, someVal string) (string, error) {
	someVal = "activity(" + someVal + ")"
	// Make some calls
	activity.GetInfo(ctx)
	activity.GetLogger(ctx)
	activity.GetMetricsHandler(ctx)
	activity.RecordHeartbeat(ctx, "details")
	activity.HasHeartbeatDetails(ctx)
	_ = activity.GetHeartbeatDetails(ctx)
	activity.GetWorkerStopChannel(ctx)
	return someVal, nil
}

func (a *Activities) ExternalSignalsAndQueries(ctx context.Context) error {
	// Signal with start
	workflowOpts := client.StartWorkflowOptions{TaskQueue: activity.GetInfo(ctx).TaskQueue}
	run, err := a.client.SignalWithStartWorkflow(ctx, "test-external-signals-and-queries", "start-signal",
		"signal-value", workflowOpts, new(Workflows).SignalsAndQueries, false, false)
	if err != nil {
		return err
	}

	// Query
	val, err := a.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "workflow-query", nil)
	if err != nil {
		return err
	}
	var queryResp string
	if err := val.Get(&queryResp); err != nil {
		return err
	} else if queryResp != "query-response" {
		return fmt.Errorf("bad query response")
	}

	// Finish signal
	if err := a.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish-signal", nil); err != nil {
		return err
	}
	return run.Get(ctx, nil)
}

func (a *Activities) CheckBaggage(ctx context.Context, key string) (string, error) {
	return baggage.FromContext(ctx).Member(key).Value(), nil
}

func (*Activities) TooFewParams(
	ctx context.Context,
	param1 string,
	param2 int,
	param3 bool,
	param4 struct{ SomeField string },
	param5 *ParamsValue,
	param6 []byte,
) (*ParamsValue, error) {
	return &ParamsValue{Param1: param1, Param2: param2, Param3: param3, Param4: param4, Param5: param5, Param6: param6}, nil
}

func (*Activities) ReturnCancelError(ctx context.Context, waitForCancel, goCancelError bool) error {
	// If waiting for cancel, heartbeat every 100ms until cancelled
	if waitForCancel {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}

	// Return canceled
	if goCancelError {
		return context.Canceled
	}
	return temporal.NewCanceledError("some details")
}

func (a *Activities) register(worker worker.Worker) {
	worker.RegisterActivity(a)
	// Check reregistration
	worker.RegisterActivityWithOptions(a.fail, activity.RegisterOptions{Name: "Fail", DisableAlreadyRegisteredCheck: true})
	worker.RegisterActivityWithOptions(a.failNTimes, activity.RegisterOptions{Name: "FailNTimes", DisableAlreadyRegisteredCheck: true})
	// Check prefix
	worker.RegisterActivityWithOptions(a.activities2, activity.RegisterOptions{Name: "Prefix_", DisableAlreadyRegisteredCheck: true})
	worker.RegisterActivityWithOptions(a.InspectActivityInfo, activity.RegisterOptions{Name: "inspectActivityInfo"})
}
