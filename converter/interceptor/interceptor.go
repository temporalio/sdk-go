// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

package interceptor

import (
	"context"

	"go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
)

type (
	// Intercept Request/Response from WorkflowService
	requestResponseInterceptor interface {
		RegisterNamespaceRequest(req *workflowservice.RegisterNamespaceRequest) error
		RegisterNamespaceResponse(req *workflowservice.RegisterNamespaceResponse) error
		ListNamespacesRequest(req *workflowservice.ListNamespacesRequest) error
		ListNamespacesResponse(req *workflowservice.ListNamespacesResponse) error
		DescribeNamespaceRequest(req *workflowservice.DescribeNamespaceRequest) error
		DescribeNamespaceResponse(req *workflowservice.DescribeNamespaceResponse) error
		UpdateNamespaceRequest(req *workflowservice.UpdateNamespaceRequest) error
		UpdateNamespaceResponse(req *workflowservice.UpdateNamespaceResponse) error
		DeprecateNamespaceRequest(req *workflowservice.DeprecateNamespaceRequest) error
		DeprecateNamespaceResponse(req *workflowservice.DeprecateNamespaceResponse) error
		StartWorkflowExecutionRequest(req *workflowservice.StartWorkflowExecutionRequest) error
		StartWorkflowExecutionResponse(req *workflowservice.StartWorkflowExecutionResponse) error
		GetWorkflowExecutionHistoryRequest(req *workflowservice.GetWorkflowExecutionHistoryRequest) error
		GetWorkflowExecutionHistoryResponse(req *workflowservice.GetWorkflowExecutionHistoryResponse) error
		PollWorkflowTaskQueueRequest(req *workflowservice.PollWorkflowTaskQueueRequest) error
		PollWorkflowTaskQueueResponse(req *workflowservice.PollWorkflowTaskQueueResponse) error
		RespondWorkflowTaskCompletedRequest(req *workflowservice.RespondWorkflowTaskCompletedRequest) error
		RespondWorkflowTaskCompletedResponse(req *workflowservice.RespondWorkflowTaskCompletedResponse) error
		RespondWorkflowTaskFailedRequest(req *workflowservice.RespondWorkflowTaskFailedRequest) error
		RespondWorkflowTaskFailedResponse(req *workflowservice.RespondWorkflowTaskFailedResponse) error
		PollActivityTaskQueueRequest(req *workflowservice.PollActivityTaskQueueRequest) error
		PollActivityTaskQueueResponse(req *workflowservice.PollActivityTaskQueueResponse) error
		RecordActivityTaskHeartbeatRequest(req *workflowservice.RecordActivityTaskHeartbeatRequest) error
		RecordActivityTaskHeartbeatResponse(req *workflowservice.RecordActivityTaskHeartbeatResponse) error
		RecordActivityTaskHeartbeatByIdRequest(req *workflowservice.RecordActivityTaskHeartbeatByIdRequest) error
		RecordActivityTaskHeartbeatByIdResponse(req *workflowservice.RecordActivityTaskHeartbeatByIdResponse) error
		RespondActivityTaskCompletedRequest(req *workflowservice.RespondActivityTaskCompletedRequest) error
		RespondActivityTaskCompletedResponse(req *workflowservice.RespondActivityTaskCompletedResponse) error
		RespondActivityTaskCompletedByIdRequest(req *workflowservice.RespondActivityTaskCompletedByIdRequest) error
		RespondActivityTaskCompletedByIdResponse(req *workflowservice.RespondActivityTaskCompletedByIdResponse) error
		RespondActivityTaskFailedRequest(req *workflowservice.RespondActivityTaskFailedRequest) error
		RespondActivityTaskFailedResponse(req *workflowservice.RespondActivityTaskFailedResponse) error
		RespondActivityTaskFailedByIdRequest(req *workflowservice.RespondActivityTaskFailedByIdRequest) error
		RespondActivityTaskFailedByIdResponse(req *workflowservice.RespondActivityTaskFailedByIdResponse) error
		RespondActivityTaskCanceledRequest(req *workflowservice.RespondActivityTaskCanceledRequest) error
		RespondActivityTaskCanceledResponse(req *workflowservice.RespondActivityTaskCanceledResponse) error
		RespondActivityTaskCanceledByIdRequest(req *workflowservice.RespondActivityTaskCanceledByIdRequest) error
		RespondActivityTaskCanceledByIdResponse(req *workflowservice.RespondActivityTaskCanceledByIdResponse) error
		RequestCancelWorkflowExecutionRequest(req *workflowservice.RequestCancelWorkflowExecutionRequest) error
		RequestCancelWorkflowExecutionResponse(req *workflowservice.RequestCancelWorkflowExecutionResponse) error
		SignalWorkflowExecutionRequest(req *workflowservice.SignalWorkflowExecutionRequest) error
		SignalWorkflowExecutionResponse(req *workflowservice.SignalWorkflowExecutionResponse) error
		SignalWithStartWorkflowExecutionRequest(req *workflowservice.SignalWithStartWorkflowExecutionRequest) error
		SignalWithStartWorkflowExecutionResponse(req *workflowservice.SignalWithStartWorkflowExecutionResponse) error
		ResetWorkflowExecutionRequest(req *workflowservice.ResetWorkflowExecutionRequest) error
		ResetWorkflowExecutionResponse(req *workflowservice.ResetWorkflowExecutionResponse) error
		TerminateWorkflowExecutionRequest(req *workflowservice.TerminateWorkflowExecutionRequest) error
		TerminateWorkflowExecutionResponse(req *workflowservice.TerminateWorkflowExecutionResponse) error
		ListOpenWorkflowExecutionsRequest(req *workflowservice.ListOpenWorkflowExecutionsRequest) error
		ListOpenWorkflowExecutionsResponse(req *workflowservice.ListOpenWorkflowExecutionsResponse) error
		ListClosedWorkflowExecutionsRequest(req *workflowservice.ListClosedWorkflowExecutionsRequest) error
		ListClosedWorkflowExecutionsResponse(req *workflowservice.ListClosedWorkflowExecutionsResponse) error
		ListWorkflowExecutionsRequest(req *workflowservice.ListWorkflowExecutionsRequest) error
		ListWorkflowExecutionsResponse(req *workflowservice.ListWorkflowExecutionsResponse) error
		ListArchivedWorkflowExecutionsRequest(req *workflowservice.ListArchivedWorkflowExecutionsRequest) error
		ListArchivedWorkflowExecutionsResponse(req *workflowservice.ListArchivedWorkflowExecutionsResponse) error
		ScanWorkflowExecutionsRequest(req *workflowservice.ScanWorkflowExecutionsRequest) error
		ScanWorkflowExecutionsResponse(req *workflowservice.ScanWorkflowExecutionsResponse) error
		CountWorkflowExecutionsRequest(req *workflowservice.CountWorkflowExecutionsRequest) error
		CountWorkflowExecutionsResponse(req *workflowservice.CountWorkflowExecutionsResponse) error
		GetSearchAttributesRequest(req *workflowservice.GetSearchAttributesRequest) error
		GetSearchAttributesResponse(req *workflowservice.GetSearchAttributesResponse) error
		RespondQueryTaskCompletedRequest(req *workflowservice.RespondQueryTaskCompletedRequest) error
		RespondQueryTaskCompletedResponse(req *workflowservice.RespondQueryTaskCompletedResponse) error
		ResetStickyTaskQueueRequest(req *workflowservice.ResetStickyTaskQueueRequest) error
		ResetStickyTaskQueueResponse(req *workflowservice.ResetStickyTaskQueueResponse) error
		QueryWorkflowRequest(req *workflowservice.QueryWorkflowRequest) error
		QueryWorkflowResponse(req *workflowservice.QueryWorkflowResponse) error
		DescribeWorkflowExecutionRequest(req *workflowservice.DescribeWorkflowExecutionRequest) error
		DescribeWorkflowExecutionResponse(req *workflowservice.DescribeWorkflowExecutionResponse) error
		DescribeTaskQueueRequest(req *workflowservice.DescribeTaskQueueRequest) error
		DescribeTaskQueueResponse(req *workflowservice.DescribeTaskQueueResponse) error
		GetClusterInfoRequest(req *workflowservice.GetClusterInfoRequest) error
		GetClusterInfoResponse(req *workflowservice.GetClusterInfoResponse) error
		ListTaskQueuePartitionsRequest(req *workflowservice.ListTaskQueuePartitionsRequest) error
		ListTaskQueuePartitionsResponse(req *workflowservice.ListTaskQueuePartitionsResponse) error
	}

	// Intercept commands contained within WorkflowService requests/responses
	commandInterceptor interface {
		ScheduleActivityTask(attrs *command.ScheduleActivityTaskCommandAttributes) error
		RequestCancelActivityTask(attrs *command.RequestCancelActivityTaskCommandAttributes) error
		StartTimer(attrs *command.StartTimerCommandAttributes) error
		CompleteWorkflowExecution(attrs *command.CompleteWorkflowExecutionCommandAttributes) error
		FailWorkflowExecution(attrs *command.FailWorkflowExecutionCommandAttributes) error
		CancelTimer(attrs *command.CancelTimerCommandAttributes) error
		CancelWorkflowExecution(attrs *command.CancelWorkflowExecutionCommandAttributes) error
		RequestCancelExternalWorkflowExecution(attrs *command.RequestCancelExternalWorkflowExecutionCommandAttributes) error
		SignalExternalWorkflowExecution(attrs *command.SignalExternalWorkflowExecutionCommandAttributes) error
		UpsertWorkflowSearchAttributes(attrs *command.UpsertWorkflowSearchAttributesCommandAttributes) error
		RecordMarker(attrs *command.RecordMarkerCommandAttributes) error
		ContinueAsNewWorkflowExecution(attrs *command.ContinueAsNewWorkflowExecutionCommandAttributes) error
		StartChildWorkflowExecution(attrs *command.StartChildWorkflowExecutionCommandAttributes) error
	}

	// Intercept history events contained within WorkflowService requests/responses
	eventInterceptor interface {
		WorkflowExecutionStarted(attrs *historypb.WorkflowExecutionStartedEventAttributes) error
		WorkflowExecutionCompleted(attrs *historypb.WorkflowExecutionCompletedEventAttributes) error
		WorkflowExecutionFailed(attrs *historypb.WorkflowExecutionFailedEventAttributes) error
		WorkflowExecutionTimedOut(attrs *historypb.WorkflowExecutionTimedOutEventAttributes) error
		WorkflowExecutionContinuedAsNew(attrs *historypb.WorkflowExecutionContinuedAsNewEventAttributes) error
		WorkflowTaskScheduled(attrs *historypb.WorkflowTaskScheduledEventAttributes) error
		WorkflowTaskStarted(attrs *historypb.WorkflowTaskStartedEventAttributes) error
		WorkflowTaskCompleted(attrs *historypb.WorkflowTaskCompletedEventAttributes) error
		WorkflowTaskTimedOut(attrs *historypb.WorkflowTaskTimedOutEventAttributes) error
		WorkflowTaskFailed(attrs *historypb.WorkflowTaskFailedEventAttributes) error
		ActivityTaskScheduled(attrs *historypb.ActivityTaskScheduledEventAttributes) error
		ActivityTaskStarted(attrs *historypb.ActivityTaskStartedEventAttributes) error
		ActivityTaskCompleted(attrs *historypb.ActivityTaskCompletedEventAttributes) error
		ActivityTaskFailed(attrs *historypb.ActivityTaskFailedEventAttributes) error
		ActivityTaskTimedOut(attrs *historypb.ActivityTaskTimedOutEventAttributes) error
		ActivityTaskCancelRequested(attrs *historypb.ActivityTaskCancelRequestedEventAttributes) error
		ActivityTaskCanceled(attrs *historypb.ActivityTaskCanceledEventAttributes) error
		TimerStarted(attrs *historypb.TimerStartedEventAttributes) error
		TimerFired(attrs *historypb.TimerFiredEventAttributes) error
		TimerCanceled(attrs *historypb.TimerCanceledEventAttributes) error
		WorkflowExecutionCancelRequested(attrs *historypb.WorkflowExecutionCancelRequestedEventAttributes) error
		WorkflowExecutionCanceled(attrs *historypb.WorkflowExecutionCanceledEventAttributes) error
		MarkerRecorded(attrs *historypb.MarkerRecordedEventAttributes) error
		WorkflowExecutionSignaled(attrs *historypb.WorkflowExecutionSignaledEventAttributes) error
		WorkflowExecutionTerminated(attrs *historypb.WorkflowExecutionTerminatedEventAttributes) error
		RequestCancelExternalWorkflowExecutionInitiated(attrs *historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes) error
		RequestCancelExternalWorkflowExecutionFailed(attrs *historypb.RequestCancelExternalWorkflowExecutionFailedEventAttributes) error
		ExternalWorkflowExecutionCancelRequested(attrs *historypb.ExternalWorkflowExecutionCancelRequestedEventAttributes) error
		SignalExternalWorkflowExecutionInitiated(attrs *historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes) error
		SignalExternalWorkflowExecutionFailed(attrs *historypb.SignalExternalWorkflowExecutionFailedEventAttributes) error
		ExternalWorkflowExecutionSignaled(attrs *historypb.ExternalWorkflowExecutionSignaledEventAttributes) error
		UpsertWorkflowSearchAttributes(attrs *historypb.UpsertWorkflowSearchAttributesEventAttributes) error
		StartChildWorkflowExecutionInitiated(attrs *historypb.StartChildWorkflowExecutionInitiatedEventAttributes) error
		StartChildWorkflowExecutionFailed(attrs *historypb.StartChildWorkflowExecutionFailedEventAttributes) error
		ChildWorkflowExecutionStarted(attrs *historypb.ChildWorkflowExecutionStartedEventAttributes) error
		ChildWorkflowExecutionCompleted(attrs *historypb.ChildWorkflowExecutionCompletedEventAttributes) error
		ChildWorkflowExecutionFailed(attrs *historypb.ChildWorkflowExecutionFailedEventAttributes) error
		ChildWorkflowExecutionCanceled(attrs *historypb.ChildWorkflowExecutionCanceledEventAttributes) error
		ChildWorkflowExecutionTimedOut(attrs *historypb.ChildWorkflowExecutionTimedOutEventAttributes) error
		ChildWorkflowExecutionTerminated(attrs *historypb.ChildWorkflowExecutionTerminatedEventAttributes) error
	}

	serviceInterceptor struct {
		RequestResponse requestResponseInterceptor
		Command         commandInterceptor
		Event           eventInterceptor
	}
)

