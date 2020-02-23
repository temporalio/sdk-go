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
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/temporal-proto/serviceerror"
	"go.uber.org/atomic"
	"go.uber.org/zap"

	commonproto "go.temporal.io/temporal-proto/common"
	"go.temporal.io/temporal-proto/enums"
)

type WorkflowTestSuiteUnitTest struct {
	suite.Suite
	WorkflowTestSuite
	activityOptions      ActivityOptions
	localActivityOptions LocalActivityOptions
}

type testContextKey string

func (s *WorkflowTestSuiteUnitTest) SetupSuite() {
	s.activityOptions = ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	s.localActivityOptions = LocalActivityOptions{
		ScheduleToCloseTimeout: time.Second * 3,
	}
	s.header = &commonproto.Header{
		Fields: map[string][]byte{"test": []byte("test-data")},
	}
	s.ctxProps = []ContextPropagator{NewStringMapPropagator([]string{"test"})}
	RegisterWorkflowWithOptions(testWorkflowHello, RegisterWorkflowOptions{Name: "testWorkflowHello"})
	RegisterWorkflow(testWorkflowContext)
	RegisterWorkflow(testWorkflowHeartbeat)
	RegisterActivityWithOptions(testActivityHello, RegisterActivityOptions{Name: "testActivityHello"})
	RegisterActivity(testActivityContext)
	RegisterActivity(testActivityHeartbeat)
	RegisterActivity(testActivityCanceled)
}

func TestUnitTestSuite(t *testing.T) {
	suite.Run(t, new(WorkflowTestSuiteUnitTest))
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityMockFunction() {
	mockActivity := func(ctx context.Context, msg string) (string, error) {
		return "mock_" + msg, nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).Return(mockActivity).Once()

	env.ExecuteWorkflow(testWorkflowHello)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("mock_world", result)
	env.AssertExpectations(s.T())
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityMockFunction_WithDataConverter() {
	mockActivity := func(ctx context.Context, msg string) (string, error) {
		return "mock_" + msg, nil
	}

	workflowFn := func(ctx Context) (string, error) {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       20 * time.Second,
		}
		ctx = WithActivityOptions(ctx, ao)

		var result string
		ctx = WithDataConverter(ctx, newTestDataConverter())
		err := ExecuteActivity(ctx, testActivityHello, "world").Get(ctx, &result)
		if err != nil {
			return "", err
		}

		var result1 string
		ctx1 := WithDataConverter(ctx, getDefaultDataConverter()) // use another converter to run activity
		err1 := ExecuteActivity(ctx1, testActivityHello, "world1").Get(ctx1, &result1)
		if err1 != nil {
			return "", err1
		}
		return result + "," + result1, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.SetWorkerOptions(WorkerOptions{DataConverter: newTestDataConverter()})
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).Return(mockActivity).Twice()

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("mock_world,mock_world1", result)
	env.AssertExpectations(s.T())
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityMockValues() {
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).Return("mock_value", nil).Once()

	env.ExecuteWorkflow(testWorkflowHello)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("mock_value", result)
	env.AssertExpectations(s.T())
}

func (s *WorkflowTestSuiteUnitTest) Test_OnActivityStartedListener() {
	runCount := 100
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)

		for i := 1; i <= runCount; i++ {
			err := ExecuteActivity(ctx, testActivityHello, fmt.Sprintf("msg%d", i)).Get(ctx, nil)
			if err != nil {
				return err
			}
		}
		return nil
	} // END of workflow code

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	var activityCalls []string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args Values) {
		var input string
		s.NoError(args.Get(&input))
		activityCalls = append(activityCalls, fmt.Sprintf("%s:%s", activityInfo.ActivityType.Name, input))
	})
	var expectedCalls []string
	for i := 1; i <= runCount; i++ {
		expectedCalls = append(expectedCalls, fmt.Sprintf("testActivityHello:msg%v", i))
	}

	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(expectedCalls, activityCalls)
}

