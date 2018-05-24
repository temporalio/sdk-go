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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/zap"
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
	RegisterWorkflowWithOptions(testWorkflowHello, RegisterWorkflowOptions{Name: "testWorkflowHello"})
	RegisterWorkflow(testWorkflowHeartbeat)
	RegisterActivityWithOptions(testActivityHello, RegisterActivityOptions{Name: "testActivityHello"})
	RegisterActivity(testActivityHeartbeat)
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
	env.GetWorkflowResult(&result)
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
		ctx1 := WithDataConverter(ctx, newDefaultDataConverter()) // use another converter to run activity
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
	env.GetWorkflowResult(&result)
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
	env.GetWorkflowResult(&result)
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
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values) {
		var input string
		s.NoError(args.Get(&input))
		activityCalls = append(activityCalls, fmt.Sprintf("%s:%s", activityInfo.ActivityType.Name, input))
	})
	expectedCalls := []string{}
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
	env.GetWorkflowResult(&result)
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

		t1.Get(ctx, nil) // wait for the long timer to fire.

		return "expected", nil
	} // END of workflow code

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	env.GetWorkflowResult(&result)
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
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values) {
		var msg string
		s.NoError(args.Get(&msg))
		activityMap[msg] = activityInfo.ActivityID
	})
	env.SetOnActivityCompletedListener(func(activityInfo *ActivityInfo, result encoded.Value, err error) {
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
	blob.Get(&value)
	s.Equal(testValue, value)
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
	env.GetWorkflowResult(&result)
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

func testActivityHello(ctx context.Context, msg string) (string, error) {
	return "hello" + "_" + msg, nil
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
	env.SetOnChildWorkflowStartedListener(func(workflowInfo *WorkflowInfo, ctx Context, args encoded.Values) {
		childWorkflowName = workflowInfo.WorkflowType.Name
	})
	env.SetOnChildWorkflowCompletedListener(func(workflowInfo *WorkflowInfo, result encoded.Value, err error) {
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

		t1.Get(ctx, nil)
		f1.Get(ctx, nil)

		return nil
	}

	workflowFn := func(ctx Context) error {
		t1 := NewTimer(ctx, time.Minute)
		t2 := NewTimer(ctx, time.Minute*10)

		cwo := ChildWorkflowOptions{ExecutionStartToCloseTimeout: time.Minute}
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
	env.SetTestTimeout(time.Hour)

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

func (s *WorkflowTestSuiteUnitTest) Test_ActivityWithThriftTypes() {
	actualValues := []string{}
	retVal := &shared.WorkflowExecution{WorkflowId: common.StringPtr("retwID2"), RunId: common.StringPtr("retrID2")}

	// Passing one argument
	activitySingleFn := func(ctx context.Context, wf *shared.WorkflowExecution) (*shared.WorkflowExecution, error) {
		actualValues = append(actualValues, wf.GetWorkflowId())
		actualValues = append(actualValues, wf.GetRunId())
		return retVal, nil
	}
	RegisterActivity(activitySingleFn)

	input := &shared.WorkflowExecution{WorkflowId: common.StringPtr("wID1"), RunId: common.StringPtr("rID1")}
	env := s.NewTestActivityEnvironment()
	blob, err := env.ExecuteActivity(activitySingleFn, input)
	s.NoError(err)
	var ret *shared.WorkflowExecution
	blob.Get(&ret)
	s.Equal(retVal, ret)

	// Passing more than one argument
	activityDoubleArgFn := func(ctx context.Context, wf *shared.WorkflowExecution, t *shared.WorkflowType) (*shared.WorkflowExecution, error) {
		actualValues = append(actualValues, wf.GetWorkflowId())
		actualValues = append(actualValues, wf.GetRunId())
		actualValues = append(actualValues, t.GetName())
		return retVal, nil
	}
	RegisterActivity(activityDoubleArgFn)

	input = &shared.WorkflowExecution{WorkflowId: common.StringPtr("wID2"), RunId: common.StringPtr("rID3")}
	wt := &shared.WorkflowType{Name: common.StringPtr("wType")}
	env = s.NewTestActivityEnvironment()
	blob, err = env.ExecuteActivity(activityDoubleArgFn, input, wt)
	s.NoError(err)
	blob.Get(&ret)
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
	encodedValue.Get(&output)
	s.Equal(input, output)

	encodedValue, err = env.ExecuteActivity(activityAlias, input)
	s.NoError(err)
	output = ""
	encodedValue.Get(&output)
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
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values) {
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
	env.SetOnChildWorkflowStartedListener(func(workflowInfo *WorkflowInfo, ctx Context, args encoded.Values) {
		called = append(called, workflowInfo.WorkflowType.Name)
	})

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(2, len(called))
	s.Equal("testWorkflowHello", called[0])
	s.Equal("testWorkflowHello", called[1])
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
	s.Contains(env.GetWorkflowError().Error(), "Unable to find activityType")
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowFullyQualifiedName() {
	defer func() {
		if r := recover(); r != nil {
			s.Contains(r.(error).Error(), "Unable to find workflow type")
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
		err := SetQueryHandler(ctx, queryType, func() (string, error) {
			return state, nil
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
		encodedValue, err := env.QueryWorkflow(queryType)
		s.NoError(err)
		var state string
		err = encodedValue.Get(&state)
		s.NoError(err)
		s.Equal(expected, state)
	}
	env.RegisterDelayedCallback(func() {
		verifyStateWithQuery(stateWaitSignal)
		env.SignalWorkflow("query-signal", "hello-query")
	}, time.Hour)
	env.OnActivity(testActivityHello, mock.Anything, mock.Anything).After(time.Hour).Return("hello_mock", nil)
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values) {
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
	var startedCount, completedCount, canceledCount int
	env.SetOnLocalActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args []interface{}) {
		startedCount++
	})

	env.SetOnLocalActivityCompletedListener(func(activityInfo *ActivityInfo, result encoded.Value, err error) {
		s.NoError(err)
		var resultValue string
		err = result.Get(&resultValue)
		s.NoError(err)
		s.Equal("hello mock", resultValue)
		completedCount++
	})

	env.SetOnLocalActivityCanceledListener(func(activityInfo *ActivityInfo) {
		canceledCount++
	})

	env.ExecuteWorkflow(workflowFn)
	env.AssertExpectations(s.T())
	s.Equal(2, startedCount)
	s.Equal(1, completedCount)
	s.Equal(1, canceledCount)
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
			Domain: "test-domain",
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
			Domain: "test-domain",
			ExecutionStartToCloseTimeout: time.Minute,
		}

		childCtx := WithChildWorkflowOptions(ctx, cwo)
		childCtx, cancel := WithCancel(childCtx)
		childFuture := ExecuteChildWorkflow(childCtx, childWorkflowFn)
		Sleep(ctx, 2*time.Second)
		cancel()

		err := childFuture.Get(childCtx, nil)
		if _, ok := err.(*CanceledError); !ok {
			return fmt.Errorf("Cancel child workflow should receive CanceledError, instead got: %v", err)
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

		return nil
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

func (s *WorkflowTestSuiteUnitTest) Test_SignalChannel() {
	workflowFn := func(ctx Context) error {
		signalCh := GetSignalChannel(ctx, "test-signal")
		encodedValue, _ := signalCh.ReceiveEncodedValue(ctx)

		var signal string
		err := encodedValue.Get(&signal)
		return err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("test-signal", 123)
	}, time.Minute)

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
	s.Contains(env.GetWorkflowError().Error(), "decode")
}

func (s *WorkflowTestSuiteUnitTest) Test_ContextMisuse() {
	workflowFn := func(ctx Context) error {
		ch := NewChannel(ctx)

		Go(ctx, func(shouldUseThisCtx Context) {
			Sleep(ctx, time.Hour)
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
