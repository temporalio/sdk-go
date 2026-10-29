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
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

func (ts *IntegrationTestSuite) TestWorkflowSignalWithStart() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	id := uuid.NewString()
	numCalls := 5

	// Execute a workflow that uses signal-with-start to signal a workflow five times,
	// expecting the signalled workflow to start automatically.
	// The responses from each call are written into the responses array.
	var responses []workflow.SignalWithStartWorkflowResponse
	ts.NoError(ts.executeWorkflow(
		"caller" + id,
		ts.workflows.RepeatedlySignalAndStart,
		&responses,
		workflow.SignalWithStartWorkflowOptions{
			ID:                       "callee" + id,
			TaskQueue:                ts.taskQueueName,
			WorkflowExecutionTimeout: 20 * time.Second,
		},
		numCalls,
	))

	// Expect 5 responses, all with the same ID. The first response also started the workflow.
	ts.Len(responses, numCalls)
	for i := range responses {
		if i == 0 {
			ts.True(*responses[0].Started)
			continue
		}
		ts.False(*responses[i].Started)
		ts.Equal(*responses[0].RunID, *responses[i].RunID)
	}

	// The callee returns an array with all its signals.
	var wantSignals, gotSignals []int
	ts.NoError(ts.client.GetWorkflow(ctx, "callee" + id, *responses[0].RunID).Get(ctx, &gotSignals))
	for i := range responses {
		wantSignals = append(wantSignals, i)
	}
	ts.Equal(wantSignals, gotSignals)
}

func (ts *IntegrationTestSuite) TestWorkflowSignalWithStartOptions() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	taskQueue := ts.taskQueueName + "-system-nexus-proto"
	id := uuid.NewString()

	c, w := ts.newSerCtxClientAndWorker(taskQueue)
	defer c.Close()
	w.RegisterWorkflow(ts.workflows.RepeatedlySignalAndStartWithOptions)
	w.RegisterWorkflow(ts.workflows.RepeatedlyReceiveSignal)
	ts.NoError(w.Start())
	defer w.Stop()

	callerRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       "caller" + id,
		TaskQueue:                taskQueue,
		WorkflowExecutionTimeout: 20 * time.Second,
	}, ts.workflows.RepeatedlySignalAndStartWithOptions, "callee" + id, taskQueue, 1)
	ts.NoError(err)

	var responses []workflow.SignalWithStartWorkflowResponse
	ts.NoError(callerRun.Get(ctx, &responses))
	ts.Len(responses, 1)
	ts.True(*responses[0].Started)
	targetRunID := *responses[0].RunID
	ts.NotEmpty(targetRunID)

	var result []int
	ts.NoError(c.GetWorkflow(ctx, "callee" + id, targetRunID).Get(ctx, &result))
	ts.Equal([]int{0}, result)

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
	ts.Equal("callee" + id, string(request.GetInput().GetPayloads()[0].Metadata["ctx-signature"]))
	ts.Len(request.GetSignalInput().GetPayloads(), 1)
	ts.Equal("callee" + id, string(request.GetSignalInput().GetPayloads()[0].Metadata["ctx-signature"]))
	ts.Equal("callee" + id, string(request.GetMemo().GetFields()["memo"].Metadata["ctx-signature"]))
	ts.Equal("callee" + id, string(request.GetUserMetadata().GetSummary().GetMetadata()["ctx-signature"]))
	ts.Equal("callee" + id, string(request.GetUserMetadata().GetDetails().GetMetadata()["ctx-signature"]))
	ts.Equal(20*time.Second, request.GetWorkflowExecutionTimeout().AsDuration())
	ts.Equal(15*time.Second, request.GetWorkflowRunTimeout().AsDuration())
	ts.Equal(5*time.Second, request.GetWorkflowTaskTimeout().AsDuration())
	ts.Equal(enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY, request.GetWorkflowIdReusePolicy())
	ts.Equal(enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING, request.GetWorkflowIdConflictPolicy())
	ts.Equal(int32(1), request.GetRetryPolicy().GetMaximumAttempts())
	ts.Equal(time.Second, request.GetRetryPolicy().GetInitialInterval().AsDuration())
	ts.Equal(int32(2), request.GetPriority().GetPriorityKey())
	ts.Equal(time.Second, request.GetWorkflowStartDelay().AsDuration())
	ts.Equal("Keyword", string(request.GetSearchAttributes().GetIndexedFields()["CustomKeywordField"].GetMetadata()["type"]))

	calleeHistory := c.GetWorkflowHistory(ctx, "callee" + id, targetRunID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	event, err := calleeHistory.Next()
	ts.NoError(err)
	started := event.GetWorkflowExecutionStartedEventAttributes()
	ts.NotNil(started)
	ts.Equal(time.Second, started.GetFirstWorkflowTaskBackoff().AsDuration())
	ts.Equal(20*time.Second, started.GetWorkflowExecutionTimeout().AsDuration())
	ts.Equal(15*time.Second, started.GetWorkflowRunTimeout().AsDuration())
	ts.Equal(5*time.Second, started.GetWorkflowTaskTimeout().AsDuration())
	ts.Equal(time.Second, started.GetRetryPolicy().GetInitialInterval().AsDuration())
	ts.Equal(int32(1), started.GetRetryPolicy().GetMaximumAttempts())
	ts.True(proto.Equal(request.GetMemo(), started.GetMemo()))
	ts.True(proto.Equal(request.GetSearchAttributes(), started.GetSearchAttributes()))
	ts.True(proto.Equal(request.GetPriority(), started.GetPriority()))
	ts.True(proto.Equal(request.GetUserMetadata(), event.GetUserMetadata()))
}

