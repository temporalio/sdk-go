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

	deploymentpb "go.temporal.io/api/deployment/v1"
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

	// WorkflowExecutionOptions describes options for a workflow execution.
	// NOTE: Experimental
	WorkflowExecutionOptions struct {
		// If set, it takes precedence over the Versioning Behavior provided with code annotations.
		VersioningOverride VersioningOverride
	}

	// VersioningOverride changes the versioning configuration of a specific workflow execution.
	// If set, it takes precedence over the Versioning Behavior provided with workflow type registration or
	// default worker options.
	// To remove the override, the UpdateWorkflowExecutionOptionsRequest should include a default VersioningOverride
	// value in WorkflowExecutionOptions, and a FieldMask that contains the string "VersioningOverride".
	// NOTE: Experimental
	VersioningOverride struct {
		// The new Versioning Behavior. This field is required.
		Behavior VersioningBehavior
		// Identifies the Build ID and Deployment Series Name to pin the workflow to. Ignored when Behavior is not
		// VersioningBehaviorPinned.
		Deployment Deployment
	}

	// Deployment identifies a set of workers. This identifier combines the deployment series
	// name with their Build ID.
	// NOTE: Experimental
	Deployment struct {
		// Name of the deployment series. Different versions of the same worker service/application are
		// linked together by sharing a series name.
		SeriesName string
		// Build ID for the worker's code and configuration version.
		BuildId string
	}
)

// Mapping WorkflowExecutionOptions field names to proto ones.
var workflowExecutionOptionsMap map[string]string = map[string]string{
	"VersioningOverride": "versioning_override",
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

func workerDeploymentToProto(d Deployment) *deploymentpb.Deployment {
	return &deploymentpb.Deployment{
		SeriesName: d.SeriesName,
		BuildId:    d.BuildId,
	}
}

func workflowExecutionOptionsToProto(options WorkflowExecutionOptions) *workflowpb.WorkflowExecutionOptions {
	return &workflowpb.WorkflowExecutionOptions{
		VersioningOverride: &workflowpb.VersioningOverride{
			Behavior:   versioningBehaviorToProto(options.VersioningOverride.Behavior),
			Deployment: workerDeploymentToProto(options.VersioningOverride.Deployment),
		},
	}
}

func workflowExecutionOptionsFromProtoUpdateResponse(response *workflowservice.UpdateWorkflowExecutionOptionsResponse) WorkflowExecutionOptions {
	if response == nil {
		return WorkflowExecutionOptions{}
	}

	versioningOverride := response.GetWorkflowExecutionOptions().GetVersioningOverride()

	return WorkflowExecutionOptions{
		VersioningOverride: VersioningOverride{
			Behavior: VersioningBehavior(versioningOverride.GetBehavior()),
			Deployment: Deployment{
				SeriesName: versioningOverride.GetDeployment().GetSeriesName(),
				BuildId:    versioningOverride.GetDeployment().GetBuildId(),
			},
		},
	}
}
