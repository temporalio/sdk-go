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

package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

type WorkflowUnitTest struct {
	suite.Suite
	WorkflowTestSuite
	activityOptions ActivityOptions
}

func (s *WorkflowUnitTest) SetupSuite() {
	s.activityOptions = ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
}

func TestWorkflowUnitTest(t *testing.T) {
	suite.Run(t, new(WorkflowUnitTest))
}

func worldWorkflow(_ Context, input string) (result string, err error) {
	return input + " World!", nil
}

func (s *WorkflowUnitTest) Test_WorldWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(worldWorkflow, "Hello")
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowUnitTest) Test_WorkflowWithLocalActivityDefaultRetryPolicy() {
	env := s.NewTestWorkflowEnvironment()
	laOpts := LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	}
	env.ExecuteWorkflow(workflowWithFailingLocalActivity, laOpts, 2)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowUnitTest) Test_WorkflowWithLocalActivityWithMaxAttempts() {
	env := s.NewTestWorkflowEnvironment()
	laOpts := LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy: &RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	env.ExecuteWorkflow(workflowWithFailingLocalActivity, laOpts, 2)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowUnitTest) Test_WorkflowWithLocalActivityWithMaxAttemptsExceeded() {
	env := s.NewTestWorkflowEnvironment()
	laOpts := LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy: &RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	env.ExecuteWorkflow(workflowWithFailingLocalActivity, laOpts, 5)
	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
}

func workflowWithFailingLocalActivity(ctx Context, laOpts LocalActivityOptions, laTimesToFail int) error {
	ctx = WithLocalActivityOptions(ctx, laOpts)
	activity := &FailNTimesAct{timesToFail: laTimesToFail}
	f := ExecuteLocalActivity(ctx, activity.run)
	err := f.Get(ctx, nil)
	if err != nil {
		return err
	}
	return nil
}

type FailNTimesAct struct {
	timesExecuted int
	timesToFail   int
}

func (a *FailNTimesAct) run(_ context.Context) error {
	a.timesExecuted++
	if a.timesExecuted <= a.timesToFail {
		return fmt.Errorf("simulated activity failure on attempt %v", a.timesExecuted)
	}
	return nil
}

func helloWorldAct(ctx context.Context) (string, error) {
	s := ctx.Value(unitTestKey).(*WorkflowUnitTest)
	info := GetActivityInfo(ctx)
	s.Equal(taskqueue, info.TaskQueue)
	s.Equal(2*time.Second, info.HeartbeatTimeout)
	return "test", nil
}

func helloWorldActivityWorkflow(ctx Context, _ string) (result string, err error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
		HeartbeatTimeout:       2 * time.Second,
		ActivityID:             "id1",
		TaskQueue:              taskqueue,
	}
	ctx1 := WithActivityOptions(ctx, ao)
	f := ExecuteActivity(ctx1, helloWorldAct)
	var r1 string
	err = f.Get(ctx, &r1)
	if err != nil {
		return "", err
	}
	return r1, nil
}

type key int

const unitTestKey key = 1

func (s *WorkflowUnitTest) Test_SingleActivityWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	ctx := context.WithValue(context.Background(), unitTestKey, s)
	env.SetWorkerOptions(WorkerOptions{BackgroundActivityContext: ctx})
	env.RegisterActivity(helloWorldAct)
	env.ExecuteWorkflow(helloWorldActivityWorkflow, "Hello")
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func splitJoinActivityWorkflow(ctx Context, testPanic bool) (result string, err error) {
	var result1, result2 string
	var err1 error

	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	c1 := NewChannel(ctx)
	c2 := NewChannel(ctx)
	Go(ctx, func(ctx Context) {
		ao.ActivityID = "id1"
		ctx1 := WithActivityOptions(ctx, ao)
		f := ExecuteActivity(ctx1, testAct)
		err1 = f.Get(ctx, &result1)
		if err1 == nil {
			c1.Send(ctx, true)
		}
	})
	Go(ctx, func(ctx Context) {
		ao.ActivityID = "id2"
		ctx2 := WithActivityOptions(ctx, ao)
		f := ExecuteActivity(ctx2, testAct)
		err1 := f.Get(ctx, &result2)
		if testPanic {
			panic("simulated")
		}
		if err1 == nil {
			c2.Send(ctx, true)
		}
	})

	c1.Receive(ctx, nil)
	// Use selector to test it
	selected := false
	NewSelector(ctx).AddReceive(c2, func(c ReceiveChannel, more bool) {
		if !more {
			panic("more should be true")
		}
		selected = true
	}).Select(ctx)
	if !selected {
		return "", errors.New("selector does not work")
	}
	if err1 != nil {
		return "", err1
	}

	return result1 + result2, nil
}

func returnPanicWorkflow(_ Context) (err error) {
	return newPanicError("panicError", "stackTrace")
}

