package internal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Test_DetectEnhancedNotSupported_fromProtoResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *workflowservice.DescribeTaskQueueResponse
		want     error
	}{
		{
			name: "enhanced task queue info",
			response: &workflowservice.DescribeTaskQueueResponse{
				VersionsInfo: map[string]*taskqueuepb.TaskQueueVersionInfo{
					"one": {
						TypesInfo:        map[int32]*taskqueuepb.TaskQueueTypeInfo{},
						TaskReachability: enumspb.BUILD_ID_TASK_REACHABILITY_REACHABLE,
					},
				},
			},
			want: nil,
		},
		{
			name: "legacy task queue info",
			response: &workflowservice.DescribeTaskQueueResponse{
				TaskQueueStatus: &taskqueuepb.TaskQueueStatus{},
			},
			want: errors.New("server does not support `DescribeTaskQueueEnhanced`"),
		},
		{
			name:     "empty response assumed enhanced",
			response: &workflowservice.DescribeTaskQueueResponse{},
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, detectTaskQueueEnhancedNotSupported(tt.response), "detectEnhancedNotSupported(%v)", tt.response)
		})
	}
}

func Test_TaskQueueDescription_fromProtoResponse(t *testing.T) {
	nowProto := timestamppb.Now()
	now := nowProto.AsTime()
	tests := []struct {
		name     string
		response *workflowservice.DescribeTaskQueueResponse
		want     TaskQueueDescription
	}{
		{
			name:     "nil response",
			response: nil,
			want:     TaskQueueDescription{},
		},
		{
			name: "normal task queue info",
			response: &workflowservice.DescribeTaskQueueResponse{
				VersionsInfo: map[string]*taskqueuepb.TaskQueueVersionInfo{
					"one": {
						TypesInfo: map[int32]*taskqueuepb.TaskQueueTypeInfo{
							int32(enumspb.TASK_QUEUE_TYPE_WORKFLOW): {
								Pollers: []*taskqueuepb.PollerInfo{
									{LastAccessTime: nowProto, Identity: "me", RatePerSecond: 3.0, WorkerVersionCapabilities: &common.WorkerVersionCapabilities{BuildId: "1.0", UseVersioning: true, DeploymentSeriesName: "prod1"}},
								},
							},
						},
						TaskReachability: enumspb.BUILD_ID_TASK_REACHABILITY_REACHABLE,
					},
				},
				VersioningInfo: &taskqueuepb.TaskQueueVersioningInfo{
					CurrentVersion:           "foo.build1",
					RampingVersion:           "foo.build2",
					RampingVersionPercentage: 3.0,
					UpdateTime:               nowProto,
				},
			},
			want: TaskQueueDescription{
				VersionsInfo: map[string]TaskQueueVersionInfo{
					"one": {
						TypesInfo: map[TaskQueueType]TaskQueueTypeInfo{
							TaskQueueTypeWorkflow: {
								Pollers: []TaskQueuePollerInfo{
									{LastAccessTime: now, Identity: "me", RatePerSecond: 3.0, WorkerVersionCapabilities: &WorkerVersionCapabilities{BuildID: "1.0", UseVersioning: true, DeploymentSeriesName: "prod1"}},
								},
							},
						},
						TaskReachability: BuildIDTaskReachabilityReachable,
					},
				},
				VersioningInfo: &TaskQueueVersioningInfo{
					CurrentVersion:           &WorkerDeploymentVersion{DeploymentName: "foo", BuildId: "build1"},
					RampingVersion:           &WorkerDeploymentVersion{DeploymentName: "foo", BuildId: "build2"},
					RampingVersionPercentage: 3.0,
					UpdateTime:               now,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, taskQueueDescriptionFromResponse(tt.response), "taskQueueInfoFromResponse(%v)", tt.response)
		})
	}
}
