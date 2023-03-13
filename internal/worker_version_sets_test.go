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

func Test_WorkerVersionSets_fromProtoResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *workflowservice.GetWorkerBuildIdCompatabilityResponse
		want     *WorkerBuildIDVersionSets
	}{
		{
			name:     "nil response",
			response: nil,
			want:     nil,
		},
		{
			name: "normal sets",
			response: &workflowservice.GetWorkerBuildIdCompatabilityResponse{
				MajorVersionSets: []*taskqueuepb.CompatibleVersionSet{
					{BuildIds: []string{"1.0", "1.1"}, VersionSetId: "1"},
					{BuildIds: []string{"2.0"}, VersionSetId: "2"},
				},
			},
			want: &WorkerBuildIDVersionSets{
				Sets: []*CompatibleVersionSet{
					{BuildIDs: []string{"1.0", "1.1"}, versionSetId: "1"},
					{BuildIDs: []string{"2.0"}, versionSetId: "2"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, workerVersionSetsFromProtoResponse(tt.response), "workerVersionSetsFromProtoResponse(%v)", tt.response)
		})
	}
}
