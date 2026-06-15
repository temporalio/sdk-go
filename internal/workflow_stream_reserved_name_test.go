package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsWorkflowStreamReservedName(t *testing.T) {
	require.True(t, isWorkflowStreamReservedName("__temporal_workflow_stream_publish"))
	require.True(t, isWorkflowStreamReservedName("__temporal_workflow_stream_poll"))
	require.True(t, isWorkflowStreamReservedName("__temporal_workflow_stream_offset"))
	require.False(t, isWorkflowStreamReservedName("__temporal_"))
	require.False(t, isWorkflowStreamReservedName("__temporal_foo"))
	require.False(t, isWorkflowStreamReservedName("__internal"))
	require.False(t, isWorkflowStreamReservedName("events"))
}

// TestIsWorkflowStreamReservedNameAllowsHandlers verifies that the
// "__temporal_workflow_stream_" sub-namespace is permitted for signal, update,
// and query handler registration even though the "__temporal_"/"__" prefixes
// are otherwise reserved. This backs the workflowstreams contrib package.
func TestIsWorkflowStreamReservedNameAllowsHandlers(t *testing.T) {
	var ts WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	wf := func(ctx Context) error {
		// Signal channel registration must not panic on the workflow-stream name.
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

// TestIsWorkflowStreamReservedNameStillRejectsOtherNames verifies that names outside the
// workflow-stream sub-namespace are still rejected.
func TestIsWorkflowStreamReservedNameStillRejectsOtherNames(t *testing.T) {
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
