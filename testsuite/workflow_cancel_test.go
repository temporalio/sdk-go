package testsuite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestWorkflowCancelWaitsForRunningActivityBeforeDefer(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	completed := make(chan string, 2)
	release := make(chan struct{})
	blockingActivity := func(ctx context.Context, label string, block bool) error {
		if block {
			<-release
		}
		completed <- label
		return nil
	}

	workflowFn := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			ScheduleToCloseTimeout: time.Minute,
		})
		defer func() {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			_ = workflow.ExecuteActivity(disconnectedCtx, blockingActivity, "deferred", false).Get(disconnectedCtx, &struct{}{})
		}()
		return workflow.ExecuteActivity(ctx, blockingActivity, "main", true).Get(ctx, &struct{}{})
	}

	env.RegisterWorkflow(workflowFn)
	env.RegisterActivity(blockingActivity)
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, 10*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		close(release)
	}, 20*time.Millisecond)
	env.RegisterDelayedCallback(func() {}, 30*time.Millisecond)
	env.ExecuteWorkflow(workflowFn)

	require.True(t, env.IsWorkflowCompleted())
	require.True(t, temporal.IsCanceledError(env.GetWorkflowError()))
	require.Equal(t, "main", <-completed)
	require.Equal(t, "deferred", <-completed)
}