func (s *WorkflowUnitTest) Test_SplitJoinActivityWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(testAct, RegisterActivityOptions{Name: "testActivityWithOptions"})
	env.OnActivity(testAct, mock.Anything).Return(func(ctx context.Context) (string, error) {
		activityID := GetActivityInfo(ctx).ActivityID
		switch activityID {
		case "id1":
			return "Hello", nil
		case "id2":
			return " Flow!", nil
		default:
			panic(fmt.Sprintf("Unexpected activityID: %v", activityID))
		}
	}).Twice()
	tracer := tracingWorkerInterceptor{}
	env.SetWorkerOptions(WorkerOptions{Interceptors: []WorkerInterceptor{&tracer}})
	env.ExecuteWorkflow(splitJoinActivityWorkflow, false)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("Hello Flow!", result)
	s.Equal(1, len(tracer.instances))
	trace := tracer.instances[len(tracer.instances)-1].trace
	s.Equal([]string{
		"ExecuteWorkflow splitJoinActivityWorkflow begin",
		"ExecuteActivity testActivityWithOptions",
		"ExecuteActivity testActivityWithOptions",
		"ExecuteWorkflow splitJoinActivityWorkflow end",
	}, trace)
}

func TestWorkflowPanic(t *testing.T) {
	ts := &WorkflowTestSuite{}
	ts.SetLogger(ilog.NewNopLogger()) // this test simulate panic, use nop logger to avoid logging noise
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterActivity(testAct)
	env.ExecuteWorkflow(splitJoinActivityWorkflow, true)
	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	var workflowErr *WorkflowExecutionError
	require.True(t, errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var resultErr *PanicError
	require.True(t, errors.As(err, &resultErr))

	require.EqualValues(t, "simulated", resultErr.Error())
	require.Contains(t, resultErr.StackTrace(), "go.temporal.io/sdk/internal.splitJoinActivityWorkflow")
}

func TestWorkflowReturnsPanic(t *testing.T) {
	ts := &WorkflowTestSuite{}
	ts.SetLogger(ilog.NewNopLogger()) // this test simulate panic, use nop logger to avoid logging noise
	env := ts.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(returnPanicWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	var workflowErr *WorkflowExecutionError
	require.True(t, errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var resultErr *PanicError
	require.True(t, errors.As(err, &resultErr))
	require.EqualValues(t, "panicError", resultErr.Error())
	require.EqualValues(t, "stackTrace", resultErr.StackTrace())
}

func testClockWorkflow(ctx Context) (time.Time, error) {
	c := Now(ctx)
	return c, nil
}

func (s *WorkflowUnitTest) Test_ClockWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(testClockWorkflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var nowTime time.Time
	_ = env.GetWorkflowResult(&nowTime)
	s.False(nowTime.IsZero())
}

type testTimerWorkflow struct {
	t *testing.T
}

func (w *testTimerWorkflow) Execute(ctx Context, _ []byte) (result []byte, err error) {
	// Start a timer.
	t := NewTimer(ctx, 1)

	isWokeByTimer := false

	NewSelector(ctx).AddFuture(t, func(f Future) {
		err := f.Get(ctx, nil)
		require.NoError(w.t, err)
		isWokeByTimer = true
	}).Select(ctx)

	require.True(w.t, isWokeByTimer)

	// Start a timer and cancel it.
	ctx2, c2 := WithCancel(ctx)
	t2 := NewTimer(ctx2, 1)
	c2()
	err2 := t2.Get(ctx2, nil)

	require.Error(w.t, err2)
	_, isCancelErr := err2.(*CanceledError)
	require.True(w.t, isCancelErr)

	// Sleep 1 sec
	ctx3, _ := WithCancel(ctx)
	err3 := Sleep(ctx3, 1)
	require.NoError(w.t, err3)

	// Sleep and cancel.
	ctx4, c4 := WithCancel(ctx)
	c4()
	err4 := Sleep(ctx4, 1)

	require.Error(w.t, err4)
	_, isCancelErr = err4.(*CanceledError)
	require.True(w.t, isCancelErr)

	return []byte("workflow-completed"), nil
}

func TestTimerWorkflow(t *testing.T) {
	ts := &WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	w := &testTimerWorkflow{t: t}
	env.RegisterWorkflow(w.Execute)
	env.ExecuteWorkflow(w.Execute, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

type testActivityCancelWorkflow struct {
	t *testing.T
}

func testAct(_ context.Context) (string, error) {
	return "test", nil
}

func (w *testActivityCancelWorkflow) Execute(ctx Context, _ []byte) (result []byte, err error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	// Sync cancellation
	ctx1, c1 := WithCancel(ctx)
	defer c1()

	ao.ActivityID = "id1"
	ctx1 = WithActivityOptions(ctx1, ao)
	f := ExecuteActivity(ctx1, testAct)
	var res1 string
	err1 := f.Get(ctx, &res1)
	require.NoError(w.t, err1, err1)
	require.Equal(w.t, res1, "test")

	// Async Cancellation (Callback completes before cancel)
	ctx2, c2 := WithCancel(ctx)
	ao.ActivityID = "id2"
	ctx2 = WithActivityOptions(ctx2, ao)
	f = ExecuteActivity(ctx2, testAct)
	c2()
	var res2 string
	err2 := f.Get(ctx, &res2)
	require.NotNil(w.t, err2)
	_, ok := err2.(*CanceledError)
	require.True(w.t, ok)

	return []byte("workflow-completed"), nil
}

func TestActivityCancellation(t *testing.T) {
	ts := &WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterActivity(testAct)
	w := &testActivityCancelWorkflow{t: t}
	env.RegisterWorkflow(w.Execute)
	env.ExecuteWorkflow(w.Execute, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

type sayGreetingActivityRequest struct {
	Name     string
	Greeting string
}

func getGreetingActivity() (string, error) {
	return "Hello", nil
}
func getNameActivity() (string, error) {
	return "temporal", nil
}
func sayGreetingActivity(input *sayGreetingActivityRequest) (string, error) {
	return fmt.Sprintf("%v %v!", input.Greeting, input.Name), nil
}

// Greetings Workflow.
func greetingsWorkflow(ctx Context) (result string, err error) {
	// Get Greeting.
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx1 := WithActivityOptions(ctx, ao)

	f := ExecuteActivity(ctx1, getGreetingActivity)
	var greetResult string
	err = f.Get(ctx, &greetResult)
	if err != nil {
		return "", err
	}

	// Get Name.
	f = ExecuteActivity(ctx1, getNameActivity)
	var nameResult string
	err = f.Get(ctx, &nameResult)
	if err != nil {
		return "", err
	}

	// Say Greeting.
	request := &sayGreetingActivityRequest{Name: nameResult, Greeting: greetResult}
	err = ExecuteActivity(ctx1, sayGreetingActivity, request).Get(ctx, &result)
	if err != nil {
		return "", err
	}
	return result, nil
}

func (s *WorkflowUnitTest) Test_ExternalExampleWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(getGreetingActivity)
	env.RegisterActivity(getNameActivity)
	env.RegisterActivity(sayGreetingActivity)

	env.ExecuteWorkflow(greetingsWorkflow)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("Hello temporal!", result)
}

func continueAsNewWorkflowTest(ctx Context) error {
	return NewContinueAsNewError(ctx, "continueAsNewWorkflowTest", []byte("start"))
}

func (s *WorkflowUnitTest) Test_ContinueAsNewWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(StartWorkflowOptions{
		WorkflowExecutionTimeout: 100 * time.Second,
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowRunTimeout:       50 * time.Second,
	})
	env.ExecuteWorkflow(continueAsNewWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	var workflowErr *WorkflowExecutionError
	s.True(errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var resultErr *ContinueAsNewError
	s.True(errors.As(err, &resultErr))
	s.EqualValues("continueAsNewWorkflowTest", resultErr.WorkflowType.Name)
	s.EqualValues(100*time.Second, resultErr.WorkflowExecutionTimeout)
	s.EqualValues(50*time.Second, resultErr.WorkflowRunTimeout)
	s.EqualValues(5*time.Second, resultErr.WorkflowTaskTimeout)
	s.EqualValues("default-test-taskqueue", resultErr.TaskQueueName)
}

func cancelWorkflowTest(ctx Context) (string, error) {
	if ctx.Done().Receive(ctx, nil); ctx.Err() == ErrCanceled {
		return "Canceled.", ctx.Err()
	}
	return "Completed.", nil
}

func (s *WorkflowUnitTest) Test_CancelWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Hour)
	env.ExecuteWorkflow(cancelWorkflowTest)
	s.True(env.IsWorkflowCompleted(), "Workflow failed to complete")
}

func cancelWorkflowAfterActivityTest(ctx Context) ([]byte, error) {
	// The workflow cancellation should handle activity and timer cancellation
	// not to propagate those commands.

	// schedule an activity.
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	err := ExecuteActivity(ctx, testAct).Get(ctx, nil)
	if err != nil {
		return nil, err
	}

	// schedule a timer
	err2 := Sleep(ctx, 1)
	if err2 != nil {
		return nil, err2
	}

	if ctx.Done().Receive(ctx, nil); ctx.Err() == ErrCanceled {
		return []byte("Canceled."), ctx.Err()
	}
	return []byte("Completed."), nil
}

func (s *WorkflowUnitTest) Test_CancelWorkflowAfterActivity() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Hour)
	env.ExecuteWorkflow(cancelWorkflowAfterActivityTest)
	s.True(env.IsWorkflowCompleted())
}

func cancelledWorkflowStartingChildWorkflow(ctx Context) (bool, error) {
	// schedule a timer, which will be cancelled
	_ = Sleep(ctx, 5*time.Minute)

	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})
	childErr := ExecuteChildWorkflow(ctx, sleepWorkflow, time.Second).Get(ctx, nil)
	if childErr != nil {
		return false, childErr
	}

	return true, nil
}

func (s *WorkflowUnitTest) Test_CancelledWorkflowCantStartChild() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(sleepWorkflow)
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Minute)
	env.ExecuteWorkflow(cancelledWorkflowStartingChildWorkflow)
	err := env.GetWorkflowError()
	s.Error(err)
	var cancelErr *CanceledError
	s.True(errors.As(err, &cancelErr))
}

