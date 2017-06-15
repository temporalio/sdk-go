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

package cadence

import (
	"errors"

	"github.com/pborman/uuid"
	"github.com/uber-go/tally"
	m "go.uber.org/cadence/.gen/go/cadence"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/backoff"
	"go.uber.org/cadence/common/metrics"
)

// Assert that structs do indeed implement the interfaces
var _ Client = (*workflowClient)(nil)
var _ DomainClient = (*domainClient)(nil)

const (
	defaultDecisionTaskTimeoutInSecs = 20
)

type (
	// workflowClient is the client for starting a workflow execution.
	workflowClient struct {
		workflowExecution WorkflowExecution
		workflowService   m.TChanWorkflowService
		domain            string
		metricsScope      tally.Scope
		identity          string
	}

	// domainClient is the client for managing domains.
	domainClient struct {
		workflowService m.TChanWorkflowService
		metricsScope    tally.Scope
		identity        string
	}
)

// StartWorkflow starts a workflow execution
// The user can use this to start using a functor like.
// Either by
//     StartWorkflow(options, "workflowTypeName", input)
//     or
//     StartWorkflow(options, workflowExecuteFn, arg1, arg2, arg3)
func (wc *workflowClient) StartWorkflow(
	options StartWorkflowOptions,
	workflowFunc interface{},
	args ...interface{},
) (*WorkflowExecution, error) {
	workflowID := options.ID
	if workflowID == "" {
		workflowID = uuid.NewRandom().String()
	}

	if options.TaskList == "" {
		return nil, errors.New("missing TaskList")
	}

	executionTimeout := int32(options.ExecutionStartToCloseTimeout.Seconds())
	if executionTimeout <= 0 {
		return nil, errors.New("missing or invalid ExecutionStartToCloseTimeout")
	}

	decisionTaskTimeout := int32(options.DecisionTaskStartToCloseTimeout.Seconds())
	if decisionTaskTimeout < 0 {
		return nil, errors.New("negative DecisionTaskStartToCloseTimeout provided")
	}
	if decisionTaskTimeout == 0 {
		decisionTaskTimeout = defaultDecisionTaskTimeoutInSecs
	}

	// Validate type and its arguments.
	workflowType, input, err := getValidatedWorkerFunction(workflowFunc, args)
	if err != nil {
		return nil, err
	}

	startRequest := &s.StartWorkflowExecutionRequest{
		Domain:       common.StringPtr(wc.domain),
		RequestId:    common.StringPtr(uuid.New()),
		WorkflowId:   common.StringPtr(workflowID),
		WorkflowType: workflowTypePtr(*workflowType),
		TaskList:     common.TaskListPtr(s.TaskList{Name: common.StringPtr(options.TaskList)}),
		Input:        input,
		ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(executionTimeout),
		TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(decisionTaskTimeout),
		Identity:                            common.StringPtr(wc.identity)}

	var response *s.StartWorkflowExecutionResponse

	// Start creating workflow request.
	err = backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()

			var err1 error
			response, err1 = wc.workflowService.StartWorkflowExecution(ctx, startRequest)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return nil, err
	}

	if wc.metricsScope != nil {
		wc.metricsScope.Counter(metrics.WorkflowsStartTotalCounter).Inc(1)
	}

	executionInfo := &WorkflowExecution{
		ID:    options.ID,
		RunID: response.GetRunId()}
	return executionInfo, nil
}

// SignalWorkflow signals a workflow in execution.
func (wc *workflowClient) SignalWorkflow(workflowID string, runID string, signalName string, arg interface{}) error {
	input, err := getHostEnvironment().encodeArg(arg)
	if err != nil {
		return err
	}
	request := &s.SignalWorkflowExecutionRequest{
		Domain: common.StringPtr(wc.domain),
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      common.StringPtr(runID),
		},
		SignalName: common.StringPtr(signalName),
		Input:      input,
		Identity:   common.StringPtr(wc.identity),
	}

	return backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			return wc.workflowService.SignalWorkflowExecution(ctx, request)
		}, serviceOperationRetryPolicy, isServiceTransientError)
}

// CancelWorkflow cancels a workflow in execution.
func (wc *workflowClient) CancelWorkflow(workflowID string, runID string) error {
	request := &s.RequestCancelWorkflowExecutionRequest{
		Domain: common.StringPtr(wc.domain),
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      common.StringPtr(runID),
		},
		Identity: common.StringPtr(wc.identity),
	}

	return backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			return wc.workflowService.RequestCancelWorkflowExecution(ctx, request)
		}, serviceOperationRetryPolicy, isServiceTransientError)
}

// TerminateWorkflow terminates a workflow execution.
// workflowID is required, other parameters are optional.
// If runID is omit, it will terminate currently running workflow (if there is one) based on the workflowID.
func (wc *workflowClient) TerminateWorkflow(workflowID string, runID string, reason string, details []byte) error {
	request := &s.TerminateWorkflowExecutionRequest{
		Domain: common.StringPtr(wc.domain),
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      common.StringPtr(runID),
		},
		Reason:   common.StringPtr(reason),
		Identity: common.StringPtr(wc.identity),
	}

	err := backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			return wc.workflowService.TerminateWorkflowExecution(ctx, request)
		}, serviceOperationRetryPolicy, isServiceTransientError)

	return err
}

