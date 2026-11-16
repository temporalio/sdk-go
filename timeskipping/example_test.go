package timeskipping_test

import (
	"context"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/timeskipping"
	"go.temporal.io/sdk/workflow"
)

func MyWorkflow(ctx workflow.Context) error {
	return workflow.Sleep(ctx, time.Hour)
}

func ExampleFastForwardWorkflow() {
	ctx := context.Background()
	c, _ := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "my-namespace",
	})

	// Time skipping is switched on per execution, when the workflow is started.
	cfg := client.TimeSkippingConfig{Enabled: true}
	run, _ := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                 "my-workflow-id",
		TaskQueue:          "my-task-queue",
		TimeSkippingConfig: &cfg,
	}, MyWorkflow)

	// Advance an hour of virtual time and wait for it to land. The hour-long
	// sleep in MyWorkflow completes in moments.
	completed, _ := timeskipping.FastForwardWorkflow(ctx, c, run,
		timeskipping.WorkflowFastForwardOptions{Duration: time.Hour})
	if completed {
		// Virtual time reached the target, and time skipping switched itself
		// off there.
	}
}