func signalWorkflowTest(ctx Context) ([]byte, error) {
	// read multiple times.
	var result string
	ch := GetSignalChannel(ctx, "testSig1")
	var v string
	ok := ch.ReceiveAsync(&v)
	if !ok {
		return nil, errors.New("testSig1 not received")
	}
	result += v
	ch.Receive(ctx, &v)
	result += v

	// Read on a selector.
	ch2 := GetSignalChannel(ctx, "testSig2")
	s := NewSelector(ctx)
	s.AddReceive(ch2, func(c ReceiveChannel, more bool) {
		c.Receive(ctx, &v)
		result += v
	})
	s.Select(ctx)
	s.Select(ctx)
	s.Select(ctx)

	// Read on a selector inside the callback, multiple times.
	ch2 = GetSignalChannel(ctx, "testSig2")
	s = NewSelector(ctx)
	s.AddReceive(ch2, func(c ReceiveChannel, more bool) {
		for i := 0; i < 4; i++ {
			c.Receive(ctx, &v)
			result += v
		}
	})
	s.Select(ctx)

	// Check un handled signals.
	list := getWorkflowEnvOptions(ctx).getUnhandledSignals()
	if len(list) != 1 || list[0] != "testSig3" {
		panic("expecting one unhandled signal")
	}
	ch3 := GetSignalChannel(ctx, "testSig3")
	ch3.Receive(ctx, &v)
	result += v
	list = getWorkflowEnvOptions(ctx).getUnhandledSignals()
	if len(list) != 0 {
		panic("expecting no unhandled signals")
	}
	return []byte(result), nil
}