// GetWorkflowHistory gets history of a particular workflow.
func (wc *workflowClient) GetWorkflowHistory(workflowID string, runID string) (*s.History, error) {
	history := s.NewHistory()
	history.Events = make([]*s.HistoryEvent, 0)
	var nextPageToken []byte

GetHistoryLoop:
	for {
		request := &s.GetWorkflowExecutionHistoryRequest{
			Domain: common.StringPtr(wc.domain),
			Execution: &s.WorkflowExecution{
				WorkflowId: common.StringPtr(workflowID),
				RunId:      common.StringPtr(runID),
			},
			NextPageToken: nextPageToken,
		}

		var response *s.GetWorkflowExecutionHistoryResponse
		err := backoff.Retry(
			func() error {
				var err1 error
				ctx, cancel := newTChannelContext()
				defer cancel()
				response, err1 = wc.workflowService.GetWorkflowExecutionHistory(ctx, request)
				return err1
			}, serviceOperationRetryPolicy, isServiceTransientError)
		if err != nil {
			return nil, err
		}
		history.Events = append(history.Events, response.GetHistory().GetEvents()...)
		if response.GetNextPageToken() == nil {
			break GetHistoryLoop
		}
		nextPageToken = response.GetNextPageToken()
	}
	return history, nil
}

// CompleteActivity reports activity completed. activity Execute method can return cadence.ErrActivityResultPending to
// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
// should be called when that activity is completed with the actual result and error. If err is nil, activity task
// completed event will be reported; if err is CanceledError, activity task cancelled event will be reported; otherwise,
// activity task failed event will be reported.
func (wc *workflowClient) CompleteActivity(taskToken []byte, result interface{}, err error) error {
	if taskToken == nil {
		return errors.New("invalid task token provided")
	}

	var data []byte
	if result != nil {
		var err0 error
		data, err0 = getHostEnvironment().encodeArg(result)
		if err0 != nil {
			return err0
		}
	}
	request := convertActivityResultToRespondRequest(wc.identity, taskToken, data, err)
	return reportActivityComplete(wc.workflowService, request, wc.metricsScope)
}

// RecordActivityHeartbeat records heartbeat for an activity.
func (wc *workflowClient) RecordActivityHeartbeat(taskToken []byte, details ...interface{}) error {
	data, err := getHostEnvironment().encodeArgs(details)
	if err != nil {
		return err
	}
	return recordActivityHeartbeat(wc.workflowService, wc.identity, taskToken, data, serviceOperationRetryPolicy)
}

// ListClosedWorkflow gets closed workflow executions based on request filters
// The errors it can throw:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *workflowClient) ListClosedWorkflow(request *s.ListClosedWorkflowExecutionsRequest) (*s.ListClosedWorkflowExecutionsResponse, error) {
	if len(request.GetDomain()) == 0 {
		request.Domain = common.StringPtr(wc.domain)
	}
	var response *s.ListClosedWorkflowExecutionsResponse
	err := backoff.Retry(
		func() error {
			var err1 error
			ctx, cancel := newTChannelContext()
			defer cancel()
			response, err1 = wc.workflowService.ListClosedWorkflowExecutions(ctx, request)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// ListClosedWorkflow gets open workflow executions based on request filters
// The errors it can throw:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *workflowClient) ListOpenWorkflow(request *s.ListOpenWorkflowExecutionsRequest) (*s.ListOpenWorkflowExecutionsResponse, error) {
	if len(request.GetDomain()) == 0 {
		request.Domain = common.StringPtr(wc.domain)
	}
	var response *s.ListOpenWorkflowExecutionsResponse
	err := backoff.Retry(
		func() error {
			var err1 error
			ctx, cancel := newTChannelContext()
			defer cancel()
			response, err1 = wc.workflowService.ListOpenWorkflowExecutions(ctx, request)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Register a domain with cadence server
// The errors it can throw:
//	- DomainAlreadyExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Register(request *s.RegisterDomainRequest) error {
	return backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			return dc.workflowService.RegisterDomain(ctx, request)
		}, serviceOperationRetryPolicy, isServiceTransientError)
}

// Describe a domain. The domain has two part of information
// DomainInfo - Which has Name, Status, Description, Owner Email
// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Describe(name string) (*s.DomainInfo, *s.DomainConfiguration, error) {
	request := &s.DescribeDomainRequest{
		Name: common.StringPtr(name),
	}

	var response *s.DescribeDomainResponse
	err := backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			var err error
			response, err = dc.workflowService.DescribeDomain(ctx, request)
			return err
		}, serviceOperationRetryPolicy, isServiceTransientError)
	if err != nil {
		return nil, nil, err
	}
	return response.GetDomainInfo(), response.GetConfiguration(), nil
}

// Update a domain. The domain has two part of information
// DomainInfo - Which has Name, Status, Description, Owner Email
// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Update(name string, domainInfo *s.UpdateDomainInfo, domainConfig *s.DomainConfiguration) error {
	request := &s.UpdateDomainRequest{
		Name:          common.StringPtr(name),
		UpdatedInfo:   domainInfo,
		Configuration: domainConfig,
	}

	return backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			_, err := dc.workflowService.UpdateDomain(ctx, request)
			return err
		}, serviceOperationRetryPolicy, isServiceTransientError)
}
