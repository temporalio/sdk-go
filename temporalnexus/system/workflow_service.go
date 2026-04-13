package system

import (
	"github.com/nexus-rpc/sdk-go/nexus"
	workflowservice "go.temporal.io/api/workflowservice/v1"
)

// WorkflowService contains references for Temporal's built-in workflow Nexus
// operations.
var WorkflowService = struct {
	ServiceName string

	SignalWithStartWorkflowExecution nexus.OperationReference[
		*workflowservice.SignalWithStartWorkflowExecutionRequest,
		*workflowservice.SignalWithStartWorkflowExecutionResponse,
	]
}{
	ServiceName: "WorkflowService",
	SignalWithStartWorkflowExecution: nexus.NewOperationReference[
		*workflowservice.SignalWithStartWorkflowExecutionRequest,
		*workflowservice.SignalWithStartWorkflowExecutionResponse,
	]("SignalWithStartWorkflowExecution"),
}
