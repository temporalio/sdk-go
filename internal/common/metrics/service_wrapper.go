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

package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/uber-go/tally"
	"go.temporal.io/temporal-proto/workflowservice"
	"google.golang.org/grpc"
)

type (
	workflowServiceMetricsWrapper struct {
		service     workflowservice.WorkflowServiceClient
		scope       tally.Scope
		childScopes map[string]tally.Scope
		mutex       sync.Mutex
	}
)

const (
	scopeNameDeprecateNamespace                 = TemporalMetricsPrefix + "DeprecateNamespace"
	scopeNameDescribeNamespace                  = TemporalMetricsPrefix + "DescribeNamespace"
	scopeNameListNamespaces                     = TemporalMetricsPrefix + "ListNamespaces"
	scopeNameGetWorkflowExecutionHistory        = TemporalMetricsPrefix + "GetWorkflowExecutionHistory"
	scopeNamePollForWorkflowExecutionRawHistory = TemporalMetricsPrefix + "PollForWorkflowExecutionRawHistory"
	scopeNameListClosedWorkflowExecutions       = TemporalMetricsPrefix + "ListClosedWorkflowExecutions"
	scopeNameListOpenWorkflowExecutions         = TemporalMetricsPrefix + "ListOpenWorkflowExecutions"
	scopeNameListWorkflowExecutions             = TemporalMetricsPrefix + "ListWorkflowExecutions"
	scopeNameListArchivedWorkflowExecutions     = TemporalMetricsPrefix + "ListArchviedExecutions"
	scopeNameScanWorkflowExecutions             = TemporalMetricsPrefix + "ScanWorkflowExecutions"
	scopeNameCountWorkflowExecutions            = TemporalMetricsPrefix + "CountWorkflowExecutions"
	scopeNamePollForActivityTask                = TemporalMetricsPrefix + "PollForActivityTask"
	scopeNamePollForDecisionTask                = TemporalMetricsPrefix + "PollForDecisionTask"
	scopeNameRecordActivityTaskHeartbeat        = TemporalMetricsPrefix + "RecordActivityTaskHeartbeat"
	scopeNameRecordActivityTaskHeartbeatByID    = TemporalMetricsPrefix + "RecordActivityTaskHeartbeatByID"
	scopeNameRegisterNamespace                  = TemporalMetricsPrefix + "RegisterNamespace"
	scopeNameRequestCancelWorkflowExecution     = TemporalMetricsPrefix + "RequestCancelWorkflowExecution"
	scopeNameRespondActivityTaskCanceled        = TemporalMetricsPrefix + "RespondActivityTaskCanceled"
	scopeNameRespondActivityTaskCompleted       = TemporalMetricsPrefix + "RespondActivityTaskCompleted"
	scopeNameRespondActivityTaskFailed          = TemporalMetricsPrefix + "RespondActivityTaskFailed"
	scopeNameRespondActivityTaskCanceledByID    = TemporalMetricsPrefix + "RespondActivityTaskCanceledByID"
	scopeNameRespondActivityTaskCompletedByID   = TemporalMetricsPrefix + "RespondActivityTaskCompletedByID"
	scopeNameRespondActivityTaskFailedByID      = TemporalMetricsPrefix + "RespondActivityTaskFailedByID"
	scopeNameRespondDecisionTaskCompleted       = TemporalMetricsPrefix + "RespondDecisionTaskCompleted"
	scopeNameRespondDecisionTaskFailed          = TemporalMetricsPrefix + "RespondDecisionTaskFailed"
	scopeNameSignalWorkflowExecution            = TemporalMetricsPrefix + "SignalWorkflowExecution"
	scopeNameSignalWithStartWorkflowExecution   = TemporalMetricsPrefix + "SignalWithStartWorkflowExecution"
	scopeNameStartWorkflowExecution             = TemporalMetricsPrefix + "StartWorkflowExecution"
	scopeNameTerminateWorkflowExecution         = TemporalMetricsPrefix + "TerminateWorkflowExecution"
	scopeNameResetWorkflowExecution             = TemporalMetricsPrefix + "ResetWorkflowExecution"
	scopeNameUpdateNamespace                    = TemporalMetricsPrefix + "UpdateNamespace"
	scopeNameQueryWorkflow                      = TemporalMetricsPrefix + "QueryWorkflow"
	scopeNameDescribeTaskList                   = TemporalMetricsPrefix + "DescribeTaskList"
	scopeNameRespondQueryTaskCompleted          = TemporalMetricsPrefix + "RespondQueryTaskCompleted"
	scopeNameDescribeWorkflowExecution          = TemporalMetricsPrefix + "DescribeWorkflowExecution"
	scopeNameResetStickyTaskList                = TemporalMetricsPrefix + "ResetStickyTaskList"
	scopeNameGetSearchAttributes                = TemporalMetricsPrefix + "GetSearchAttributes"
	scopeNameListTaskListPartitions             = TemporalMetricsPrefix + "ListTaskListPartitions"
	scopeNameGetClusterInfo                     = TemporalMetricsPrefix + "GetClusterInfo"
	scopeNameGetWorkflowExecutionRawHistory     = TemporalMetricsPrefix + "GetWorkflowExecutionRawHistory"
)

