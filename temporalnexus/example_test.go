package temporalnexus_test

import (
	"context"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type MyWorkflowInput struct {
}

type MyOutput struct {
}

type MyInput struct {
	ID string
}

type MyQueryOutput struct {
}

func MyHandlerWorkflow(workflow.Context, MyInput) (MyOutput, error) {
	return MyOutput{}, nil
}

func MyHandlerWorkflowWithAlternativeInput(workflow.Context, MyWorkflowInput) (MyOutput, error) {
	return MyOutput{}, nil
}

func ExampleNewWorkflowRunOperation() {
	op := temporalnexus.NewWorkflowRunOperation(
		"my-async-operation",
		MyHandlerWorkflow,
		func(ctx context.Context, input MyInput, opts nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{
				// Workflow ID is required and must be deterministically generated from the input in order
				// for the operation to be idempotent as the request to start the operation may be retried.
				ID: input.ID,
			}, nil
		})

	service := nexus.NewService("my-service")
	_ = service.Register(op)

	c, _ := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "my-namespace",
	})
	w := worker.New(c, "my-task-queue", worker.Options{})
	w.RegisterWorkflow(MyHandlerWorkflow)
	w.RegisterNexusService(service)
}

func ExampleNewWorkflowRunOperationWithOptions() {
	// Alternative 1 - long form version of NewWorkflowRunOperation.
	opAlt1, _ := temporalnexus.NewWorkflowRunOperationWithOptions(
		temporalnexus.WorkflowRunOperationOptions[MyInput, MyOutput]{
			Name:     "my-async-op-1",
			Workflow: MyHandlerWorkflow,
			GetOptions: func(ctx context.Context, input MyInput, opts nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
				return client.StartWorkflowOptions{
					// Workflow ID is required and must be deterministically generated from the input in order
					// for the operation to be idempotent as the request to start the operation may be retried.
					ID: input.ID,
				}, nil
			},
		})

	// Alternative 2 - start a workflow with alternative inputs.
	opAlt2, _ := temporalnexus.NewWorkflowRunOperationWithOptions(
		temporalnexus.WorkflowRunOperationOptions[MyInput, MyOutput]{
			Name: "my-async-op-2",
			Handler: func(ctx context.Context, input MyInput, opts nexus.StartOperationOptions) (temporalnexus.WorkflowHandle[MyOutput], error) {
				// Workflows started with this API must take a single input and return single output.
				// To start workflows with different signatures, use ExecuteUntypedWorkflow.
				return temporalnexus.ExecuteWorkflow(ctx, opts, client.StartWorkflowOptions{
					// Workflow ID is required and must be deterministically generated from the input in order
					// for the operation to be idempotent as the request to start the operation may be retried.
					ID: input.ID,
				}, MyHandlerWorkflowWithAlternativeInput, MyWorkflowInput{})
			},
		})

	service := nexus.NewService("my-service")
	_ = service.Register(opAlt1, opAlt2)

	c, _ := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "my-namespace",
	})
	w := worker.New(c, "my-task-queue", worker.Options{})
	w.RegisterWorkflow(MyHandlerWorkflow)
	w.RegisterWorkflow(MyHandlerWorkflowWithAlternativeInput)
	w.RegisterNexusService(service)
}

func ExampleNewSyncOperation() {
	opRead := temporalnexus.NewSyncOperation("my-read-only-operation", func(ctx context.Context, c client.Client, input MyInput, opts nexus.StartOperationOptions) (MyQueryOutput, error) {
		var ret MyQueryOutput
		res, err := c.QueryWorkflow(ctx, input.ID, "", "some-query", nil)
		if err != nil {
			return ret, err
		}
		return ret, res.Get(&ret)
	})

	// Operations don't have to return values.
	opWrite := temporalnexus.NewSyncOperation("my-write-operation", func(ctx context.Context, c client.Client, input MyInput, opts nexus.StartOperationOptions) (nexus.NoValue, error) {
		return nil, c.SignalWorkflow(ctx, input.ID, "", "some-signal", nil)
	})

	service := nexus.NewService("my-service")
	_ = service.Register(opRead, opWrite)

	c, _ := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "my-namespace",
	})
	w := worker.New(c, "my-task-queue", worker.Options{})
	w.RegisterNexusService(service)
}