func (s *WorkflowUnitTest) Test_SignalWorkflow() {
	expected := []string{
		"Sig1Value1;",
		"Sig1Value2;",
		"Sig2Value1;",
		"Sig2Value2;",
		"Sig2Value3;",
		"Sig2Value4;",
		"Sig2Value5;",
		"Sig2Value6;",
		"Sig2Value7;",
		"Sig3Value1;",
	}
	env := s.NewTestWorkflowEnvironment()

	// Setup signals.
	for i := 0; i < 2; i++ {
		msg := expected[i]
		var delay time.Duration
		if i > 0 {
			delay = time.Second
		}
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow("testSig1", msg)
		}, delay)
	}
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("testSig3", expected[9])
	}, time.Hour)
	for i := 2; i < 9; i++ {
		msg := expected[i]
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow("testSig2", msg)
		}, time.Hour)
	}

	env.ExecuteWorkflow(signalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result []byte
	_ = env.GetWorkflowResult(&result)
	s.EqualValues(strings.Join(expected, ""), string(result))
}

type message struct {
	Value string
}

func receiveCorruptSignalWorkflowTest(ctx Context) ([]message, error) {
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage")
	var result []message
	var m message
	ch.Receive(ctx, &m)
	result = append(result, m)
	return result, nil
}

func receiveCorruptSignalOnClosedChannelWorkflowTest(ctx Context) ([]message, error) {
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage")
	var result []message
	var m message
	ch.(Channel).Close()
	more := ch.Receive(ctx, &m)

	result = append(result, message{Value: fmt.Sprintf("%v", more)})
	return result, nil
}

func receiveWithSelectorCorruptSignalWorkflowTest(ctx Context) ([]message, error) {
	var result []message

	// Read on a selector
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage")
	s := NewSelector(ctx)
	s.AddReceive(ch, func(c ReceiveChannel, more bool) {
		var m message
		ch.Receive(ctx, &m)
		result = append(result, m)
	})
	s.Select(ctx)
	return result, nil
}

func receiveAsyncCorruptSignalOnClosedChannelWorkflowTest(ctx Context) ([]int, error) {
	ch := GetSignalChannel(ctx, "channelExpectingInt").(Channel)
	var result []int
	var m int

	ch.SendAsync("wrong")
	ch.Close()
	ok := ch.ReceiveAsync(&m)
	if ok == true {
		result = append(result, m)
	}

	return result, nil
}

func receiveAsyncCorruptSignalWorkflowTest(ctx Context) ([]message, error) {
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage").(Channel)
	var result []message
	var m message

	ch.SendAsync("wrong")
	ok := ch.ReceiveAsync(&m)
	if ok == true {
		result = append(result, m)
	}

	ch.SendAsync("wrong again")
	ch.SendAsync(message{
		Value: "the right interface",
	})
	ok = ch.ReceiveAsync(&m)
	if ok == true {
		result = append(result, m)
	}
	return result, nil
}

