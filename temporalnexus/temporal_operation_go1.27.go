//go:build go1.27

package temporalnexus

import (
	"context"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

// StartWorkflow starts a type-safe workflow run for a Nexus operation.
//
// The workflow parameter must have the signature func(workflow.Context, I) (O, error).
// For workflows that don't follow this signature, use [StartUntypedWorkflow].
//
// This method is available when building with Go 1.27 or later. The package-level
// [StartWorkflow] function provides the same behavior for earlier Go versions.
//
// Example:
//
//	op := MustNewTemporalOperation(TemporalOperationOptions[MyInput, MyOutput]{
//		Name: "my-workflow-operation",
//		Start: func(ctx context.Context, nc NexusClient, input MyInput, _ StartTemporalOperationOptions) (TemporalOperationResult[MyOutput], error) {
//			return nc.StartWorkflow(
//				ctx,
//				client.StartWorkflowOptions{ID: "workflow-" + input.ID},
//				MyWorkflow,
//				input,
//			)
//		},
//	})
//
// NOTE: Experimental
func (nc NexusClient) StartWorkflow[I, O any, WF func(workflow.Context, I) (O, error)](
	ctx context.Context,
	workflowOpts client.StartWorkflowOptions,
	workflow WF,
	arg I,
) (TemporalOperationResult[O], error) {
	return StartWorkflow(ctx, nc, workflowOpts, workflow, arg)
}

// StartUpdateWorkflow starts a type-safe workflow update run for a Nexus operation.
//
// This method is available when building with Go 1.27 or later. The package-level
// [StartUpdateWorkflow] function provides the same behavior for earlier Go versions.
//
// Example:
//
//	op := MustNewTemporalOperation(TemporalOperationOptions[MyInput, MyOutput]{
//		Name: "my-update-operation",
//		Start: func(ctx context.Context, nc NexusClient, input MyInput, _ StartTemporalOperationOptions) (TemporalOperationResult[MyOutput], error) {
//			return nc.StartUpdateWorkflow[MyOutput](ctx, client.UpdateWorkflowOptions{
//				WorkflowID:   input.ID,
//				UpdateName:   "MyUpdate",
//				Args:         []any{input},
//				WaitForStage: client.WorkflowUpdateStageAccepted,
//			})
//		},
//	})
//
// NOTE: Experimental
func (nc NexusClient) StartUpdateWorkflow[R any](
	ctx context.Context,
	updateWorkflowOptions client.UpdateWorkflowOptions,
) (TemporalOperationResult[R], error) {
	return StartUpdateWorkflow[R](ctx, nc, updateWorkflowOptions)
}

// StartActivity starts a type-safe stand-alone activity execution for a Nexus operation.
//
// The activity parameter must have the signature func(context.Context, I) (O, error).
// For activities that don't follow this signature, use [StartUntypedActivity].
//
// This method is available when building with Go 1.27 or later. The package-level
// [StartActivity] function provides the same behavior for earlier Go versions.
//
// Example:
//
//	op := MustNewTemporalOperation(TemporalOperationOptions[MyInput, MyOutput]{
//		Name: "my-activity-operation",
//		Start: func(ctx context.Context, nc NexusClient, input MyInput, _ StartTemporalOperationOptions) (TemporalOperationResult[MyOutput], error) {
//			return nc.StartActivity(
//				ctx,
//				client.StartActivityOptions{
//					ID:                  "activity-" + input.ID,
//					StartToCloseTimeout: time.Minute,
//				},
//				MyActivity,
//				input,
//			)
//		},
//	})
//
// NOTE: Experimental
func (nc NexusClient) StartActivity[I, O any, AF func(context.Context, I) (O, error)](
	ctx context.Context,
	activityOpts client.StartActivityOptions,
	activity AF,
	arg I,
) (TemporalOperationResult[O], error) {
	return StartActivity(ctx, nc, activityOpts, activity, arg)
}
