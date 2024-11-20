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
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type (
	// UpdateWorkflowExecutionOptionsRequest is a request for Client.UpdateWorkflowExecutionOptions.
	// NOTE: Experimental
	UpdateWorkflowExecutionOptionsRequest struct {
		// ID of the workflow.
		WorkflowId string
		// Running execution for a workflow ID. If empty string then it will pick the last running execution.
		RunId string
		// RequestId is an optional identifier to deduplicate requests.
		RequestId string
		// WorkflowExecutionOptions specifies options for a target workflow execution. Fields not in
		// UpdatedFields are ignored.
		WorkflowExecutionOptions WorkflowExecutionOptions
		// Field names in WorkflowExecutionOptions that will be updated.
		// When it includes a field name, but the corresponding WorkflowExecutionOptions field has not been set,
		// it will remove previous overrides for that field.
		// It panics when it includes a field name not in WorkflowExecutionOptions.
		// An empty UpdatedFields never modifies WorkflowExecutionOptions.
		UpdatedFields []string
	}

	// GetWorkflowExecutionOptionsRequest is a request for Client.GetWorkflowExecutionOptions.
	// NOTE: Experimental
	GetWorkflowExecutionOptionsRequest struct {
		// ID of the workflow.
		WorkflowId string
		// Running execution for a workflow ID. If empty string then it will pick the last running execution.
		RunId string
	}

	// WorkflowExecutionOptions describes options for a workflow execution.
	// NOTE: Experimental
	WorkflowExecutionOptions struct {
		// If set, it takes precedence over the Versioning Behavior provided with code annotations.
		VersioningOverride VersioningOverride
	}

	// VersioningOverride changes the Versioning Behavior of of a specific workflow execution. If set, it takes precedence
	// over the Versioning Behavior provided with code annotations.
	// To remove the override, the UpdateWorkflowExecutionOptionsRequest should include a default VersioningOverride value
	// in WorkflowExecutionOptions, and a FieldMask that contains the string "VersioningOverride".
	// NOTE: Experimental
	VersioningOverride struct {
		// The new Versioning Behavior. This field is required.
		Behavior VersioningBehavior
		// Identifies the Build ID and Deployment Name to pin the workflow to. Ignored when Behavior is not VersioningBehaviorPinned.
		WorkerDeployment WorkerDeployment
	}

	// WorkerDeployment provides a Build ID and Deployment Name for the workflow.
	// NOTE: Experimental
	WorkerDeployment struct {
		// Name of the deployment.
		DeploymentName string
		// Target Build ID for the deployment.
		BuildId string
	}
)

// Mapping WorkflowExecutionOptions field names to proto ones.
var workflowExecutionOptionsMap map[string]string = map[string]string{
	// TODO: change to "versioning_override" with API upgrade
	"VersioningOverride": "versioning_behavior_override",
}

func generateWorkflowExecutionOptionsPaths(mask []string) []string {
	var result []string
	for _, field := range mask {
		val, ok := workflowExecutionOptionsMap[field]
		if !ok {
			panic(fmt.Sprintf("invalid UpdatedFields entry %s not a field in WorkflowExecutionOptions", field))
		}
		result = append(result, val)
	}
	return result
}

func workflowExecutionOptionsMaskToProto(mask []string) *fieldmaskpb.FieldMask {
	paths := generateWorkflowExecutionOptionsPaths(mask)
	var workflowExecutionOptions *workflowpb.WorkflowExecutionOptions
	protoMask, err := fieldmaskpb.New(workflowExecutionOptions, paths...)
	if err != nil {
		panic("invalid field mask for WorkflowExecutionOptions")
	}
	return protoMask
}

func workerDeploymentToProto(d WorkerDeployment) *commonpb.WorkerDeployment {
	return &commonpb.WorkerDeployment{
		DeploymentName: d.DeploymentName,
		BuildId:        d.BuildId,
	}
}

func workflowExecutionOptionsToProto(options WorkflowExecutionOptions) *workflowpb.WorkflowExecutionOptions {
	return &workflowpb.WorkflowExecutionOptions{
		VersioningBehaviorOverride: &commonpb.VersioningBehaviorOverride{
			Behavior:         versioningBehaviorToProto(options.VersioningOverride.Behavior),
			WorkerDeployment: workerDeploymentToProto(options.VersioningOverride.WorkerDeployment),
		},
	}
}

func workflowExecutionOptionsFromProtoUpdateResponse(response *workflowservice.UpdateWorkflowExecutionOptionsResponse) WorkflowExecutionOptions {
	if response == nil {
		return WorkflowExecutionOptions{}
	}

	behaviorOverride := response.GetWorkflowExecutionOptions().GetVersioningBehaviorOverride()

	return WorkflowExecutionOptions{
		VersioningOverride: VersioningOverride{
			Behavior: VersioningBehavior(behaviorOverride.GetBehavior()),
			WorkerDeployment: WorkerDeployment{
				DeploymentName: behaviorOverride.GetWorkerDeployment().GetDeploymentName(),
				BuildId:        behaviorOverride.GetWorkerDeployment().GetBuildId(),
			},
		},
	}
}
