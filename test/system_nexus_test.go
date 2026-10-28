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
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
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
			WorkflowRunTimeout:       15 * time.Second,
			WorkflowTaskTimeout:      5 * time.Second,
			RetryPolicy:              &temporal.RetryPolicy{InitialInterval: time.Second, MaximumAttempts: 1},
			Priority:                 &temporal.Priority{PriorityKey: 2},
			StartDelay:               time.Second,
			Memo:                     map[string]any{"memo": "memo value"},
			StaticSummary:            "summary",
			StaticDetails:            "details",
			TypedSearchAttributes: temporal.NewSearchAttributes(
				temporal.NewSearchAttributeKeyKeyword("CustomKeywordField").ValueSet("keyword"),
			),
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
	ts.Len(request.GetSignalInput().GetPayloads(), 1)
	ts.Equal(calleeID, string(request.GetSignalInput().GetPayloads()[0].Metadata["ctx-signature"]))
	ts.Equal(calleeID, string(request.GetMemo().GetFields()["memo"].Metadata["ctx-signature"]))
	ts.Equal(calleeID, string(request.GetUserMetadata().GetSummary().GetMetadata()["ctx-signature"]))
	ts.Equal(calleeID, string(request.GetUserMetadata().GetDetails().GetMetadata()["ctx-signature"]))
	ts.Equal(20*time.Second, request.GetWorkflowExecutionTimeout().AsDuration())
	ts.Equal(15*time.Second, request.GetWorkflowRunTimeout().AsDuration())
	ts.Equal(5*time.Second, request.GetWorkflowTaskTimeout().AsDuration())
	ts.Equal(int32(1), request.GetRetryPolicy().GetMaximumAttempts())
	ts.Equal(time.Second, request.GetRetryPolicy().GetInitialInterval().AsDuration())
	ts.Equal(int32(2), request.GetPriority().GetPriorityKey())
	ts.Equal(time.Second, request.GetWorkflowStartDelay().AsDuration())
	ts.Equal("Keyword", string(request.GetSearchAttributes().GetIndexedFields()["CustomKeywordField"].GetMetadata()["type"]))

	calleeHistory := c.GetWorkflowHistory(ctx, calleeID, targetRunID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	event, err := calleeHistory.Next()
	ts.NoError(err)
	ts.Equal(time.Second, event.GetWorkflowExecutionStartedEventAttributes().GetFirstWorkflowTaskBackoff().AsDuration())
}

func (ts *IntegrationTestSuite) TestSystemNexusSignalWithStartWorkflowExisting() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	caller := "system-nexus-existing-caller"
	callee := "system-nexus-existing-callee"
	taskQueue := ts.taskQueueName + "-system-nexus-existing"
	calleeID := callee + uuid.NewString()
	callerWorkflow := func(ctx workflow.Context) ([]workflow.SignalWithStartWorkflowResponse, error) {
		var responses []workflow.SignalWithStartWorkflowResponse
		for _, signal := range []string{"first", "second"} {
			var response workflow.SignalWithStartWorkflowResponse
			err := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
				ID: calleeID, TaskQueue: taskQueue, WorkflowExecutionTimeout: 20 * time.Second,
			}, "greeting", signal, callee).Get(ctx, &response)
			if err != nil {
				return nil, err
			}
			responses = append(responses, response)
		}
		return responses, nil
	}
	calleeWorkflow := func(ctx workflow.Context) ([]string, error) {
		var signals []string
		for range 2 {
			var signal string
			workflow.GetSignalChannel(ctx, "greeting").Receive(ctx, &signal)
			signals = append(signals, signal)
		}
		return signals, nil
	}

	c, w := ts.newSerCtxClientAndWorker(taskQueue)
	defer c.Close()
	w.RegisterWorkflowWithOptions(callerWorkflow, workflow.RegisterOptions{Name: caller})
	w.RegisterWorkflowWithOptions(calleeWorkflow, workflow.RegisterOptions{Name: callee})
	ts.NoError(w.Start())
	defer w.Stop()
	callerRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: caller + uuid.NewString(), TaskQueue: taskQueue,
	}, caller)
	ts.NoError(err)

	var responses []workflow.SignalWithStartWorkflowResponse
	ts.NoError(callerRun.Get(ctx, &responses))
	ts.Len(responses, 2)
	ts.True(*responses[0].Started)
	ts.False(*responses[1].Started)
	ts.Equal(*responses[0].RunID, *responses[1].RunID)
	var signals []string
	ts.NoError(c.GetWorkflow(ctx, calleeID, *responses[0].RunID).Get(ctx, &signals))
	ts.Equal([]string{"first", "second"}, signals)
}

