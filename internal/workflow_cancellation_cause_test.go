package internal

import (
	"testing"

	"github.com/stretchr/testify/require"

	historypb "go.temporal.io/api/history/v1"
)

func TestWorkflowExecutionCancelRequestedReason(t *testing.T) {
	for _, reason := range []string{"", "no longer needed", "  preserve whitespace  "} {
		t.Run(reason, func(t *testing.T) {
			env := &workflowEnvironmentImpl{}
			require.Empty(t, env.GetCancellationReason())
			called := false
			env.RegisterCancelHandler(func() {
				called = true
				require.Equal(t, reason, env.GetCancellationReason())
			})
			handler := &workflowExecutionEventHandlerImpl{workflowEnvironmentImpl: env}
			handler.handleWorkflowExecutionCancelRequested(createTestEventWorkflowExecutionCancelRequested(
				1, &historypb.WorkflowExecutionCancelRequestedEventAttributes{Cause: reason},
			))
			require.True(t, called)
			handler.handleWorkflowExecutionCancelRequested(createTestEventWorkflowExecutionCancelRequested(
				2, &historypb.WorkflowExecutionCancelRequestedEventAttributes{Cause: "ignored later reason"},
			))
			require.Equal(t, reason, env.GetCancellationReason())
		})
	}
}
