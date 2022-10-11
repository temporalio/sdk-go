package interceptor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.temporal.io/sdk/workflow"
)

func TestIdempotencyKeyGeneration(t *testing.T) {
	i := &tracingWorkflowInboundInterceptor{
		root: nil,
		info: &workflow.Info{
			Namespace: "default",
			WorkflowExecution: workflow.Execution{
				ID:    "foo",
				RunID: "bar",
			},
		},
	}

	// Verify that each call to get an idempotency key results in a deterministic and different result.
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:1")
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:2")
}
