package test_test

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// These tests exercise delivery of application context to a converter, not
// encryption or authorization. The caller supplies an account ID, as it might
// when selecting an account-specific schema. Namespace and workflow ID alone
// cannot supply this business value.
//
// WithContext and WithWorkflowContext receive the Go or workflow context,
// including application values. WithSerializationContext instead receives SDK
// metadata about the serialization location: for workflow payloads, only the
// namespace and workflow ID.
type testUserIDContextKey struct{}

// testUserPayload identifies the application input or result whose conversion
// requires an account ID. The update handler's wait flag and the mutable side
// effect marker's string ID and integer counter do not require application context.
type testUserPayload string

// testContextRequiredConverter checks context delivery, then uses ordinary JSON
// conversion. It deliberately does not implement account-specific schemas.
type testContextRequiredConverter struct {
	// DataConverter handles the actual encoding and decoding of all values.
	converter.DataConverter
	// userID is the caller's account ID copied when the SDK binds the converter.
	// Each binding returns a copy so concurrent requests do not share this value.
	userID string
}

func (dc testContextRequiredConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.userID, _ = ctx.Value(testUserIDContextKey{}).(string)
	return dc
}

func (dc testContextRequiredConverter) WithWorkflowContext(ctx workflow.Context) converter.DataConverter {
	dc.userID, _ = ctx.Value(testUserIDContextKey{}).(string)
	return dc
}

func (dc testContextRequiredConverter) ToPayload(value any) (*commonpb.Payload, error) {
	_, applicationValue := value.(testUserPayload)
	if applicationValue && dc.userID == "" {
		return nil, fmt.Errorf("encode application payload %T: expected nonempty user ID under context key %T, got user ID %q", value, testUserIDContextKey{}, dc.userID)
	}
	payload, err := dc.DataConverter.ToPayload(value)
	if err != nil {
		return nil, fmt.Errorf("encode payload %T for user ID %q using default converter: %w", value, dc.userID, err)
	}
	return payload, nil
}

func (dc testContextRequiredConverter) FromPayload(payload *commonpb.Payload, value any) error {
	_, applicationValue := value.(*testUserPayload)
	if applicationValue && dc.userID == "" {
		return fmt.Errorf("decode application payload into %T: expected nonempty user ID under context key %T", value, testUserIDContextKey{})
	}
	if err := dc.DataConverter.FromPayload(payload, value); err != nil {
		return fmt.Errorf("decode payload into %T for context user ID %q using default converter: %w", value, dc.userID, err)
	}
	return nil
}

func (dc testContextRequiredConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	payloads := &commonpb.Payloads{}
	for i, value := range values {
		payload, err := dc.ToPayload(value)
		if err != nil {
			return nil, fmt.Errorf("encode payload at index %d: %w", i, err)
		}
		payloads.Payloads = append(payloads.Payloads, payload)
	}
	return payloads, nil
}

func (dc testContextRequiredConverter) FromPayloads(payloads *commonpb.Payloads, values ...any) error {
	for i, value := range values {
		if i >= len(payloads.GetPayloads()) {
			break
		}
		if err := dc.FromPayload(payloads.Payloads[i], value); err != nil {
			return fmt.Errorf("decode payload at index %d: %w", i, err)
		}
	}
	return nil
}

// testUserIDHeader carries the caller's account ID to the workflow context.
// Result, query, update, and mutable side effect workflows need it to convert
// application values. The termination test converts details only on the client;
// its workflow does not need the account ID.
const testUserIDHeader = "converter-binding-user-id"

type testUserIDPropagator struct{}

func (testUserIDPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	return injectTestUserID(ctx.Value(testUserIDContextKey{}), writer)
}

func (testUserIDPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	return injectTestUserID(ctx.Value(testUserIDContextKey{}), writer)
}

func injectTestUserID(value any, writer workflow.HeaderWriter) error {
	userID, ok := value.(string)
	if !ok || userID == "" {
		return fmt.Errorf("inject header %q: expected nonempty string user ID under context key %T, got %T (%v)", testUserIDHeader, testUserIDContextKey{}, value, value)
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(userID)
	if err != nil {
		return fmt.Errorf("inject header %q for user ID %q: %w", testUserIDHeader, userID, err)
	}
	writer.Set(testUserIDHeader, payload)
	return nil
}

func (testUserIDPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	userID, err := extractTestUserID(reader)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, testUserIDContextKey{}, userID), nil
}

func (testUserIDPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	userID, err := extractTestUserID(reader)
	if err != nil {
		return ctx, err
	}
	return workflow.WithValue(ctx, testUserIDContextKey{}, userID), nil
}