func (s *WorkflowUnitTest) Test_CorruptedSignalWorkflow_ShouldLogMetricsAndNotPanic() {
	metricsHandler := metrics.NewCapturingHandler()
	s.SetMetricsHandler(metricsHandler)
	env := s.NewTestWorkflowEnvironment()

	// Setup signals.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", "wrong")
	}, time.Millisecond)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", message{
			Value: "the right interface",
		})
	}, time.Second)

	env.ExecuteWorkflow(receiveCorruptSignalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	_ = env.GetWorkflowResult(&result)

	s.EqualValues(1, len(result))
	s.EqualValues("the right interface", result[0].Value)

	counters := metricsHandler.Counters()
	s.EqualValues(1, len(counters))
	s.EqualValues(metrics.CorruptedSignalsCounter, counters[0].Name)
	s.EqualValues(1, counters[0].Value())
}

func (s *WorkflowUnitTest) Test_CorruptedSignalWorkflow_OnSelectorRead_ShouldLogMetricsAndNotPanic() {
	metricsHandler := metrics.NewCapturingHandler()
	s.SetMetricsHandler(metricsHandler)
	env := s.NewTestWorkflowEnvironment()

	// Setup signals.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", "wrong")
	}, time.Second)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", message{
			Value: "the right interface",
		})
	}, 3*time.Second)

	env.ExecuteWorkflow(receiveWithSelectorCorruptSignalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	_ = env.GetWorkflowResult(&result)

	s.EqualValues(1, len(result))
	s.EqualValues("the right interface", result[0].Value)

	counters := metricsHandler.Counters()
	s.EqualValues(1, len(counters))
	s.EqualValues(metrics.CorruptedSignalsCounter, counters[0].Name)
	s.EqualValues(1, counters[0].Value())
}

