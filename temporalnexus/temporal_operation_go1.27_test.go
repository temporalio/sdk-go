//go:build go1.27

package temporalnexus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestNexusClientStartWorkflowSmoke(t *testing.T) {
	backingWorkflow := func(_ workflow.Context, input string) (string, error) {
		return "workflow:" + input, nil
	}
	op := MustNewTemporalOperation(TemporalOperationOptions[string, string]{
		Name: "workflow-op",
		Start: func(ctx context.Context, nc NexusClient, input string, _ StartTemporalOperationOptions) (TemporalOperationResult[string], error) {
			return nc.StartWorkflow(
				ctx,
				client.StartWorkflowOptions{ID: "workflow-" + input},
				backingWorkflow,
				input,
			)
		},
	})
	callerWorkflow := func(ctx workflow.Context, input string) (string, error) {
		nc := workflow.NewNexusClient("endpoint", "service")
		var result string
		err := nc.ExecuteOperation(ctx, op, input, workflow.NexusOperationOptions{}).Get(ctx, &result)
		return result, err
	}
	service := nexus.NewService("service")
	require.NoError(t, service.Register(op))

	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(backingWorkflow)
	env.RegisterNexusService(service)
	env.ExecuteWorkflow(callerWorkflow, "input")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "workflow:input", result)
}

func TestNexusClientStartUpdateWorkflowSmoke(t *testing.T) {
	op := MustNewTemporalOperation(TemporalOperationOptions[string, string]{
		Name: "update-op",
		Start: func(ctx context.Context, nc NexusClient, input string, _ StartTemporalOperationOptions) (TemporalOperationResult[string], error) {
			return nc.StartUpdateWorkflow[string](ctx, client.UpdateWorkflowOptions{
				WorkflowID:   input,
				UpdateName:   "update",
				WaitForStage: client.WorkflowUpdateStageAccepted,
			})
		},
	})

	require.Equal(t, "update-op", op.Name())
}

func TestNexusClientStartActivitySmoke(t *testing.T) {
	backingActivity := func(_ context.Context, input string) (string, error) {
		return "activity:" + input, nil
	}
	op := MustNewTemporalOperation(TemporalOperationOptions[string, string]{
		Name: "activity-op",
		Start: func(ctx context.Context, nc NexusClient, input string, _ StartTemporalOperationOptions) (TemporalOperationResult[string], error) {
			return nc.StartActivity(
				ctx,
				client.StartActivityOptions{
					ID:                  "activity-" + input,
					StartToCloseTimeout: time.Minute,
				},
				backingActivity,
				input,
			)
		},
	})
	callerWorkflow := func(ctx workflow.Context, input string) (string, error) {
		nc := workflow.NewNexusClient("endpoint", "service")
		var result string
		err := nc.ExecuteOperation(ctx, op, input, workflow.NexusOperationOptions{}).Get(ctx, &result)
		return result, err
	}
	service := nexus.NewService("service")
	require.NoError(t, service.Register(op))

	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(backingActivity)
	env.RegisterNexusService(service)
	env.ExecuteWorkflow(callerWorkflow, "input")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "activity:input", result)
}

func TestNexusClientStartWorkflow(t *testing.T) {
	var started atomic.Bool
	started.Store(true)
	nc := NexusClient{asyncStarted: &started}

	_, err := nc.StartWorkflow(
		t.Context(),
		client.StartWorkflowOptions{},
		func(workflow.Context, string) (string, error) { return "", nil },
		"ignored",
	)

	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, errMultipleAsyncOperationsMsg)
}

func TestNexusClientStartUpdateWorkflow(t *testing.T) {
	var started atomic.Bool
	started.Store(true)
	nc := NexusClient{
		asyncStarted:          &started,
		startOperationOptions: nexus.StartOperationOptions{CallbackURL: "temporal://dummy"},
	}
	ctx := internal.ContextWithNexusOperationContext(t.Context(), &internal.NexusOperationContext{})

	_, err := nc.StartUpdateWorkflow[string](ctx, client.UpdateWorkflowOptions{
		WorkflowID:   "workflow-id",
		UpdateName:   "update-name",
		WaitForStage: client.WorkflowUpdateStageAccepted,
	})

	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, errMultipleAsyncOperationsMsg)
}

func TestNexusClientStartActivity(t *testing.T) {
	var started atomic.Bool
	started.Store(true)
	nc := NexusClient{asyncStarted: &started}

	_, err := nc.StartActivity(
		t.Context(),
		client.StartActivityOptions{},
		func(context.Context, string) (string, error) { return "", nil },
		"ignored",
	)

	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, errMultipleAsyncOperationsMsg)
}