func extractTestUserID(reader workflow.HeaderReader) (string, error) {
	payload, ok := reader.Get(testUserIDHeader)
	if !ok {
		return "", fmt.Errorf("extract user ID into context key %T: expected header %q, got no header", testUserIDContextKey{}, testUserIDHeader)
	}
	var userID string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &userID); err != nil {
		return "", fmt.Errorf("extract header %q into string user ID under context key %T: %w", testUserIDHeader, testUserIDContextKey{}, err)
	}
	if userID == "" {
		return "", fmt.Errorf("extract header %q: expected nonempty string user ID, got %q", testUserIDHeader, userID)
	}
	return userID, nil
}

func (ts *IntegrationTestSuite) startUserContextWorker(workflowFunc any) (client.Client, string) {
	ts.T().Helper()
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = testContextRequiredConverter{DataConverter: converter.GetDefaultDataConverter()}
		options.ContextPropagators = []workflow.ContextPropagator{testUserIDPropagator{}}
	})
	ts.NoError(err)
	ts.T().Cleanup(c.Close)
	taskQueue := "user-context-" + uuid.NewString()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(workflowFunc)
	ts.NoError(w.Start())
	ts.T().Cleanup(w.Stop)
	return c, taskQueue
}

// Workflow results.

func userContextResultWorkflow(ctx workflow.Context) (testUserPayload, error) {
	return testUserPayload(ctx.Value(testUserIDContextKey{}).(string)), nil
}

// testAccountResultConverter adds a result-specific business rule: this workflow
// returns the account ID, which must match the account requesting the result.
// This is a decoding check, not encryption or an authorization mechanism.
type testAccountResultConverter struct {
	testContextRequiredConverter
}

func (dc testAccountResultConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.userID, _ = ctx.Value(testUserIDContextKey{}).(string)
	return dc
}

func (dc testAccountResultConverter) FromPayloads(payloads *commonpb.Payloads, values ...any) error {
	if err := dc.testContextRequiredConverter.FromPayloads(payloads, values...); err != nil {
		return err
	}
	for _, value := range values {
		if result, ok := value.(*testUserPayload); ok && string(*result) != dc.userID {
			return fmt.Errorf("decode account result: context account ID %q does not match result account ID %q", dc.userID, *result)
		}
	}
	return nil
}

func (ts *IntegrationTestSuite) TestConverterContext_WorkflowResult() {
	c, taskQueue := ts.startUserContextWorker(userContextResultWorkflow)
	resultClient, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = testAccountResultConverter{
			testContextRequiredConverter{DataConverter: converter.GetDefaultDataConverter()},
		}
	})
	ts.NoError(err)
	ts.T().Cleanup(resultClient.Close)
	baseCtx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	for _, userID := range []string{"user-alice", "user-bob"} {
		ctx := context.WithValue(baseCtx, testUserIDContextKey{}, userID)
		run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID: "user-result-" + uuid.NewString(), TaskQueue: taskQueue,
		}, userContextResultWorkflow)
		ts.NoError(err)
		// GetWorkflow does not bind application context; Get does.
		result := resultClient.GetWorkflow(baseCtx, run.GetID(), run.GetRunID())
		var got testUserPayload
		ts.Error(result.Get(baseCtx, &got))
		wrongCtx := context.WithValue(baseCtx, testUserIDContextKey{}, "wrong-user")
		ts.ErrorContains(result.Get(wrongCtx, &got), "does not match result account ID")
		ts.NoError(result.Get(ctx, &got))
		ts.Equal(testUserPayload(userID), got)
	}
}

// Queries.

func userContextQueryWorkflow(ctx workflow.Context) error {
	if err := workflow.SetQueryHandler(ctx, "query", func(input testUserPayload) (testUserPayload, error) {
		return "query:" + input, nil
	}); err != nil {
		return fmt.Errorf("register query handler for context user ID %q, expecting %T input and result: %w", ctx.Value(testUserIDContextKey{}), testUserPayload(""), err)
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return nil
}

func (ts *IntegrationTestSuite) TestConverterContext_Query() {
	c, taskQueue := ts.startUserContextWorker(userContextQueryWorkflow)
	baseCtx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	ctx := context.WithValue(baseCtx, testUserIDContextKey{}, "user-alice")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "user-query-" + uuid.NewString(), TaskQueue: taskQueue,
	}, userContextQueryWorkflow)
	ts.NoError(err)
	query, err := c.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "query", testUserPayload("input"))
	ts.NoError(err)
	var got testUserPayload
	ts.NoError(query.Get(&got))
	ts.Equal(testUserPayload("query:input"), got)
	missingContextQuery, err := c.QueryWorkflow(baseCtx, run.GetID(), run.GetRunID(), "query", testUserPayload("input"))
	ts.Error(err)
	ts.Nil(missingContextQuery)
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
	ts.NoError(run.Get(ctx, nil))
}

// Updates, including deferred result polling.

func userContextUpdateWorkflow(ctx workflow.Context) error {
	if err := workflow.SetUpdateHandler(ctx, "update", func(ctx workflow.Context, input testUserPayload, wait bool) (testUserPayload, error) {
		if wait {
			workflow.GetSignalChannel(ctx, "release").Receive(ctx, nil)
		}
		return "update:" + input, nil
	}); err != nil {
		return fmt.Errorf("register update handler for context user ID %q, expecting %T input and result: %w", ctx.Value(testUserIDContextKey{}), testUserPayload(""), err)
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) })
}