func (ts *IntegrationTestSuite) TestSystemNexusSignalWithStartWorkflowEncodingError() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	caller := "system-nexus-encoding-caller"
	callee := "system-nexus-encoding-callee"
	taskQueue := ts.taskQueueName + "-system-nexus-encoding"
	calleeID := callee + uuid.NewString()
	callerWorkflow := func(ctx workflow.Context) error {
		return workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
			ID: calleeID, TaskQueue: taskQueue,
		}, "greeting", func() {}, callee).Get(ctx, nil)
	}
	c, w := ts.newSerCtxClientAndWorker(taskQueue)
	defer c.Close()
	w.RegisterWorkflowWithOptions(callerWorkflow, workflow.RegisterOptions{Name: caller})
	ts.NoError(w.Start())
	defer w.Stop()
	callerRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: caller + uuid.NewString(), TaskQueue: taskQueue,
	}, caller)
	ts.NoError(err)
	ts.ErrorContains(callerRun.Get(ctx, nil), "unsupported type")

	history := c.GetWorkflowHistory(ctx, callerRun.GetID(), callerRun.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for history.HasNext() {
		event, err := history.Next()
		ts.NoError(err)
		ts.Nil(event.GetNexusOperationScheduledEventAttributes(), "encoding failure must not schedule an operation")
	}
}

func (ts *IntegrationTestSuite) TestSystemNexusSignalWithStartWorkflowContextPropagation() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	caller := "system-nexus-context-caller"
	callee := "system-nexus-context-callee"
	taskQueue := ts.taskQueueName + "-system-nexus-context"
	calleeID := callee + uuid.NewString()
	callerWorkflow := func(ctx workflow.Context) (string, error) {
		var response workflow.SignalWithStartWorkflowResponse
		err := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
			ID: calleeID, TaskQueue: taskQueue, WorkflowExecutionTimeout: 20 * time.Second,
		}, "greeting", "hello", callee).Get(ctx, &response)
		if err != nil {
			return "", err
		}
		return *response.RunID, nil
	}
	calleeWorkflow := func(ctx workflow.Context) (string, error) {
		var signal string
		workflow.GetSignalChannel(ctx, "greeting").Receive(ctx, &signal)
		value, _ := ctx.Value(contextKey(testContextKey1)).(string)
		return value, nil
	}

	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.ContextPropagators = []workflow.ContextPropagator{NewKeysPropagator([]string{testContextKey1})}
	})
	ts.NoError(err)
	defer c.Close()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(callerWorkflow, workflow.RegisterOptions{Name: caller})
	w.RegisterWorkflowWithOptions(calleeWorkflow, workflow.RegisterOptions{Name: callee})
	ts.NoError(w.Start())
	defer w.Stop()
	ctx = context.WithValue(ctx, contextKey(testContextKey1), "propagated")
	callerRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: caller + uuid.NewString(), TaskQueue: taskQueue,
	}, caller)
	ts.NoError(err)

	var targetRunID string
	ts.NoError(callerRun.Get(ctx, &targetRunID))
	var result string
	ts.NoError(c.GetWorkflow(ctx, calleeID, targetRunID).Get(ctx, &result))
	ts.Equal("propagated", result)
}