var (
	_ workflowservice.WorkflowServiceClient = (*workflowServiceMetricsWrapper)(nil)
)

// NewWorkflowServiceWrapper creates a new wrapper to WorkflowService that will emit metrics for each service call.
func NewWorkflowServiceWrapper(service workflowservice.WorkflowServiceClient, scope tally.Scope) workflowservice.WorkflowServiceClient {
	return &workflowServiceMetricsWrapper{service: service, scope: scope, childScopes: make(map[string]tally.Scope)}
}

func (w *workflowServiceMetricsWrapper) getScope(scopeName string) tally.Scope {
	w.mutex.Lock()
	scope, ok := w.childScopes[scopeName]
	if ok {
		w.mutex.Unlock()
		return scope
	}
	scope = w.scope.SubScope(scopeName)
	w.childScopes[scopeName] = scope
	w.mutex.Unlock()
	return scope
}

func (w *workflowServiceMetricsWrapper) getOperationScope(scopeName string) *operationScope {
	scope := w.getScope(scopeName)
	scope.Counter(TemporalRequest).Inc(1)

	return &operationScope{scope: scope, startTime: time.Now()}
}

func (w *workflowServiceMetricsWrapper) DeprecateNamespace(ctx context.Context, request *workflowservice.DeprecateNamespaceRequest, opts ...grpc.CallOption) (*workflowservice.DeprecateNamespaceResponse, error) {
	scope := w.getOperationScope(scopeNameDeprecateNamespace)
	result, err := w.service.DeprecateNamespace(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListNamespaces(ctx context.Context, request *workflowservice.ListNamespacesRequest, opts ...grpc.CallOption) (*workflowservice.ListNamespacesResponse, error) {
	scope := w.getOperationScope(scopeNameListNamespaces)
	result, err := w.service.ListNamespaces(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) DescribeNamespace(ctx context.Context, request *workflowservice.DescribeNamespaceRequest, opts ...grpc.CallOption) (*workflowservice.DescribeNamespaceResponse, error) {
	scope := w.getOperationScope(scopeNameDescribeNamespace)
	result, err := w.service.DescribeNamespace(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) DescribeWorkflowExecution(ctx context.Context, request *workflowservice.DescribeWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameDescribeWorkflowExecution)
	result, err := w.service.DescribeWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) GetWorkflowExecutionHistory(ctx context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, opts ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	scope := w.getOperationScope(scopeNameGetWorkflowExecutionHistory)
	result, err := w.service.GetWorkflowExecutionHistory(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) PollForWorkflowExecutionRawHistory(ctx context.Context, in *workflowservice.PollForWorkflowExecutionRawHistoryRequest, opts ...grpc.CallOption) (*workflowservice.PollForWorkflowExecutionRawHistoryResponse, error) {
	scope := w.getOperationScope(scopeNamePollForWorkflowExecutionRawHistory)
	result, err := w.service.PollForWorkflowExecutionRawHistory(ctx, in, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListClosedWorkflowExecutions(ctx context.Context, request *workflowservice.ListClosedWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListClosedWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameListClosedWorkflowExecutions)
	result, err := w.service.ListClosedWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListOpenWorkflowExecutions(ctx context.Context, request *workflowservice.ListOpenWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListOpenWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameListOpenWorkflowExecutions)
	result, err := w.service.ListOpenWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListWorkflowExecutions(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameListWorkflowExecutions)
	result, err := w.service.ListWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListArchivedWorkflowExecutions(ctx context.Context, request *workflowservice.ListArchivedWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameListArchivedWorkflowExecutions)
	result, err := w.service.ListArchivedWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ScanWorkflowExecutions(ctx context.Context, request *workflowservice.ScanWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.ScanWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameScanWorkflowExecutions)
	result, err := w.service.ScanWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) CountWorkflowExecutions(ctx context.Context, request *workflowservice.CountWorkflowExecutionsRequest, opts ...grpc.CallOption) (*workflowservice.CountWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameCountWorkflowExecutions)
	result, err := w.service.CountWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) PollForActivityTask(ctx context.Context, request *workflowservice.PollForActivityTaskRequest, opts ...grpc.CallOption) (*workflowservice.PollForActivityTaskResponse, error) {
	scope := w.getOperationScope(scopeNamePollForActivityTask)
	result, err := w.service.PollForActivityTask(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) PollForDecisionTask(ctx context.Context, request *workflowservice.PollForDecisionTaskRequest, opts ...grpc.CallOption) (*workflowservice.PollForDecisionTaskResponse, error) {
	scope := w.getOperationScope(scopeNamePollForDecisionTask)
	result, err := w.service.PollForDecisionTask(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RecordActivityTaskHeartbeat(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) (*workflowservice.RecordActivityTaskHeartbeatResponse, error) {
	scope := w.getOperationScope(scopeNameRecordActivityTaskHeartbeat)
	result, err := w.service.RecordActivityTaskHeartbeat(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RecordActivityTaskHeartbeatByID(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatByIDRequest, opts ...grpc.CallOption) (*workflowservice.RecordActivityTaskHeartbeatByIDResponse, error) {
	scope := w.getOperationScope(scopeNameRecordActivityTaskHeartbeatByID)
	result, err := w.service.RecordActivityTaskHeartbeatByID(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RegisterNamespace(ctx context.Context, request *workflowservice.RegisterNamespaceRequest, opts ...grpc.CallOption) (*workflowservice.RegisterNamespaceResponse, error) {
	scope := w.getOperationScope(scopeNameRegisterNamespace)
	result, err := w.service.RegisterNamespace(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RequestCancelWorkflowExecution(ctx context.Context, request *workflowservice.RequestCancelWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.RequestCancelWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameRequestCancelWorkflowExecution)
	result, err := w.service.RequestCancelWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCanceled(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCanceledResponse, error) {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCanceled)
	result, err := w.service.RespondActivityTaskCanceled(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCompleted(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCompletedResponse, error) {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCompleted)
	result, err := w.service.RespondActivityTaskCompleted(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskFailed(ctx context.Context, request *workflowservice.RespondActivityTaskFailedRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskFailedResponse, error) {
	scope := w.getOperationScope(scopeNameRespondActivityTaskFailed)
	result, err := w.service.RespondActivityTaskFailed(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCanceledByID(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledByIDRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCanceledByIDResponse, error) {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCanceledByID)
	result, err := w.service.RespondActivityTaskCanceledByID(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCompletedByID(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedByIDRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskCompletedByIDResponse, error) {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCompletedByID)
	result, err := w.service.RespondActivityTaskCompletedByID(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskFailedByID(ctx context.Context, request *workflowservice.RespondActivityTaskFailedByIDRequest, opts ...grpc.CallOption) (*workflowservice.RespondActivityTaskFailedByIDResponse, error) {
	scope := w.getOperationScope(scopeNameRespondActivityTaskFailedByID)
	result, err := w.service.RespondActivityTaskFailedByID(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondDecisionTaskCompleted(ctx context.Context, request *workflowservice.RespondDecisionTaskCompletedRequest, opts ...grpc.CallOption) (*workflowservice.RespondDecisionTaskCompletedResponse, error) {
	scope := w.getOperationScope(scopeNameRespondDecisionTaskCompleted)
	result, err := w.service.RespondDecisionTaskCompleted(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondDecisionTaskFailed(ctx context.Context, request *workflowservice.RespondDecisionTaskFailedRequest, opts ...grpc.CallOption) (*workflowservice.RespondDecisionTaskFailedResponse, error) {
	scope := w.getOperationScope(scopeNameRespondDecisionTaskFailed)
	result, err := w.service.RespondDecisionTaskFailed(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) SignalWorkflowExecution(ctx context.Context, request *workflowservice.SignalWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.SignalWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameSignalWorkflowExecution)
	result, err := w.service.SignalWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) SignalWithStartWorkflowExecution(ctx context.Context, request *workflowservice.SignalWithStartWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameSignalWithStartWorkflowExecution)
	result, err := w.service.SignalWithStartWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) StartWorkflowExecution(ctx context.Context, request *workflowservice.StartWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.StartWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameStartWorkflowExecution)
	result, err := w.service.StartWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) TerminateWorkflowExecution(ctx context.Context, request *workflowservice.TerminateWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.TerminateWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameTerminateWorkflowExecution)
	result, err := w.service.TerminateWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ResetWorkflowExecution(ctx context.Context, request *workflowservice.ResetWorkflowExecutionRequest, opts ...grpc.CallOption) (*workflowservice.ResetWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameResetWorkflowExecution)
	result, err := w.service.ResetWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) UpdateNamespace(ctx context.Context, request *workflowservice.UpdateNamespaceRequest, opts ...grpc.CallOption) (*workflowservice.UpdateNamespaceResponse, error) {
	scope := w.getOperationScope(scopeNameUpdateNamespace)
	result, err := w.service.UpdateNamespace(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) QueryWorkflow(ctx context.Context, request *workflowservice.QueryWorkflowRequest, opts ...grpc.CallOption) (*workflowservice.QueryWorkflowResponse, error) {
	scope := w.getOperationScope(scopeNameQueryWorkflow)
	result, err := w.service.QueryWorkflow(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ResetStickyTaskList(ctx context.Context, request *workflowservice.ResetStickyTaskListRequest, opts ...grpc.CallOption) (*workflowservice.ResetStickyTaskListResponse, error) {
	scope := w.getOperationScope(scopeNameResetStickyTaskList)
	result, err := w.service.ResetStickyTaskList(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) DescribeTaskList(ctx context.Context, request *workflowservice.DescribeTaskListRequest, opts ...grpc.CallOption) (*workflowservice.DescribeTaskListResponse, error) {
	scope := w.getOperationScope(scopeNameDescribeTaskList)
	result, err := w.service.DescribeTaskList(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondQueryTaskCompleted(ctx context.Context, request *workflowservice.RespondQueryTaskCompletedRequest, opts ...grpc.CallOption) (*workflowservice.RespondQueryTaskCompletedResponse, error) {
	scope := w.getOperationScope(scopeNameRespondQueryTaskCompleted)
	result, err := w.service.RespondQueryTaskCompleted(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) GetSearchAttributes(ctx context.Context, request *workflowservice.GetSearchAttributesRequest, opts ...grpc.CallOption) (*workflowservice.GetSearchAttributesResponse, error) {
	scope := w.getOperationScope(scopeNameGetSearchAttributes)
	result, err := w.service.GetSearchAttributes(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) GetClusterInfo(ctx context.Context, request *workflowservice.GetClusterInfoRequest, opts ...grpc.CallOption) (*workflowservice.GetClusterInfoResponse, error) {
	scope := w.getOperationScope(scopeNameGetClusterInfo)
	result, err := w.service.GetClusterInfo(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListTaskListPartitions(ctx context.Context, request *workflowservice.ListTaskListPartitionsRequest, opts ...grpc.CallOption) (*workflowservice.ListTaskListPartitionsResponse, error) {
	scope := w.getOperationScope(scopeNameListTaskListPartitions)
	result, err := w.service.ListTaskListPartitions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) GetWorkflowExecutionRawHistory(ctx context.Context, request *workflowservice.GetWorkflowExecutionRawHistoryRequest, opts ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionRawHistoryResponse, error) {
	scope := w.getOperationScope(scopeNameGetWorkflowExecutionRawHistory)
	result, err := w.service.GetWorkflowExecutionRawHistory(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}
