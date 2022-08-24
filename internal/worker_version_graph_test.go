package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func Test_WorkerVersionGraph_fromProtoResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *workflowservice.GetWorkerBuildIdOrderingResponse
		want     *WorkerBuildIDVersionGraph
	}{
		{
			name:     "nil response",
			response: nil,
			want:     nil,
		},
		{
			name: "normal graph",
			response: &workflowservice.GetWorkerBuildIdOrderingResponse{
				CurrentDefault: &taskqueuepb.VersionIdNode{
					Version: &taskqueuepb.VersionId{
						WorkerBuildId: "2.0",
					},
					PreviousIncompatible: &taskqueuepb.VersionIdNode{
						Version: &taskqueuepb.VersionId{
							WorkerBuildId: "1.0",
						},
					},
				},
				CompatibleLeaves: []*taskqueuepb.VersionIdNode{
					{
						Version: &taskqueuepb.VersionId{
							WorkerBuildId: "1.1",
						},
						PreviousCompatible: &taskqueuepb.VersionIdNode{
							Version: &taskqueuepb.VersionId{
								WorkerBuildId: "1.0",
							},
						},
					},
				},
			},
			want: &WorkerBuildIDVersionGraph{
				CurrentDefault: &WorkerVersionIDNode{
					WorkerBuildID: "2.0",
					PreviousIncompatible: &WorkerVersionIDNode{
						WorkerBuildID: "1.0",
					},
				},
				CompatibleLeaves: []*WorkerVersionIDNode{
					{
						WorkerBuildID: "1.1",
						PreviousCompatible: &WorkerVersionIDNode{
							WorkerBuildID: "1.0",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, fromProtoResponse(tt.response), "fromProtoResponse(%v)", tt.response)
		})
	}
}
