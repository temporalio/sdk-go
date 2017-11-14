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
	"context"
	"errors"
	"github.com/pborman/uuid"
	"github.com/uber-go/tally"

	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
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
		workflowService   workflowserviceclient.Interface
		domain            string
		metricsScope      tally.Scope
		identity          string
	}

	// domainClient is the client for managing domains.
	domainClient struct {
		workflowService workflowserviceclient.Interface
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
	ctx context.Context,
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
	err = backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()

			var err1 error
			response, err1 = wc.workflowService.StartWorkflowExecution(tchCtx, startRequest, opt...)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return nil, err
	}

	if wc.metricsScope != nil {
		wc.metricsScope.Counter(metrics.WorkflowStartCounter).Inc(1)
	}

	executionInfo := &WorkflowExecution{
		ID:    options.ID,
		RunID: response.GetRunId()}
	return executionInfo, nil
}

// SignalWorkflow signals a workflow in execution.
func (wc *workflowClient) SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error {
	var input []byte
	if arg != nil {
		var err error
		if input, err = getHostEnvironment().encodeArg(arg); err != nil {
			return err
		}
	}

	request := &s.SignalWorkflowExecutionRequest{
		Domain: common.StringPtr(wc.domain),
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      getRunID(runID),
		},
		SignalName: common.StringPtr(signalName),
		Input:      input,
		Identity:   common.StringPtr(wc.identity),
	}

	return backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			return wc.workflowService.SignalWorkflowExecution(tchCtx, request, opt...)
		}, serviceOperationRetryPolicy, isServiceTransientError)
}

// CancelWorkflow cancels a workflow in execution.
func (wc *workflowClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	request := &s.RequestCancelWorkflowExecutionRequest{
		Domain: common.StringPtr(wc.domain),
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      getRunID(runID),
		},
		Identity: common.StringPtr(wc.identity),
	}

	return backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			return wc.workflowService.RequestCancelWorkflowExecution(tchCtx, request, opt...)
		}, serviceOperationRetryPolicy, isServiceTransientError)
}

// TerminateWorkflow terminates a workflow execution.
// workflowID is required, other parameters are optional.
// If runID is omit, it will terminate currently running workflow (if there is one) based on the workflowID.
func (wc *workflowClient) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details []byte) error {
	request := &s.TerminateWorkflowExecutionRequest{
		Domain: common.StringPtr(wc.domain),
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      getRunID(runID),
		},
		Reason:   common.StringPtr(reason),
		Identity: common.StringPtr(wc.identity),
	}

	err := backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			return wc.workflowService.TerminateWorkflowExecution(tchCtx, request, opt...)
		}, serviceOperationRetryPolicy, isServiceTransientError)

	return err
}

// GetWorkflowHistory gets history of a particular workflow.
func (wc *workflowClient) GetWorkflowHistory(ctx context.Context, workflowID string, runID string) (*s.History, error) {
	history := &s.History{}
	history.Events = make([]*s.HistoryEvent, 0)
	var nextPageToken []byte

GetHistoryLoop:
	for {
		request := &s.GetWorkflowExecutionHistoryRequest{
			Domain: common.StringPtr(wc.domain),
			Execution: &s.WorkflowExecution{
				WorkflowId: common.StringPtr(workflowID),
				RunId:      getRunID(runID),
			},
			NextPageToken: nextPageToken,
		}

		var response *s.GetWorkflowExecutionHistoryResponse
		err := backoff.Retry(ctx,
			func() error {
				var err1 error
				tchCtx, cancel, opt := newChannelContext(ctx)
				defer cancel()
				response, err1 = wc.workflowService.GetWorkflowExecutionHistory(tchCtx, request, opt...)
				return err1
			}, serviceOperationRetryPolicy, isServiceTransientError)
		if err != nil {
			return nil, err
		}
		history.Events = append(history.Events, response.History.Events...)
		if response.NextPageToken == nil {
			break GetHistoryLoop
		}
		nextPageToken = response.NextPageToken
	}
	return history, nil
}

// CompleteActivity reports activity completed. activity Execute method can return cadence.ErrActivityResultPending to
// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
// should be called when that activity is completed with the actual result and error. If err is nil, activity task
// completed event will be reported; if err is CanceledError, activity task cancelled event will be reported; otherwise,
// activity task failed event will be reported.
func (wc *workflowClient) CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error {
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
	return reportActivityComplete(ctx, wc.workflowService, request, wc.metricsScope)
}