func (s *WorkflowUnitTest) Test_CorruptedSignalWorkflow_ReceiveAsync_ShouldLogMetricsAndNotPanic() {
	metricsHandler := metrics.NewCapturingHandler()
	s.SetMetricsHandler(metricsHandler)
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(receiveAsyncCorruptSignalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	_ = env.GetWorkflowResult(&result)
	s.EqualValues(1, len(result))
	s.EqualValues("the right interface", result[0].Value)

	counters := metricsHandler.Counters()
	s.EqualValues(1, len(counters))
	s.EqualValues(metrics.CorruptedSignalsCounter, counters[0].Name)
	s.EqualValues(2, counters[0].Value())
}

func (s *WorkflowUnitTest) Test_CorruptedSignalOnClosedChannelWorkflow_ReceiveAsync_ShouldComplete() {
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(receiveAsyncCorruptSignalOnClosedChannelWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	_ = env.GetWorkflowResult(&result)
	s.EqualValues(0, len(result))
}

func (s *WorkflowUnitTest) Test_CorruptedSignalOnClosedChannelWorkflow_Receive_ShouldComplete() {
	env := s.NewTestWorkflowEnvironment()

	// Setup signals.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", "wrong")
	}, time.Second)

	env.ExecuteWorkflow(receiveCorruptSignalOnClosedChannelWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	_ = env.GetWorkflowResult(&result)
	s.EqualValues(1, len(result))
	s.Equal("false", result[0].Value)
}

func closeChannelTest(ctx Context) error {
	ch := NewChannel(ctx)
	Go(ctx, func(ctx Context) {
		var dummy struct{}
		ch.Receive(ctx, &dummy)
		ch.Close()
	})

	ch.Send(ctx, struct{}{})
	return nil
}

func (s *WorkflowUnitTest) Test_CloseChannelWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(closeChannelTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func closeChannelInSelectTest(ctx Context) error {
	s := NewSelector(ctx)
	sendCh := NewChannel(ctx)
	receiveCh := NewChannel(ctx)
	expectedValue := "expected value"

	Go(ctx, func(ctx Context) {
		sendCh.Close()
		receiveCh.Send(ctx, expectedValue)
	})

	var v string
	s.AddSend(sendCh, struct{}{}, func() {
		panic("callback for sendCh should not be executed")
	})
	s.AddReceive(receiveCh, func(c ReceiveChannel, m bool) {
		c.Receive(ctx, &v)
	})
	s.Select(ctx)
	if v != expectedValue {
		panic("callback for receiveCh is not executed")
	}
	return nil
}

func (s *WorkflowUnitTest) Test_CloseChannelInSelectWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(closeChannelInSelectTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

var selectDoesntLoopForeverCounter uint64

func (s *WorkflowUnitTest) selectDoesntLoopForever(ctx Context) error {
	cancelCtx, cancelFunc := WithCancel(ctx)
	selector := NewSelector(ctx)

	cancelFunc()
	selector.AddReceive(cancelCtx.Done(), func(c ReceiveChannel, more bool) {
		res := c.Receive(ctx, nil)
		s.Assert().Equal(false, res)
		s.Assert().Equal(false, more)
	})

	for {
		selector.Select(ctx)
		atomic.AddUint64(&selectDoesntLoopForeverCounter, 1)
	}
}

func (s *WorkflowUnitTest) Test_SelectDoesntLoopForever() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(s.selectDoesntLoopForever)
	s.True(env.IsWorkflowCompleted())
	// Hits workflow timeout, since it blocked "forever"
	s.Error(env.GetWorkflowError())
	// The loop over the select should have blocked
	s.Equal(uint64(1), selectDoesntLoopForeverCounter)
}

func bufferedChanWorkflowTest(ctx Context, bufferSize int) error {
	bufferedCh := NewBufferedChannel(ctx, bufferSize)

	Go(ctx, func(ctx Context) {
		var dummy int
		for i := 0; i < bufferSize; i++ {
			bufferedCh.Receive(ctx, &dummy)
		}
	})

	for i := 0; i < bufferSize+1; i++ {
		bufferedCh.Send(ctx, i)
	}
	return nil
}

func (s *WorkflowUnitTest) Test_BufferedChanWorkflow() {
	bufferSizeList := []int{1, 5}
	for _, bufferSize := range bufferSizeList {
		env := s.NewTestWorkflowEnvironment()
		env.ExecuteWorkflow(bufferedChanWorkflowTest, bufferSize)
		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())
	}
}

func bufferedChanWithSelectorWorkflowTest(ctx Context, bufferSize int) error {
	bufferedCh := NewBufferedChannel(ctx, bufferSize)
	selectedCh := NewChannel(ctx)
	done := NewChannel(ctx)
	var dummy struct{}

	// 1. First we need to fill the buffer
	for i := 0; i < bufferSize; i++ {
		bufferedCh.Send(ctx, dummy)
	}

	// DO NOT change the order of these coroutines.
	Go(ctx, func(ctx Context) {
		// 3. Add another send callback to bufferedCh's blockedSends.
		bufferedCh.Send(ctx, dummy)
		done.Send(ctx, dummy)
	})

	Go(ctx, func(ctx Context) {
		// 4.  Make sure selectedCh is selected
		selectedCh.Receive(ctx, nil)

		// 5. Get a value from channel buffer. Receive call will also check if there's any blocked sends.
		// The first blockedSends is added by Select(). Since bufferedCh is not selected, it's fn() will
		// return false. The Receive call should continue to check other blockedSends, until fn() returns
		// true or the list is empty. In this case, it will move the value sent in step 3 into buffer
		// and thus unblocks it.
		bufferedCh.Receive(ctx, nil)
	})

	selector := NewSelector(ctx)
	selector.AddSend(selectedCh, dummy, func() {})
	selector.AddSend(bufferedCh, dummy, func() {})
	// 2. When select is called, callback for the second send will be added to bufferedCh's blockedSends
	if selector.HasPending() {
		panic("HasPending should be true")
	}
	selector.Select(ctx)
	if selector.HasPending() {
		panic("HasPending should be true")
	}

	// Make sure no coroutine blocks
	done.Receive(ctx, nil)
	return nil
}

func (s *WorkflowUnitTest) Test_BufferedChanWithSelectorWorkflow() {
	bufferSizeList := []int{1, 5}
	for _, bufferSize := range bufferSizeList {
		env := s.NewTestWorkflowEnvironment()
		env.ExecuteWorkflow(bufferedChanWithSelectorWorkflowTest, bufferSize)
		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())
	}
}

func activityOptionsWorkflow(ctx Context) (result string, err error) {
	ao1 := ActivityOptions{
		ActivityID: "id1",
	}
	ao2 := ActivityOptions{
		ActivityID: "id2",
	}
	ctx1 := WithActivityOptions(ctx, ao1)
	ctx2 := WithActivityOptions(ctx, ao2)

	ctx1Ao := getActivityOptions(ctx1)
	ctx2Ao := getActivityOptions(ctx2)
	return ctx1Ao.ActivityID + " " + ctx2Ao.ActivityID, nil
}

// Test that activity options are correctly spawned with WithActivityOptions is called.
// See https://github.com/temporalio/go-sdk/issues/49
func (s *WorkflowUnitTest) Test_ActivityOptionsWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(activityOptionsWorkflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("id1 id2", result)
}

const (
	memoTestKey = "testKey"
	memoTestVal = "testVal"
)

func getMemoTest(ctx Context) (result string, err error) {
	info := GetWorkflowInfo(ctx)
	val, ok := info.Memo.Fields[memoTestKey]
	if !ok {
		return "", errors.New("no memo found")
	}
	err = converter.GetDefaultDataConverter().FromPayload(val, &result)
	return result, err
}

