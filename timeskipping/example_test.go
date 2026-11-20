package timeskipping_test

import (
	"context"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/timeskipping"
	"go.temporal.io/sdk/workflow"
)

func MyWorkflow(ctx workflow.Context) error {
	return workflow.Sleep(ctx, 2*time.Hour)
}

func ExampleFastForwardWorkflow() {
	ctx := context.Background()
	c, _ := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "my-namespace",
	})

	// Start the workflow with a bounded fast forward.
	cfg := client.TimeSkippingConfig{
		Enabled: true,
		FastForwardConfig: &client.FastForwardConfig{
			ID:       "initial-skip",
			Duration: 30 * time.Minute,
		},
	}
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                 "my-workflow-id",
		TaskQueue:          "my-task-queue",
		TimeSkippingConfig: &cfg,
	}, MyWorkflow)
	if err != nil {
		return
	}

	// Advance another hour of virtual time. The workflow is now ninety minutes
	// into its two-hour sleep, having waited no real time.
	completed, err := timeskipping.FastForwardWorkflow(ctx, c, run,
		timeskipping.FastForwardWorkflowOptions{Duration: time.Hour})
	if err != nil {
		return
	}
	if completed {
		// The fast-forward reached its own target, ninety minutes of virtual
		// time, and time skipping switched itself off there. The workflow is
		// still sleeping.
	}
}
