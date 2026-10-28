package test_test

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

func (ts *IntegrationTestSuite) TestSystemNexusSignalWithStartWorkflow() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	caller := "system-nexus-proto-caller"
	callee := "system-nexus-proto-callee"
	taskQueue := ts.taskQueueName + "-system-nexus-proto"
	id := uuid.NewString()
	callerID := caller + id
	calleeID := callee + id

	callerWorkflow := func(ctx workflow.Context, targetID, taskQueue string) (string, error) {
		var response workflow.SignalWithStartWorkflowResponse
		err := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
			ID:                       targetID,
			TaskQueue:                taskQueue,
			WorkflowExecutionTimeout: 20 * time.Second,
		}, "greeting", "hello", callee, "started").Get(ctx, &response)
		if err != nil {
			return "", err
		}
		return *response.RunID, nil
	}
	calleeWorkflow := func(ctx workflow.Context, input string) (string, error) {
		var signal string
		workflow.GetSignalChannel(ctx, "greeting").Receive(ctx, &signal)
		return input + ": " + signal, nil
	}

	c, w := ts.newSerCtxClientAndWorker(taskQueue)
	defer c.Close()
	w.RegisterWorkflowWithOptions(callerWorkflow, workflow.RegisterOptions{Name: caller})
	w.RegisterWorkflowWithOptions(calleeWorkflow, workflow.RegisterOptions{Name: callee})
	ts.NoError(w.Start())
	defer w.Stop()

	callerRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       callerID,
		TaskQueue:                taskQueue,
		WorkflowExecutionTimeout: 20 * time.Second,
	}, caller, calleeID, taskQueue)
	ts.NoError(err)

	var targetRunID string
	ts.NoError(callerRun.Get(ctx, &targetRunID))
	ts.NotEmpty(targetRunID)

	var result string
	ts.NoError(c.GetWorkflow(ctx, calleeID, targetRunID).Get(ctx, &result))
	ts.Equal("started: hello", result)

	history := c.GetWorkflowHistory(ctx, callerRun.GetID(), callerRun.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var scheduledInput *commonpb.Payload
	for history.HasNext() {
		event, err := history.Next()
		ts.NoError(err)
		if attrs := event.GetNexusOperationScheduledEventAttributes(); attrs != nil {
			scheduledInput = attrs.GetInput()
			break
		}
	}
	ts.NotNil(scheduledInput, "caller history must contain the system Nexus request")
	var request workflowservice.SignalWithStartWorkflowExecutionRequest
	ts.NoError(proto.Unmarshal(scheduledInput.GetData(), &request))
	ts.Len(request.GetInput().GetPayloads(), 1)
	ts.Equal(calleeID, string(request.GetInput().GetPayloads()[0].Metadata["ctx-signature"]))
}
