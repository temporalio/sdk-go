package test_test

import (
	"context"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// intTestTransferType has no serializable fields. Without its transfer
// converter, the default data converter will replace it with an empty struct.
type intTestTransferType struct {
	value int
}

func (intTestTransferType) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return converter.NewTransferTypeConverter(
		func(value *intTestTransferType) (*int, error) {
			return &value.value, nil
		},
		func(transferType *int, value *intTestTransferType) error {
			value.value = *transferType
			return nil
		},
	)
}

func intTestTransferWorkflow(_ workflow.Context, input intTestTransferType) (intTestTransferType, error) {
	input.value++
	return input, nil
}

func intTestTransferActivity(_ context.Context, input intTestTransferType) (intTestTransferType, error) {
	input.value++
	return input, nil
}

func intTestTransferActivityWorkflow(ctx workflow.Context, input intTestTransferType) (intTestTransferType, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	var result intTestTransferType
	err := workflow.ExecuteActivity(ctx, intTestTransferActivity, input).Get(ctx, &result)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (ts *IntegrationTestSuite) newTransferTypesClientAndWorker(taskQueue string) (client.Client, worker.Worker) {
	c, err := ts.newDefaultClient()
	ts.NoError(err)
	return c, worker.New(c, taskQueue, worker.Options{})
}

func (ts *IntegrationTestSuite) TestTransferTypes_WorkflowRoundTrip() {
	taskQueue := "transfer-types-workflow-" + ts.T().Name()
	c, w := ts.newTransferTypesClientAndWorker(taskQueue)
	defer c.Close()

	w.RegisterWorkflow(intTestTransferWorkflow)
	ts.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "transfer-types-workflow-" + ts.T().Name(),
		TaskQueue: taskQueue,
	}, intTestTransferWorkflow, intTestTransferType{value: 41})
	ts.NoError(err)

	var result intTestTransferType
	ts.NoError(run.Get(ctx, &result))
	// Without transfer conversion, the struct's private field gets mapped to zero.
	ts.Equal(intTestTransferType{value: 42}, result)
}

func (ts *IntegrationTestSuite) TestTransferTypes_RemoteActivityRoundTrip() {
	taskQueue := "transfer-types-activity-" + ts.T().Name()
	c, w := ts.newTransferTypesClientAndWorker(taskQueue)
	defer c.Close()

	w.RegisterWorkflow(intTestTransferActivityWorkflow)
	w.RegisterActivity(intTestTransferActivity)
	ts.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "transfer-types-activity-" + ts.T().Name(),
		TaskQueue: taskQueue,
	}, intTestTransferActivityWorkflow, intTestTransferType{value: 41})
	ts.NoError(err)

	var result intTestTransferType
	ts.NoError(run.Get(ctx, &result))
	// Without transfer conversion, the struct's private field gets mapped to zero.
	ts.Equal(intTestTransferType{value: 42}, result)
}