func (s *WorkflowUnitTest) Test_MemoWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	memo := map[string]interface{}{
		memoTestKey: memoTestVal,
	}
	err := env.SetMemoOnStart(memo)
	s.NoError(err)

	env.ExecuteWorkflow(getMemoTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal(memoTestVal, result)
}

func sleepWorkflow(ctx Context, input time.Duration) (int, error) {
	if err := Sleep(ctx, input); err != nil {
		return 0, err
	}

	return 1, nil
}

func waitGroupWorkflowTest(ctx Context, n int) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})

	var err error
	results := make([]int, 0, n)
	waitGroup := NewWaitGroup(ctx)
	for i := 0; i < n; i++ {
		waitGroup.Add(1)
		t := time.Second * time.Duration(i+1)
		Go(ctx, func(ctx Context) {
			var result int
			err = ExecuteChildWorkflow(ctx, sleepWorkflow, t).Get(ctx, &result)
			results = append(results, result)
			waitGroup.Done()
		})
	}

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	sum := 0
	for _, v := range results {
		sum = sum + v
	}

	return sum, nil
}

func waitGroupWaitForMWorkflowTest(ctx Context, n int, m int) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})

	var err error
	results := make([]int, 0, n)
	waitGroup := NewWaitGroup(ctx)
	waitGroup.Add(m)
	for i := 0; i < n; i++ {
		t := time.Second * time.Duration(i+1)
		Go(ctx, func(ctx Context) {
			var result int
			err = ExecuteChildWorkflow(ctx, sleepWorkflow, t).Get(ctx, &result)
			results = append(results, result)
			waitGroup.Done()
		})
	}

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	sum := 0
	for _, v := range results {
		sum = sum + v
	}

	return sum, nil
}

func waitGroupMultipleWaitsWorkflowTest(ctx Context) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})

	n := 10
	var err error
	results := make([]int, 0, n)
	waitGroup := NewWaitGroup(ctx)
	waitGroup.Add(4)
	for i := 0; i < n; i++ {
		t := time.Second * time.Duration(i+1)
		Go(ctx, func(ctx Context) {
			var result int
			err = ExecuteChildWorkflow(ctx, sleepWorkflow, t).Get(ctx, &result)
			results = append(results, result)
			waitGroup.Done()
		})
	}

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	waitGroup.Add(6)
	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	sum := 0
	for _, v := range results {
		sum = sum + v
	}

	return sum, nil
}

func waitGroupMultipleConcurrentWaitsPanicsWorkflowTest(ctx Context) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})

	var err error
	var result1 int
	var result2 int

	waitGroup := NewWaitGroup(ctx)
	waitGroup.Add(2)

	Go(ctx, func(ctx Context) {
		err = ExecuteChildWorkflow(ctx, sleepWorkflow, time.Second*5).Get(ctx, &result1)
		waitGroup.Done()
	})

	Go(ctx, func(ctx Context) {
		err = ExecuteChildWorkflow(ctx, sleepWorkflow, time.Second*10).Get(ctx, &result2)
		waitGroup.Wait(ctx)
	})

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	return result1 + result2, nil
}

func waitGroupNegativeCounterPanicsWorkflowTest(ctx Context) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})

	var err error
	var result int
	waitGroup := NewWaitGroup(ctx)

	Go(ctx, func(ctx Context) {
		waitGroup.Done()
		err = ExecuteChildWorkflow(ctx, sleepWorkflow, time.Second*5).Get(ctx, &result)
	})

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	return result, nil
}

func (s *WorkflowUnitTest) Test_waitGroupNegativeCounterPanicsWorkflowTest() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitGroupNegativeCounterPanicsWorkflowTest)
	env.ExecuteWorkflow(waitGroupNegativeCounterPanicsWorkflowTest)
	s.True(env.IsWorkflowCompleted())

	err := env.GetWorkflowError()
	s.Error(err)
	var workflowErr *WorkflowExecutionError
	s.True(errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var resultErr *PanicError
	s.True(errors.As(err, &resultErr))

	s.EqualValues("negative WaitGroup counter", resultErr.Error())
	s.Contains(resultErr.StackTrace(), "go.temporal.io/sdk/internal.waitGroupNegativeCounterPanicsWorkflowTest")
}

func (s *WorkflowUnitTest) Test_WaitGroupMultipleConcurrentWaitsPanicsWorkflowTest() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitGroupMultipleConcurrentWaitsPanicsWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)
	env.ExecuteWorkflow(waitGroupMultipleConcurrentWaitsPanicsWorkflowTest)
	s.True(env.IsWorkflowCompleted())

	err := env.GetWorkflowError()
	s.Error(err)
	var workflowErr *WorkflowExecutionError
	s.True(errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var resultErr *PanicError
	s.True(errors.As(err, &resultErr))

	s.EqualValues("WaitGroup is reused before previous Wait has returned", resultErr.Error())
	s.Contains(resultErr.StackTrace(), "go.temporal.io/sdk/internal.waitGroupMultipleConcurrentWaitsPanicsWorkflowTest")
}

