package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/common/metrics"
)

func TestDeclareRegisteredWorkflowOutcomeCounters(t *testing.T) {
	capture := metrics.NewCapturingHandler()
	metrics.DeclareRegisteredWorkflowOutcomeCounters(capture, []string{"OrderWorkflow", "PaymentWorkflow"})

	require.Len(t, capture.Counters(), 8)

	counterNames := map[string]int{
		metrics.WorkflowCompletedCounter:     0,
		metrics.WorkflowCanceledCounter:      0,
		metrics.WorkflowFailedCounter:        0,
		metrics.WorkflowContinueAsNewCounter: 0,
	}
	workflowTypes := map[string]int{
		"OrderWorkflow":   0,
		"PaymentWorkflow": 0,
	}

	for _, counter := range capture.Counters() {
		require.Equal(t, int64(0), counter.Value())
		counterNames[counter.Name]++
		wfType := counter.Tags[metrics.WorkflowTypeNameTagName]
		require.Contains(t, workflowTypes, wfType)
		workflowTypes[wfType]++
	}

	for name, count := range counterNames {
		require.Equal(t, 2, count, "expected 2 counters named %s", name)
	}
	for wfType, count := range workflowTypes {
		require.Equal(t, 4, count, "expected 4 counters for workflow type %s", wfType)
	}
}

func TestDeclareRegisteredWorkflowOutcomeCounters_NopHandler(t *testing.T) {
	capture := metrics.NewCapturingHandler()
	metrics.DeclareRegisteredWorkflowOutcomeCounters(metrics.NopHandler, []string{"OrderWorkflow"})
	require.Empty(t, capture.Counters())
}

func TestDeclareRegisteredWorkflowOutcomeCounters_EmptyWorkflowTypes(t *testing.T) {
	capture := metrics.NewCapturingHandler()
	metrics.DeclareRegisteredWorkflowOutcomeCounters(capture, nil)
	require.Empty(t, capture.Counters())
}
