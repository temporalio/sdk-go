package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReservedNamePrefixException(t *testing.T) {
	require.True(t, isReservedNamePrefixException("__temporal_workflow_stream_publish"))
	require.True(t, isReservedNamePrefixException("__temporal_workflow_stream_poll"))
	require.True(t, isReservedNamePrefixException("__temporal_workflow_stream_offset"))
	require.False(t, isReservedNamePrefixException("__temporal_"))
	require.False(t, isReservedNamePrefixException("__temporal_foo"))
	require.False(t, isReservedNamePrefixException("__internal"))
	require.False(t, isReservedNamePrefixException("events"))
}

// TestReservedNamePrefixExceptionAllowsWorkflowStreamHandlers verifies that the
// "__temporal_workflow_stream_" sub-namespace is permitted for signal, update,
// and query handler registration even though the "__temporal_"/"__" prefixes
// are otherwise reserved. This backs the workflowstreams contrib package.
func TestReservedNamePrefixExceptionAllowsWorkflowStreamHandlers(t *testing.T) {
	var ts WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	wf := func(ctx Context) error {
		// Signal channel registration must not panic on the exception name.
		_ = GetSignalChannel(ctx, "__temporal_workflow_stream_publish")
		if err := SetUpdateHandler(ctx, "__temporal_workflow_stream_poll",
			func(ctx Context) error { return nil }, UpdateHandlerOptions{}); err != nil {
			return err
		}
		return SetQueryHandler(ctx, "__temporal_workflow_stream_offset",
			func() (int, error) { return 0, nil })
	}

	env.ExecuteWorkflow(wf)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestReservedNamePrefixStillRejectsOtherNames verifies that names outside the
// exception sub-namespace are still rejected.
func TestReservedNamePrefixStillRejectsOtherNames(t *testing.T) {
	var ts WorkflowTestSuite

	t.Run("signal panics", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		wf := func(ctx Context) error {
			_ = GetSignalChannel(ctx, "__temporal_other")
			return nil
		}
		env.ExecuteWorkflow(wf)
		require.True(t, env.IsWorkflowCompleted())
		require.Error(t, env.GetWorkflowError())
	})

	t.Run("update rejected", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		var updateErr error
		wf := func(ctx Context) error {
			updateErr = SetUpdateHandler(ctx, "__temporal_other",
				func(ctx Context) error { return nil }, UpdateHandlerOptions{})
			return nil
		}
		env.ExecuteWorkflow(wf)
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		require.Error(t, updateErr)
	})

	t.Run("query rejected", func(t *testing.T) {
		env := ts.NewTestWorkflowEnvironment()
		var queryErr error
		wf := func(ctx Context) error {
			queryErr = SetQueryHandler(ctx, "__temporal_other",
				func() (int, error) { return 0, nil })
			return nil
		}
		env.ExecuteWorkflow(wf)
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		require.Error(t, queryErr)
	})
}
