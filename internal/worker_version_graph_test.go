// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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
				Default: &WorkerVersionIDNode{
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
			assert.Equalf(t, tt.want, workerVersionGraphFromProtoResponse(tt.response), "workerVersionGraphFromProtoResponse(%v)", tt.response)
		})
	}
}
