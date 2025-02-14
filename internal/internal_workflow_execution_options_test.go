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
						Behavior: enumspb.VersioningBehavior(VersioningBehaviorPinned),
						Deployment: &deploymentpb.Deployment{
							SeriesName: "my series",
							BuildId:    "v1",
						},
					},
				},
			},
			want: WorkflowExecutionOptions{
				VersioningOverride: VersioningOverride{
					Behavior: VersioningBehaviorPinned,
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
