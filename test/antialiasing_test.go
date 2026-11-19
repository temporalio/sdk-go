package test_test

import (
	"context"
	"fmt"
	"time"

	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func workflowAliasOriginal(workflow.Context) (string, error) { return "original", nil }
func workflowAliasTarget(workflow.Context) (string, error)   { return "target", nil }

func (ts *IntegrationTestSuite) TestWorkflowRegistrationNameClash() {
	for _, disableAliasing := range []bool{false, true} {
		for _, aliasFirst := range []bool{false, true} {
			ts.Run(fmt.Sprintf("disableAliasing=%v/aliasFirst=%v", disableAliasing, aliasFirst), func() {
				t := ts.T()
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				taskQueue := fmt.Sprintf("%s-aliasing-%v-%v", ts.taskQueueName, disableAliasing, aliasFirst)
				w := worker.New(ts.client, taskQueue, worker.Options{DisableRegistrationAliasing: disableAliasing})
				if aliasFirst {
					w.RegisterWorkflowWithOptions(workflowAliasTarget, workflow.RegisterOptions{Name: "Workflow_v2"})
					w.RegisterWorkflowWithOptions(workflowAliasOriginal, workflow.RegisterOptions{Name: "workflowAliasTarget"})
				} else {
					w.RegisterWorkflowWithOptions(workflowAliasOriginal, workflow.RegisterOptions{Name: "workflowAliasTarget"})
					w.RegisterWorkflowWithOptions(workflowAliasTarget, workflow.RegisterOptions{Name: "Workflow_v2"})
				}
				w.RegisterWorkflowWithOptions(func(ctx workflow.Context) (string, error) {
					var result string
					err := workflow.ExecuteChildWorkflow(ctx, "workflowAliasTarget").Get(ctx, &result)
					return result, err
				}, workflow.RegisterOptions{Name: "Parent"})
				require.NoError(t, w.Start())
				defer w.Stop()

				for _, workflowName := range []string{"workflowAliasTarget", "Parent"} {
					run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, workflowName)
					require.NoError(t, err)
					var result string
					require.NoError(t, run.Get(ctx, &result))
					if disableAliasing {
						require.Equal(t, "original", result, "workflow %s", workflowName)
					} else {
						require.Equal(t, "target", result, "workflow %s", workflowName)
					}
				}
			})
		}
	}
}
