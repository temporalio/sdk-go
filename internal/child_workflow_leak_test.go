package internal_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func ChildWorkflow(ctx workflow.Context) error {
	return workflow.Sleep(ctx, 24*time.Hour)
}

func ParentWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteChildWorkflow(ctx, "ChildWorkflow", nil).Get(ctx, nil)
}

// Optional sanity test (no goleak)
func TestChildWorkflowLeak(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(ChildWorkflow)
	env.RegisterWorkflow(ParentWorkflow)

	env.ExecuteWorkflow(ParentWorkflow)

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
