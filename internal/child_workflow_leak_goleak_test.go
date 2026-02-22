//go:build goleak
// +build goleak

package internal_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.uber.org/goleak"
)

func TestChildWorkflowLeak_Goleak(t *testing.T) {
	// IMPORTANT: do NOT IgnoreCurrent() if you want a real repro.
	defer goleak.VerifyNone(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(ChildWorkflow)
	env.RegisterWorkflow(ParentWorkflow)

	env.ExecuteWorkflow(ParentWorkflow)

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
