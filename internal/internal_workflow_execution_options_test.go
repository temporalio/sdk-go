package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentpb "go.temporal.io/api/deployment/v1"
	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/durationpb"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, workflowExecutionOptionsFromProtoUpdateResponse(tt.response), "workflowExecutionOptions(%v)", tt.response)
		})
	}
}

func Test_WorkflowExecutionOptions_TimeSkippingConfig_fromProtoResponse(t *testing.T) {
	response := &workflowservice.UpdateWorkflowExecutionOptionsResponse{
		WorkflowExecutionOptions: &workflowpb.WorkflowExecutionOptions{
			TimeSkippingConfig: &workflowpb.TimeSkippingConfig{
				Enabled: true,
				Bound: &workflowpb.TimeSkippingConfig_MaxElapsedDuration{
					MaxElapsedDuration: durationpb.New(time.Hour),
				},
			},
		},
	}

	got := workflowExecutionOptionsFromProtoUpdateResponse(response)
	require.Equal(t, TimeSkippingConfig{
		Enabled:            true,
		MaxElapsedDuration: time.Hour,
	}, got.TimeSkippingConfig)
}

func Test_WorkflowExecutionOptionsChanges_TimeSkippingConfig_toProto(t *testing.T) {
	changes := WorkflowExecutionOptionsChanges{
		TimeSkippingConfig: &TimeSkippingConfigChange{
			Value: TimeSkippingConfig{Enabled: true, MaxSkippedDuration: 5 * time.Minute},
		},
	}

	options, mask := workflowExecutionOptionsChangesToProto(changes)
	require.Equal(t, []string{"time_skipping_config"}, mask.GetPaths())
	require.True(t, options.GetTimeSkippingConfig().GetEnabled())
	require.Equal(t, 5*time.Minute, options.GetTimeSkippingConfig().GetMaxSkippedDuration().AsDuration())

	// An empty value still sets the mask path (clearing the config server-side) and sends nil.
	clearChanges := WorkflowExecutionOptionsChanges{
		TimeSkippingConfig: &TimeSkippingConfigChange{Value: TimeSkippingConfig{}},
	}
	clearOptions, clearMask := workflowExecutionOptionsChangesToProto(clearChanges)
	require.Equal(t, []string{"time_skipping_config"}, clearMask.GetPaths())
	require.Nil(t, clearOptions.GetTimeSkippingConfig())
}

func Test_UpdateWorkflowExecutionOptions_TimeSkippingConfigOnly_valid(t *testing.T) {
	req := &UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: "wf-id",
		WorkflowExecutionOptionsChanges: WorkflowExecutionOptionsChanges{
			TimeSkippingConfig: &TimeSkippingConfigChange{
				Value: TimeSkippingConfig{Enabled: true},
			},
		},
	}

	got, err := req.validateAndConvertToProto("ns")
	require.NoError(t, err)
	require.Equal(t, []string{"time_skipping_config"}, got.GetUpdateMask().GetPaths())
	require.True(t, got.GetWorkflowExecutionOptions().GetTimeSkippingConfig().GetEnabled())
}
