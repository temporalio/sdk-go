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
	//
	// XXX(jlegrone): Any change to the idempotency key format in this test constitutes a breaking
	//                change to the SDK. This is because tracers that were using the old idempotency
	//                key format will generate new IDs for the same spans after updating, and that
	//                will lead to disconnected traces for workflows still running at the time of the
	//                SDK upgrade.
	//
	//                If possible, changing the idempotency keys should be avoided. Otherwise at a
	//                minimum, a description of this upgrade behavior should be included in the next
	//                SDK version's release notes.
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:1")
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:2")
}
