package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentpb "go.temporal.io/api/deployment/v1"
	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func Test_WorkflowExecutionOptions_fromProtoResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *workflowservice.UpdateWorkflowExecutionOptionsResponse
		want     WorkflowExecutionOptions
	}{
		{
			name:     "nil response",
			response: nil,
			want:     WorkflowExecutionOptions{},
		},
		{
			name: "normal workflow execution options",
			response: &workflowservice.UpdateWorkflowExecutionOptionsResponse{
				WorkflowExecutionOptions: &workflowpb.WorkflowExecutionOptions{
					VersioningOverride: &workflowpb.VersioningOverride{
						Behavior:      enumspb.VersioningBehavior(VersioningBehaviorPinned),
						PinnedVersion: "my series.v1",
						Deployment: &deploymentpb.Deployment{
							SeriesName: "my series",
							BuildId:    "v1",
						},
					},
				},
			},
			want: WorkflowExecutionOptions{
				VersioningOverride: &PinnedVersioningOverride{
					Version: WorkerDeploymentVersion{
						DeploymentName: "my series",
						BuildID:        "v1",
					},
				}},
		},
		{
			name: "one time versioning override",
			response: &workflowservice.UpdateWorkflowExecutionOptionsResponse{
				WorkflowExecutionOptions: &workflowpb.WorkflowExecutionOptions{
					VersioningOverride: &workflowpb.VersioningOverride{
						Override: &workflowpb.VersioningOverride_OneTime{
							OneTime: &workflowpb.VersioningOverride_OneTimeOverride{
								TargetDeploymentVersion: &deploymentpb.WorkerDeploymentVersion{
									DeploymentName: "my deployment",
									BuildId:        "v1",
								},
							},
						},
					},
				},
			},
			want: WorkflowExecutionOptions{
				VersioningOverride: &OneTimeVersioningOverride{
					TargetVersion: WorkerDeploymentVersion{
						DeploymentName: "my deployment",
						BuildID:        "v1",
					},
				}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, workflowExecutionOptionsFromProtoUpdateResponse(tt.response), "workflowExecutionOptions(%v)", tt.response)
		})
	}
}

func Test_WorkflowExecutionOptions_toProto_OneTimeVersioningOverride(t *testing.T) {
	options := workflowExecutionOptionsToProto(WorkflowExecutionOptions{
		VersioningOverride: &OneTimeVersioningOverride{
			TargetVersion: WorkerDeploymentVersion{
				DeploymentName: "my deployment",
				BuildID:        "v1",
			},
		},
	})

	oneTime := options.GetVersioningOverride().GetOneTime()
	require.NotNil(t, oneTime)
	require.Equal(t, "my deployment", oneTime.GetTargetDeploymentVersion().GetDeploymentName())
	require.Equal(t, "v1", oneTime.GetTargetDeploymentVersion().GetBuildId())
	require.Nil(t, options.GetVersioningOverride().GetPinned())
	require.False(t, options.GetVersioningOverride().GetAutoUpgrade())
}

func Test_WorkflowExecutionOptionsChanges_toProto_OneTimeVersioningOverride(t *testing.T) {
	options, mask := workflowExecutionOptionsChangesToProto(WorkflowExecutionOptionsChanges{
		VersioningOverride: &VersioningOverrideChange{
			Value: &OneTimeVersioningOverride{
				TargetVersion: WorkerDeploymentVersion{
					DeploymentName: "my deployment",
					BuildID:        "v1",
				},
			},
		},
	})

	require.Equal(t, []string{"versioning_override"}, mask.GetPaths())
	oneTime := options.GetVersioningOverride().GetOneTime()
	require.NotNil(t, oneTime)
	require.Equal(t, "my deployment", oneTime.GetTargetDeploymentVersion().GetDeploymentName())
	require.Equal(t, "v1", oneTime.GetTargetDeploymentVersion().GetBuildId())
}

func Test_UpdateWorkflowExecutionOptionsRequest_validateAndConvertToProto_OneTimeVersioningOverride(t *testing.T) {
	request, err := (&UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: "workflow-id",
		RunId:      "run-id",
		WorkflowExecutionOptionsChanges: WorkflowExecutionOptionsChanges{
			VersioningOverride: &VersioningOverrideChange{
				Value: &OneTimeVersioningOverride{
					TargetVersion: WorkerDeploymentVersion{
						DeploymentName: "my deployment",
						BuildID:        "v1",
					},
				},
			},
		},
	}).validateAndConvertToProto("namespace")

	require.NoError(t, err)
	require.Equal(t, "namespace", request.GetNamespace())
	require.Equal(t, "workflow-id", request.GetWorkflowExecution().GetWorkflowId())
	require.Equal(t, "run-id", request.GetWorkflowExecution().GetRunId())
	require.Equal(t, []string{"versioning_override"}, request.GetUpdateMask().GetPaths())
	oneTime := request.GetWorkflowExecutionOptions().GetVersioningOverride().GetOneTime()
	require.NotNil(t, oneTime)
	require.Equal(t, "my deployment", oneTime.GetTargetDeploymentVersion().GetDeploymentName())
	require.Equal(t, "v1", oneTime.GetTargetDeploymentVersion().GetBuildId())
}