func (ts *IntegrationTestSuite) TestSignalWithStartCron() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	calleeID := "signal-with-start-cron-" + uuid.NewString()
	// A cron execution remains open after the first run completes.
	defer func() {
		ts.NoError(ts.client.TerminateWorkflow(ts.T().Context(), calleeID, "", "test cleanup"))
	}()
	var responses []workflow.SignalWithStartWorkflowResponse
	ts.NoError(ts.executeWorkflow(
		"signal-with-start-cron-caller-"+uuid.NewString(),
		ts.workflows.RepeatedlySignalAndStart,
		&responses,
		workflow.SignalWithStartWorkflowOptions{
			ID: calleeID, TaskQueue: ts.taskQueueName, CronSchedule: "@every 1h",
		},
		1,
	))
	ts.Len(responses, 1)
	ts.True(*responses[0].Started)
	history, err := ts.getHistory(calleeID, *responses[0].RunID)
	ts.NoError(err)
	ts.NotEmpty(history.Events)
	started := history.Events[0].GetWorkflowExecutionStartedEventAttributes()
	ts.NotNil(started)
	ts.Equal("@every 1h", started.GetCronSchedule())
}

func (ts *IntegrationTestSuite) TestSignalWithStartInvalidConflictPolicy() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions("signal-with-start-invalid-policy-"+uuid.NewString()),
		ts.workflows.RepeatedlySignalAndStart,
		workflow.SignalWithStartWorkflowOptions{
			ID:                       "signal-with-start-invalid-target-" + uuid.NewString(),
			TaskQueue:                ts.taskQueueName,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		},
		1,
	)
	ts.NoError(err)
	defer func() {
		ts.NoError(ts.client.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test cleanup"))
	}()
	// Invalid System Nexus requests fail the workflow task, not the operation future.
	ts.waitForHistoryEvent(run.GetID(), run.GetRunID(), enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED, 10*time.Second)
	history, err := ts.getHistory(run.GetID(), run.GetRunID())
	ts.NoError(err)
	for _, event := range history.Events {
		if failed := event.GetWorkflowTaskFailedEventAttributes(); failed != nil {
			ts.Contains(failed.GetFailure().GetMessage(), "WORKFLOW_ID_CONFLICT_POLICY_FAIL")
			return
		}
	}
	ts.FailNow("missing workflow task failure")
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
