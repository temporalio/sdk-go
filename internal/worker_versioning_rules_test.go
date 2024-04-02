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
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Test_WorkerVersioningRules_fromProtoResponse(t *testing.T) {
	nowProto := timestamppb.Now()
	timestamp := nowProto.AsTime()
	tests := []struct {
		name     string
		response *workflowservice.GetWorkerVersioningRulesResponse
		want     *WorkerVersioningRules
	}{
		{
			name:     "nil response",
			response: nil,
			want:     nil,
		},
		{
			name: "normal rules",
			response: &workflowservice.GetWorkerVersioningRulesResponse{
				AssignmentRules: []*taskqueuepb.TimestampedBuildIdAssignmentRule{
					{Rule: &taskqueuepb.BuildIdAssignmentRule{
						TargetBuildId: "one", Ramp: &taskqueuepb.BuildIdAssignmentRule_PercentageRamp{
							PercentageRamp: &taskqueuepb.RampByPercentage{RampPercentage: 50.0},
						},
					},
						CreateTime: nowProto,
					},
				},
				CompatibleRedirectRules: []*taskqueuepb.TimestampedCompatibleBuildIdRedirectRule{
					{Rule: &taskqueuepb.CompatibleBuildIdRedirectRule{SourceBuildId: "one", TargetBuildId: "two"}, CreateTime: nowProto},
					{Rule: &taskqueuepb.CompatibleBuildIdRedirectRule{SourceBuildId: "two", TargetBuildId: "three"}, CreateTime: nowProto},
				},
				ConflictToken: []byte("This is a token"),
			},
			want: &WorkerVersioningRules{
				AssignmentRules: []*VersioningAssignmentRuleWithTimestamp{
					{Rule: VersioningAssignmentRule{TargetBuildID: "one", Ramp: &VersioningRampByPercentage{Percentage: 50.0}}, CreateTime: timestamp},
				},
				RedirectRules: []*VersioningRedirectRuleWithTimestamp{
					{Rule: VersioningRedirectRule{SourceBuildID: "one", TargetBuildID: "two"}, CreateTime: timestamp},
					{Rule: VersioningRedirectRule{SourceBuildID: "two", TargetBuildID: "three"}, CreateTime: timestamp},
				},
				ConflictToken: VersioningConflictToken{
					token: []byte("This is a token"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, workerVersioningRulesFromProtoResponse(tt.response), "workerVersioningRulesFromProtoResponse(%v)", tt.response)
		})
	}
}