func (s *WorkflowTestSuiteUnitTest) Test_TimerWorkflow_ClockAutoFastForward() {
	var firedTimerRecord []string
	workflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Second*5)
		t2 := NewTimer(ctx, time.Second*1)
		t3 := NewTimer(ctx, time.Second*2)
		t4 := NewTimer(ctx, time.Second*5)

		selector := NewSelector(ctx)
		selector.AddFuture(t1, func(f Future) {
			firedTimerRecord = append(firedTimerRecord, "t1")
		}).AddFuture(t2, func(f Future) {
			firedTimerRecord = append(firedTimerRecord, "t2")
		}).AddFuture(t3, func(f Future) {
			firedTimerRecord = append(firedTimerRecord, "t3")
		}).AddFuture(t4, func(f Future) {
			firedTimerRecord = append(firedTimerRecord, "t4")
		})

		selector.Select(ctx)
		selector.Select(ctx)
		selector.Select(ctx)
		selector.Select(ctx)

		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal([]string{"t2", "t3", "t1", "t4"}, firedTimerRecord)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowAutoForwardClock() {
	workflowFn := func(ctx Context) (string, error) {
		// Schedule a timer with long duration. In this test, we won't actually wait for that long, because the test suite
		// will auto forward clock when workflow is blocked and there is no running activity.
		f1 := NewTimer(ctx, time.Minute)

		ctx = WithActivityOptions(ctx, s.activityOptions)
		// Execute activity that returns immediately, once the activity returns, the workflow will be blocked on timer
		// and the test suite will auto forward clock to fire the timer.
		f2 := ExecuteActivity(ctx, testActivityHello, "controlled_execution")

		timerErr := f1.Get(ctx, nil) // wait until timer fires
		if timerErr != nil {
			return "", timerErr
		}

		if !f2.IsReady() {
			return "", errors.New("activity is not completed when timer fired")
		}

		var activityResult string
		activityErr := f2.Get(ctx, &activityResult)
		if activityErr != nil {
			return "", activityErr
		}

		return activityResult, nil
	} // END of workflow code

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("hello_controlled_execution", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowMixedClock() {
	workflowFn := func(ctx Context) (string, error) {
		// Schedule a long timer.
		t1 := NewTimer(ctx, time.Minute)

		ctx = WithActivityOptions(ctx, s.activityOptions)
		// Schedule 2 activities, one returns immediately, the other will take 10s to run.
		f1 := ExecuteActivity(ctx, testActivityHello, "controlled_execution")
		f2Ctx, f2Cancel := WithCancel(ctx)
		f2 := ExecuteActivity(f2Ctx, testActivityHeartbeat, time.Second*10)

		err := f1.Get(ctx, nil)
		if err != nil {
			return "", err
		}

		// Schedule a short timer after f1 completed.
		t2 := NewTimer(ctx, time.Millisecond)
		NewSelector(ctx).AddFuture(t2, func(f Future) {
			// when t2 fires, we would cancel f2
			if !f2.IsReady() {
				f2Cancel()
			}
		}).Select(ctx) // wait until t2 fires

		_ = t1.Get(ctx, nil) // wait for the long timer to fire.

		return "expected", nil
	} // END of workflow code

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("expected", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowActivityCancellation() {
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)

		ctx, cancelHandler := WithCancel(ctx)
		f1 := ExecuteActivity(ctx, testActivityHeartbeat, "fast", time.Millisecond) // fast activity
		f2 := ExecuteActivity(ctx, testActivityHeartbeat, "slow", time.Second*3)    // slow activity

		NewSelector(ctx).AddFuture(f1, func(f Future) {
			cancelHandler()
		}).AddFuture(f2, func(f Future) {
			cancelHandler()
		}).Select(ctx)

		err := f2.Get(ctx, nil) // verify slow activity is cancelled
		if _, ok := err.(*CanceledError); !ok {
			return err
		}
		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	activityMap := make(map[string]string) // msg -> activityID
	var completedActivityID, cancelledActivityID string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args Values) {
		var msg string
		s.NoError(args.Get(&msg))
		activityMap[msg] = activityInfo.ActivityID
	})
	env.SetOnActivityCompletedListener(func(activityInfo *ActivityInfo, result Value, err error) {
		completedActivityID = activityInfo.ActivityID
	})
	env.SetOnActivityCanceledListener(func(activityInfo *ActivityInfo) {
		cancelledActivityID = activityInfo.ActivityID
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(activityMap["fast"], completedActivityID)
	s.Equal(activityMap["slow"], cancelledActivityID)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityWithUserContext() {
	testKey, testValue := testContextKey("test_key"), "test_value"
	userCtx := context.WithValue(context.Background(), testKey, testValue)
	workerOptions := WorkerOptions{}
	workerOptions.BackgroundActivityContext = userCtx

	// inline activity using value passing through user context.
	activityWithUserContext := func(ctx context.Context, keyName testContextKey) (string, error) {
		value := ctx.Value(keyName)
		if value != nil {
			return value.(string), nil
		}
		return "", errors.New("value not found from ctx")
	}
	RegisterActivity(activityWithUserContext)

	env := s.NewTestActivityEnvironment()
	env.SetWorkerOptions(workerOptions)
	blob, err := env.ExecuteActivity(activityWithUserContext, testKey)
	s.NoError(err)
	var value string
	_ = blob.Get(&value)
	s.Equal(testValue, value)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityWithHeaderContext() {
	workerOptions := WorkerOptions{
		ContextPropagators: []ContextPropagator{NewStringMapPropagator([]string{testHeader})},
	}

	// inline activity using value passing through user context.
	activityWithUserContext := func(ctx context.Context) (string, error) {
		value := ctx.Value(contextKey(testHeader))
		if val, ok := value.(string); ok {
			return val, nil
		}
		return "", errors.New("value not found from ctx")
	}
	RegisterActivity(activityWithUserContext)

	s.SetHeader(&commonproto.Header{
		Fields: map[string][]byte{
			testHeader: []byte("test-data"),
		},
	})

	env := s.NewTestActivityEnvironment()
	env.SetWorkerOptions(workerOptions)
	blob, err := env.ExecuteActivity(activityWithUserContext)
	s.NoError(err)
	var value string
	_ = blob.Get(&value)
	s.Equal("test-data", value)
}

func (s *WorkflowTestSuiteUnitTest) Test_CompleteActivity() {
	env := s.NewTestWorkflowEnvironment()
	var activityInfo ActivityInfo
	mockActivity := func(ctx context.Context, msg string) (string, error) {
		activityInfo = GetActivityInfo(ctx)
		env.RegisterDelayedCallback(func() {
			err := env.CompleteActivity(activityInfo.TaskToken, "async_complete", nil)
			s.NoError(err)
		}, time.Minute)
		return "", ErrActivityResultPending
	}

	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).Return(mockActivity).Once()
	env.ExecuteWorkflow(testWorkflowHello)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("async_complete", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityReturnsErrActivityResultPending() {
	env := s.NewTestActivityEnvironment()
	activityFn := func(ctx context.Context) (string, error) {
		return "", ErrActivityResultPending
	}
	RegisterActivity(activityFn)
	_, err := env.ExecuteActivity(activityFn)
	s.Equal(ErrActivityResultPending, err)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowCancellation() {
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		f := ExecuteActivity(ctx, testActivityHeartbeat, "msg1", time.Second*10)
		err := f.Get(ctx, nil) // wait for result
		return err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	// Register a delayed callback using workflow timer internally. The callback will be called when workflow clock passed
	// by the specified delay duration. The test suite enables the auto clock forwarding when workflow is blocked and no
	// activities are running. However, if there are running activities, test suite's workflow clock will move forward at
	// the real wall clock pace. In this test case, the activity is configured to run for 10s, so the workflow will be
	// blocked. But after 1ms, the registered callback will be invoked, which will request to cancel the workflow.
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Millisecond)

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NotNil(env.GetWorkflowError())
	_, ok := env.GetWorkflowError().(*CanceledError)
	s.True(ok)
}

func testWorkflowHello(ctx Context) (string, error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	var result string
	err := ExecuteActivity(ctx, testActivityHello, "world").Get(ctx, &result)
	if err != nil {
		return "", err
	}
	return result, nil
}

func testWorkflowContext(ctx Context) (string, error) {
	value := ctx.Value(contextKey(testHeader))
	if val, ok := value.(string); ok {
		return val, nil
	}
	return "", fmt.Errorf("context did not propagate to workflow")
}

func testActivityHello(_ context.Context, msg string) (string, error) {
	return "hello" + "_" + msg, nil
}

func testActivityContext(ctx context.Context) (string, error) {
	value := ctx.Value(contextKey(testHeader))
	if val, ok := value.(string); ok {
		return val, nil
	}
	return "", fmt.Errorf("context did not propagate to workflow")
}

func testActivityCanceled(ctx context.Context) (int32, error) {
	info := GetActivityInfo(ctx)
	if info.Attempt < 2 {
		return int32(-1), NewCanceledError("details")
	}
	return info.Attempt, nil
}

func testWorkflowHeartbeat(ctx Context, msg string, waitTime time.Duration) (string, error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	var result string
	err := ExecuteActivity(ctx, testActivityHeartbeat, msg, waitTime).Get(ctx, &result)
	if err != nil {
		return "", err
	}
	return result, nil
}

func testActivityHeartbeat(ctx context.Context, msg string, waitTime time.Duration) (string, error) {
	GetActivityLogger(ctx).Info("testActivityHeartbeat start",
		zap.String("msg", msg), zap.Duration("waitTime", waitTime))

	currWaitTime := time.Duration(0)
	for currWaitTime < waitTime {
		RecordActivityHeartbeat(ctx)
		select {
		case <-ctx.Done():
			// We have been cancelled.
			return "", ctx.Err()
		default:
			// We are not cancelled yet.
		}

		sleepDuration := time.Second
		if currWaitTime+sleepDuration > waitTime {
			sleepDuration = waitTime - currWaitTime
		}
		time.Sleep(sleepDuration)
		currWaitTime += sleepDuration
	}

	return "heartbeat_" + msg, nil
}

func (s *WorkflowTestSuiteUnitTest) Test_SideEffect() {
	value := 12345
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		se := SideEffect(ctx, func(ctx Context) interface{} {
			return value
		})
		var v int
		if err := se.Get(&v); err != nil {
			return err
		}
		if v != value {
			return errors.New("unexpected")
		}
		f := ExecuteActivity(ctx, testActivityHello, "msg1")
		err := f.Get(ctx, nil) // wait for result
		return err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_Basic() {
	workflowFn := func(ctx Context) (string, error) {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var helloActivityResult string
		err := ExecuteActivity(ctx, testActivityHello, "activity").Get(ctx, &helloActivityResult)
		if err != nil {
			return "", err
		}

		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Minute}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var helloWorkflowResult string
		err = ExecuteChildWorkflow(ctx, testWorkflowHello).Get(ctx, &helloWorkflowResult)
		if err != nil {
			return "", err
		}

		return helloActivityResult + " " + helloWorkflowResult, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var actualResult string
	s.NoError(env.GetWorkflowResult(&actualResult))
	s.Equal("hello_activity hello_world", actualResult)
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_Basic_WithDataConverter() {
	workflowFn := func(ctx Context) (string, error) {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var helloActivityResult string
		err := ExecuteActivity(ctx, testActivityHello, "activity").Get(ctx, &helloActivityResult)
		if err != nil {
			return "", err
		}

		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Minute}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var helloWorkflowResult string
		ctx = WithDataConverter(ctx, newTestDataConverter())
		err = ExecuteChildWorkflow(ctx, testWorkflowHello).Get(ctx, &helloWorkflowResult)
		if err != nil {
			return "", err
		}

		return helloActivityResult + " " + helloWorkflowResult, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var actualResult string
	s.NoError(env.GetWorkflowResult(&actualResult))
	s.Equal("hello_activity hello_world", actualResult)
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflowCancel() {
	workflowFn := func(ctx Context) error {
		cwo := ChildWorkflowOptions{
			ExecutionStartToCloseTimeout: time.Minute,
			WaitForCancellation:          true,
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		ctx1, cancel1 := WithCancel(ctx)
		ctx2, cancel2 := WithCancel(ctx)
		f1 := ExecuteChildWorkflow(ctx1, testWorkflowHeartbeat, "fast", time.Millisecond)
		f2 := ExecuteChildWorkflow(ctx2, testWorkflowHeartbeat, "slow", time.Hour)

		NewSelector(ctx).AddFuture(f1, func(f Future) {
			cancel2()
		}).AddFuture(f2, func(f Future) {
			cancel1()
		}).Select(ctx)

		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_Mock() {
	workflowFn := func(ctx Context) (string, error) {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var helloActivityResult string
		err := ExecuteActivity(ctx, testActivityHello, "activity").Get(ctx, &helloActivityResult)
		if err != nil {
			return "", err
		}

		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Minute}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var helloWorkflowResult string
		err = ExecuteChildWorkflow(ctx, testWorkflowHello).Get(ctx, &helloWorkflowResult)
		if err != nil {
			return "", err
		}
		var heartbeatWorkflowResult string
		err = ExecuteChildWorkflow(ctx, testWorkflowHeartbeat, "slow", time.Hour).Get(ctx, &heartbeatWorkflowResult)
		if err != nil {
			return "", err
		}

		return helloActivityResult + " " + helloWorkflowResult + " " + heartbeatWorkflowResult, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).Return("mock_msg", nil)
	env.OnWorkflow(testWorkflowHeartbeat, mock.Anything, mock.Anything, mock.Anything).
		Return("mock_heartbeat", nil)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var actualResult string
	s.NoError(env.GetWorkflowResult(&actualResult))
	s.Equal("mock_msg mock_msg mock_heartbeat", actualResult)
}

// Test_ChildWorkflow_Mock_Panic_GetChildWorkflowExecution verifies that
// ExecuteChildWorkflow(...).GetChildWorkflowExecution().Get() doesn't block forever when mock panics
func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_Mock_Panic_GetChildWorkflowExecution() {
	workflowFn := func(ctx Context) (string, error) {
		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Minute}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var helloWorkflowResult string
		childWorkflow := ExecuteChildWorkflow(ctx, testWorkflowHello)
		_ = childWorkflow.GetChildWorkflowExecution().Get(ctx, nil)
		err := childWorkflow.Get(ctx, &helloWorkflowResult)
		if err != nil {
			return "", err
		}
		return helloWorkflowResult, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything, mock.Anything, mock.Anything).
		Return("mock_result", nil, "extra_argument") // extra arg causes panic
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	workflowError := env.GetWorkflowError()
	s.Error(workflowError)
	s.Equal("mock of testWorkflowHello has incorrect number of returns, expected 2, but actual is 3",
		workflowError.Error())
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_StartFailed() {
	workflowFn := func(ctx Context) (string, error) {
		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Minute}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		err := ExecuteChildWorkflow(ctx, testWorkflowHello).GetChildWorkflowExecution().Get(ctx, nil)
		if err != nil {
			return "", errors.New("fail to start child")
		}

		return "should-not-go-here", nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything).Return("", ErrMockStartChildWorkflowFailed)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	s.Equal("fail to start child", env.GetWorkflowError().Error())
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_Listener() {
	workflowFn := func(ctx Context) (string, error) {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var helloActivityResult string
		err := ExecuteActivity(ctx, testActivityHello, "activity").Get(ctx, &helloActivityResult)
		if err != nil {
			return "", err
		}

		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Minute}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var helloWorkflowResult string
		err = ExecuteChildWorkflow(ctx, testWorkflowHello).Get(ctx, &helloWorkflowResult)
		if err != nil {
			return "", err
		}

		return helloActivityResult + " " + helloWorkflowResult, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	var childWorkflowName, childWorkflowResult string
	env.SetOnChildWorkflowStartedListener(func(workflowInfo *WorkflowInfo, ctx Context, args Values) {
		childWorkflowName = workflowInfo.WorkflowType.Name
	})
	env.SetOnChildWorkflowCompletedListener(func(workflowInfo *WorkflowInfo, result Value, err error) {
		s.NoError(err)
		s.NoError(result.Get(&childWorkflowResult))
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var actualResult string
	s.NoError(env.GetWorkflowResult(&actualResult))
	s.Equal("hello_activity hello_world", actualResult)
	s.Equal("hello_world", childWorkflowResult)
	s.Equal("testWorkflowHello", childWorkflowName)
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_Clock() {
	expected := []string{
		"child: activity completed",
		"parent: 1m timer fired",
		"parent: 10m timer fired",
		"child: 1h timer fired",
		"parent: child completed",
	}

	var history []string
	mutex := sync.Mutex{}
	addHistory := func(event string) {
		mutex.Lock()
		history = append(history, event)
		mutex.Unlock()
	}
	childWorkflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Hour)
		ctx = WithActivityOptions(ctx, s.activityOptions)
		f1 := ExecuteActivity(ctx, testActivityHello, "from child workflow")

		selector := NewSelector(ctx)
		selector.AddFuture(t1, func(f Future) {
			addHistory("child: 1h timer fired")
		}).AddFuture(f1, func(f Future) {
			addHistory("child: activity completed")
		})

		selector.Select(ctx)
		selector.Select(ctx)

		_ = t1.Get(ctx, nil)
		_ = f1.Get(ctx, nil)

		return nil
	}

	workflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Minute)
		t2 := NewTimer(ctx, time.Minute*10)

		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Hour * 2}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		f1 := ExecuteChildWorkflow(ctx, childWorkflowFn)

		selector := NewSelector(ctx)
		selector.AddFuture(f1, func(f Future) {
			addHistory("parent: child completed")
		}).AddFuture(t1, func(f Future) {
			addHistory("parent: 1m timer fired")
		}).AddFuture(t2, func(f Future) {
			addHistory("parent: 10m timer fired")
		})

		selector.Select(ctx)
		selector.Select(ctx)
		selector.Select(ctx)

		return nil
	}

	RegisterWorkflow(workflowFn)
	RegisterWorkflow(childWorkflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(expected, history)
}

func (s *WorkflowTestSuiteUnitTest) Test_MockActivityWait() {
	workflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Hour)
		ctx = WithActivityOptions(ctx, s.activityOptions)
		f1 := ExecuteActivity(ctx, testActivityHello, "mock_delay")

		NewSelector(ctx).AddFuture(t1, func(f Future) {
			// timer fired
		}).AddFuture(f1, func(f Future) {
			// activity completed
		}).Select(ctx)

		// either t1 or f1 is ready.
		if f1.IsReady() {
			return nil
		}

		// activity takes too long
		return errors.New("activity takes too long")
	}

	// no delay to the mock call, workflow should return no error
	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// delay 10 minutes, which is shorter than the 1 hour timer, so workflow should return no error.
	env = s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).After(time.Minute*10).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// delay 2 hours, which is longer than the 1 hour timer, and workflow should return error.
	env = s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).After(time.Hour*2).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// no mock
	env = s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_MockActivityWaitFn() {
	workflowFn := func(ctx Context) ([]string, error) {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var first, second string
		_ = ExecuteActivity(ctx, testActivityHello, "mock_delay_1").Get(ctx, &first)
		_ = ExecuteActivity(ctx, testActivityHello, "mock_delay_2").Get(ctx, &second)
		return []string{first, second}, nil
	}

	// extract results from the env and compare to expected values
	expectResult := func(env *TestWorkflowEnvironment, expected []string) {
		var result []string
		err := env.GetWorkflowResult(&result)
		s.NoError(err)
		s.Equal(expected, result)
	}

	// wrap around ExecuteWorkflow call to track env execution time
	expectDuration := func(env *TestWorkflowEnvironment, seconds int, fn func()) {
		before := env.Now()

		fn()

		after := env.Now()
		expected := time.Second * time.Duration(seconds)
		s.Truef(after.Sub(before).Round(time.Second) == expected,
			"Expected %v to be %v after %v, real diff: %v", after, expected, before, after.Sub(before))
	}

	RegisterWorkflow(workflowFn)

	// multiple different mocked delays and values should work
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, "mock_delay_1").After(time.Second).Return("one", nil).Once()
	env.OnActivity(testActivityHello, mock.Anything, "mock_delay_2").After(time.Minute).Return("two", nil).Once()
	expectDuration(env, 61, func() {
		env.ExecuteWorkflow(workflowFn)
	})
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
	expectResult(env, []string{"one", "two"})

	// multiple different dynamically mocked delays and values should work
	env = s.NewTestWorkflowEnvironment()
	afterCount, returnCount := 0, 0
	afters := []time.Duration{time.Second, time.Minute}
	returns := []string{"first", "second"}
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).
		AfterFn(func() time.Duration {
			defer func() { afterCount++ }()
			return afters[afterCount]
		}).
		Return(func(ctx context.Context, msg string) (string, error) {
			defer func() { returnCount++ }()
			return returns[returnCount], nil
		}).Twice()
	expectDuration(env, 61, func() {
		env.ExecuteWorkflow(workflowFn)
	})
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
	expectResult(env, returns)
}

func (s *WorkflowTestSuiteUnitTest) Test_MockWorkflowWait() {
	workflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Hour)
		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Hour /* this is currently ignored by test suite */}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		f1 := ExecuteChildWorkflow(ctx, testWorkflowHello)

		NewSelector(ctx).AddFuture(t1, func(f Future) {
			// timer fired
		}).AddFuture(f1, func(f Future) {
			// child workflow completed
		}).Select(ctx)

		// either t1 or f1 is ready.
		if f1.IsReady() {
			return nil
		}

		// child workflow takes too long
		return errors.New("child workflow takes too long")
	}

	RegisterWorkflow(workflowFn)
	// no delay to the mock call, workflow should return no error
	env := s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// delay 10 minutes, which is shorter than the 1 hour timer, so workflow should return no error.
	env = s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything).After(time.Minute*10).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// delay 2 hours, which is longer than the 1 hour timer, and workflow should return error.
	env = s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything).After(time.Hour*2).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// no mock
	env = s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_MockPanic() {
	// mock panic, verify that the panic won't be swallowed by our panic handler to detect unexpected mock call.
	oldLogger := s.GetLogger()
	s.SetLogger(zap.NewNop()) // use no-op logger to avoid noisy logging by panic
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).
		Return("hello_mock_panic", nil).
		Run(func(args mock.Arguments) {
			panic("mock-panic")
		})
	env.ExecuteWorkflow(testWorkflowHello)
	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "mock-panic")
	env.AssertExpectations(s.T())
	s.SetLogger(oldLogger) // restore original logger
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWithChild() {
	childWorkflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Hour)
		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Hour /* this is currently ignored by test suite */}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		f1 := ExecuteChildWorkflow(ctx, testWorkflowHello)

		NewSelector(ctx).AddFuture(t1, func(f Future) {
			// timer fired
		}).AddFuture(f1, func(f Future) {
			// child workflow completed
		}).Select(ctx)

		// either t1 or f1 is ready.
		if f1.IsReady() {
			return nil
		}

		// child workflow takes too long
		return errors.New("child workflow takes too long")
	}

	workflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Hour)
		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Hour /* this is currently ignored by test suite */}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		f1 := ExecuteChildWorkflow(ctx, childWorkflowFn) // execute child workflow which in turn execute another child workflow

		NewSelector(ctx).AddFuture(t1, func(f Future) {
			// timer fired
		}).AddFuture(f1, func(f Future) {
			// child workflow completed
		}).Select(ctx)

		// either t1 or f1 is ready.
		if f1.IsReady() {
			return f1.Get(ctx, nil)
		}

		// child workflow takes too long
		return errors.New("child workflow takes too long")
	}

	RegisterWorkflow(workflowFn)
	RegisterWorkflow(childWorkflowFn)

	// no delay to the mock call, workflow should return no error
	env := s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// delay 10 minutes, which is shorter than the 1 hour timer, so workflow should return no error.
	env = s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything).After(time.Minute*10).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// delay 2 hours, which is longer than the 1 hour timer, and workflow should return error.
	env = s.NewTestWorkflowEnvironment()
	env.OnWorkflow(testWorkflowHello, mock.Anything).After(time.Hour*2).Return("hello_mock_delayed", nil).Once()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// no mock
	env = s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_GetVersion() {
	oldActivity := func(ctx context.Context, msg string) (string, error) {
		return "hello" + "_" + msg, nil
	}
	newActivity := func(ctx context.Context, msg string) (string, error) {
		return "hello" + "_" + msg, nil
	}
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var f Future
		v := GetVersion(ctx, "test_change_id", DefaultVersion, 2)
		if v == DefaultVersion {
			f = ExecuteActivity(ctx, oldActivity, "ols_msg")
		} else {
			f = ExecuteActivity(ctx, newActivity, "new_msg")
		}
		err := f.Get(ctx, nil) // wait for result
		if err != nil {
			return err
		}

		// test searchable change version
		wfInfo := GetWorkflowInfo(ctx)
		s.NotNil(wfInfo.SearchAttributes)
		changeVersionsBytes, ok := wfInfo.SearchAttributes.IndexedFields[CadenceChangeVersion]
		s.True(ok)
		var changeVersions []string
		err = json.Unmarshal(changeVersionsBytes, &changeVersions)
		s.NoError(err)
		s.Equal(1, len(changeVersions))
		s.Equal("test_change_id-2", changeVersions[0])

		return err
	}

	RegisterWorkflow(workflowFn)
	RegisterActivity(oldActivity)
	RegisterActivity(newActivity)
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(newActivity, mock.Anything, "new_msg").Return("hello new_mock_msg", nil).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *WorkflowTestSuiteUnitTest) Test_MockGetVersion() {
	oldActivity := func(ctx context.Context, msg string) (string, error) {
		return "hello" + "_" + msg, nil
	}
	newActivity := func(ctx context.Context, msg string) (string, error) {
		return "hello" + "_" + msg, nil
	}
	workflowFn := func(ctx Context) (string, error) {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var f Future
		v1 := GetVersion(ctx, "change_1", DefaultVersion, 2)
		if v1 == DefaultVersion {
			f = ExecuteActivity(ctx, oldActivity, "old1")
		} else {
			f = ExecuteActivity(ctx, newActivity, "new1")
		}
		var ret1 string
		err := f.Get(ctx, &ret1) // wait for result
		if err != nil {
			return "", err
		}

		v2 := GetVersion(ctx, "change_2", DefaultVersion, 2)
		if v2 == DefaultVersion {
			f = ExecuteActivity(ctx, oldActivity, "old2")
		} else {
			f = ExecuteActivity(ctx, newActivity, "new2")
		}
		var ret2 string
		err = f.Get(ctx, &ret2) // wait for result
		if err != nil {
			return "", err
		}

		// test searchable change version
		wfInfo := GetWorkflowInfo(ctx)
		s.NotNil(wfInfo.SearchAttributes)
		changeVersionsBytes, ok := wfInfo.SearchAttributes.IndexedFields[CadenceChangeVersion]
		s.True(ok)
		var changeVersions []string
		err = json.Unmarshal(changeVersionsBytes, &changeVersions)
		s.NoError(err)
		s.Equal(2, len(changeVersions))
		s.Equal("change_2-2", changeVersions[0])
		s.Equal("change_1--1", changeVersions[1])

		return ret1 + ret2, err
	}

	RegisterWorkflow(workflowFn)
	RegisterActivity(oldActivity)
	RegisterActivity(newActivity)
	env := s.NewTestWorkflowEnvironment()

	env.OnGetVersion("change_1", DefaultVersion, 2).Return(func(string, Version, Version) Version {
		return DefaultVersion
	})
	env.OnGetVersion(mock.Anything, DefaultVersion, 2).Return(Version(2))
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
	var ret string
	s.NoError(env.GetWorkflowResult(&ret))
	s.Equal("hello_old1hello_new2", ret)
	env.AssertExpectations(s.T())
}

