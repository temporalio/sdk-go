// Copyright (c) 2017 Uber Technologies, Inc.
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
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type WorkflowUnitTest struct {
	suite.Suite
	WorkflowTestSuite
	activityOptions ActivityOptions
}

func (s *WorkflowUnitTest) SetupSuite() {
	RegisterWorkflow(worldWorkflow)
	RegisterWorkflow(helloWorldActivityWorkflow)
	RegisterWorkflow(testClockWorkflow)
	RegisterWorkflow(greetingsWorkflow)
	RegisterWorkflow(continueAsNewWorkflowTest)
	RegisterWorkflow(cancelWorkflowTest)
	RegisterWorkflow(cancelWorkflowAfterActivityTest)
	RegisterWorkflow(signalWorkflowTest)
	RegisterWorkflow(splitJoinActivityWorkflow)
	RegisterWorkflow(activityOptionsWorkflow)

	s.activityOptions = ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	RegisterActivity(testAct)
	RegisterActivity(helloWorldAct)
	RegisterActivity(getGreetingActivity)
	RegisterActivity(getNameActivity)
	RegisterActivity(sayGreetingActivity)
}

func TestWorkflowUnitTest(t *testing.T) {
	suite.Run(t, new(WorkflowUnitTest))
}

func worldWorkflow(ctx Context, input string) (result string, err error) {
	return input + " World!", nil
}

func (s *WorkflowUnitTest) Test_WorldWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(worldWorkflow, "Hello")
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func helloWorldAct(ctx context.Context) (string, error) {
	s := ctx.Value(unitTestKey).(*WorkflowUnitTest)
	info := GetActivityInfo(ctx)
	s.Equal(tasklist, info.TaskList)
	s.Equal(2*time.Second, info.HeartbeatTimeout)
	return "test", nil
}

func helloWorldActivityWorkflow(ctx Context, input string) (result string, err error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
		HeartbeatTimeout:       2 * time.Second,
		ActivityID:             "id1",
		TaskList:               tasklist,
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
	env.ExecuteWorkflow(helloWorldActivityWorkflow, "Hello")
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func splitJoinActivityWorkflow(ctx Context, testPanic bool) (result string, err error) {
	var result1, result2 string
	var err1, err2 error

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
	NewSelector(ctx).AddReceive(c2, func(c Channel, more bool) {
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
	if err2 != nil {
		return "", err2
	}

	return result1 + result2, nil
}

func (s *WorkflowUnitTest) Test_SplitJoinActivityWorkflow() {
	env := s.NewTestWorkflowEnvironment()
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
	env.ExecuteWorkflow(splitJoinActivityWorkflow, false)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal("Hello Flow!", result)
}

func TestWorkflowPanic(t *testing.T) {
	ts := &WorkflowTestSuite{}
	ts.SetLogger(zap.NewNop()) // this test simulate panic, use nop logger to avoid logging noise
	env := ts.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(splitJoinActivityWorkflow, true)
	require.True(t, env.IsWorkflowCompleted())
	require.NotNil(t, env.GetWorkflowError())
	resultErr := env.GetWorkflowError().(*PanicError)
	require.EqualValues(t, "simulated", resultErr.Error())
	require.Contains(t, resultErr.StackTrace(), "cadence/internal.splitJoinActivityWorkflow")
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
	env.GetWorkflowResult(&nowTime)
	s.False(nowTime.IsZero())
}

type testTimerWorkflow struct {
	t *testing.T
}

func (w *testTimerWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
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
	RegisterWorkflow(w.Execute)
	env.ExecuteWorkflow(w.Execute, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

type testActivityCancelWorkflow struct {
	t *testing.T
}

func testAct(ctx context.Context) (string, error) {
	return "test", nil
}

func (w *testActivityCancelWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
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
	w := &testActivityCancelWorkflow{t: t}
	RegisterWorkflow(w.Execute)
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
	return "cadence", nil
}
func sayGreetingActivity(input *sayGreetingActivityRequest) (string, error) {
	return fmt.Sprintf("%v %v!", input.Greeting, input.Name), nil
}

// Greetings Workflow Decider.
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
	env.ExecuteWorkflow(greetingsWorkflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal("Hello cadence!", result)
}

func continueAsNewWorkflowTest(ctx Context) error {
	return NewContinueAsNewError(ctx, "continueAsNewWorkflowTest", []byte("start"))
}

func (s *WorkflowUnitTest) Test_ContinueAsNewWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(continueAsNewWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NotNil(env.GetWorkflowError())
	resultErr := env.GetWorkflowError().(*ContinueAsNewError)
	s.EqualValues("continueAsNewWorkflowTest", resultErr.params.workflowType.Name)
	s.EqualValues(1, *resultErr.params.executionStartToCloseTimeoutSeconds)
	s.EqualValues(1, *resultErr.params.taskStartToCloseTimeoutSeconds)
	s.EqualValues("default-test-tasklist", *resultErr.params.taskListName)
}

func cancelWorkflowTest(ctx Context) (string, error) {
	if ctx.Done().Receive(ctx, nil); ctx.Err() == ErrCanceled {
		return "Cancelled.", ctx.Err()
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
	// not to propagate those decisions.

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
		return []byte("Cancelled."), ctx.Err()
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

func signalWorkflowTest(ctx Context) ([]byte, error) {
	// read multiple times.
	var result string
	ch := GetSignalChannel(ctx, "testSig1")
	var v string
	ch.Receive(ctx, &v)
	result += v
	ch.Receive(ctx, &v)
	result += v

	// Read on a selector.
	ch2 := GetSignalChannel(ctx, "testSig2")
	s := NewSelector(ctx)
	s.AddReceive(ch2, func(c Channel, more bool) {
		c.Receive(ctx, &v)
		result += v
	})
	s.Select(ctx)
	s.Select(ctx)
	s.Select(ctx)

	// Read on a selector inside the callback, multiple times.
	ch2 = GetSignalChannel(ctx, "testSig2")
	s = NewSelector(ctx)
	s.AddReceive(ch2, func(c Channel, more bool) {
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
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow("testSig1", msg)
		}, time.Second)
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
	env.GetWorkflowResult(&result)
	s.EqualValues(strings.Join(expected, ""), string(result))
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
	return *ctx1Ao.ActivityID + " " + *ctx2Ao.ActivityID, nil
}

// Test that activity options are correctly spawned with WithActivityOptions is called.
// See https://github.com/uber-go/cadence-client/issues/372
func (s *WorkflowUnitTest) Test_ActivityOptionsWorkflow() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(activityOptionsWorkflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal("id1 id2", result)
}
