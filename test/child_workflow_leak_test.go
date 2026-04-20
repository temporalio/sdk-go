package test

import (
	"testing"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/goleak"
)

func ChildWorkflow(ctx workflow.Context) (string, error) {
	return "ok", nil
}

func ParentWorkflow(ctx workflow.Context) (string, error) {
	// Execute child by *registered name* (the path that triggered #2090).
	var out string
	if err := workflow.ExecuteChildWorkflow(ctx, "ChildWorkflow").Get(ctx, &out); err != nil {
		return "", err
	}
	return out, nil
}

func TestChildWorkflowByName_NoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	// Register child under the name we call above.
	env.RegisterWorkflowWithOptions(ChildWorkflow, workflow.RegisterOptions{Name: "ChildWorkflow"})
	env.RegisterWorkflow(ParentWorkflow)

	env.ExecuteWorkflow(ParentWorkflow)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
}