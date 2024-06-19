package temporalnexus_test

import (
	"context"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/workflow"
)

func TestNewWorkflowRunOperationWithOptions(t *testing.T) {
	_, err := temporalnexus.NewWorkflowRunOperationWithOptions(temporalnexus.WorkflowRunOperationOptions[string, string]{})
	require.ErrorContains(t, err, "Name is required")

	_, err = temporalnexus.NewWorkflowRunOperationWithOptions(temporalnexus.WorkflowRunOperationOptions[string, string]{
		Name: "test",
	})
	require.ErrorContains(t, err, "either GetOptions and Workflow, or Handler are required")

	_, err = temporalnexus.NewWorkflowRunOperationWithOptions(temporalnexus.WorkflowRunOperationOptions[string, string]{
		Name: "test",
		Workflow: func(workflow.Context, string) (string, error) {
			return "", nil
		},
	})
	require.ErrorContains(t, err, "must provide both Workflow and GetOptions")

	_, err = temporalnexus.NewWorkflowRunOperationWithOptions(temporalnexus.WorkflowRunOperationOptions[string, string]{
		Name: "test",
		GetOptions: func(ctx context.Context, s string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{}, nil
		},
	})
	require.ErrorContains(t, err, "must provide both Workflow and GetOptions")

	_, err = temporalnexus.NewWorkflowRunOperationWithOptions(temporalnexus.WorkflowRunOperationOptions[string, string]{
		Name: "test",
		Workflow: func(workflow.Context, string) (string, error) {
			return "", nil
		},
		GetOptions: func(ctx context.Context, s string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{}, nil
		},
		Handler: func(ctx context.Context, s string, soo nexus.StartOperationOptions) (temporalnexus.WorkflowHandle[string], error) {
			return nil, nil
		},
	})
	require.ErrorContains(t, err, "Workflow is mutually exclusive with Handler")

	_, err = temporalnexus.NewWorkflowRunOperationWithOptions(temporalnexus.WorkflowRunOperationOptions[string, string]{
		Name: "test",
		Workflow: func(workflow.Context, string) (string, error) {
			return "", nil
		},
		GetOptions: func(ctx context.Context, s string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{}, nil
		},
	})
	require.NoError(t, err)

	_, err = temporalnexus.NewWorkflowRunOperationWithOptions(temporalnexus.WorkflowRunOperationOptions[string, string]{
		Name: "test",
		Handler: func(ctx context.Context, s string, soo nexus.StartOperationOptions) (temporalnexus.WorkflowHandle[string], error) {
			return nil, nil
		},
	})
	require.NoError(t, err)
}
