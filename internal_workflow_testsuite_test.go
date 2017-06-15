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

package cadence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type WorkflowTestSuiteUnitTest struct {
	suite.Suite
	WorkflowTestSuite
	activityOptions ActivityOptions
}

type testContextKey string

func (s *WorkflowTestSuiteUnitTest) SetupSuite() {
	s.activityOptions = ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	s.RegisterWorkflow(testWorkflowHello)
	s.RegisterWorkflow(testWorkflowHeartbeat)
	s.RegisterActivity(testActivityHello)
	s.RegisterActivity(testActivityHeartbeat)
}

func TestUnitTestSuite(t *testing.T) {
	suite.Run(t, new(WorkflowTestSuiteUnitTest))
}

func (s *WorkflowTestSuiteUnitTest) Test_ActivityOverride() {
	fakeActivity := func(ctx context.Context, msg string) (string, error) {
		return "fake_" + msg, nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.OverrideActivity(testActivityHello, fakeActivity)

	env.ExecuteWorkflow(testWorkflowHello)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal("fake_world", result)
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
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)

		for i := 1; i <= 3; i++ {
			err := ExecuteActivity(ctx, testActivityHello, fmt.Sprintf("msg%d", i)).Get(ctx, nil)
			if err != nil {
				return err
			}
		}
		return nil
	} // END of workflow code

	env := s.NewTestWorkflowEnvironment()

	var activityCalls []string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args EncodedValues) {
		var input string
		s.NoError(args.Get(&input))
		activityCalls = append(activityCalls, fmt.Sprintf("%s:%s", activityInfo.ActivityType.Name, input))
	})
	expectedCalls := []string{
		"go.uber.org/cadence.testActivityHello:msg1",
		"go.uber.org/cadence.testActivityHello:msg2",
		"go.uber.org/cadence.testActivityHello:msg3",
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
		if _, ok := err.(CanceledError); !ok {
			return err
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	activityMap := make(map[string]string) // msg -> activityID
	var completedActivityID, cancelledActivityID string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args EncodedValues) {
		var msg string
		s.NoError(args.Get(&msg))
		activityMap[msg] = activityInfo.ActivityID
	})
	env.SetOnActivityCompletedListener(func(activityInfo *ActivityInfo, result EncodedValue, err error) {
		completedActivityID = activityInfo.ActivityID
		fmt.Printf("OnActivityCompletedListener %+v", activityInfo)
	})
	env.SetOnActivityCanceledListener(func(activityInfo *ActivityInfo) {
		cancelledActivityID = activityInfo.ActivityID
		fmt.Printf("OnActivityCanceledListener %+v", activityInfo)
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

	env := s.NewTestActivityEnvironment()
	env.SetWorkerOption(workerOptions)
	blob, err := env.ExecuteActivity(activityWithUserContext, testKey)
	s.NoError(err)
	var value string
	blob.Get(&value)
	s.Equal(testValue, value)
}

func (s *WorkflowTestSuiteUnitTest) Test_CompleteActivity() {
	env := s.NewTestWorkflowEnvironment()
	var activityInfo ActivityInfo
	fakeActivity := func(ctx context.Context, msg string) (string, error) {
		activityInfo = GetActivityInfo(ctx)
		env.RegisterDelayedCallback(func() {
			err := env.CompleteActivity(activityInfo.TaskToken, "async_complete", nil)
			s.NoError(err)
		}, time.Minute)
		return "", ErrActivityResultPending
	}

	env.OverrideActivity(testActivityHello, fakeActivity)
	env.SetTestTimeout(time.Second * 2) // don't waist time waiting

	env.ExecuteWorkflow(testWorkflowHello) // workflow won't complete, as the fakeActivity returns ErrActivityResultPending
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result string
	env.GetWorkflowResult(&result)
	s.Equal("async_complete", result)
}

func (s *WorkflowTestSuiteUnitTest) Test_WorkflowCancellation() {
	workflowFn := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, s.activityOptions)
		f := ExecuteActivity(ctx, testActivityHeartbeat, "msg1", time.Second*10)
		err := f.Get(ctx, nil) // wait for result
		return err
	}

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
	_, ok := env.GetWorkflowError().(CanceledError)
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

	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
}

func (s *WorkflowTestSuiteUnitTest) Test_ChildWorkflow_Override() {
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

	env := s.NewTestWorkflowEnvironment()
	env.OverrideActivity(testActivityHello, func(ctx context.Context, msg string) (string, error) {
		return "fake_" + msg, nil
	})
	env.OverrideWorkflow(testWorkflowHeartbeat, func(ctx Context, msg string, waitTime time.Duration) (string, error) {
		return "fake_heartbeat", nil
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var actualResult string
	s.NoError(env.GetWorkflowResult(&actualResult))
	s.Equal("fake_activity fake_world fake_heartbeat", actualResult)
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

	env := s.NewTestWorkflowEnvironment()
	var childWorkflowName, childWorkflowResult string
	env.SetOnChildWorkflowStartedListener(func(workflowInfo *WorkflowInfo, ctx Context, args EncodedValues) {
		childWorkflowName = workflowInfo.WorkflowType.Name
	})
	env.SetOnChildWorkflowCompletedListener(func(workflowInfo *WorkflowInfo, result EncodedValue, err error) {
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
	s.Equal(getFunctionName(testWorkflowHello), childWorkflowName)
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

	s.RegisterWorkflow(workflowFn)
	s.RegisterWorkflow(childWorkflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.SetTestTimeout(time.Hour)

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(expected, history)
}
