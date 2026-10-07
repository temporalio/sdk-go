package test_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// testContextKey is a place the user might store some contextual info.
type testContextKey struct{}

// testEncryptedString stands in for a value requiring a context-supplied key.
// A distinct type avoids requiring that key for the SDK's internal marker strings.
type testEncryptedString string

// testContextualConverter requires a key from testContextKey for testEncryptedString
// values. It checks context delivery; actual conversion uses JSON, not encryption.
type testContextualConverter struct {
	converter.DataConverter
	key string
}

func (dc testContextualConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.key, _ = ctx.Value(testContextKey{}).(string)
	return dc
}

func (dc testContextualConverter) WithWorkflowContext(ctx workflow.Context) converter.DataConverter {
	dc.key, _ = ctx.Value(testContextKey{}).(string)
	return dc
}

func (dc testContextualConverter) ToPayload(value any) (*commonpb.Payload, error) {
	if _, ok := value.(testEncryptedString); ok && dc.key == "" {
		return nil, fmt.Errorf("encode %T: converter did not receive a key from testContextKey", value)
	}
	return dc.DataConverter.ToPayload(value)
}

func (dc testContextualConverter) FromPayload(payload *commonpb.Payload, value any) error {
	if _, ok := value.(*testEncryptedString); ok && dc.key == "" {
		return fmt.Errorf("decode into %T: converter did not receive a key from testContextKey", value)
	}
	return dc.DataConverter.FromPayload(payload, value)
}

func (dc testContextualConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	for _, value := range values {
		if _, ok := value.(testEncryptedString); ok && dc.key == "" {
			return nil, fmt.Errorf("encode %T: converter did not receive a key from testContextKey", value)
		}
	}
	return dc.DataConverter.ToPayloads(values...)
}

func (dc testContextualConverter) FromPayloads(payloads *commonpb.Payloads, values ...any) error {
	for _, value := range values {
		if _, ok := value.(*testEncryptedString); ok && dc.key == "" {
			return fmt.Errorf("decode into %T: converter did not receive a key from testContextKey", value)
		}
	}
	return dc.DataConverter.FromPayloads(payloads, values...)
}

func (ts *IntegrationTestSuite) TestConverterContext_WorkerDeploymentMetadata() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, testContextKey{}, "key")
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = testContextualConverter{DataConverter: converter.GetDefaultDataConverter()}
	})
	ts.NoError(err)
	defer c.Close()
	name := "converter-context-" + uuid.NewString()
	handle := c.WorkerDeploymentClient().GetHandle(name)
	_, err = handle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		BuildID: "build", AllowNoPollers: true,
	})
	ts.NoError(err)
	_, err = handle.UpdateVersionMetadata(ctx, client.WorkerDeploymentUpdateVersionMetadataOptions{
		Version:        worker.WorkerDeploymentVersion{DeploymentName: name, BuildID: "build"},
		MetadataUpdate: client.WorkerDeploymentMetadataUpdate{UpsertEntries: map[string]any{"value": testEncryptedString("metadata")}},
	})
	ts.NoError(err)
}

// startContextWorker sets up a client to use our [testContextualConverter]
// and sets up a worker that registers workflowFunc and activities and fails on panic.
// Returns the client and the task queue name.
func (ts *IntegrationTestSuite) startContextWorker(workflowFunc any, activities ...any) (client.Client, string) {
	ts.T().Helper()
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = testContextualConverter{DataConverter: converter.GetDefaultDataConverter()}
	})
	ts.NoError(err)
	ts.T().Cleanup(c.Close)
	taskQueue := "converter-context-" + uuid.NewString()
	w := worker.New(c, taskQueue, worker.Options{
		WorkflowPanicPolicy:       worker.FailWorkflow,
		BackgroundActivityContext: context.WithValue(ts.T().Context(), testContextKey{}, "key"),
	})
	w.RegisterWorkflow(workflowFunc)
	for _, activity := range activities {
		w.RegisterActivity(activity)
	}
	ts.NoError(w.Start())
	ts.T().Cleanup(w.Stop)
	return c, taskQueue
}

// Workflow results.

func contextResultWorkflow(workflow.Context) (string, error) {
	return "result", nil
}