func (s *WorkflowUnitTest) Test_WaitGroupMultipleWaitsWorkflowTest() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitGroupMultipleWaitsWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)
	env.ExecuteWorkflow(waitGroupMultipleWaitsWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var total int
	_ = env.GetWorkflowResult(&total)
	s.Equal(10, total)
}

func (s *WorkflowUnitTest) Test_WaitGroupWaitForMWorkflowTest() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitGroupWaitForMWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)

	n := 10
	m := 5
	env.ExecuteWorkflow(waitGroupWaitForMWorkflowTest, n, m)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var total int
	_ = env.GetWorkflowResult(&total)
	s.Equal(m, total)
}

func (s *WorkflowUnitTest) Test_WaitGroupWorkflowTest() {
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitGroupWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)

	n := 10
	env.ExecuteWorkflow(waitGroupWorkflowTest, n)
	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
	s.NoError(env.GetWorkflowError())

	var total int
	_ = env.GetWorkflowResult(&total)
	s.Equal(n, total)
}

func (s *WorkflowUnitTest) Test_StaleGoroutinesAreShutDown() {
	env := s.NewTestWorkflowEnvironment()
	deferred := make(chan struct{})
	after := make(chan struct{})
	wf := func(ctx Context) error {
		Go(ctx, func(ctx Context) {
			defer func() { close(deferred) }()
			_ = Sleep(ctx, time.Hour) // outlive the workflow
			close(after)
		})
		_ = Sleep(ctx, time.Minute)
		return nil
	}
	env.RegisterWorkflow(wf)

	env.ExecuteWorkflow(wf)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	// goroutines are shut down async at the moment, so wait with a timeout.
	// give it up to 1s total.

	started := time.Now()
	maxWait := time.NewTimer(time.Second)
	defer maxWait.Stop()
	select {
	case <-deferred:
		s.T().Logf("deferred callback executed after %v", time.Since(started))
	case <-maxWait.C:
		s.Fail("deferred func should have been called within 1 second")
	}
	// if deferred code has run, this has already occurred-or-not.
	// if it timed out waiting for the deferred code, it has waited long enough, and this is mostly a curiosity.
	select {
	case <-after:
		s.Fail("code after sleep should not have run")
	default:
		s.T().Log("code after sleep correctly not executed")
	}
}

var _ WorkerInterceptor = (*tracingWorkerInterceptor)(nil)
var _ WorkflowOutboundInterceptor = (*tracingWorkflowOutboundInterceptor)(nil)

type (
	tracingWorkerInterceptor struct {
		WorkerInterceptorBase
		instances []*tracingWorkflowInboundInterceptor
	}

	tracingWorkflowInboundInterceptor struct {
		WorkflowInboundInterceptorBase
		trace []string
	}

	tracingWorkflowOutboundInterceptor struct {
		WorkflowOutboundInterceptorBase
		inbound *tracingWorkflowInboundInterceptor
	}
)

func (t *tracingWorkerInterceptor) InterceptWorkflow(ctx Context, next WorkflowInboundInterceptor) WorkflowInboundInterceptor {
	var result tracingWorkflowInboundInterceptor
	result.Next = next
	t.instances = append(t.instances, &result)
	return &result
}

func (t *tracingWorkflowInboundInterceptor) Init(outbound WorkflowOutboundInterceptor) error {
	return t.Next.Init(&tracingWorkflowOutboundInterceptor{
		WorkflowOutboundInterceptorBase{Next: outbound}, t})
}

func (t *tracingWorkflowInboundInterceptor) ExecuteWorkflow(ctx Context, in *ExecuteWorkflowInput) (interface{}, error) {
	t.trace = append(t.trace, "ExecuteWorkflow "+GetWorkflowInfo(ctx).WorkflowType.Name+" begin")
	result, err := t.Next.ExecuteWorkflow(ctx, in)
	t.trace = append(t.trace, "ExecuteWorkflow "+GetWorkflowInfo(ctx).WorkflowType.Name+" end")
	return result, err
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteActivity(ctx Context, activityType string, args ...interface{}) Future {
	t.inbound.trace = append(t.inbound.trace, "ExecuteActivity "+activityType)
	return t.Next.ExecuteActivity(ctx, activityType, args...)
}

func TestStackTraceInvalidDepthBounded(t *testing.T) {
	// Confirm at 2 depth there are 3 lines (1 for header, 2 for fn and path)
	lines := strings.Split(getStackTrace("mycoroutine", "success", 2), "\n")
	require.Len(t, lines, 3)
	// But at invalid 100 depth, there are more than three lines but less than 100
	// because we show the full trace when bounds are wrong
	lines = strings.Split(getStackTrace("mycoroutine", "success", 100), "\n")
	require.True(t, len(lines) > 3 && len(lines) < 100)
}
