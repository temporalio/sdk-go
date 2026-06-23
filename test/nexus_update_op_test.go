package test_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type updateAddInput struct {
	CounterID string
	Amount    int
}

type updateAddOutput struct {
	NewValue int
}

const (
	serviceName  = "counter-update-service"
	addOperation = "addOperation"
	addUpdate    = "addUpdate"
	doneSignal   = "done"
)

func TestAsyncUpdateWorkflowOperation(t *testing.T) {
	testUpdateWorkflowOperation(t, true)
}

func TestSyncUpdateWorkflowOperation(t *testing.T) {
	testUpdateWorkflowOperation(t, false)
}

// run both via "go run . integration-test -dev-server -run UpdateWorkflowOperation"
func testUpdateWorkflowOperation(t *testing.T, isAsync bool) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	addOp, err := getAddOperation(isAsync)
	require.NoError(t, err)

	callerWorkflow := getCallerWorkflow(tc, addOp, isAsync)

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService(serviceName)
	require.NoError(t, service.Register(addOp))

	w.RegisterNexusService(service)
	w.RegisterWorkflow(counterWorkflow)
	w.RegisterWorkflow(callerWorkflow)
	require.NoError(t, w.Start())
	defer w.Stop()

	counterWorkflowID := "counter-" + uuid.NewString()
	counterRun, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        counterWorkflowID,
		TaskQueue: tc.taskQueue,
	}, counterWorkflow)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, tc.client.SignalWorkflow(ctx, counterWorkflowID, "", doneSignal, nil))
		require.NoError(t, counterRun.Get(ctx, nil))
	}()

	counterUpdateWorkflowRun, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                  "caller-" + uuid.NewString(),
		TaskQueue:           tc.taskQueue,
		WorkflowTaskTimeout: time.Second,
	}, callerWorkflow, updateAddInput{CounterID: counterWorkflowID, Amount: 5})
	require.NoError(t, err)

	var out updateAddOutput
	require.NoError(t, counterUpdateWorkflowRun.Get(ctx, &out))
	require.Equal(t, 5, out.NewValue)
}

func counterWorkflow(ctx workflow.Context) (int, error) {
	counter := 0

	if err := workflow.SetUpdateHandler(ctx, addUpdate, func(ctx workflow.Context, amount int) (updateAddOutput, error) {
		counter += amount
		_ = workflow.Sleep(ctx, time.Second)
		return updateAddOutput{NewValue: counter}, nil
	}); err != nil {
		return 0, err
	}

	workflow.GetSignalChannel(ctx, doneSignal).Receive(ctx, nil)
	workflow.GetLogger(ctx).Info("finished workflow, exiting now...", "final counter", counter)
	return counter, nil
}

func getAddOperation(isAsync bool) (nexus.Operation[updateAddInput, updateAddOutput], error) {
	updateStage := client.WorkflowUpdateStageCompleted
	if isAsync {
		updateStage = client.WorkflowUpdateStageAccepted
	}
	return temporalnexus.NewTemporalOperation(temporalnexus.TemporalOperationOptions[updateAddInput, updateAddOutput]{
		Name: addOperation,
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input updateAddInput, _ temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[updateAddOutput], error) {
			return temporalnexus.StartUpdateWorkflow[updateAddOutput](ctx, nc, client.UpdateWorkflowOptions{
				WorkflowID:   input.CounterID,
				UpdateName:   addUpdate,
				Args:         []any{input.Amount},
				WaitForStage: updateStage,
			})
		},
	})
}

// caller workflow that does sync/async specific checks
func getCallerWorkflow(
	tc *testContext,
	addOp nexus.Operation[updateAddInput, updateAddOutput],
	isAsync bool,
) func(workflow.Context, updateAddInput) (updateAddOutput, error) {
	return func(ctx workflow.Context, input updateAddInput) (updateAddOutput, error) {
		nc := workflow.NewNexusClient(tc.endpoint, serviceName)
		fut := nc.ExecuteOperation(ctx, addOp, input, workflow.NexusOperationOptions{})

		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return updateAddOutput{}, err
		}

		if isAsync {
			if exec.OperationToken == "" {
				return updateAddOutput{}, errors.New("expected a non-empty async operation token")
			}
		} else {
			if exec.OperationToken != "" {
				return updateAddOutput{}, errors.New("unexpected operation token on a sync operation")
			}
		}

		var out updateAddOutput
		if err := fut.Get(ctx, &out); err != nil {
			return updateAddOutput{}, err
		}

		return out, nil
	}
}