func (ts *IntegrationTestSuite) TestConverterContext_CompletedUpdate() {
	c, taskQueue := ts.startUserContextWorker(userContextUpdateWorkflow)
	baseCtx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	ctx := context.WithValue(baseCtx, testUserIDContextKey{}, "user-alice")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "user-update-" + uuid.NewString(), TaskQueue: taskQueue,
	}, userContextUpdateWorkflow)
	ts.NoError(err)
	completed, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "completed",
		UpdateName: "update", Args: []any{testUserPayload("completed"), false},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	ts.NoError(err)
	// A completed handle retains the UpdateWorkflow call's converter, even
	// when Get receives a context without a user ID.
	var got testUserPayload
	ts.NoError(completed.Get(baseCtx, &got))
	ts.Equal(testUserPayload("update:completed"), got)
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
	ts.NoError(run.Get(ctx, nil))
}

func (ts *IntegrationTestSuite) TestConverterContext_UpdatePolling() {
	c, taskQueue := ts.startUserContextWorker(userContextUpdateWorkflow)
	baseCtx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	ctx := context.WithValue(baseCtx, testUserIDContextKey{}, "user-bob")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "user-update-poll-" + uuid.NewString(), TaskQueue: taskQueue,
	}, userContextUpdateWorkflow)
	ts.NoError(err)
	accepted, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "polled",
		UpdateName: "update", Args: []any{testUserPayload("polled"), true},
		WaitForStage: client.WorkflowUpdateStageAccepted,
	})
	ts.NoError(err)
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "release", nil))
	var got testUserPayload
	ts.NoError(accepted.Get(ctx, &got))
	ts.Equal(testUserPayload("update:polled"), got)
	retrieved := c.GetWorkflowUpdateHandle(client.GetWorkflowUpdateHandleOptions{
		WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "polled",
	})
	ts.NoError(retrieved.Get(ctx, &got))
	ts.Equal(testUserPayload("update:polled"), got)
	ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
	ts.NoError(run.Get(ctx, nil))
}

// Mutable side effects.

func userContextMutableSideEffectWorkflow(ctx workflow.Context) (testUserPayload, error) {
	var mutable testUserPayload
	for _, next := range []testUserPayload{"first", "second", "second"} {
		err := workflow.MutableSideEffect(ctx, "user-value", func(workflow.Context) any {
			return next
		}, func(a, b any) bool {
			return a.(testUserPayload) == b.(testUserPayload)
		}).Get(&mutable)
		if err != nil {
			return "", fmt.Errorf("decode mutable side effect for context user ID %q, expected value %q into %T: %w", ctx.Value(testUserIDContextKey{}), next, &mutable, err)
		}
		if mutable != next {
			return "", fmt.Errorf("mutable side effect for context user ID %q: expected value %q (%T), got %q (%T)", ctx.Value(testUserIDContextKey{}), next, next, mutable, mutable)
		}
	}
	return mutable, nil
}

func (ts *IntegrationTestSuite) TestConverterContext_MutableSideEffect() {
	c, taskQueue := ts.startUserContextWorker(userContextMutableSideEffectWorkflow)
	baseCtx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	ctx := context.WithValue(baseCtx, testUserIDContextKey{}, "user-alice")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "user-mutable-" + uuid.NewString(), TaskQueue: taskQueue,
	}, userContextMutableSideEffectWorkflow)
	ts.NoError(err)
	var got testUserPayload
	ts.NoError(run.Get(ctx, &got))
	ts.Equal(testUserPayload("second"), got)
}

// Termination details.

func userContextTerminationWorkflow(ctx workflow.Context) error {
	return workflow.Await(ctx, func() bool { return false })
}

func (ts *IntegrationTestSuite) TestConverterContext_Termination() {
	c, taskQueue := ts.startUserContextWorker(userContextTerminationWorkflow)
	baseCtx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	ctx := context.WithValue(baseCtx, testUserIDContextKey{}, "user-bob")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "user-termination-" + uuid.NewString(), TaskQueue: taskQueue,
	}, userContextTerminationWorkflow)
	ts.NoError(err)
	ts.NoError(c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test termination", testUserPayload("termination details")))
	history := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
	ts.True(history.HasNext())
	event, err := history.Next()
	ts.NoError(err)
	details := event.GetWorkflowExecutionTerminatedEventAttributes().GetDetails()
	ts.Len(details.GetPayloads(), 1)
	dc := testContextRequiredConverter{DataConverter: converter.GetDefaultDataConverter()}
	var got testUserPayload
	ts.Error(dc.FromPayloads(details, &got))
	ts.NoError(dc.WithContext(ctx).FromPayloads(details, &got))
	ts.Equal(testUserPayload("termination details"), got)
}
