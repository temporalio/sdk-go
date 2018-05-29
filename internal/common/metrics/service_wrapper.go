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
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/yarpc"
)

type (
	workflowServiceMetricsWrapper struct {
		service     workflowserviceclient.Interface
		scope       tally.Scope
		childScopes map[string]tally.Scope
		mutex       sync.Mutex
	}

	operationScope struct {
		scope     tally.Scope
		startTime time.Time
	}
)

const (
	scopeNameDeprecateDomain                  = CadenceMetricsPrefix + "DeprecateDomain"
	scopeNameDescribeDomain                   = CadenceMetricsPrefix + "DescribeDomain"
	scopeNameGetWorkflowExecutionHistory      = CadenceMetricsPrefix + "GetWorkflowExecutionHistory"
	scopeNameListClosedWorkflowExecutions     = CadenceMetricsPrefix + "ListClosedWorkflowExecutions"
	scopeNameListOpenWorkflowExecutions       = CadenceMetricsPrefix + "ListOpenWorkflowExecutions"
	scopeNamePollForActivityTask              = CadenceMetricsPrefix + "PollForActivityTask"
	scopeNamePollForDecisionTask              = CadenceMetricsPrefix + "PollForDecisionTask"
	scopeNameRecordActivityTaskHeartbeat      = CadenceMetricsPrefix + "RecordActivityTaskHeartbeat"
	scopeNameRecordActivityTaskHeartbeatByID  = CadenceMetricsPrefix + "RecordActivityTaskHeartbeatByID"
	scopeNameRegisterDomain                   = CadenceMetricsPrefix + "RegisterDomain"
	scopeNameRequestCancelWorkflowExecution   = CadenceMetricsPrefix + "RequestCancelWorkflowExecution"
	scopeNameRespondActivityTaskCanceled      = CadenceMetricsPrefix + "RespondActivityTaskCanceled"
	scopeNameRespondActivityTaskCompleted     = CadenceMetricsPrefix + "RespondActivityTaskCompleted"
	scopeNameRespondActivityTaskFailed        = CadenceMetricsPrefix + "RespondActivityTaskFailed"
	scopeNameRespondActivityTaskCanceledByID  = CadenceMetricsPrefix + "RespondActivityTaskCanceledByID"
	scopeNameRespondActivityTaskCompletedByID = CadenceMetricsPrefix + "RespondActivityTaskCompletedByID"
	scopeNameRespondActivityTaskFailedByID    = CadenceMetricsPrefix + "RespondActivityTaskFailedByID"
	scopeNameRespondDecisionTaskCompleted     = CadenceMetricsPrefix + "RespondDecisionTaskCompleted"
	scopeNameRespondDecisionTaskFailed        = CadenceMetricsPrefix + "RespondDecisionTaskFailed"
	scopeNameSignalWorkflowExecution          = CadenceMetricsPrefix + "SignalWorkflowExecution"
	scopeNameSignalWithStartWorkflowExecution = CadenceMetricsPrefix + "SignalWithStartWorkflowExecution"
	scopeNameStartWorkflowExecution           = CadenceMetricsPrefix + "StartWorkflowExecution"
	scopeNameTerminateWorkflowExecution       = CadenceMetricsPrefix + "TerminateWorkflowExecution"
	scopeNameUpdateDomain                     = CadenceMetricsPrefix + "UpdateDomain"
	scopeNameQueryWorkflow                    = CadenceMetricsPrefix + "QueryWorkflow"
	scopeNameDescribeTaskList                 = CadenceMetricsPrefix + "DescribeTaskList"
	scopeNameRespondQueryTaskCompleted        = CadenceMetricsPrefix + "RespondQueryTaskCompleted"
	scopeNameDescribeWorkflowExecution        = CadenceMetricsPrefix + "DescribeWorkflowExecution"
	scopeNameResetStickyTaskList              = CadenceMetricsPrefix + "ResetStickyTaskList"
)

// NewWorkflowServiceWrapper creates a new wrapper to WorkflowService that will emit metrics for each service call.
func NewWorkflowServiceWrapper(service workflowserviceclient.Interface, scope tally.Scope) workflowserviceclient.Interface {
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
	scope.Counter(CadenceRequest).Inc(1)

	return &operationScope{scope: scope, startTime: time.Now()}
}