// RecordActivityHeartbeat records heartbeat for an activity.
func (wc *workflowClient) RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error {
	data, err := getHostEnvironment().encodeArgs(details)
	if err != nil {
		return err
	}
	return recordActivityHeartbeat(ctx, wc.workflowService, wc.identity, taskToken, data, serviceOperationRetryPolicy)
}

// ListClosedWorkflow gets closed workflow executions based on request filters
// The errors it can throw:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *workflowClient) ListClosedWorkflow(ctx context.Context, request *s.ListClosedWorkflowExecutionsRequest) (*s.ListClosedWorkflowExecutionsResponse, error) {
	if len(request.GetDomain()) == 0 {
		request.Domain = common.StringPtr(wc.domain)
	}
	var response *s.ListClosedWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.ListClosedWorkflowExecutions(tchCtx, request, opt...)
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
func (wc *workflowClient) ListOpenWorkflow(ctx context.Context, request *s.ListOpenWorkflowExecutionsRequest) (*s.ListOpenWorkflowExecutionsResponse, error) {
	if len(request.GetDomain()) == 0 {
		request.Domain = common.StringPtr(wc.domain)
	}
	var response *s.ListOpenWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.ListOpenWorkflowExecutions(tchCtx, request, opt...)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// QueryWorkflow queries a given workflow execution
// workflowID and queryType are required, other parameters are optional.
// - workflow ID of the workflow.
// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
// - taskList can be default(empty string). If empty string then it will pick the taskList of the running execution of that workflow ID.
// - queryType is the type of the query.
// - args... are the optional query parameters.
// The errors it can return:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
//  - QueryFailError
func (wc *workflowClient) QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (EncodedValue, error) {
	var input []byte
	if len(args) > 0 {
		var err error
		if input, err = getHostEnvironment().encodeArgs(args); err != nil {
			return nil, err
		}
	}
	request := &s.QueryWorkflowRequest{
		Domain: common.StringPtr(wc.domain),
		Execution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      getRunID(runID),
		},
		Query: &s.WorkflowQuery{
			QueryType: common.StringPtr(queryType),
			QueryArgs: input,
		},
	}

	var resp *s.QueryWorkflowResponse
	err := backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			var err error
			resp, err = wc.workflowService.QueryWorkflow(tchCtx, request, opt...)
			return err
		}, serviceOperationRetryPolicy, isServiceTransientError)
	if err != nil {
		return nil, err
	}

	return EncodedValue(resp.QueryResult), nil
}

// Register a domain with cadence server
// The errors it can throw:
//	- DomainAlreadyExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Register(ctx context.Context, request *s.RegisterDomainRequest) error {
	return backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			return dc.workflowService.RegisterDomain(tchCtx, request, opt...)
		}, serviceOperationRetryPolicy, isServiceTransientError)
}

// Describe a domain. The domain has two part of information
// DomainInfo - Which has Name, Status, Description, Owner Email
// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Describe(ctx context.Context, name string) (*s.DomainInfo, *s.DomainConfiguration, error) {
	request := &s.DescribeDomainRequest{
		Name: common.StringPtr(name),
	}

	var response *s.DescribeDomainResponse
	err := backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			var err error
			response, err = dc.workflowService.DescribeDomain(tchCtx, request, opt...)
			return err
		}, serviceOperationRetryPolicy, isServiceTransientError)
	if err != nil {
		return nil, nil, err
	}
	return response.DomainInfo, response.Configuration, nil
}

// Update a domain. The domain has two part of information
// DomainInfo - Which has Name, Status, Description, Owner Email
// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Update(ctx context.Context, name string, domainInfo *s.UpdateDomainInfo, domainConfig *s.DomainConfiguration) error {
	request := &s.UpdateDomainRequest{
		Name:          common.StringPtr(name),
		UpdatedInfo:   domainInfo,
		Configuration: domainConfig,
	}

	return backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			_, err := dc.workflowService.UpdateDomain(tchCtx, request, opt...)
			return err
		}, serviceOperationRetryPolicy, isServiceTransientError)
}

func getRunID(runID string) *string {
	if runID == "" {
		// Cadence Server will pick current runID if provided empty.
		return nil
	}
	return common.StringPtr(runID)
}
