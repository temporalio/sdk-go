package internal

import (
	"context"
	"strings"
	"testing"
	"time"

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
	ee := env.GetWorkflowError()
	require.NotNil(t, ee)
	require.True(t, strings.HasPrefix(ee.Error(), "unable to find activityType=unregistered"), ee.Error())
}
