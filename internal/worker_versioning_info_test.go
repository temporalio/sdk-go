// The MIT License
//
// Copyright (c) 2024 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Test_TaskQueueInfo_fromProtoResponse(t *testing.T) {
	nowProto := timestamppb.Now()
	now := nowProto.AsTime()
	tests := []struct {
		name     string
		response *workflowservice.DescribeTaskQueueResponse
		want     *TaskQueueInfo
	}{
		{
			name:     "nil response",
			response: nil,
			want:     nil,
		},
		{
			name: "normal task queue info",
			response: &workflowservice.DescribeTaskQueueResponse{
				VersionsInfo: map[string]*taskqueuepb.TaskQueueVersionInfo{
					"one": {
						TypesInfo: map[int32]*taskqueuepb.TaskQueueTypeInfo{
							int32(enumspb.TASK_QUEUE_TYPE_WORKFLOW): {
								Pollers: []*taskqueuepb.PollerInfo{
									{LastAccessTime: nowProto, Identity: "me", RatePerSecond: 3.0, WorkerVersionCapabilities: &common.WorkerVersionCapabilities{BuildId: "1.0", UseVersioning: true}},
								},
							},
						},
						TaskReachability: enumspb.BUILD_ID_TASK_REACHABILITY_REACHABLE,
					},
				},
			},
			want: &TaskQueueInfo{
				VersionsInfo: map[string]TaskQueueVersionInfo{
					"one": {
						TypesInfo: map[TaskQueueType]TaskQueueTypeInfo{
							TaskQueueTypeWorkflow: {
								Pollers: []PollerInfo{
									{LastAccessTime: now, Identity: "me", RatePerSecond: 3.0, WorkerVersionCapabilities: &WorkerVersionCapabilities{BuildID: "1.0", UseVersioning: true}},
								},
							},
						},
						TaskReachability: BuildIDTaskReachabilityReachable,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, taskQueueInfoFromResponse(tt.response), "taskQueueInfoFromResponse(%v)", tt.response)
		})
	}
}
