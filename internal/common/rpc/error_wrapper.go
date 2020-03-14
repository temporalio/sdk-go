// Copyright (c) 2017 Uber Technologies, Inc.
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

package rpc

import (
	"context"

	"github.com/gogo/status"
	"go.temporal.io/temporal-proto/serviceerror"
	"go.temporal.io/temporal-proto/workflowservice"
	"google.golang.org/grpc"
)

type (
	workflowServiceErrorWrapper struct {
		service workflowservice.WorkflowServiceClient
	}
)

var (
	_ workflowservice.WorkflowServiceClient = (*workflowServiceErrorWrapper)(nil)
)

// NewWorkflowServiceErrorWrapper creates a new wrapper to WorkflowService that will convert gRPC status errors to service errors.
func NewWorkflowServiceErrorWrapper(service workflowservice.WorkflowServiceClient) workflowservice.WorkflowServiceClient {
	return &workflowServiceErrorWrapper{service: service}
}

func (w *workflowServiceErrorWrapper) convertError(err error) error {
	if err == nil {
		return nil
	}

	// err is an error from RPC call. It has status.Status from gRPC package (google.golang.org/grpc/status/status.go).
	// status.Convert converts this error to gogo status.Status.
	// serviceerror.FromStatus converts gogo Status to serviceerror.
	return serviceerror.FromStatus(status.Convert(err))
}