func (s *WorkflowTestSuiteUnitTest) Test_UpsertSearchAttributes_ReservedKey() {
	workflowFn := func(ctx Context) error {
		attr := map[string]interface{}{
			CadenceChangeVersion: "some change version",
		}
		err := UpsertSearchAttributes(ctx, attr)
		s.Error(err)

		wfInfo := GetWorkflowInfo(ctx)
		s.Nil(wfInfo.SearchAttributes)
		return nil
	}
	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *WorkflowTestSuiteUnitTest) Test_MockUpsertSearchAttributes() {
	workflowFn := func(ctx Context) error {
		attr := map[string]interface{}{}
		err := UpsertSearchAttributes(ctx, attr)
		s.Error(err)

		wfInfo := GetWorkflowInfo(ctx)
		s.Nil(wfInfo.SearchAttributes)

		attr["CustomIntField"] = 1
		err = UpsertSearchAttributes(ctx, attr)
		s.NoError(err)

		wfInfo = GetWorkflowInfo(ctx)
		s.NotNil(wfInfo.SearchAttributes)
		valBytes := wfInfo.SearchAttributes.IndexedFields["CustomIntField"]
		var result int
		_ = NewValue(valBytes).Get(&result)
		s.Equal(1, result)

		return nil
	}
	RegisterWorkflow(workflowFn)

	// no mock
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// has mock
	env = s.NewTestWorkflowEnvironment()
	env.OnUpsertSearchAttributes(map[string]interface{}{}).Return(errors.New("empty")).Once()
	env.OnUpsertSearchAttributes(map[string]interface{}{"CustomIntField": 1}).Return(nil).Once()

	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
	env.AssertExpectations(s.T())

	// mix no-mock and mock is not support
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityWithProtoTypes() {
	var actualValues []string
	retVal := &commonproto.WorkflowExecution{WorkflowId: "retwID2", RunId: "retrID2"}

	// Passing one argument
	activitySingleFn := func(ctx context.Context, wf *commonproto.WorkflowExecution) (*commonproto.WorkflowExecution, error) {
		actualValues = append(actualValues, wf.GetWorkflowId())
		actualValues = append(actualValues, wf.GetRunId())
		return retVal, nil
	}
	RegisterActivity(activitySingleFn)

	input := &commonproto.WorkflowExecution{WorkflowId: "wID1", RunId: "rID1"}
	env := s.NewTestActivityEnvironment()
	blob, err := env.ExecuteActivity(activitySingleFn, input)
	s.NoError(err)
	var ret *commonproto.WorkflowExecution
	_ = blob.Get(&ret)
	s.Equal(retVal, ret)

	// Passing more than one argument
	activityDoubleArgFn := func(ctx context.Context, wf *commonproto.WorkflowExecution, t *commonproto.WorkflowType) (*commonproto.WorkflowExecution, error) {
		actualValues = append(actualValues, wf.GetWorkflowId())
		actualValues = append(actualValues, wf.GetRunId())
		actualValues = append(actualValues, t.GetName())
		return retVal, nil
	}
	RegisterActivity(activityDoubleArgFn)

	input = &commonproto.WorkflowExecution{WorkflowId: "wID2", RunId: "rID3"}
	wt := &commonproto.WorkflowType{Name: "wType"}
	env = s.NewTestActivityEnvironment()
	blob, err = env.ExecuteActivity(activityDoubleArgFn, input, wt)
	s.NoError(err)
	_ = blob.Get(&ret)
	s.Equal(retVal, ret)

	expectedValues := []string{
		"wID1",
		"rID1",
		"wID2",
		"rID3",
		"wType",
	}
	s.EqualValues(expectedValues, actualValues)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityRegistration() {
	activityFn := func(msg string) (string, error) {
		return msg, nil
	}
	activityAlias := "some-random-activity-alias"

	RegisterActivityWithOptions(activityFn, RegisterActivityOptions{Name: activityAlias})
	env := s.NewTestActivityEnvironment()
	input := "some random input"

	encodedValue, err := env.ExecuteActivity(activityFn, input)
	s.NoError(err)
	output := ""
	_ = encodedValue.Get(&output)
	s.Equal(input, output)

	encodedValue, err = env.ExecuteActivity(activityAlias, input)
	s.NoError(err)
	output = ""
	_ = encodedValue.Get(&output)
	s.Equal(input, output)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowRegistration() {
	workflowFn := func(ctx Context) error {
		return nil
	}
	workflowAlias := "some-random-workflow-alias"

	RegisterWorkflowWithOptions(workflowFn, RegisterWorkflowOptions{Name: workflowAlias})
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(workflowFn)
	env = s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowAlias)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityFriendlyName() {
	activityFn := func(msg string) (string, error) {
		return "hello_" + msg, nil
	}

	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var result string
		err := ExecuteActivity(ctx, activityFn, "friendly_name").Get(ctx, &result)
		if err != nil {
			return err
		}
		err = ExecuteActivity(ctx, "foo", "friendly_name").Get(ctx, &result)
		return err
	}

	RegisterActivityWithOptions(activityFn, RegisterActivityOptions{Name: "foo"})
	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	var called []string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args Values) {
		called = append(called, activityInfo.ActivityType.Name)
	})

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(2, len(called))
	s.Equal("foo", called[0])
	s.Equal("foo", called[1])
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowFriendlyName() {

	workflowFn := func(ctx Context) error {
		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Hour /* this is currently ignored by test suite */}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var result string
		err := ExecuteChildWorkflow(ctx, testWorkflowHello).Get(ctx, &result)
		if err != nil {
			return err
		}
		err = ExecuteChildWorkflow(ctx, "testWorkflowHello").Get(ctx, &result)
		return err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	var called []string
	env.SetOnChildWorkflowStartedListener(func(workflowInfo *WorkflowInfo, ctx Context, args Values) {
		called = append(called, workflowInfo.WorkflowType.Name)
	})

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(2, len(called))
	s.Equal("testWorkflowHello", called[0])
	s.Equal("testWorkflowHello", called[1])
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowHeaderContext() {

	workflowFn := func(ctx Context) error {
		value := ctx.Value(contextKey(testHeader))
		if val, ok := value.(string); ok {
			s.Equal("test-data", val)
		} else {
			return fmt.Errorf("context did not propagate to workflow")
		}

		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Hour /* this is currently ignored by test suite */}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var result string
		if err := ExecuteChildWorkflow(ctx, testWorkflowContext).Get(ctx, &result); err != nil {
			return err
		}
		s.Equal("test-data", result)

		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       20 * time.Second,
		}
		ctx = WithActivityOptions(ctx, ao)
		if err := ExecuteActivity(ctx, testActivityContext).Get(ctx, &result); err != nil {
			return err
		}
		s.Equal("test-data", result)
		return nil
	}

	s.SetContextPropagators([]ContextPropagator{NewStringMapPropagator([]string{testHeader})})
	s.SetHeader(&commonproto.Header{
		Fields: map[string][]byte{
			testHeader: []byte("test-data"),
		},
	})

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityFullyQualifiedName() {
	// TODO (madhu): Add this back once test workflow environment is able to handle panics gracefully
	// Right now, the panic happens in a different goroutine and there is no way to catch it
	s.T().Skip()
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		var result string
		fut := ExecuteActivity(ctx, getFunctionName(testActivityHello), "friendly_name")
		err := fut.Get(ctx, &result)
		return err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.False(env.IsWorkflowCompleted())
	s.Contains(env.GetWorkflowError().Error(), "unable to find activityType")
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowFullyQualifiedName() {
	defer func() {
		if r := recover(); r != nil {
			s.Contains(r.(error).Error(), "unable to find workflow type")
		}
	}()
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(getFunctionName(testWorkflowHello))
	s.Fail("Should have panic'ed at ExecuteWorkflow")
}

func (s *WorkflowTestSuiteUnitTest) Test_QueryWorkflow() {
	queryType := "state"
	stateWaitSignal, stateWaitActivity, stateDone := "wait for signal", "wait for activity", "done"
	workflowFn := func(ctx Context) error {
		var state string
		err := SetQueryHandler(ctx, queryType, func(queryInput string) (string, error) {
			return queryInput + state, nil
		})
		if err != nil {
			return err
		}

		state = stateWaitSignal
		var signalData string
		GetSignalChannel(ctx, "query-signal").Receive(ctx, &signalData)

		state = stateWaitActivity
		ctx = WithActivityOptions(ctx, s.activityOptions)
		err = ExecuteActivity(ctx, testActivityHello, "mock_delay").Get(ctx, nil)
		if err != nil {
			return err
		}
		state = stateDone
		return err
	}
	RegisterWorkflow(workflowFn)

	env := s.NewTestWorkflowEnvironment()
	verifyStateWithQuery := func(expected string) {
		encodedValue, err := env.QueryWorkflow(queryType, "input")
		s.NoError(err)
		s.NotNil(encodedValue)
		var state string
		err = encodedValue.Get(&state)
		s.NoError(err)
		s.Equal("input"+expected, state)
	}
	env.RegisterDelayedCallback(func() {
		verifyStateWithQuery(stateWaitSignal)
		env.SignalWorkflow("query-signal", "hello-query")
	}, time.Hour)
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).After(time.Hour).Return("hello_mock", nil)
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args Values) {
		verifyStateWithQuery(stateWaitActivity)
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
	verifyStateWithQuery(stateDone)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowWithLocalActivity() {
	localActivityFn := func(ctx context.Context, name string) (string, error) {
		return "hello " + name, nil
	}

	workflowFn := func(ctx Context) (string, error) {
		ctx = WithLocalActivityOptions(ctx, s.localActivityOptions)
		var result string
		f := ExecuteLocalActivity(ctx, localActivityFn, "local_activity")
		err := f.Get(ctx, &result)
		return result, err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	s.NoError(err)
	s.Equal("hello local_activity", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_LocalActivity() {
	localActivityFn := func(ctx context.Context, name string) (string, error) {
		return "hello " + name, nil
	}

	env := s.NewTestActivityEnvironment()
	result, err := env.ExecuteLocalActivity(localActivityFn, "local_activity")
	s.NoError(err)
	var laResult string
	err = result.Get(&laResult)
	s.NoError(err)
	s.Equal("hello local_activity", laResult)
}

// Flaky test. Rerun if failed.
func (s *WorkflowTestSuiteUnitTest) Test_WorkflowLocalActivityWithMockAndListeners() {
	localActivityFn := func(ctx context.Context, name string) (string, error) {
		return "hello " + name, nil
	}

	cancelledLocalActivityFn := func() error {
		time.Sleep(time.Second)
		return nil
	}

	workflowFn := func(ctx Context) (string, error) {
		ctx = WithLocalActivityOptions(ctx, s.localActivityOptions)
		var result string
		f := ExecuteLocalActivity(ctx, localActivityFn, "local_activity")
		ctx2, cancel := WithCancel(ctx)
		f2 := ExecuteLocalActivity(ctx2, cancelledLocalActivityFn)

		NewSelector(ctx).AddFuture(f, func(f Future) {
			cancel()
		}).AddFuture(f2, func(f Future) {

		}).Select(ctx)

		err2 := f2.Get(ctx, nil)
		if _, ok := err2.(*CanceledError); !ok {
			return "", err2
		}

		err := f.Get(ctx, &result)
		return result, err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(localActivityFn, mock.Anything, "local_activity").Return("hello mock", nil).Once()
	var startedCount, completedCount, canceledCount atomic.Int32
	env.SetOnLocalActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args []interface{}) {
		startedCount.Inc()
	})

	env.SetOnLocalActivityCompletedListener(func(activityInfo *ActivityInfo, result Value, err error) {
		s.NoError(err)
		var resultValue string
		err = result.Get(&resultValue)
		s.NoError(err)
		s.Equal("hello mock", resultValue)
		completedCount.Inc()
	})

	env.SetOnLocalActivityCanceledListener(func(activityInfo *ActivityInfo) {
		canceledCount.Inc()
	})

	env.ExecuteWorkflow(workflowFn)
	env.AssertExpectations(s.T())
	s.EqualValues(2, startedCount.Load())
	s.EqualValues(1, completedCount.Load())
	s.EqualValues(1, canceledCount.Load())
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	s.NoError(err)
	s.Equal("hello mock", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_SignalChildWorkflow() {
	// This test will send signal from parent to child, and then child will send back signal to ack. No mock is needed.
	signalName := "test-signal-name"
	signalData := "test-signal-data"
	childWorkflowFn := func(ctx Context, parentExec WorkflowExecution) (string, error) {
		var data string
		GetSignalChannel(ctx, signalName).Receive(ctx, &data)

		err := SignalExternalWorkflow(ctx, parentExec.ID, parentExec.RunID, signalName, data+"-received").Get(ctx, nil)
		if err != nil {
			return "", err
		}

		return data + "-processed", nil
	}

	workflowFn := func(ctx Context) error {
		cwo := ChildWorkflowOptions{
			ExecutionStartToCloseTimeout: time.Minute,
			Domain:                       "test-domain",
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		childFuture := ExecuteChildWorkflow(ctx, childWorkflowFn, GetWorkflowInfo(ctx).WorkflowExecution)

		// send signal to child workflow
		signalFuture := childFuture.SignalChildWorkflow(ctx, signalName, signalData)
		if err := signalFuture.Get(ctx, nil); err != nil {
			return err
		}

		// receiving ack signal from child
		c := GetSignalChannel(ctx, signalName)
		var ackMsg string
		c.Receive(ctx, &ackMsg)
		s.Equal(signalData+"-received", ackMsg)

		var childResult string
		if err := childFuture.Get(ctx, &childResult); err != nil {
			return err
		}

		s.Equal(signalData+"-processed", childResult)
		return nil
	}

	RegisterWorkflow(childWorkflowFn)
	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_SignalExternalWorkflow() {
	signalName := "test-signal-name"
	signalData := "test-signal-data"
	workflowFn := func(ctx Context) error {
		// set domain to be more specific
		ctx = WithWorkflowDomain(ctx, "test-domain")
		f1 := SignalExternalWorkflow(ctx, "test-workflow-id1", "test-runid1", signalName, signalData)
		f2 := SignalExternalWorkflow(ctx, "test-workflow-id2", "test-runid2", signalName, signalData)
		f3 := SignalExternalWorkflow(ctx, "test-workflow-id3", "test-runid3", signalName, signalData)

		// signal1 succeed
		err1 := f1.Get(ctx, nil)
		if err1 != nil {
			return err1
		}

		// signal2 failed
		err2 := f2.Get(ctx, nil)
		if err2 == nil {
			return errors.New("signal2 should fail")
		}

		// signal3 succeed with delay
		t := NewTimer(ctx, time.Second*30)
		timerFired, signalSend := false, false

		NewSelector(ctx).AddFuture(t, func(f Future) {
			timerFired = true
		}).AddFuture(f3, func(f Future) {
			signalSend = true
		}).Select(ctx)

		// verify that timer fired first because the signal will delay 1 minute
		s.True(timerFired)
		s.False(signalSend)

		err3 := f3.Get(ctx, nil)
		if err3 != nil {
			return err3
		}

		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	// signal1 should succeed
	env.OnSignalExternalWorkflow("test-domain", "test-workflow-id1", "test-runid1", signalName, signalData).Return(nil).Once()

	// signal2 should fail
	env.OnSignalExternalWorkflow("test-domain", "test-workflow-id2", "test-runid2", signalName, signalData).Return(
		func(domainName, workflowID, runID, signalName string, arg interface{}) error {
			return errors.New("unknown external workflow")
		}).Once()

	// signal3 should succeed with delay, mock match exactly the parameters
	env.OnSignalExternalWorkflow("test-domain", "test-workflow-id3", "test-runid3", signalName, signalData).After(time.Minute).Return(nil).Once()

	env.ExecuteWorkflow(workflowFn)
	env.AssertExpectations(s.T())
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_CancelChildWorkflow() {
	childWorkflowFn := func(ctx Context) error {
		var err error
		selector := NewSelector(ctx)
		timer := NewTimer(ctx, 10*time.Second)
		selector.AddFuture(timer, func(f Future) {
			err = f.Get(ctx, nil)
		}).Select(ctx)

		fmt.Println("####")
		fmt.Println(err)
		fmt.Println("####")
		return err
	}

	workflowFn := func(ctx Context) error {

		cwo := ChildWorkflowOptions{
			Domain:                       "test-domain",
			ExecutionStartToCloseTimeout: time.Minute,
		}

		childCtx := WithChildWorkflowOptions(ctx, cwo)
		childCtx, cancel := WithCancel(childCtx)
		childFuture := ExecuteChildWorkflow(childCtx, childWorkflowFn)
		_ = Sleep(ctx, 2*time.Second)
		cancel()

		err := childFuture.Get(childCtx, nil)
		if _, ok := err.(*CanceledError); !ok {
			return fmt.Errorf("cancel child workflow should receive CanceledError, instead got: %v", err)
		}
		return nil
	}

	RegisterWorkflow(childWorkflowFn)
	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_CancelExternalWorkflow() {
	workflowFn := func(ctx Context) error {
		// set domain to be more specific
		ctx = WithWorkflowDomain(ctx, "test-domain")
		f1 := RequestCancelExternalWorkflow(ctx, "test-workflow-id1", "test-runid1")
		f2 := RequestCancelExternalWorkflow(ctx, "test-workflow-id2", "test-runid2")

		// cancellation 1 succeed
		err1 := f1.Get(ctx, nil)
		if err1 != nil {
			return err1
		}

		// cancellation 2 failed
		err2 := f2.Get(ctx, nil)
		if err2 == nil {
			return errors.New("cancellation 2 should fail")
		}

		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	// cancellation 1 should succeed
	env.OnRequestCancelExternalWorkflow("test-domain", "test-workflow-id1", "test-runid1").Return(nil).Once()

	// cancellation 2 should fail
	env.OnRequestCancelExternalWorkflow("test-domain", "test-workflow-id2", "test-runid2").Return(
		func(domainName, workflowID, runID string) error {
			return errors.New("unknown external workflow")
		}).Once()

	env.ExecuteWorkflow(workflowFn)
	env.AssertExpectations(s.T())
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_DisconnectedContext() {
	childWorkflowFn := func(ctx Context) (string, error) {
		err := NewTimer(ctx, time.Minute*10).Get(ctx, nil)
		if _, ok := err.(*CanceledError); ok {
			dCtx, _ := NewDisconnectedContext(ctx)
			dCtx = WithActivityOptions(dCtx, s.activityOptions)
			var cleanupResult string
			err := ExecuteActivity(dCtx, testActivityHello, "cleanup").Get(dCtx, &cleanupResult)
			return cleanupResult, err
		}

		// unexpected
		return "", errors.New("should not reach here")
	}

	workflowFn := func(ctx Context) (string, error) {
		cwo := ChildWorkflowOptions{
			ExecutionStartToCloseTimeout: time.Hour,
			WaitForCancellation:          true,
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		childCtx, cancelChild := WithCancel(ctx)
		childFuture := ExecuteChildWorkflow(childCtx, childWorkflowFn) // execute child workflow which in turn execute another child
		NewSelector(ctx).AddFuture(childFuture, func(f Future) {
			s.Fail("f1 should not complete before t1")
		}).AddFuture(NewTimer(ctx, time.Minute), func(f Future) {
			cancelChild() // child workflow takes too long, cancel child workflow
		}).Select(ctx)

		var result string
		err := childFuture.Get(childCtx, &result)
		return result, err
	}

	RegisterWorkflow(workflowFn)
	RegisterWorkflow(childWorkflowFn)

	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var workflowResult string
	err := env.GetWorkflowResult(&workflowResult)
	s.NoError(err)
	s.Equal("hello_cleanup", workflowResult)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowIDReusePolicy() {
	workflowFn := func(ctx Context) (string, error) {
		cwo := ChildWorkflowOptions{
			ExecutionStartToCloseTimeout: time.Minute,
			WorkflowID:                   "test-child-workflow-id",
			WorkflowIDReusePolicy:        WorkflowIDReusePolicyRejectDuplicate,
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var helloWorkflowResult string
		f := ExecuteChildWorkflow(ctx, testWorkflowHello)
		err := f.GetChildWorkflowExecution().Get(ctx, nil)
		s.NoError(err)
		err = f.Get(ctx, &helloWorkflowResult)
		s.NoError(err)

		// start child with duplicate workflow id, but with policy that won't allow duplicate
		f = ExecuteChildWorkflow(ctx, testWorkflowHello)
		err = f.GetChildWorkflowExecution().Get(ctx, nil)
		s.Error(err)
		err = f.Get(ctx, &helloWorkflowResult)
		s.Error(err)

		// now with policy allow duplicate
		cwo.WorkflowIDReusePolicy = WorkflowIDReusePolicyAllowDuplicate
		ctx = WithChildWorkflowOptions(ctx, cwo)
		f = ExecuteChildWorkflow(ctx, testWorkflowHello)
		err = f.GetChildWorkflowExecution().Get(ctx, nil)
		s.NoError(err)
		err = f.Get(ctx, &helloWorkflowResult)
		s.NoError(err)

		return helloWorkflowResult, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	var actualResult string
	s.NoError(env.GetWorkflowResult(&actualResult))
	s.Equal("hello_world", actualResult)
}

func (s *WorkflowTestSuiteUnitTest) Test_Channel() {
	workflowFn := func(ctx Context) error {

		signalCh := GetSignalChannel(ctx, "test-signal")
		doneCh := NewBufferedChannel(ctx, 100)
		selector := NewSelector(ctx)

		selector.AddReceive(signalCh, func(c Channel, more bool) {
		}).AddReceive(doneCh, func(c Channel, more bool) {
			var doneSignal string
			c.Receive(ctx, &doneSignal)
		})

		fanoutChs := []Channel{NewBufferedChannel(ctx, 100), NewBufferedChannel(ctx, 100)}

		processedCount := 0
		runningCount := 0

	mainLoop:
		for {
			selector.Select(ctx)
			var signal string
			if !signalCh.ReceiveAsync(&signal) {
				if runningCount > 0 {
					continue mainLoop
				}

				if processedCount < 4 {
					continue mainLoop
				}

				// continue as new
				return NewContinueAsNewError(ctx, "this-workflow-fn")
			}

			for i := range fanoutChs {
				ch := fanoutChs[i]
				ch.SendAsync(signal)
				processedCount++
				runningCount++
				Go(ctx, func(ctx Context) {
					doneCh.SendAsync("done")
					runningCount--
				})
			}
		}
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("test-signal", "s1")
		env.SignalWorkflow("test-signal", "s2")
	}, time.Minute)

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	_, ok := env.GetWorkflowError().(*ContinueAsNewError)
	s.True(ok)
}

func (s *WorkflowTestSuiteUnitTest) Test_ContextMisuse() {
	workflowFn := func(ctx Context) error {
		ch := NewChannel(ctx)

		Go(ctx, func(shouldUseThisCtx Context) {
			_ = Sleep(ctx, time.Hour)
			ch.Send(ctx, "done")
		})

		var done string
		ch.Receive(ctx, &done)

		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	s.Contains(env.GetWorkflowError().Error(), "block on coroutine which is already blocked")
}

func (s *WorkflowTestSuiteUnitTest) Test_DrainSignalChannel() {
	workflowFn := func(ctx Context) (string, error) {

		signalCh := GetSignalChannel(ctx, "test-signal")
		var s1, s2, s3 string
		signalCh.Receive(ctx, &s1)
		if !signalCh.ReceiveAsync(&s2) {
			return "", errors.New("expected signal")
		}
		if signalCh.ReceiveAsync(&s3) {
			return "", errors.New("unexpected signal")
		}
		return s1 + s2, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflowSkippingDecision("test-signal", "s1")
		env.SignalWorkflow("test-signal", "s2")
	}, time.Minute)

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	_ = env.GetWorkflowResult(&result)
	s.Equal("s1s2", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityRetry() {
	attempt1Count := 0
	activityFailedFn := func(ctx context.Context) (string, error) {
		attempt1Count++
		return "", NewCustomError("bad-bug")
	}

	attempt2Count := 0
	activityFn := func(ctx context.Context) (string, error) {
		attempt2Count++
		info := GetActivityInfo(ctx)
		if info.Attempt < 2 {
			return "", NewCustomError("bad-luck")
		}
		return "retry-done", nil
	}

	workflowFn := func(ctx Context) (string, error) {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          5,
				InitialInterval:          time.Second,
				MaximumInterval:          time.Second * 10,
				BackoffCoefficient:       2,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Minute,
			},
		}
		ctx = WithActivityOptions(ctx, ao)

		err := ExecuteActivity(ctx, activityFailedFn).Get(ctx, nil)
		badBug, ok := err.(*CustomError)
		s.True(ok)
		s.Equal("bad-bug", badBug.Reason())

		var result string
		err = ExecuteActivity(ctx, activityFn).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}

	env := s.NewTestWorkflowEnvironment()
	RegisterWorkflow(workflowFn)
	RegisterActivity(activityFailedFn)
	RegisterActivity(activityFn)

	// set a workflow timeout timer to test
	// if the timer will fire during activity retry
	env.SetWorkflowTimeout(10 * time.Second)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("retry-done", result)
	s.Equal(1, attempt1Count)
	s.Equal(3, attempt2Count)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityHeartbeatRetry() {
	var startedFrom []int
	activityHeartBeatFn := func(ctx context.Context, firstTaskID, taskCount int) error {
		i := firstTaskID
		if HasHeartbeatDetails(ctx) {
			var lastProcessed int
			if err := GetHeartbeatDetails(ctx, &lastProcessed); err == nil {
				i = lastProcessed + 1
			}
		}

		startedFrom = append(startedFrom, i)

		for j := 0; i < firstTaskID+taskCount; i, j = i+1, j+1 {
			// process task i
			RecordActivityHeartbeat(ctx, i)
			if j == 2 && i < firstTaskID+taskCount-1 { // simulate failure after processing 3 tasks
				return NewCustomError("bad-luck")
			}
		}

		return nil
	}

	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          3,
				InitialInterval:          time.Second,
				MaximumInterval:          time.Second * 10,
				BackoffCoefficient:       2,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Minute,
			},
		}
		ctx = WithActivityOptions(ctx, ao)

		err := ExecuteActivity(ctx, activityHeartBeatFn, 0, 9).Get(ctx, nil)
		if err != nil {
			return err
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetTestTimeout(time.Hour)
	RegisterWorkflow(workflowFn)
	RegisterActivity(activityHeartBeatFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(3, len(startedFrom))
	s.Equal([]int{0, 3, 6}, startedFrom)
}

func (s *WorkflowTestSuiteUnitTest) Test_LocalActivityRetry() {

	localActivityFn := func(ctx context.Context) (int32, error) {
		info := GetActivityInfo(ctx)
		if info.Attempt < 2 {
			return int32(-1), NewCustomError("bad-luck")
		}
		return info.Attempt, nil
	}

	workflowFn := func(ctx Context) (int32, error) {
		lao := LocalActivityOptions{
			ScheduleToCloseTimeout: time.Minute,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          3,
				InitialInterval:          time.Second,
				MaximumInterval:          time.Second * 10,
				BackoffCoefficient:       2,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Minute,
			},
		}
		ctx = WithLocalActivityOptions(ctx, lao)

		var result int32
		err := ExecuteLocalActivity(ctx, localActivityFn).Get(ctx, &result)
		if err != nil {
			return int32(-1), err
		}
		return result, nil
	}

	env := s.NewTestWorkflowEnvironment()
	RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result int32
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal(int32(2), result)
}

func (s *WorkflowTestSuiteUnitTest) Test_LocalActivityRetryOnCancel() {
	attempts := 0
	localActivityFn := func(ctx context.Context) (int32, error) {
		attempts++
		info := GetActivityInfo(ctx)
		if info.Attempt < 2 {
			return int32(-1), NewCanceledError("details")
		}
		return info.Attempt, nil
	}

	workflowFn := func(ctx Context) (int32, error) {
		lao := LocalActivityOptions{
			ScheduleToCloseTimeout: time.Minute,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          3,
				InitialInterval:          time.Second,
				MaximumInterval:          time.Second * 10,
				BackoffCoefficient:       2,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Minute,
			},
		}
		ctx = WithLocalActivityOptions(ctx, lao)

		var result int32
		err := ExecuteLocalActivity(ctx, localActivityFn).Get(ctx, &result)
		if err != nil {
			return int32(-1), err
		}
		return result, nil
	}

	env := s.NewTestWorkflowEnvironment()
	RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	s.True(IsCanceledError(env.GetWorkflowError()))
	s.Equal(1, attempts)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityRetryOnCancel() {
	workflowFn := func(ctx Context) (int32, error) {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          3,
				InitialInterval:          time.Second,
				MaximumInterval:          time.Second * 10,
				BackoffCoefficient:       2,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Minute,
			},
		}
		ctx = WithActivityOptions(ctx, ao)

		var result int32
		err := ExecuteActivity(ctx, testActivityCanceled).Get(ctx, &result)
		if err != nil {
			return int32(-1), err
		}
		return result, nil
	}

	env := s.NewTestWorkflowEnvironment()
	RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	s.True(IsCanceledError(env.GetWorkflowError()))
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflowRetry() {

	childWorkflowFn := func(ctx Context) (string, error) {
		info := GetWorkflowInfo(ctx)
		if info.Attempt < 2 {
			return "", NewCustomError("bad-luck")
		}
		return "retry-done", nil
	}

	workflowFn := func(ctx Context) (string, error) {
		cwo := ChildWorkflowOptions{
			ExecutionStartToCloseTimeout: time.Minute,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          3,
				InitialInterval:          time.Second,
				MaximumInterval:          time.Second * 10,
				BackoffCoefficient:       2,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Minute,
			},
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var childResult string
		err := ExecuteChildWorkflow(ctx, childWorkflowFn).Get(ctx, &childResult)
		if err != nil {
			return "", err
		}

		return childResult, nil
	}

	env := s.NewTestWorkflowEnvironment()
	RegisterWorkflow(childWorkflowFn)
	RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("retry-done", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_SignalChildWorkflowRetry() {
	childWorkflowFn := func(ctx Context) (string, error) {
		info := GetWorkflowInfo(ctx)
		if info.Attempt < 2 {
			return "", NewCustomError("bad-luck")
		}

		ch := GetSignalChannel(ctx, "test-signal-name")
		timeout := NewTimer(ctx, time.Second*3)
		s := NewSelector(ctx)
		var signal string
		s.AddFuture(timeout, func(f Future) {
			signal = "timeout"
		}).AddReceive(ch, func(c Channel, more bool) {
			c.Receive(ctx, &signal)
		}).Select(ctx)

		return signal, nil
	}

	workflowFn := func(ctx Context) (string, error) {
		cwo := ChildWorkflowOptions{
			WorkflowID:                   "test-retry-signal-child-workflow",
			ExecutionStartToCloseTimeout: time.Minute,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          3,
				InitialInterval:          time.Second * 3,
				MaximumInterval:          time.Second * 3,
				BackoffCoefficient:       1,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Minute,
			},
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var childResult string
		err := ExecuteChildWorkflow(ctx, childWorkflowFn).Get(ctx, &childResult)
		if err != nil {
			return "", err
		}

		return childResult, nil
	}

	env := s.NewTestWorkflowEnvironment()
	RegisterWorkflow(childWorkflowFn)
	RegisterWorkflow(workflowFn)

	env.RegisterDelayedCallback(func() {
		_ = env.SignalWorkflowByID("test-retry-signal-child-workflow", "test-signal-name", "test-signal-data")
	}, time.Second*7 /* after 2nd attempt failed, but before 3rd attempt starts */)

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("test-signal-data", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_TestWorkflowTimeoutInBusyLoop() {
	neverEndingWorkflow := func(ctx Context) error {
		for {
			_ = Sleep(ctx, time.Hour)
		}
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkflowTimeout((time.Hour * 10) + time.Minute)
	timerFiredCount := 0
	env.SetOnTimerFiredListener(func(timerID string) {
		timerFiredCount++
	})
	RegisterWorkflow(neverEndingWorkflow)

	env.ExecuteWorkflow(neverEndingWorkflow)
	s.Equal(10, timerFiredCount)
	s.Error(env.GetWorkflowError())
	_, ok := env.GetWorkflowError().(*TimeoutError)
	s.True(ok)
}

func (s *WorkflowTestSuiteUnitTest) Test_TestChildWorkflowTimeout() {
	childWorkflowFn := func(ctx Context) error {
		_ = Sleep(ctx, time.Hour*5)
		return nil
	}

	workflowFn := func(ctx Context) error {
		cwo := ChildWorkflowOptions{
			ExecutionStartToCloseTimeout: time.Hour * 3, // less than 5h that child workflow would take.
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		err := ExecuteChildWorkflow(ctx, childWorkflowFn).Get(ctx, nil)

		s.Error(err)
		if _, ok := err.(*TimeoutError); ok {
			return nil
		}
		return err
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkflowTimeout(time.Hour * 10)
	timerFiredCount := 0
	env.SetOnTimerFiredListener(func(timerID string) {
		timerFiredCount++
	})
	RegisterWorkflow(childWorkflowFn)
	RegisterWorkflow(workflowFn)

	env.ExecuteWorkflow(workflowFn)
	s.NoError(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_SameActivityIDFromDifferentChildWorkflow() {
	childWorkflowFn := func(ctx Context) (string, error) {
		ao := ActivityOptions{
			ActivityID:             "per_workflow_unique_activity_id",
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
		}
		ctx = WithActivityOptions(ctx, ao)

		info := GetWorkflowInfo(ctx)

		var result string
		err := ExecuteActivity(ctx, testActivityHello, info.WorkflowExecution.ID).Get(ctx, &result)
		if err != nil {
			return "", err
		}

		return result, nil
	}

	workflowFn := func(ctx Context) (string, error) {
		ctx1 := WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
			WorkflowID:                   "child_1",
			ExecutionStartToCloseTimeout: time.Minute,
		})
		f1 := ExecuteChildWorkflow(ctx1, childWorkflowFn)

		ctx2 := WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
			WorkflowID:                   "child_2",
			ExecutionStartToCloseTimeout: time.Minute,
		})
		f2 := ExecuteChildWorkflow(ctx2, childWorkflowFn)

		var result1, result2 string
		if err := f1.Get(ctx1, &result1); err != nil {
			return "", err
		}
		if err := f2.Get(ctx1, &result2); err != nil {
			return "", err
		}

		return result1 + " " + result2, nil
	}

	RegisterWorkflow(workflowFn)
	RegisterWorkflow(childWorkflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var actualResult string
	s.NoError(env.GetWorkflowResult(&actualResult))
	s.Equal("hello_child_1 hello_child_2", actualResult)
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflowAlreadyRunning() {
	workflowFn := func(ctx Context) (string, error) {
		ctx1 := WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
			WorkflowID:                   "Test_ChildWorkflowAlreadyRunning",
			ExecutionStartToCloseTimeout: time.Minute,
			//WorkflowIDReusePolicy:        WorkflowIDReusePolicyAllowDuplicate,
		})

		var result1, result2 string
		err := ExecuteChildWorkflow(ctx1, testWorkflowHeartbeat, "child1", time.Millisecond).Get(ctx1, &result1)
		s.NoError(err)

		f2 := ExecuteChildWorkflow(ctx1, testWorkflowHeartbeat, "child2", time.Second)

		err = f2.Get(ctx1, &result2)
		s.Error(err)
		s.IsType(&serviceerror.WorkflowExecutionAlreadyStarted{}, err)

		return result1 + " " + result2, nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.NoError(err)

	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("heartbeat_child1 ", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_CronWorkflow() {

	failedCount, successCount, lastCompletionResult := 0, 0, 0
	cronWorkflow := func(ctx Context) (int, error) {
		info := GetWorkflowInfo(ctx)
		var result int
		if HasLastCompletionResult(ctx) {
			_ = GetLastCompletionResult(ctx, &result)
		}
		_ = Sleep(ctx, time.Second*3)
		if info.Attempt == 0 {
			failedCount++
			return 0, errors.New("please-retry")
		}
		successCount++
		result++
		lastCompletionResult = result
		return result, nil
	}

	testWorkflow := func(ctx Context) error {
		ctx1 := WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
			ExecutionStartToCloseTimeout: time.Minute * 10,
			RetryPolicy: &RetryPolicy{
				MaximumAttempts:          5,
				InitialInterval:          time.Second,
				MaximumInterval:          time.Second * 10,
				BackoffCoefficient:       2,
				NonRetriableErrorReasons: []string{"bad-bug"},
				ExpirationInterval:       time.Hour,
			},
			CronSchedule: "0 * * * *", // hourly
		})

		cronFuture := ExecuteChildWorkflow(ctx1, cronWorkflow) // cron never stop so this future won't return

		timeoutTimer := NewTimer(ctx, time.Hour*3)
		selector := NewSelector(ctx)
		var err error
		selector.AddFuture(cronFuture, func(f Future) {
			err = errors.New("cron workflow returns, this is not expected")
		}).AddFuture(timeoutTimer, func(f Future) {
			// err will be nil
		}).Select(ctx)

		return err
	}

	RegisterWorkflow(cronWorkflow)
	RegisterWorkflow(testWorkflow)

	env := s.NewTestWorkflowEnvironment()
	startTime, _ := time.Parse(time.RFC3339, "2018-12-20T16:30:00+08:00")
	env.SetStartTime(startTime)
	env.ExecuteWorkflow(testWorkflow)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.NoError(err)

	s.Equal(4, failedCount)
	s.Equal(4, successCount)
	s.Equal(4, lastCompletionResult)
}

func (s *WorkflowTestSuiteUnitTest) Test_CronHasLastResult() {
	cronWorkflow := func(ctx Context) (int, error) {
		var result int
		if HasLastCompletionResult(ctx) {
			_ = GetLastCompletionResult(ctx, &result)
		}

		return result + 1, nil
	}

	RegisterWorkflow(cronWorkflow)

	env := s.NewTestWorkflowEnvironment()
	lastResult := 3
	env.SetLastCompletionResult(lastResult)
	env.ExecuteWorkflow(cronWorkflow)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result int
	err := env.GetWorkflowResult(&result)
	s.NoError(err)

	s.Equal(lastResult+1, result)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityWithProgress() {
	activityFn := func(ctx context.Context) (int, error) {
		var progress int
		if HasHeartbeatDetails(ctx) {
			_ = GetHeartbeatDetails(ctx, &progress)
		}

		return progress + 1, nil
	}

	RegisterActivity(activityFn)

	env := s.NewTestActivityEnvironment()
	lastProgress := 3
	env.SetHeartbeatDetails(lastProgress)
	result, err := env.ExecuteActivity(activityFn)

	s.NoError(err)

	var newProgress int
	err = result.Get(&newProgress)
	s.NoError(err)

	s.Equal(lastProgress+1, newProgress)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityGoexit() {
	fn := func(ctx context.Context) error {
		runtime.Goexit() // usually this is called by t.FailNow(), but can't call FailNow here since that would mark the test as failed.
		return nil
	}

	wf := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    5 * time.Second,
		}
		ctx = WithActivityOptions(ctx, ao)
		err := ExecuteActivity(ctx, fn).Get(ctx, nil)
		return err
	}

	RegisterActivity(fn)
	RegisterWorkflow(wf)

	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(wf)
	err := env.GetWorkflowError()
	s.EqualError(err, "activity called runtime.Goexit")
}

func (s *WorkflowTestSuiteUnitTest) Test_SetWorkerStopChannel() {
	env := newTestWorkflowEnvironmentImpl(&s.WorkflowTestSuite)
	c := make(chan struct{})
	env.setWorkerStopChannel(c)
	s.NotNil(env.workerStopChannel)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityTimeoutWithDetails() {
	count := 0
	timeoutFn := func() error {
		count++
		return NewTimeoutError(enums.TimeoutTypeStartToClose, testErrorDetails1)
	}
	RegisterActivity(timeoutFn)

	timeoutWf := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    5 * time.Second,
			RetryPolicy: &RetryPolicy{
				InitialInterval:          time.Second,
				BackoffCoefficient:       1.1,
				MaximumAttempts:          3,
				NonRetriableErrorReasons: []string{"cadenceInternal:Timeout TimeoutTypeStartToClose"},
			},
		}
		ctx = WithActivityOptions(ctx, ao)
		err := ExecuteActivity(ctx, timeoutFn).Get(ctx, nil)
		return err
	}
	RegisterWorkflow(timeoutWf)

	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.ExecuteWorkflow(timeoutWf)
	err := wfEnv.GetWorkflowError()
	s.Error(err)
	timeoutErr, ok := err.(*TimeoutError)
	s.True(ok)
	s.Equal(enums.TimeoutTypeStartToClose, timeoutErr.TimeoutType())
	s.True(timeoutErr.HasDetails())
	var details string
	err = timeoutErr.Details(&details)
	s.NoError(err)
	s.Equal(testErrorDetails1, details)
	s.Equal(1, count)

	activityEnv := s.NewTestActivityEnvironment()
	_, err = activityEnv.ExecuteActivity(timeoutFn)
	s.Error(err)
	timeoutErr, ok = err.(*TimeoutError)
	s.True(ok)
	s.Equal(enums.TimeoutTypeStartToClose, timeoutErr.TimeoutType())
	s.True(timeoutErr.HasDetails())
	err = timeoutErr.Details(&details)
	s.NoError(err)
	s.Equal(testErrorDetails1, details)
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityDeadlineExceeded() {
	timeoutFn := func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}
	RegisterActivity(timeoutFn)

	timeoutWf := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    1 * time.Second,
		}
		ctx = WithActivityOptions(ctx, ao)
		err := ExecuteActivity(ctx, timeoutFn).Get(ctx, nil)
		return err
	}
	RegisterWorkflow(timeoutWf)

	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.ExecuteWorkflow(timeoutWf)
	err := wfEnv.GetWorkflowError()
	s.Error(err)
	timeoutErr, ok := err.(*TimeoutError)
	s.True(ok)
	s.Equal(enums.TimeoutTypeStartToClose, timeoutErr.TimeoutType())
	s.True(timeoutErr.HasDetails())
	var details string
	err = timeoutErr.Details(&details)
	s.NoError(err)
	s.Equal("context deadline exceeded", details)
}

func (s *WorkflowTestSuiteUnitTest) Test_AwaitWithTimeoutTimeout() {
	workflowFn := func(ctx Context) (bool, error) {
		return AwaitWithTimeout(ctx, time.Second, func() bool { return false })
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	result := true
	_ = env.GetWorkflowResult(&result)
	s.False(result)
}