func (s *operationScope) handleError(err error) {
	s.scope.Timer(CadenceLatency).Record(time.Now().Sub(s.startTime))
	if err != nil {
		switch err.(type) {
		case *shared.EntityNotExistsError,
			*shared.BadRequestError,
			*shared.DomainAlreadyExistsError,
			*shared.WorkflowExecutionAlreadyStartedError,
			*shared.QueryFailedError:
			s.scope.Counter(CadenceInvalidRequest).Inc(1)
		default:
			s.scope.Counter(CadenceError).Inc(1)
		}
	}
}

func (w *workflowServiceMetricsWrapper) DeprecateDomain(ctx context.Context, request *shared.DeprecateDomainRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameDeprecateDomain)
	err := w.service.DeprecateDomain(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) DescribeDomain(ctx context.Context, request *shared.DescribeDomainRequest, opts ...yarpc.CallOption) (*shared.DescribeDomainResponse, error) {
	scope := w.getOperationScope(scopeNameDescribeDomain)
	result, err := w.service.DescribeDomain(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) DescribeWorkflowExecution(ctx context.Context, request *shared.DescribeWorkflowExecutionRequest, opts ...yarpc.CallOption) (*shared.DescribeWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameDescribeWorkflowExecution)
	result, err := w.service.DescribeWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) GetWorkflowExecutionHistory(ctx context.Context, request *shared.GetWorkflowExecutionHistoryRequest, opts ...yarpc.CallOption) (*shared.GetWorkflowExecutionHistoryResponse, error) {
	scope := w.getOperationScope(scopeNameGetWorkflowExecutionHistory)
	result, err := w.service.GetWorkflowExecutionHistory(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListClosedWorkflowExecutions(ctx context.Context, request *shared.ListClosedWorkflowExecutionsRequest, opts ...yarpc.CallOption) (*shared.ListClosedWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameListClosedWorkflowExecutions)
	result, err := w.service.ListClosedWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ListOpenWorkflowExecutions(ctx context.Context, request *shared.ListOpenWorkflowExecutionsRequest, opts ...yarpc.CallOption) (*shared.ListOpenWorkflowExecutionsResponse, error) {
	scope := w.getOperationScope(scopeNameListOpenWorkflowExecutions)
	result, err := w.service.ListOpenWorkflowExecutions(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) PollForActivityTask(ctx context.Context, request *shared.PollForActivityTaskRequest, opts ...yarpc.CallOption) (*shared.PollForActivityTaskResponse, error) {
	scope := w.getOperationScope(scopeNamePollForActivityTask)
	result, err := w.service.PollForActivityTask(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) PollForDecisionTask(ctx context.Context, request *shared.PollForDecisionTaskRequest, opts ...yarpc.CallOption) (*shared.PollForDecisionTaskResponse, error) {
	scope := w.getOperationScope(scopeNamePollForDecisionTask)
	result, err := w.service.PollForDecisionTask(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RecordActivityTaskHeartbeat(ctx context.Context, request *shared.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) (*shared.RecordActivityTaskHeartbeatResponse, error) {
	scope := w.getOperationScope(scopeNameRecordActivityTaskHeartbeat)
	result, err := w.service.RecordActivityTaskHeartbeat(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RecordActivityTaskHeartbeatByID(ctx context.Context, request *shared.RecordActivityTaskHeartbeatByIDRequest, opts ...yarpc.CallOption) (*shared.RecordActivityTaskHeartbeatResponse, error) {
	scope := w.getOperationScope(scopeNameRecordActivityTaskHeartbeatByID)
	result, err := w.service.RecordActivityTaskHeartbeatByID(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RegisterDomain(ctx context.Context, request *shared.RegisterDomainRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRegisterDomain)
	err := w.service.RegisterDomain(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RequestCancelWorkflowExecution(ctx context.Context, request *shared.RequestCancelWorkflowExecutionRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRequestCancelWorkflowExecution)
	err := w.service.RequestCancelWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCanceled(ctx context.Context, request *shared.RespondActivityTaskCanceledRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCanceled)
	err := w.service.RespondActivityTaskCanceled(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCompleted(ctx context.Context, request *shared.RespondActivityTaskCompletedRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCompleted)
	err := w.service.RespondActivityTaskCompleted(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskFailed(ctx context.Context, request *shared.RespondActivityTaskFailedRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondActivityTaskFailed)
	err := w.service.RespondActivityTaskFailed(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCanceledByID(ctx context.Context, request *shared.RespondActivityTaskCanceledByIDRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCanceledByID)
	err := w.service.RespondActivityTaskCanceledByID(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskCompletedByID(ctx context.Context, request *shared.RespondActivityTaskCompletedByIDRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondActivityTaskCompletedByID)
	err := w.service.RespondActivityTaskCompletedByID(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondActivityTaskFailedByID(ctx context.Context, request *shared.RespondActivityTaskFailedByIDRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondActivityTaskFailedByID)
	err := w.service.RespondActivityTaskFailedByID(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondDecisionTaskCompleted(ctx context.Context, request *shared.RespondDecisionTaskCompletedRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondDecisionTaskCompleted)
	err := w.service.RespondDecisionTaskCompleted(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) RespondDecisionTaskFailed(ctx context.Context, request *shared.RespondDecisionTaskFailedRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondDecisionTaskFailed)
	err := w.service.RespondDecisionTaskFailed(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) SignalWorkflowExecution(ctx context.Context, request *shared.SignalWorkflowExecutionRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameSignalWorkflowExecution)
	err := w.service.SignalWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) SignalWithStartWorkflowExecution(ctx context.Context, request *shared.SignalWithStartWorkflowExecutionRequest, opts ...yarpc.CallOption) (*shared.StartWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameSignalWithStartWorkflowExecution)
	result, err := w.service.SignalWithStartWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) StartWorkflowExecution(ctx context.Context, request *shared.StartWorkflowExecutionRequest, opts ...yarpc.CallOption) (*shared.StartWorkflowExecutionResponse, error) {
	scope := w.getOperationScope(scopeNameStartWorkflowExecution)
	result, err := w.service.StartWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) TerminateWorkflowExecution(ctx context.Context, request *shared.TerminateWorkflowExecutionRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameTerminateWorkflowExecution)
	err := w.service.TerminateWorkflowExecution(ctx, request, opts...)
	scope.handleError(err)
	return err
}

func (w *workflowServiceMetricsWrapper) UpdateDomain(ctx context.Context, request *shared.UpdateDomainRequest, opts ...yarpc.CallOption) (*shared.UpdateDomainResponse, error) {
	scope := w.getOperationScope(scopeNameUpdateDomain)
	result, err := w.service.UpdateDomain(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) QueryWorkflow(ctx context.Context, request *shared.QueryWorkflowRequest, opts ...yarpc.CallOption) (*shared.QueryWorkflowResponse, error) {
	scope := w.getOperationScope(scopeNameQueryWorkflow)
	result, err := w.service.QueryWorkflow(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) ResetStickyTaskList(ctx context.Context, request *shared.ResetStickyTaskListRequest, opts ...yarpc.CallOption) (*shared.ResetStickyTaskListResponse, error) {
	scope := w.getOperationScope(scopeNameResetStickyTaskList)
	result, err := w.service.ResetStickyTaskList(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) DescribeTaskList(ctx context.Context, request *shared.DescribeTaskListRequest, opts ...yarpc.CallOption) (*shared.DescribeTaskListResponse, error) {
	scope := w.getOperationScope(scopeNameDescribeTaskList)
	result, err := w.service.DescribeTaskList(ctx, request, opts...)
	scope.handleError(err)
	return result, err
}

func (w *workflowServiceMetricsWrapper) RespondQueryTaskCompleted(ctx context.Context, request *shared.RespondQueryTaskCompletedRequest, opts ...yarpc.CallOption) error {
	scope := w.getOperationScope(scopeNameRespondQueryTaskCompleted)
	err := w.service.RespondQueryTaskCompleted(ctx, request, opts...)
	scope.handleError(err)
	return err
}
