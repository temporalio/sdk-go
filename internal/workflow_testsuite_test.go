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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSetMemoOnStart(t *testing.T) {
	t.Parallel()
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	memo := map[string]interface{}{
		"key": make(chan int),
	}
	err := env.SetMemoOnStart(memo)
	require.Error(t, err)

	memo = map[string]interface{}{
		"memoKey": "memo",
	}
	require.Nil(t, env.impl.workflowInfo.Memo)
	err = env.SetMemoOnStart(memo)
	require.NoError(t, err)
	require.NotNil(t, env.impl.workflowInfo.Memo)
}

func TestSetSearchAttributesOnStart(t *testing.T) {
	t.Parallel()
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	invalidSearchAttr := map[string]interface{}{
		"key": make(chan int),
	}
	err := env.SetSearchAttributesOnStart(invalidSearchAttr)
	require.Error(t, err)

	searchAttr := map[string]interface{}{
		"CustomIntField": 1,
	}
	err = env.SetSearchAttributesOnStart(searchAttr)
	require.NoError(t, err)
	require.NotNil(t, env.impl.workflowInfo.SearchAttributes)
}

func TestNoExplicitRegistrationRequired(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	activity := func(ctx context.Context, arg string) (string, error) { return arg + " World!", nil }
	env.RegisterActivity(activity)
	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
			ScheduleToStartTimeout: time.Hour,
		})
		var result string
		err := ExecuteActivity(ctx, activity, arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello World!", result)
}

func TestUnregisteredActivity(t *testing.T) {
	t.Parallel()
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	env.RegisterActivity(helloWorldAct)
	workflow := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
		})
		return ExecuteActivity(ctx, "unregistered").Get(ctx, nil)
	}
	env.RegisterWorkflow(workflow)
	env.ExecuteWorkflow(workflow)
	require.Error(t, env.GetWorkflowError())
	err := env.GetWorkflowError()
	require.Error(t, err)
	var workflowErr *WorkflowExecutionError
	require.True(t, errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var activityErr *ActivityError
	require.True(t, errors.As(err, &activityErr))

	err = errors.Unwrap(activityErr)
	var err1 *ApplicationError
	require.True(t, errors.As(err, &err1))

	require.True(t, strings.HasPrefix(err1.Error(), "unable to find activityType=unregistered"), err1.Error())
}

func namedActivity(ctx context.Context, arg string) (string, error) {
	return arg + " World!", nil
}

func TestLocalActivityExecutionByActivityName(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivity(namedActivity)
	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello World!", result)
}

func TestLocalActivityExecutionByActivityNameAlias(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivityWithOptions(namedActivity, RegisterActivityOptions{
		Name: "localActivity",
	})
	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "localActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello World!", result)
}

func TestLocalActivityExecutionByActivityNameAliasMissingRegistration(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "localActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NotNil(t, env.GetWorkflowError())
}

func TestWorkflowIDInsideTestWorkflow(t *testing.T) {
	var suite WorkflowTestSuite
	// Default ID
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		return "id is: " + GetWorkflowInfo(ctx).WorkflowExecution.ID, nil
	})
	require.NoError(t, env.GetWorkflowError())
	var str string
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "id is: "+defaultTestWorkflowID, str)

	// Custom ID
	env = suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "my-workflow-id"})
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		return "id is: " + GetWorkflowInfo(ctx).WorkflowExecution.ID, nil
	})
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "id is: my-workflow-id", str)
}

func TestWorkflowIDSignalWorkflowByID(t *testing.T) {
	var suite WorkflowTestSuite
	// Test SignalWorkflowByID works with custom ID
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		err := env.SignalWorkflowByID("my-workflow-id", "signal", "payload")
		require.NoError(t, err)
	}, time.Second)

	env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "my-workflow-id"})
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		var result string
		GetSignalChannel(ctx, "signal").Receive(ctx, &result)
		return "id is: " + GetWorkflowInfo(ctx).WorkflowExecution.ID, nil
	})
	require.NoError(t, env.GetWorkflowError())
	var str string
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "id is: my-workflow-id", str)
}

func TestWorkflowIDUpdateWorkflowByID(t *testing.T) {
	var suite WorkflowTestSuite
	// Test UpdateWorkflowByID works with custom ID
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		err := env.UpdateWorkflowByID("my-workflow-id", "update", "id", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		}, "input")
		require.NoError(t, err)
	}, time.Second)

	env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "my-workflow-id"})
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		var result string
		err := SetUpdateHandler(ctx, "update", func(ctx Context, input string) error {
			result = input
			return nil
		}, UpdateHandlerOptions{})
		if err != nil {
			return "", err
		}
		err = Await(ctx, func() bool { return result != "" })
		return result, err
	})
	require.NoError(t, env.GetWorkflowError())
	var str string
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "input", str)
}

func TestWorkflowStartTimeInsideTestWorkflow(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx Context) (int64, error) {
		return GetWorkflowInfo(ctx).WorkflowStartTime.Unix(), nil
	})
	require.NoError(t, env.GetWorkflowError())
	var timestamp int64
	require.NoError(t, env.GetWorkflowResult(&timestamp))
	require.Equal(t, env.Now().Unix(), timestamp)
}

func TestActivityAssertCalled(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivity(namedActivity)
	env.OnActivity(namedActivity, mock.Anything, mock.Anything).Return("Mock!", nil)

	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")

	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)

	require.Equal(t, "Mock!", result)
	env.AssertCalled(t, "namedActivity", mock.Anything, "Hello")
	env.AssertNotCalled(t, "namedActivity", mock.Anything, "Bye")
}

func TestActivityAssertNumberOfCalls(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivity(namedActivity)
	env.OnActivity(namedActivity, mock.Anything, mock.Anything).Return("Mock!", nil)

	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		_ = ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		_ = ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		_ = ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		return result, nil
	}, "Hello")

	require.NoError(t, env.GetWorkflowError())
	env.AssertNumberOfCalls(t, "namedActivity", 3)
	env.AssertNumberOfCalls(t, "otherActivity", 0)
}

func HelloWorkflow(_ Context, name string) (string, error) {
	return "", errors.New("unimplemented")
}

func TestWorkflowMockingWithoutRegistration(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	env.OnWorkflow(HelloWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx Context, person string) (string, error) {
			return "Hello " + person + "!", nil
		})
	env.ExecuteWorkflow("HelloWorkflow", "Temporal")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello Temporal!", result)
}

func TestActivityMockingByNameWithoutRegistrationFails(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	assert.Panics(t, func() { env.OnActivity("SayHello", mock.Anything, mock.Anything) }, "The code did not panic")
}

func TestMockCallWrapperNotBefore(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	env.RegisterActivity(namedActivity)

	c1 := env.OnActivity(namedActivity, mock.Anything, "call1").Return("result1", nil)
	env.OnActivity(namedActivity, mock.Anything, "call2").Return("result2", nil).NotBefore(c1)

	env.ExecuteWorkflow(func(ctx Context) error {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		return ExecuteLocalActivity(ctx, "namedActivity", "call2").Get(ctx, &result)
	})
	var expectedErr *PanicError
	require.ErrorAs(t, env.GetWorkflowError(), &expectedErr)
	require.ErrorContains(t, expectedErr, "Must not be called before")
}
