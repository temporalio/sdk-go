package test_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	isReadOnlyQueryName  = "is-readonly"
	isReadOnlyUpdateName = "update"
	isReadOnlySignalName = "finish"
)

type isReadOnlyRecorder struct {
	calls map[string]bool
}

func (r *isReadOnlyRecorder) record(name string, ctx workflow.Context) {
	r.calls[name] = workflow.IsReadOnly(ctx)
}

// Shared with the workflow under test; reset in SetupTest.
var isReadOnlyRec *isReadOnlyRecorder

type isReadOnlyWorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
}

type isReadOnlyWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (i *isReadOnlyWorkerInterceptor) InterceptWorkflow(
	_ workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	return &isReadOnlyWorkflowInboundInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
	}
}

func (i *isReadOnlyWorkflowInboundInterceptor) ExecuteWorkflow(ctx workflow.Context, in *interceptor.ExecuteWorkflowInput) (any, error) {
	isReadOnlyRec.record("ExecuteWorkflow", ctx)
	return i.Next.ExecuteWorkflow(ctx, in)
}

func (i *isReadOnlyWorkflowInboundInterceptor) HandleSignal(ctx workflow.Context, in *interceptor.HandleSignalInput) error {
	isReadOnlyRec.record("HandleSignal", ctx)
	return i.Next.HandleSignal(ctx, in)
}

func (i *isReadOnlyWorkflowInboundInterceptor) HandleQuery(ctx workflow.Context, in *interceptor.HandleQueryInput) (any, error) {
	isReadOnlyRec.record("HandleQuery", ctx)
	return i.Next.HandleQuery(ctx, in)
}

func (i *isReadOnlyWorkflowInboundInterceptor) ValidateUpdate(ctx workflow.Context, in *interceptor.UpdateInput) error {
	isReadOnlyRec.record("ValidateUpdate", ctx)
	return i.Next.ValidateUpdate(ctx, in)
}

func (i *isReadOnlyWorkflowInboundInterceptor) ExecuteUpdate(ctx workflow.Context, in *interceptor.UpdateInput) (any, error) {
	isReadOnlyRec.record("ExecuteUpdate", ctx)
	return i.Next.ExecuteUpdate(ctx, in)
}

func isReadOnlyWorkflow(ctx workflow.Context) error {
	if err := workflow.SetQueryHandler(ctx, isReadOnlyQueryName, func() (bool, error) {
		isReadOnlyRec.record("query", ctx)
		return true, nil
	}); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx, isReadOnlyUpdateName,
		func(ctx workflow.Context) error {
			isReadOnlyRec.record("updateHandler", ctx)
			return nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context) error {
				isReadOnlyRec.record("validator", ctx)
				return nil
			},
		},
	); err != nil {
		return err
	}

	isReadOnlyRec.record("workflowTask", ctx)

	workflow.SideEffect(ctx, func(ctx workflow.Context) any {
		isReadOnlyRec.record("sideEffect", ctx)
		return nil
	}).Get(nil)

	workflow.GetSignalChannel(ctx, isReadOnlySignalName).Receive(ctx, nil)
	return nil
}

type WorkflowReadOnlyTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	worker worker.Worker
}

func TestWorkflowReadOnlyTestSuite(t *testing.T) {
	suite.Run(t, new(WorkflowReadOnlyTestSuite))
}

func (ts *WorkflowReadOnlyTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkflowReadOnlyTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkflowReadOnlyTestSuite) SetupTest() {
	ts.Assertions = require.New(ts.T())
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()

	isReadOnlyRec = &isReadOnlyRecorder{calls: map[string]bool{}}
	plugin, err := temporal.NewSimplePlugin(temporal.SimplePluginOptions{
		Name: "is-readonly-plugin",
		WorkerInterceptors: []interceptor.WorkerInterceptor{
			&isReadOnlyWorkerInterceptor{},
		},
	})
	ts.NoError(err)

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		Plugins: []worker.Plugin{plugin},
	})
	ts.worker.RegisterWorkflow(isReadOnlyWorkflow)
	ts.NoError(ts.worker.Start())
}

func (ts *WorkflowReadOnlyTestSuite) TearDownTest() {
	ts.worker.Stop()
}

func (ts *WorkflowReadOnlyTestSuite) TestIsReadOnly() {
	ctx := context.Background()
	run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions(ts.T().Name()), isReadOnlyWorkflow)
	ts.NoError(err)

	_, err = ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), isReadOnlyQueryName)
	ts.NoError(err)

	handle, err := ts.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   run.GetID(),
		RunID:        run.GetRunID(),
		UpdateName:   isReadOnlyUpdateName,
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	ts.NoError(err)
	ts.NoError(handle.Get(ctx, nil))

	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), isReadOnlySignalName, nil))
	ts.NoError(run.Get(ctx, nil))

	ts.Equal(map[string]bool{
		"ExecuteWorkflow": false,
		"workflowTask":    false,
		"ExecuteUpdate":   false,
		"updateHandler":   false,
		"HandleSignal":    false,
		"sideEffect":      true,
		"HandleQuery":     true,
		"query":           true,
		"ValidateUpdate":  true,
		"validator":       true,
	}, isReadOnlyRec.calls)
}
