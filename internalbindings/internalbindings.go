// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

// Package internalbindings contains low level APIs to be used by non Go SDKs
// built on top of the Go SDK.
//
// ATTENTION!
// The APIs found in this package should never be referenced from any application code.
// There is absolutely no guarantee of compatibility between releases.
// Always talk to Temporal team before building anything on top of them.
package internalbindings

import (
	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
)

type (
	// WorkflowType information
	WorkflowType = internal.WorkflowType
	// WorkflowExecution identifiers
	WorkflowExecution = internal.WorkflowExecution
	// WorkflowDefinitionFactory used to create instances of WorkflowDefinition
	WorkflowDefinitionFactory = internal.WorkflowDefinitionFactory
	// WorkflowDefinition is an asynchronous workflow definition
	WorkflowDefinition = internal.WorkflowDefinition
	// WorkflowEnvironment exposes APIs to the WorkflowDefinition
	WorkflowEnvironment = internal.WorkflowEnvironment
	// ExecuteWorkflowParams parameters of the workflow invocation
	ExecuteWorkflowParams = internal.ExecuteWorkflowParams
	// WorkflowOptions options passed to the workflow function
	WorkflowOptions = internal.WorkflowOptions
	// ExecuteActivityParams activity invocation parameters
	ExecuteActivityParams = internal.ExecuteActivityParams
	// ActivityID uniquely identifies activity
	ActivityID = internal.ActivityID
	// ExecuteActivityOptions option for executing an activity
	ExecuteActivityOptions = internal.ExecuteActivityOptions
	// ExecuteLocalActivityParams local activity invocation parameters
	ExecuteLocalActivityParams = internal.ExecuteLocalActivityParams
	// LocalActivityID uniquely identifies a local activity
	LocalActivityID = internal.LocalActivityID
	// ExecuteLocalActivityOptions options for executing a local activity
	ExecuteLocalActivityOptions = internal.ExecuteLocalActivityOptions
	// LocalActivityResultHandler that returns local activity result
	LocalActivityResultHandler = internal.LocalActivityResultHandler
	// LocalActivityResultWrapper contains result of a local activity
	LocalActivityResultWrapper = internal.LocalActivityResultWrapper
	// ActivityType type of activity
	ActivityType = internal.ActivityType
	// ResultHandler result handler function
	ResultHandler = internal.ResultHandler
	// TimerID uniquely identifies timer
	TimerID = internal.TimerID
	// ContinueAsNewError used by a workflow to request continue as new
	ContinueAsNewError = internal.ContinueAsNewError
)

// ConvertErrorToFailure converts Go error to the correspondent Failure protobuf.
func ConvertErrorToFailure(err error, dc converter.DataConverter) *failurepb.Failure {
	return internal.ConvertErrorToFailure(err, dc)
}

// ConvertFailureToError converts Failure protobuf to the correspondent Go error.
func ConvertFailureToError(failure *failurepb.Failure, dc converter.DataConverter) error {
	return internal.ConvertFailureToError(failure, dc)
}

// GetLastCompletionResult returns last completion result from workflow.
func GetLastCompletionResult(env WorkflowEnvironment) *commonpb.Payloads {
	return internal.GetLastCompletionResultFromWorkflowInfo(env.WorkflowInfo())
}