func (ts *IntegrationTestSuite) TestConverterContext_WorkflowResult() {
	c, taskQueue := ts.startContextWorker(contextResultWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextResultWorkflow)
	ts.NoError(err)
	var result testEncryptedString
	ts.NoError(run.Get(context.WithValue(ctx, testContextKey{}, "key"), &result))
}

func contextCancellationWorkflow(workflow.Context) error {
	return temporal.NewCanceledError("details")
}

func (ts *IntegrationTestSuite) TestConverterContext_CancellationDetails() {
	c, taskQueue := ts.startContextWorker(contextCancellationWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextCancellationWorkflow)
	ts.NoError(err)
	var canceled *temporal.CanceledError
	ts.ErrorAs(run.Get(context.WithValue(ctx, testContextKey{}, "key"), nil), &canceled)
	var details testEncryptedString
	ts.NoError(canceled.Details(&details))
}

// Metadata APIs decode ordinary strings, so this converter requires context for
// every single-payload decode rather than only testEncryptedString.
type testMetadataContextualConverter struct {
	testContextualConverter
}

func (dc testMetadataContextualConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.key, _ = ctx.Value(testContextKey{}).(string)
	return dc
}

func (dc testMetadataContextualConverter) FromPayload(payload *commonpb.Payload, value any) error {
	if dc.key == "" {
		return fmt.Errorf("decode metadata into %T: converter did not receive a key from testContextKey", value)
	}
	return dc.DataConverter.FromPayload(payload, value)
}

func (ts *IntegrationTestSuite) TestConverterContext_WorkflowMetadata() {
	c, taskQueue := ts.startContextWorker(contextResultWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, testContextKey{}, "key")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue: taskQueue, StaticSummary: "summary", StaticDetails: "details",
		Memo: map[string]any{"memo": testEncryptedString("memo")},
	}, contextResultWorkflow)
	ts.NoError(err)
	reader, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = testMetadataContextualConverter{
			testContextualConverter{DataConverter: converter.GetDefaultDataConverter()},
		}
	})
	ts.NoError(err)
	defer reader.Close()
	description, err := reader.DescribeWorkflow(ctx, run.GetID(), run.GetRunID())
	ts.NoError(err)
	_, err = description.GetStaticSummary()
	ts.NoError(err)
	_, err = description.GetStaticDetails()
	ts.NoError(err)
	var memo testEncryptedString
	ts.NoError(description.GetMemoValue("memo", &memo))
}

func (ts *IntegrationTestSuite) TestConverterContext_ScheduleMetadata() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, testContextKey{}, "key")
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = testMetadataContextualConverter{
			testContextualConverter{DataConverter: converter.GetDefaultDataConverter()},
		}
	})
	ts.NoError(err)
	defer c.Close()
	handle, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   "converter-context-" + uuid.NewString(),
		Spec: client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Hour}}},
		Action: &client.ScheduleWorkflowAction{
			Workflow: "contextResultWorkflow", TaskQueue: "unused",
			StaticSummary: "summary", StaticDetails: "details",
		},
	})
	ts.NoError(err)
	defer func() { ts.NoError(handle.Delete(ctx)) }()
	_, err = handle.Describe(ctx)
	ts.NoError(err)
	ts.NoError(handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			return nil, temporal.ErrSkipScheduleUpdate
		},
	}))
}

// Queries.

func contextQueryWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithValue(ctx, testContextKey{}, "key")
	if err := workflow.SetQueryHandler(ctx, "query", func(input testEncryptedString) (testEncryptedString, error) {
		return input, nil
	}); err != nil {
		return err
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return nil
}

func (ts *IntegrationTestSuite) TestConverterContext_Query() {
	c, taskQueue := ts.startContextWorker(contextQueryWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, testContextKey{}, "key")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextQueryWorkflow)
	ts.NoError(err)
	query, err := c.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "query", testEncryptedString("input"))
	ts.NoError(err)
	var result testEncryptedString
	ts.NoError(query.Get(&result))
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
}

// Updates, including deferred result polling.

func contextUpdateWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithValue(ctx, testContextKey{}, "key")
	if err := workflow.SetUpdateHandler(ctx, "update", func(ctx workflow.Context, input testEncryptedString, wait bool) (testEncryptedString, error) {
		if wait {
			workflow.GetSignalChannel(ctx, "release").Receive(ctx, nil)
		}
		return input, nil
	}); err != nil {
		return err
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) })
}

