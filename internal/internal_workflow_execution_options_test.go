package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
				VersioningOverride: VersioningOverride{
					Behavior:      VersioningBehaviorPinned,
					PinnedVersion: "my series.v1",
					Deployment: Deployment{
						SeriesName: "my series",
						BuildID:    "v1",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, workflowExecutionOptionsFromProtoUpdateResponse(tt.response), "workflowExecutionOptions(%v)", tt.response)
		})
	}
}