type baseRequestResponseInterceptor struct{}

var _ requestResponseInterceptor = &baseRequestResponseInterceptor{}

func (*baseRequestResponseInterceptor) RegisterNamespaceRequest(req *workflowservice.RegisterNamespaceRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RegisterNamespaceResponse(req *workflowservice.RegisterNamespaceResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListNamespacesRequest(req *workflowservice.ListNamespacesRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListNamespacesResponse(req *workflowservice.ListNamespacesResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) DescribeNamespaceRequest(req *workflowservice.DescribeNamespaceRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) DescribeNamespaceResponse(req *workflowservice.DescribeNamespaceResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) UpdateNamespaceRequest(req *workflowservice.UpdateNamespaceRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) UpdateNamespaceResponse(req *workflowservice.UpdateNamespaceResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) DeprecateNamespaceRequest(req *workflowservice.DeprecateNamespaceRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) DeprecateNamespaceResponse(req *workflowservice.DeprecateNamespaceResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) StartWorkflowExecutionRequest(req *workflowservice.StartWorkflowExecutionRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) StartWorkflowExecutionResponse(req *workflowservice.StartWorkflowExecutionResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) GetWorkflowExecutionHistoryRequest(req *workflowservice.GetWorkflowExecutionHistoryRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) GetWorkflowExecutionHistoryResponse(req *workflowservice.GetWorkflowExecutionHistoryResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) PollWorkflowTaskQueueRequest(req *workflowservice.PollWorkflowTaskQueueRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) PollWorkflowTaskQueueResponse(req *workflowservice.PollWorkflowTaskQueueResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondWorkflowTaskCompletedRequest(req *workflowservice.RespondWorkflowTaskCompletedRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondWorkflowTaskCompletedResponse(req *workflowservice.RespondWorkflowTaskCompletedResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondWorkflowTaskFailedRequest(req *workflowservice.RespondWorkflowTaskFailedRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondWorkflowTaskFailedResponse(req *workflowservice.RespondWorkflowTaskFailedResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) PollActivityTaskQueueRequest(req *workflowservice.PollActivityTaskQueueRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) PollActivityTaskQueueResponse(req *workflowservice.PollActivityTaskQueueResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RecordActivityTaskHeartbeatRequest(req *workflowservice.RecordActivityTaskHeartbeatRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RecordActivityTaskHeartbeatResponse(req *workflowservice.RecordActivityTaskHeartbeatResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RecordActivityTaskHeartbeatByIdRequest(req *workflowservice.RecordActivityTaskHeartbeatByIdRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RecordActivityTaskHeartbeatByIdResponse(req *workflowservice.RecordActivityTaskHeartbeatByIdResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCompletedRequest(req *workflowservice.RespondActivityTaskCompletedRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCompletedResponse(req *workflowservice.RespondActivityTaskCompletedResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCompletedByIdRequest(req *workflowservice.RespondActivityTaskCompletedByIdRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCompletedByIdResponse(req *workflowservice.RespondActivityTaskCompletedByIdResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskFailedRequest(req *workflowservice.RespondActivityTaskFailedRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskFailedResponse(req *workflowservice.RespondActivityTaskFailedResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskFailedByIdRequest(req *workflowservice.RespondActivityTaskFailedByIdRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskFailedByIdResponse(req *workflowservice.RespondActivityTaskFailedByIdResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCanceledRequest(req *workflowservice.RespondActivityTaskCanceledRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCanceledResponse(req *workflowservice.RespondActivityTaskCanceledResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCanceledByIdRequest(req *workflowservice.RespondActivityTaskCanceledByIdRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondActivityTaskCanceledByIdResponse(req *workflowservice.RespondActivityTaskCanceledByIdResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RequestCancelWorkflowExecutionRequest(req *workflowservice.RequestCancelWorkflowExecutionRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RequestCancelWorkflowExecutionResponse(req *workflowservice.RequestCancelWorkflowExecutionResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) SignalWorkflowExecutionRequest(req *workflowservice.SignalWorkflowExecutionRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) SignalWorkflowExecutionResponse(req *workflowservice.SignalWorkflowExecutionResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) SignalWithStartWorkflowExecutionRequest(req *workflowservice.SignalWithStartWorkflowExecutionRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) SignalWithStartWorkflowExecutionResponse(req *workflowservice.SignalWithStartWorkflowExecutionResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ResetWorkflowExecutionRequest(req *workflowservice.ResetWorkflowExecutionRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ResetWorkflowExecutionResponse(req *workflowservice.ResetWorkflowExecutionResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) TerminateWorkflowExecutionRequest(req *workflowservice.TerminateWorkflowExecutionRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) TerminateWorkflowExecutionResponse(req *workflowservice.TerminateWorkflowExecutionResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListOpenWorkflowExecutionsRequest(req *workflowservice.ListOpenWorkflowExecutionsRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListOpenWorkflowExecutionsResponse(req *workflowservice.ListOpenWorkflowExecutionsResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListClosedWorkflowExecutionsRequest(req *workflowservice.ListClosedWorkflowExecutionsRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListClosedWorkflowExecutionsResponse(req *workflowservice.ListClosedWorkflowExecutionsResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListWorkflowExecutionsRequest(req *workflowservice.ListWorkflowExecutionsRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListWorkflowExecutionsResponse(req *workflowservice.ListWorkflowExecutionsResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListArchivedWorkflowExecutionsRequest(req *workflowservice.ListArchivedWorkflowExecutionsRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListArchivedWorkflowExecutionsResponse(req *workflowservice.ListArchivedWorkflowExecutionsResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ScanWorkflowExecutionsRequest(req *workflowservice.ScanWorkflowExecutionsRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ScanWorkflowExecutionsResponse(req *workflowservice.ScanWorkflowExecutionsResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) CountWorkflowExecutionsRequest(req *workflowservice.CountWorkflowExecutionsRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) CountWorkflowExecutionsResponse(req *workflowservice.CountWorkflowExecutionsResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) GetSearchAttributesRequest(req *workflowservice.GetSearchAttributesRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) GetSearchAttributesResponse(req *workflowservice.GetSearchAttributesResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondQueryTaskCompletedRequest(req *workflowservice.RespondQueryTaskCompletedRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) RespondQueryTaskCompletedResponse(req *workflowservice.RespondQueryTaskCompletedResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ResetStickyTaskQueueRequest(req *workflowservice.ResetStickyTaskQueueRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ResetStickyTaskQueueResponse(req *workflowservice.ResetStickyTaskQueueResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) QueryWorkflowRequest(req *workflowservice.QueryWorkflowRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) QueryWorkflowResponse(req *workflowservice.QueryWorkflowResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) DescribeWorkflowExecutionRequest(req *workflowservice.DescribeWorkflowExecutionRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) DescribeWorkflowExecutionResponse(req *workflowservice.DescribeWorkflowExecutionResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) DescribeTaskQueueRequest(req *workflowservice.DescribeTaskQueueRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) DescribeTaskQueueResponse(req *workflowservice.DescribeTaskQueueResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) GetClusterInfoRequest(req *workflowservice.GetClusterInfoRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) GetClusterInfoResponse(req *workflowservice.GetClusterInfoResponse) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListTaskQueuePartitionsRequest(req *workflowservice.ListTaskQueuePartitionsRequest) error {
	return nil
}

func (*baseRequestResponseInterceptor) ListTaskQueuePartitionsResponse(req *workflowservice.ListTaskQueuePartitionsResponse) error {
	return nil
}

type baseCommandInterceptor struct{}

var _ commandInterceptor = &baseCommandInterceptor{}

func (*baseCommandInterceptor) ScheduleActivityTask(attrs *command.ScheduleActivityTaskCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) RequestCancelActivityTask(attrs *command.RequestCancelActivityTaskCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) StartTimer(attrs *command.StartTimerCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) CompleteWorkflowExecution(attrs *command.CompleteWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) FailWorkflowExecution(attrs *command.FailWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) CancelTimer(attrs *command.CancelTimerCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) CancelWorkflowExecution(attrs *command.CancelWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) RequestCancelExternalWorkflowExecution(attrs *command.RequestCancelExternalWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) SignalExternalWorkflowExecution(attrs *command.SignalExternalWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) UpsertWorkflowSearchAttributes(attrs *command.UpsertWorkflowSearchAttributesCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) RecordMarker(attrs *command.RecordMarkerCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) ContinueAsNewWorkflowExecution(attrs *command.ContinueAsNewWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*baseCommandInterceptor) StartChildWorkflowExecution(attrs *command.StartChildWorkflowExecutionCommandAttributes) error {
	return nil
}

type baseEventInterceptor struct{}

var _ eventInterceptor = &baseEventInterceptor{}

func (*baseEventInterceptor) WorkflowExecutionStarted(attrs *historypb.WorkflowExecutionStartedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionCompleted(attrs *historypb.WorkflowExecutionCompletedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionFailed(attrs *historypb.WorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionTimedOut(attrs *historypb.WorkflowExecutionTimedOutEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionContinuedAsNew(attrs *historypb.WorkflowExecutionContinuedAsNewEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowTaskScheduled(attrs *historypb.WorkflowTaskScheduledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowTaskStarted(attrs *historypb.WorkflowTaskStartedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowTaskCompleted(attrs *historypb.WorkflowTaskCompletedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowTaskTimedOut(attrs *historypb.WorkflowTaskTimedOutEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowTaskFailed(attrs *historypb.WorkflowTaskFailedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ActivityTaskScheduled(attrs *historypb.ActivityTaskScheduledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ActivityTaskStarted(attrs *historypb.ActivityTaskStartedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ActivityTaskCompleted(attrs *historypb.ActivityTaskCompletedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ActivityTaskFailed(attrs *historypb.ActivityTaskFailedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ActivityTaskTimedOut(attrs *historypb.ActivityTaskTimedOutEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ActivityTaskCancelRequested(attrs *historypb.ActivityTaskCancelRequestedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ActivityTaskCanceled(attrs *historypb.ActivityTaskCanceledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) TimerStarted(attrs *historypb.TimerStartedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) TimerFired(attrs *historypb.TimerFiredEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) TimerCanceled(attrs *historypb.TimerCanceledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionCancelRequested(attrs *historypb.WorkflowExecutionCancelRequestedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionCanceled(attrs *historypb.WorkflowExecutionCanceledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) MarkerRecorded(attrs *historypb.MarkerRecordedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionSignaled(attrs *historypb.WorkflowExecutionSignaledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) WorkflowExecutionTerminated(attrs *historypb.WorkflowExecutionTerminatedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) RequestCancelExternalWorkflowExecutionInitiated(attrs *historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) RequestCancelExternalWorkflowExecutionFailed(attrs *historypb.RequestCancelExternalWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ExternalWorkflowExecutionCancelRequested(attrs *historypb.ExternalWorkflowExecutionCancelRequestedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) SignalExternalWorkflowExecutionInitiated(attrs *historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) SignalExternalWorkflowExecutionFailed(attrs *historypb.SignalExternalWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ExternalWorkflowExecutionSignaled(attrs *historypb.ExternalWorkflowExecutionSignaledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) UpsertWorkflowSearchAttributes(attrs *historypb.UpsertWorkflowSearchAttributesEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) StartChildWorkflowExecutionInitiated(attrs *historypb.StartChildWorkflowExecutionInitiatedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) StartChildWorkflowExecutionFailed(attrs *historypb.StartChildWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ChildWorkflowExecutionStarted(attrs *historypb.ChildWorkflowExecutionStartedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ChildWorkflowExecutionCompleted(attrs *historypb.ChildWorkflowExecutionCompletedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ChildWorkflowExecutionFailed(attrs *historypb.ChildWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ChildWorkflowExecutionCanceled(attrs *historypb.ChildWorkflowExecutionCanceledEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ChildWorkflowExecutionTimedOut(attrs *historypb.ChildWorkflowExecutionTimedOutEventAttributes) error {
	return nil
}

func (*baseEventInterceptor) ChildWorkflowExecutionTerminated(attrs *historypb.ChildWorkflowExecutionTerminatedEventAttributes) error {
	return nil
}

func (s *serviceInterceptor) processRequest(req interface{}) error {
	var err error

	switch r := req.(type) {
	case *workflowservice.RegisterNamespaceRequest:
		err = s.RequestResponse.RegisterNamespaceRequest(r)
	case *workflowservice.ListNamespacesRequest:
		err = s.RequestResponse.ListNamespacesRequest(r)
	case *workflowservice.DescribeNamespaceRequest:
		err = s.RequestResponse.DescribeNamespaceRequest(r)
	case *workflowservice.UpdateNamespaceRequest:
		err = s.RequestResponse.UpdateNamespaceRequest(r)
	case *workflowservice.DeprecateNamespaceRequest:
		err = s.RequestResponse.DeprecateNamespaceRequest(r)
	case *workflowservice.StartWorkflowExecutionRequest:
		err = s.RequestResponse.StartWorkflowExecutionRequest(r)
	case *workflowservice.GetWorkflowExecutionHistoryRequest:
		err = s.RequestResponse.GetWorkflowExecutionHistoryRequest(r)
	case *workflowservice.PollWorkflowTaskQueueRequest:
		err = s.RequestResponse.PollWorkflowTaskQueueRequest(r)
	case *workflowservice.RespondWorkflowTaskCompletedRequest:
		err = s.RequestResponse.RespondWorkflowTaskCompletedRequest(r)
		if err != nil {
			return err
		}
		err = s.processCommands(r.Commands)
	case *workflowservice.RespondWorkflowTaskFailedRequest:
		err = s.RequestResponse.RespondWorkflowTaskFailedRequest(r)
	case *workflowservice.PollActivityTaskQueueRequest:
		err = s.RequestResponse.PollActivityTaskQueueRequest(r)
	case *workflowservice.RecordActivityTaskHeartbeatRequest:
		err = s.RequestResponse.RecordActivityTaskHeartbeatRequest(r)
	case *workflowservice.RecordActivityTaskHeartbeatByIdRequest:
		err = s.RequestResponse.RecordActivityTaskHeartbeatByIdRequest(r)
	case *workflowservice.RespondActivityTaskCompletedRequest:
		err = s.RequestResponse.RespondActivityTaskCompletedRequest(r)
	case *workflowservice.RespondActivityTaskCompletedByIdRequest:
		err = s.RequestResponse.RespondActivityTaskCompletedByIdRequest(r)
	case *workflowservice.RespondActivityTaskFailedRequest:
		err = s.RequestResponse.RespondActivityTaskFailedRequest(r)
	case *workflowservice.RespondActivityTaskFailedByIdRequest:
		err = s.RequestResponse.RespondActivityTaskFailedByIdRequest(r)
	case *workflowservice.RespondActivityTaskCanceledRequest:
		err = s.RequestResponse.RespondActivityTaskCanceledRequest(r)
	case *workflowservice.RespondActivityTaskCanceledByIdRequest:
		err = s.RequestResponse.RespondActivityTaskCanceledByIdRequest(r)
	case *workflowservice.RequestCancelWorkflowExecutionRequest:
		err = s.RequestResponse.RequestCancelWorkflowExecutionRequest(r)
	case *workflowservice.SignalWorkflowExecutionRequest:
		err = s.RequestResponse.SignalWorkflowExecutionRequest(r)
	case *workflowservice.SignalWithStartWorkflowExecutionRequest:
		err = s.RequestResponse.SignalWithStartWorkflowExecutionRequest(r)
	case *workflowservice.ResetWorkflowExecutionRequest:
		err = s.RequestResponse.ResetWorkflowExecutionRequest(r)
	case *workflowservice.TerminateWorkflowExecutionRequest:
		err = s.RequestResponse.TerminateWorkflowExecutionRequest(r)
	case *workflowservice.ListOpenWorkflowExecutionsRequest:
		err = s.RequestResponse.ListOpenWorkflowExecutionsRequest(r)
	case *workflowservice.ListClosedWorkflowExecutionsRequest:
		err = s.RequestResponse.ListClosedWorkflowExecutionsRequest(r)
	case *workflowservice.ListWorkflowExecutionsRequest:
		err = s.RequestResponse.ListWorkflowExecutionsRequest(r)
	case *workflowservice.ListArchivedWorkflowExecutionsRequest:
		err = s.RequestResponse.ListArchivedWorkflowExecutionsRequest(r)
	case *workflowservice.ScanWorkflowExecutionsRequest:
		err = s.RequestResponse.ScanWorkflowExecutionsRequest(r)
	case *workflowservice.CountWorkflowExecutionsRequest:
		err = s.RequestResponse.CountWorkflowExecutionsRequest(r)
	case *workflowservice.GetSearchAttributesRequest:
		err = s.RequestResponse.GetSearchAttributesRequest(r)
	case *workflowservice.RespondQueryTaskCompletedRequest:
		err = s.RequestResponse.RespondQueryTaskCompletedRequest(r)
	case *workflowservice.ResetStickyTaskQueueRequest:
		err = s.RequestResponse.ResetStickyTaskQueueRequest(r)
	case *workflowservice.QueryWorkflowRequest:
		err = s.RequestResponse.QueryWorkflowRequest(r)
	case *workflowservice.DescribeWorkflowExecutionRequest:
		err = s.RequestResponse.DescribeWorkflowExecutionRequest(r)
	case *workflowservice.DescribeTaskQueueRequest:
		err = s.RequestResponse.DescribeTaskQueueRequest(r)
	case *workflowservice.GetClusterInfoRequest:
		err = s.RequestResponse.GetClusterInfoRequest(r)
	case *workflowservice.ListTaskQueuePartitionsRequest:
		err = s.RequestResponse.ListTaskQueuePartitionsRequest(r)
	}

	return err
}

func (s *serviceInterceptor) processResponse(response interface{}) error {
	var err error

	switch r := response.(type) {
	case *workflowservice.RegisterNamespaceResponse:
		err = s.RequestResponse.RegisterNamespaceResponse(r)
	case *workflowservice.ListNamespacesResponse:
		err = s.RequestResponse.ListNamespacesResponse(r)
	case *workflowservice.DescribeNamespaceResponse:
		err = s.RequestResponse.DescribeNamespaceResponse(r)
	case *workflowservice.UpdateNamespaceResponse:
		err = s.RequestResponse.UpdateNamespaceResponse(r)
	case *workflowservice.DeprecateNamespaceResponse:
		err = s.RequestResponse.DeprecateNamespaceResponse(r)
	case *workflowservice.StartWorkflowExecutionResponse:
		err = s.RequestResponse.StartWorkflowExecutionResponse(r)
	case *workflowservice.GetWorkflowExecutionHistoryResponse:
		err = s.RequestResponse.GetWorkflowExecutionHistoryResponse(r)
		if err != nil {
			return err
		}

		err = s.processEvents(r.History.Events)
	case *workflowservice.PollWorkflowTaskQueueResponse:
		if r.WorkflowType != nil {
			err = s.RequestResponse.PollWorkflowTaskQueueResponse(r)
			if err != nil {
				return err
			}
			err = s.processEvents(r.History.Events)
		}
	case *workflowservice.RespondWorkflowTaskCompletedResponse:
		err = s.RequestResponse.RespondWorkflowTaskCompletedResponse(r)
	case *workflowservice.RespondWorkflowTaskFailedResponse:
		err = s.RequestResponse.RespondWorkflowTaskFailedResponse(r)
	case *workflowservice.PollActivityTaskQueueResponse:
		if r.ActivityType != nil {
			err = s.RequestResponse.PollActivityTaskQueueResponse(r)
		}
	case *workflowservice.RecordActivityTaskHeartbeatResponse:
		err = s.RequestResponse.RecordActivityTaskHeartbeatResponse(r)
	case *workflowservice.RecordActivityTaskHeartbeatByIdResponse:
		err = s.RequestResponse.RecordActivityTaskHeartbeatByIdResponse(r)
	case *workflowservice.RespondActivityTaskCompletedResponse:
		err = s.RequestResponse.RespondActivityTaskCompletedResponse(r)
	case *workflowservice.RespondActivityTaskCompletedByIdResponse:
		err = s.RequestResponse.RespondActivityTaskCompletedByIdResponse(r)
	case *workflowservice.RespondActivityTaskFailedResponse:
		err = s.RequestResponse.RespondActivityTaskFailedResponse(r)
	case *workflowservice.RespondActivityTaskFailedByIdResponse:
		err = s.RequestResponse.RespondActivityTaskFailedByIdResponse(r)
	case *workflowservice.RespondActivityTaskCanceledResponse:
		err = s.RequestResponse.RespondActivityTaskCanceledResponse(r)
	case *workflowservice.RespondActivityTaskCanceledByIdResponse:
		err = s.RequestResponse.RespondActivityTaskCanceledByIdResponse(r)
	case *workflowservice.RequestCancelWorkflowExecutionResponse:
		err = s.RequestResponse.RequestCancelWorkflowExecutionResponse(r)
	case *workflowservice.SignalWorkflowExecutionResponse:
		err = s.RequestResponse.SignalWorkflowExecutionResponse(r)
	case *workflowservice.SignalWithStartWorkflowExecutionResponse:
		err = s.RequestResponse.SignalWithStartWorkflowExecutionResponse(r)
	case *workflowservice.ResetWorkflowExecutionResponse:
		err = s.RequestResponse.ResetWorkflowExecutionResponse(r)
	case *workflowservice.TerminateWorkflowExecutionResponse:
		err = s.RequestResponse.TerminateWorkflowExecutionResponse(r)
	case *workflowservice.ListOpenWorkflowExecutionsResponse:
		err = s.RequestResponse.ListOpenWorkflowExecutionsResponse(r)
	case *workflowservice.ListClosedWorkflowExecutionsResponse:
		err = s.RequestResponse.ListClosedWorkflowExecutionsResponse(r)
	case *workflowservice.ListWorkflowExecutionsResponse:
		err = s.RequestResponse.ListWorkflowExecutionsResponse(r)
	case *workflowservice.ListArchivedWorkflowExecutionsResponse:
		err = s.RequestResponse.ListArchivedWorkflowExecutionsResponse(r)
	case *workflowservice.ScanWorkflowExecutionsResponse:
		err = s.RequestResponse.ScanWorkflowExecutionsResponse(r)
	case *workflowservice.CountWorkflowExecutionsResponse:
		err = s.RequestResponse.CountWorkflowExecutionsResponse(r)
	case *workflowservice.GetSearchAttributesResponse:
		err = s.RequestResponse.GetSearchAttributesResponse(r)
	case *workflowservice.RespondQueryTaskCompletedResponse:
		err = s.RequestResponse.RespondQueryTaskCompletedResponse(r)
	case *workflowservice.ResetStickyTaskQueueResponse:
		err = s.RequestResponse.ResetStickyTaskQueueResponse(r)
	case *workflowservice.QueryWorkflowResponse:
		err = s.RequestResponse.QueryWorkflowResponse(r)
	case *workflowservice.DescribeWorkflowExecutionResponse:
		err = s.RequestResponse.DescribeWorkflowExecutionResponse(r)
	case *workflowservice.DescribeTaskQueueResponse:
		err = s.RequestResponse.DescribeTaskQueueResponse(r)
	case *workflowservice.GetClusterInfoResponse:
		err = s.RequestResponse.GetClusterInfoResponse(r)
	case *workflowservice.ListTaskQueuePartitionsResponse:
		err = s.RequestResponse.ListTaskQueuePartitionsResponse(r)
	}

	return err
}

func (s *serviceInterceptor) processCommands(commands []*command.Command) error {
	var err error

	for _, c := range commands {
		switch c.CommandType {
		case enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK:
			err = s.Command.ScheduleActivityTask(c.GetScheduleActivityTaskCommandAttributes())
		case enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK:
			err = s.Command.RequestCancelActivityTask(c.GetRequestCancelActivityTaskCommandAttributes())
		case enumspb.COMMAND_TYPE_START_TIMER:
			err = s.Command.StartTimer(c.GetStartTimerCommandAttributes())
		case enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION:
			err = s.Command.CompleteWorkflowExecution(c.GetCompleteWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_FAIL_WORKFLOW_EXECUTION:
			err = s.Command.FailWorkflowExecution(c.GetFailWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_CANCEL_TIMER:
			err = s.Command.CancelTimer(c.GetCancelTimerCommandAttributes())
		case enumspb.COMMAND_TYPE_CANCEL_WORKFLOW_EXECUTION:
			err = s.Command.CancelWorkflowExecution(c.GetCancelWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION:
			err = s.Command.RequestCancelExternalWorkflowExecution(c.GetRequestCancelExternalWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_RECORD_MARKER:
			err = s.Command.RecordMarker(c.GetRecordMarkerCommandAttributes())
		case enumspb.COMMAND_TYPE_CONTINUE_AS_NEW_WORKFLOW_EXECUTION:
			err = s.Command.ContinueAsNewWorkflowExecution(c.GetContinueAsNewWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_START_CHILD_WORKFLOW_EXECUTION:
			err = s.Command.StartChildWorkflowExecution(c.GetStartChildWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION:
			err = s.Command.SignalExternalWorkflowExecution(c.GetSignalExternalWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES:
			err = s.Command.UpsertWorkflowSearchAttributes(c.GetUpsertWorkflowSearchAttributesCommandAttributes())
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *serviceInterceptor) processEvents(events []*historypb.HistoryEvent) error {
	if s.Event == nil {
		return nil
	}

	var err error

	for _, e := range events {
		switch e.EventType {
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
			err = s.Event.WorkflowExecutionStarted(e.GetWorkflowExecutionStartedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
			err = s.Event.WorkflowExecutionCompleted(e.GetWorkflowExecutionCompletedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
			err = s.Event.WorkflowExecutionFailed(e.GetWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
			err = s.Event.WorkflowExecutionTimedOut(e.GetWorkflowExecutionTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED:
			err = s.Event.WorkflowTaskScheduled(e.GetWorkflowTaskScheduledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED:
			err = s.Event.WorkflowTaskStarted(e.GetWorkflowTaskStartedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			err = s.Event.ActivityTaskScheduled(e.GetActivityTaskScheduledEventAttributes())
		case enumspb.EVENT_TYPE_TIMER_STARTED:
			err = s.Event.TimerStarted(e.GetTimerStartedEventAttributes())
		case enumspb.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES:
			err = s.Event.UpsertWorkflowSearchAttributes(e.GetUpsertWorkflowSearchAttributesEventAttributes())
		case enumspb.EVENT_TYPE_MARKER_RECORDED:
			err = s.Event.MarkerRecorded(e.GetMarkerRecordedEventAttributes())
		case enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED:
			err = s.Event.StartChildWorkflowExecutionInitiated(e.GetStartChildWorkflowExecutionInitiatedEventAttributes())
		case enumspb.EVENT_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED:
			err = s.Event.RequestCancelExternalWorkflowExecutionInitiated(e.GetRequestCancelExternalWorkflowExecutionInitiatedEventAttributes())
		case enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED:
			err = s.Event.SignalExternalWorkflowExecutionInitiated(e.GetSignalExternalWorkflowExecutionInitiatedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
			err = s.Event.WorkflowExecutionCanceled(e.GetWorkflowExecutionCanceledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
			err = s.Event.WorkflowExecutionContinuedAsNew(e.GetWorkflowExecutionContinuedAsNewEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED:
			err = s.Event.WorkflowTaskCompleted(e.GetWorkflowTaskCompletedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_TIMED_OUT:
			err = s.Event.WorkflowTaskTimedOut(e.GetWorkflowTaskTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED:
			err = s.Event.WorkflowTaskFailed(e.GetWorkflowTaskFailedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED:
			err = s.Event.ActivityTaskStarted(e.GetActivityTaskStartedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			err = s.Event.ActivityTaskCompleted(e.GetActivityTaskCompletedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
			err = s.Event.ActivityTaskFailed(e.GetActivityTaskFailedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			err = s.Event.ActivityTaskTimedOut(e.GetActivityTaskTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED:
			err = s.Event.ActivityTaskCancelRequested(e.GetActivityTaskCancelRequestedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED:
			err = s.Event.ActivityTaskCanceled(e.GetActivityTaskCanceledEventAttributes())
		case enumspb.EVENT_TYPE_TIMER_FIRED:
			err = s.Event.TimerFired(e.GetTimerFiredEventAttributes())
		case enumspb.EVENT_TYPE_TIMER_CANCELED:
			err = s.Event.TimerCanceled(e.GetTimerCanceledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCEL_REQUESTED:
			err = s.Event.WorkflowExecutionCancelRequested(e.GetWorkflowExecutionCancelRequestedEventAttributes())
		case enumspb.EVENT_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION_FAILED:
			err = s.Event.RequestCancelExternalWorkflowExecutionFailed(e.GetRequestCancelExternalWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_EXTERNAL_WORKFLOW_EXECUTION_CANCEL_REQUESTED:
			err = s.Event.ExternalWorkflowExecutionCancelRequested(e.GetExternalWorkflowExecutionCancelRequestedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
			err = s.Event.WorkflowExecutionSignaled(e.GetWorkflowExecutionSignaledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
			err = s.Event.WorkflowExecutionTerminated(e.GetWorkflowExecutionTerminatedEventAttributes())
		case enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_FAILED:
			err = s.Event.StartChildWorkflowExecutionFailed(e.GetStartChildWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_STARTED:
			err = s.Event.ChildWorkflowExecutionStarted(e.GetChildWorkflowExecutionStartedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
			err = s.Event.ChildWorkflowExecutionCompleted(e.GetChildWorkflowExecutionCompletedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_FAILED:
			err = s.Event.ChildWorkflowExecutionFailed(e.GetChildWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_CANCELED:
			err = s.Event.ChildWorkflowExecutionCanceled(e.GetChildWorkflowExecutionCanceledEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_TIMED_OUT:
			err = s.Event.ChildWorkflowExecutionTimedOut(e.GetChildWorkflowExecutionTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_TERMINATED:
			err = s.Event.ChildWorkflowExecutionTerminated(e.GetChildWorkflowExecutionTerminatedEventAttributes())
		case enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_FAILED:
			err = s.Event.SignalExternalWorkflowExecutionFailed(e.GetSignalExternalWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_EXTERNAL_WORKFLOW_EXECUTION_SIGNALED:
			err = s.Event.ExternalWorkflowExecutionSignaled(e.GetExternalWorkflowExecutionSignaledEventAttributes())
		}
		if err != nil {
			return err
		}
	}

	return nil
}

type (
	payloadEncoderRequestResponseInterceptor struct {
		baseRequestResponseInterceptor
		encoders []converter.PayloadEncoder
	}

	payloadEncoderCommandInterceptor struct {
		baseCommandInterceptor
		encoders []converter.PayloadEncoder
	}

	payloadEncoderEventInterceptor struct {
		baseEventInterceptor
		encoders []converter.PayloadEncoder
	}
)

// NewPayloadEncoderGRPCServerInterceptor returns a GRPC Server Interceptor that will mimic the encoding
// that the SDK system would perform when configured with a matching EncodingDataConverter.
// Note: This approach does not support use cases that rely on the ContextAware DataConverter interface as
// workflow context is not available at the GRPC level.
func NewPayloadEncoderGRPCServerInterceptor(encoders ...converter.PayloadEncoder) grpc.UnaryServerInterceptor {
	s := serviceInterceptor{
		RequestResponse: &payloadEncoderRequestResponseInterceptor{encoders: encoders},
		Command:         &payloadEncoderCommandInterceptor{encoders: encoders},
		Event:           &payloadEncoderEventInterceptor{encoders: encoders},
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		err := s.processRequest(req)
		if err != nil {
			return nil, err
		}

		resp, err := handler(ctx, req)
		if err != nil {
			return nil, err
		}

		return s.processResponse(resp), err
	}
}

// NewPayloadEncoderGRPCClientInterceptor returns a GRPC Client Interceptor that will mimic the encoding
// that the SDK system would perform when configured with a matching EncodingDataConverter.
// Note: This approach does not support use cases that rely on the ContextAware DataConverter interface as
// workflow context is not available at the GRPC level.
func NewPayloadEncoderGRPCClientInterceptor(encoders ...converter.PayloadEncoder) grpc.UnaryClientInterceptor {
	s := serviceInterceptor{
		RequestResponse: &payloadEncoderRequestResponseInterceptor{encoders: encoders},
		Command:         &payloadEncoderCommandInterceptor{encoders: encoders},
		Event:           &payloadEncoderEventInterceptor{encoders: encoders},
	}

	return func(ctx context.Context, method string, req, response interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := s.processRequest(req)
		if err != nil {
			return err
		}

		err = invoker(ctx, method, req, response, cc, opts...)
		if err != nil {
			return err
		}

		return s.processResponse(response)
	}
}

func encodePayloads(payloads *commonpb.Payloads, encoders ...converter.PayloadEncoder) error {
	for _, payload := range payloads.Payloads {
		for i := len(encoders) - 1; i >= 0; i-- {
			if err := encoders[i].Encode(payload); err != nil {
				return err
			}
		}
	}

	return nil
}

func decodePayloads(payloads *commonpb.Payloads, encoders ...converter.PayloadEncoder) error {
	for _, payload := range payloads.Payloads {
		for _, encoder := range encoders {
			if err := encoder.Decode(payload); err != nil {
				return err
			}
		}
	}

	return nil
}

func (i *payloadEncoderRequestResponseInterceptor) StartWorkflowExecutionRequest(req *workflowservice.StartWorkflowExecutionRequest) error {
	return encodePayloads(req.Input, i.encoders...)
}

func (i *payloadEncoderRequestResponseInterceptor) SignalWorkflowExecutionRequest(req *workflowservice.SignalWorkflowExecutionRequest) error {
	return encodePayloads(req.Input, i.encoders...)
}

func (i *payloadEncoderRequestResponseInterceptor) SignalWithStartWorkflowExecutionRequest(req *workflowservice.SignalWithStartWorkflowExecutionRequest) error {
	err := encodePayloads(req.Input, i.encoders...)
	if err != nil {
		return err
	}

	return encodePayloads(req.SignalInput, i.encoders...)
}

func (i *payloadEncoderRequestResponseInterceptor) RespondActivityTaskCompletedRequest(req *workflowservice.RespondActivityTaskCompletedRequest) error {
	return encodePayloads(req.Result, i.encoders...)
}

func (i *payloadEncoderRequestResponseInterceptor) RespondActivityTaskCompletedByIdRequest(req *workflowservice.RespondActivityTaskCompletedByIdRequest) error {
	return encodePayloads(req.Result, i.encoders...)
}

func (i *payloadEncoderRequestResponseInterceptor) PollActivityTaskQueueResponse(res *workflowservice.PollActivityTaskQueueResponse) error {
	if res.Input != nil {
		err := decodePayloads(res.Input, i.encoders...)
		if err != nil {
			return err
		}
	}

	if res.HeartbeatDetails != nil {
		return decodePayloads(res.HeartbeatDetails, i.encoders...)
	}

	return nil
}

func (i *payloadEncoderRequestResponseInterceptor) RecordActivityTaskHeartbeatRequest(req *workflowservice.RecordActivityTaskHeartbeatRequest) error {
	return encodePayloads(req.Details, i.encoders...)
}

func (i *payloadEncoderRequestResponseInterceptor) RecordActivityTaskHeartbeatByIdRequest(req *workflowservice.RecordActivityTaskHeartbeatByIdRequest) error {
	return encodePayloads(req.Details, i.encoders...)
}

func (i *payloadEncoderCommandInterceptor) ScheduleActivityTask(attrs *command.ScheduleActivityTaskCommandAttributes) error {
	return encodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderCommandInterceptor) CompleteWorkflowExecution(attrs *command.CompleteWorkflowExecutionCommandAttributes) error {
	return encodePayloads(attrs.Result, i.encoders...)
}

func (i *payloadEncoderCommandInterceptor) SignalExternalWorkflowExecution(attrs *command.SignalExternalWorkflowExecutionCommandAttributes) error {
	return encodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderCommandInterceptor) ContinueAsNewWorkflowExecution(attrs *command.ContinueAsNewWorkflowExecutionCommandAttributes) error {
	return encodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderCommandInterceptor) StartChildWorkflowExecution(attrs *command.StartChildWorkflowExecutionCommandAttributes) error {
	return encodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) WorkflowExecutionStarted(attrs *historypb.WorkflowExecutionStartedEventAttributes) error {
	return decodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) WorkflowExecutionContinuedAsNew(attrs *historypb.WorkflowExecutionContinuedAsNewEventAttributes) error {
	return decodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) ActivityTaskScheduled(attrs *historypb.ActivityTaskScheduledEventAttributes) error {
	return decodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) ActivityTaskCompleted(attrs *historypb.ActivityTaskCompletedEventAttributes) error {
	return decodePayloads(attrs.Result, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) WorkflowExecutionSignaled(attrs *historypb.WorkflowExecutionSignaledEventAttributes) error {
	return decodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) WorkflowExecutionCompleted(attrs *historypb.WorkflowExecutionCompletedEventAttributes) error {
	return decodePayloads(attrs.Result, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) SignalExternalWorkflowExecutionInitiated(attrs *historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes) error {
	return decodePayloads(attrs.Input, i.encoders...)
}

func (i *payloadEncoderEventInterceptor) StartChildWorkflowExecutionInitiated(attrs *historypb.StartChildWorkflowExecutionInitiatedEventAttributes) error {
	return decodePayloads(attrs.Input, i.encoders...)
}