func (w *workflowServiceErrorWrapper) DeprecateDomain(ctx context.Context, request *workflowservice.DeprecateDomainRequest, opts ...grpc.CallOption) (*workflowservice.DeprecateDomainResponse, error) {
	result, err := w.service.DeprecateDomain(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ListDomains(ctx context.Context, request *workflowservice.ListDomainsRequest, opts ...grpc.CallOption) (*workflowservice.ListDomainsResponse, error) {
	result, err := w.service.ListDomains(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) DescribeDomain(ctx context.Context, request *workflowservice.DescribeDomainRequest, opts ...grpc.CallOption) (*workflowservice.DescribeDomainResponse, error) {
	result, err := w.service.DescribeDomain(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) DescribeWorkflowExecution(ctx context.Context, request *workflowservice.DescribeWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	result, err := w.service.DescribeWorkflowExecution(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) GetWorkflowExecutionHistory(ctx context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, opts ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	result, err := w.service.GetWorkflowExecutionHistory(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) PollForWorkflowExecutionRawHistory(ctx context.Context, in *workflowservice.PollForWorkflowExecutionRawHistoryRequest, opts ...grpc.CallOption) (*workflowservice.PollForWorkflowExecutionRawHistoryResponse, error) {
	result, err := w.service.PollForWorkflowExecutionRawHistory(ctx, in, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ListClosedWorkflowExecutions(ctx context.Context, request *workflowservice.ListClosedWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListClosedWorkflowExecutionsResponse, error) {
	result, err := w.service.ListClosedWorkflowExecutions(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ListOpenWorkflowExecutions(ctx context.Context, request *workflowservice.ListOpenWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListOpenWorkflowExecutionsResponse, error) {
	result, err := w.service.ListOpenWorkflowExecutions(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ListWorkflowExecutions(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	result, err := w.service.ListWorkflowExecutions(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ListArchivedWorkflowExecutions(ctx context.Context, request *workflowservice.ListArchivedWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error) {
	result, err := w.service.ListArchivedWorkflowExecutions(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ScanWorkflowExecutions(ctx context.Context, request *workflowservice.ScanWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ScanWorkflowExecutionsResponse, error) {
	result, err := w.service.ScanWorkflowExecutions(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) CountWorkflowExecutions(ctx context.Context, request *workflowservice.CountWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.CountWorkflowExecutionsResponse, error) {
	result, err := w.service.CountWorkflowExecutions(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) PollForActivityTask(ctx context.Context, request *workflowservice.PollForActivityTaskRequest, opts ...grpc.CallOption) (*workflowservice.PollForActivityTaskResponse, error) {
	result, err := w.service.PollForActivityTask(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) PollForDecisionTask(ctx context.Context, request *workflowservice.PollForDecisionTaskRequest, opts ...grpc.CallOption) (*workflowservice.PollForDecisionTaskResponse, error) {
	result, err := w.service.PollForDecisionTask(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RecordActivityTaskHeartbeat(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) (*workflowservice.RecordActivityTaskHeartbeatResponse, error) {
	result, err := w.service.RecordActivityTaskHeartbeat(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RecordActivityTaskHeartbeatByID(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatByIDRequest, opts ...grpc.CallOption) (*workflowservice.RecordActivityTaskHeartbeatByIDResponse, error) {
	result, err := w.service.RecordActivityTaskHeartbeatByID(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RegisterDomain(ctx context.Context, request *workflowservice.RegisterDomainRequest, opts ...grpc.CallOption) (*workflowservice.RegisterDomainResponse, error) {
	result, err := w.service.RegisterDomain(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RequestCancelWorkflowExecution(ctx context.Context, request *workflowservice.RequestCancelWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.RequestCancelWorkflowExecutionResponse, error) {
	result, err := w.service.RequestCancelWorkflowExecution(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondActivityTaskCanceled(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCanceledResponse, error) {
	result, err := w.service.RespondActivityTaskCanceled(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondActivityTaskCompleted(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCompletedResponse, error) {
	result, err := w.service.RespondActivityTaskCompleted(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondActivityTaskFailed(ctx context.Context, request *workflowservice.RespondActivityTaskFailedRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskFailedResponse, error) {
	result, err := w.service.RespondActivityTaskFailed(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondActivityTaskCanceledByID(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledByIDRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCanceledByIDResponse, error) {
	result, err := w.service.RespondActivityTaskCanceledByID(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondActivityTaskCompletedByID(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedByIDRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCompletedByIDResponse, error) {
	result, err := w.service.RespondActivityTaskCompletedByID(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondActivityTaskFailedByID(ctx context.Context, request *workflowservice.RespondActivityTaskFailedByIDRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskFailedByIDResponse, error) {
	result, err := w.service.RespondActivityTaskFailedByID(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondDecisionTaskCompleted(ctx context.Context, request *workflowservice.RespondDecisionTaskCompletedRequest, opts ...grpc.CallOption) (*workflowservice.RespondDecisionTaskCompletedResponse, error) {
	result, err := w.service.RespondDecisionTaskCompleted(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondDecisionTaskFailed(ctx context.Context, request *workflowservice.RespondDecisionTaskFailedRequest, opts ...grpc.CallOption) (*workflowservice.RespondDecisionTaskFailedResponse, error) {
	result, err := w.service.RespondDecisionTaskFailed(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) SignalWorkflowExecution(ctx context.Context, request *workflowservice.SignalWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.SignalWorkflowExecutionResponse, error) {
	result, err := w.service.SignalWorkflowExecution(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) SignalWithStartWorkflowExecution(ctx context.Context, request *workflowservice.SignalWithStartWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
	result, err := w.service.SignalWithStartWorkflowExecution(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) StartWorkflowExecution(ctx context.Context, request *workflowservice.StartWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.StartWorkflowExecutionResponse, error) {
	result, err := w.service.StartWorkflowExecution(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) TerminateWorkflowExecution(ctx context.Context, request *workflowservice.TerminateWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.TerminateWorkflowExecutionResponse, error) {
	result, err := w.service.TerminateWorkflowExecution(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ResetWorkflowExecution(ctx context.Context, request *workflowservice.ResetWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.ResetWorkflowExecutionResponse, error) {
	result, err := w.service.ResetWorkflowExecution(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) UpdateDomain(ctx context.Context, request *workflowservice.UpdateDomainRequest, opts ...grpc.CallOption) (*workflowservice.UpdateDomainResponse, error) {
	result, err := w.service.UpdateDomain(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) QueryWorkflow(ctx context.Context, request *workflowservice.QueryWorkflowRequest, opts ...grpc.CallOption) (*workflowservice.QueryWorkflowResponse, error) {
	result, err := w.service.QueryWorkflow(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ResetStickyTaskList(ctx context.Context, request *workflowservice.ResetStickyTaskListRequest, opts ...grpc.CallOption) (*workflowservice.ResetStickyTaskListResponse, error) {
	result, err := w.service.ResetStickyTaskList(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) DescribeTaskList(ctx context.Context, request *workflowservice.DescribeTaskListRequest, opts ...grpc.CallOption) (*workflowservice.DescribeTaskListResponse, error) {
	result, err := w.service.DescribeTaskList(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) RespondQueryTaskCompleted(ctx context.Context, request *workflowservice.RespondQueryTaskCompletedRequest, opts ...grpc.CallOption) (*workflowservice.RespondQueryTaskCompletedResponse, error) {
	result, err := w.service.RespondQueryTaskCompleted(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) GetSearchAttributes(ctx context.Context, request *workflowservice.GetSearchAttributesRequest, opts ...grpc.CallOption) (*workflowservice.GetSearchAttributesResponse, error) {
	result, err := w.service.GetSearchAttributes(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) GetClusterInfo(ctx context.Context, request *workflowservice.GetClusterInfoRequest, opts ...grpc.CallOption) (*workflowservice.GetClusterInfoResponse, error) {
	result, err := w.service.GetClusterInfo(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) ListTaskListPartitions(ctx context.Context, request *workflowservice.ListTaskListPartitionsRequest, opts ...grpc.CallOption) (*workflowservice.ListTaskListPartitionsResponse, error) {
	result, err := w.service.ListTaskListPartitions(ctx, request, opts...)
	return result, w.convertError(err)
}

func (w *workflowServiceErrorWrapper) GetWorkflowExecutionRawHistory(ctx context.Context, request *workflowservice.GetWorkflowExecutionRawHistoryRequest, opts ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionRawHistoryResponse, error) {
	result, err := w.service.GetWorkflowExecutionRawHistory(ctx, request, opts...)
	return result, w.convertError(err)
}