func (ts *IntegrationTestSuite) TestConverterContext_CompletedUpdate() {
	c, taskQueue := ts.startContextWorker(contextUpdateWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, testContextKey{}, "key")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextUpdateWorkflow)
	ts.NoError(err)
	completed, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "completed",
		UpdateName: "update", Args: []any{testEncryptedString("completed"), false},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	ts.NoError(err)
	var result testEncryptedString
	ts.NoError(completed.Get(ctx, &result))
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
}

func (ts *IntegrationTestSuite) TestConverterContext_UpdatePolling() {
	c, taskQueue := ts.startContextWorker(contextUpdateWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, testContextKey{}, "key")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextUpdateWorkflow)
	ts.NoError(err)
	accepted, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "polled",
		UpdateName: "update", Args: []any{testEncryptedString("polled"), true},
		WaitForStage: client.WorkflowUpdateStageAccepted,
	})
	ts.NoError(err)
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "release", nil))
	var result testEncryptedString
	ts.NoError(accepted.Get(ctx, &result))
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
}

// Side effects.

func contextSideEffectWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithValue(ctx, testContextKey{}, "key")
	var result testEncryptedString
	return workflow.SideEffect(ctx, func(workflow.Context) any {
		return testEncryptedString("value")
	}).Get(&result)
}

func (ts *IntegrationTestSuite) TestConverterContext_SideEffectMock() {
	c, taskQueue := ts.startContextWorker(contextSideEffectWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextSideEffectWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))

	// The same workflow must work when the side effect is mocked.
	for _, result := range []any{testEncryptedString("value"), func() any { return testEncryptedString("value") }} {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.SetDataConverter(testContextualConverter{DataConverter: converter.GetDefaultDataConverter()})
		env.OnSideEffect().Return(result)
		env.ExecuteWorkflow(contextSideEffectWorkflow)
		ts.NoError(env.GetWorkflowError())
	}
}

func contextMutableSideEffectWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithValue(ctx, testContextKey{}, "key")
	for i := 0; i < 2; i++ {
		value := workflow.MutableSideEffect(ctx, "value", func(workflow.Context) any {
			return testEncryptedString("value")
		}, func(a, b any) bool {
			return a == b
		})
		var result testEncryptedString
		if err := value.Get(&result); err != nil {
			return err
		}
	}
	return nil
}

func (ts *IntegrationTestSuite) TestConverterContext_MutableSideEffect() {
	c, taskQueue := ts.startContextWorker(contextMutableSideEffectWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextMutableSideEffectWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
}

// Activity heartbeat details.

func contextHeartbeatActivity(ctx context.Context) error {
	if activity.GetInfo(ctx).Attempt == 1 {
		activity.RecordHeartbeat(ctx, "progress")
		return fmt.Errorf("retry to read heartbeat details")
	}
	var details testEncryptedString
	return activity.GetHeartbeatDetails(ctx, &details)
}

func contextHeartbeatWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: ctxTimeout,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: time.Millisecond, MaximumAttempts: 2},
	})
	return workflow.ExecuteActivity(ctx, contextHeartbeatActivity).Get(ctx, nil)
}

func (ts *IntegrationTestSuite) TestConverterContext_ActivityHeartbeatDetails() {
	c, taskQueue := ts.startContextWorker(contextHeartbeatWorkflow, contextHeartbeatActivity)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextHeartbeatWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
}

// Activity cancellation details.

func contextCanceledActivity(ctx context.Context) error {
	info := activity.GetInfo(ctx)
	if err := activity.GetClient(ctx).SignalWorkflow(ctx, info.WorkflowExecution.ID, info.WorkflowExecution.RunID, "started", nil); err != nil {
		return err
	}
	// Heartbeats receive the cancellation request from the server.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		activity.RecordHeartbeat(ctx, "waiting")
		select {
		case <-ctx.Done():
			return temporal.NewCanceledError(testEncryptedString("details"))
		case <-ticker.C:
		}
	}
}

func contextActivityCancellationWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: ctxTimeout, HeartbeatTimeout: time.Second, WaitForCancellation: true,
		RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	activityCtx, cancel := workflow.WithCancel(ctx)
	result := workflow.ExecuteActivity(activityCtx, contextCanceledActivity)
	workflow.GetSignalChannel(ctx, "started").Receive(ctx, nil)
	cancel()
	err := result.Get(ctx, nil)
	var canceled *temporal.CanceledError
	if !errors.As(err, &canceled) {
		return fmt.Errorf("expected canceled activity, got %v", err)
	}
	var details string
	return canceled.Details(&details)
}

func (ts *IntegrationTestSuite) TestConverterContext_ActivityCancellationDetails() {
	c, taskQueue := ts.startContextWorker(contextActivityCancellationWorkflow, contextCanceledActivity)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextActivityCancellationWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
}

// Termination details.

func contextTerminationWorkflow(ctx workflow.Context) error {
	return workflow.Await(ctx, func() bool { return false })
}

func (ts *IntegrationTestSuite) TestConverterContext_Termination() {
	c, taskQueue := ts.startContextWorker(contextTerminationWorkflow)
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, testContextKey{}, "key")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, contextTerminationWorkflow)
	ts.NoError(err)
	ts.NoError(c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test termination", testEncryptedString("termination details")))
}

// Nexus input and synchronous result conversion.

type testNexusContextualConverter struct {
	testContextualConverter
}

func (dc testNexusContextualConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.key, _ = ctx.Value(testContextKey{}).(string)
	if nexus.IsHandlerContext(ctx) {
		dc.key = nexus.ExtractHandlerInfo(ctx).Operation
	}
	return dc
}

func (ts *IntegrationTestSuite) TestConverterContext_NexusInput() {
	skipOnCloud(ts.T(), cloudRequiresLocalServer, "creates a Nexus endpoint through Operator Service")
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	tc := newTestContext(ts.T(), ctx, withDataConverter(testNexusContextualConverter{
		testContextualConverter{DataConverter: converter.GetDefaultDataConverter()},
	}))
	defer tc.client.Close()
	service := nexus.NewService("test")
	ts.NoError(service.Register(nexus.NewSyncOperation("input",
		func(context.Context, testEncryptedString, nexus.StartOperationOptions) (string, error) {
			return "result", nil
		},
	)))
	w := worker.New(tc.client, tc.taskQueue, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	w.RegisterNexusService(service)
	wf := func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, testContextKey{}, "key")
		var result string
		return workflow.NewNexusClient(tc.endpoint, "test").ExecuteOperation(
			ctx, "input", testEncryptedString("input"), workflow.NexusOperationOptions{ScheduleToCloseTimeout: 5 * time.Second},
		).Get(ctx, &result)
	}
	w.RegisterWorkflow(wf)
	ts.NoError(w.Start())
	defer w.Stop()
	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: tc.taskQueue}, wf)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil), "Nexus worker logs: %v", tc.logger.Lines())
}

func (ts *IntegrationTestSuite) TestConverterContext_NexusResult() {
	skipOnCloud(ts.T(), cloudRequiresLocalServer, "creates a Nexus endpoint through Operator Service")
	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	tc := newTestContext(ts.T(), ctx, withDataConverter(testNexusContextualConverter{
		testContextualConverter{DataConverter: converter.GetDefaultDataConverter()},
	}))
	defer tc.client.Close()
	service := nexus.NewService("test")
	ts.NoError(service.Register(nexus.NewSyncOperation("result",
		func(context.Context, string, nexus.StartOperationOptions) (testEncryptedString, error) {
			return "result", nil
		},
	)))
	w := worker.New(tc.client, tc.taskQueue, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	w.RegisterNexusService(service)
	wf := func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, testContextKey{}, "key")
		var result testEncryptedString
		return workflow.NewNexusClient(tc.endpoint, "test").ExecuteOperation(
			ctx, "result", "input", workflow.NexusOperationOptions{ScheduleToCloseTimeout: 5 * time.Second},
		).Get(ctx, &result)
	}
	w.RegisterWorkflow(wf)
	ts.NoError(w.Start())
	defer w.Stop()
	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: tc.taskQueue}, wf)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil), "Nexus worker logs: %v", tc.logger.Lines())
}
