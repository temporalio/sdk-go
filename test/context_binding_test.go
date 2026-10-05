package test_test

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
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

// startContextWorker sets up a client to use our [testContextualConverter]
// and sets up a worker that registers workflowFunc and fails on panic.
// Returns the client and the task queue name.
func (ts *IntegrationTestSuite) startContextWorker(workflowFunc any) (client.Client, string) {
	ts.T().Helper()
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = testContextualConverter{DataConverter: converter.GetDefaultDataConverter()}
	})
	ts.NoError(err)
	ts.T().Cleanup(c.Close)
	taskQueue := "converter-context-" + uuid.NewString()
	w := worker.New(c, taskQueue, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	w.RegisterWorkflow(workflowFunc)
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

// Mutable side effects.

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
