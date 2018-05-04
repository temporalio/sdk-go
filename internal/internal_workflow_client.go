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

package internal

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/pborman/uuid"
	"github.com/uber-go/tally"

	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/cadence/internal/common/backoff"
	"go.uber.org/cadence/internal/common/metrics"
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
		workflowService workflowserviceclient.Interface
		domain          string
		metricsScope    *metrics.TaggedScope
		identity        string
	}

	// domainClient is the client for managing domains.
	domainClient struct {
		workflowService workflowserviceclient.Interface
		metricsScope    tally.Scope
		identity        string
	}

	// WorkflowRun represents a started non child workflow
	WorkflowRun interface {
		// GetRunID return the first started workflow run ID (please see below)
		GetRunID() string

		// Get will fill the workflow execution result to valuePtr,
		// if workflow execution is a success, or return corresponding,
		// error. This is a blocking API.
		Get(ctx context.Context, valuePtr interface{}) error

		// NOTE: if the started workflow return ContinueAsNewError during the workflow execution, the
		// return result of GetRunID() will be the started workflow run ID, not the new run ID caused by ContinueAsNewError,
		// however, Get(ctx context.Context, valuePtr interface{}) will return result from the run which did not return ContinueAsNewError.
		// Say ExecuteWorkflow started a workflow, in its first run, has run ID "run ID 1", and returned ContinueAsNewError,
		// the second run has run ID "run ID 2" and return some result other than ContinueAsNewError:
		// GetRunID() will always return "run ID 1" and  Get(ctx context.Context, valuePtr interface{}) will return the result of second run.
		// NOTE: DO NOT USE client.ExecuteWorkflow API INSIDE A WORKFLOW, USE workflow.ExecuteChildWorkflow instead
	}

	// workflowRunImpl is an implementation of WorkflowRun
	workflowRunImpl struct {
		workflowFn   interface{}
		firstRunID   string
		currentRunID string
		iterFn       func(ctx context.Context, runID string) HistoryEventIterator
	}

	// HistoryEventIterator represents the interface for
	// history event iterator
	HistoryEventIterator interface {
		// HasNext return whether this iterator has next value
		HasNext() bool
		// Next returns the next history events and error
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		Next() (*s.HistoryEvent, error)
	}

	// historyEventIteratorImpl is the implementation of HistoryEventIterator
	historyEventIteratorImpl struct {
		// whether this iterator is initialized
		initialized bool
		// local cached histroy events and corresponding comsuming index
		nextEventIndex int
		events         []*s.HistoryEvent
		// token to get next page of history events
		nexttoken []byte
		// err when getting next page of history events
		err error
		// func which use a next token to get next page of history events
		paginate func(nexttoken []byte) (*s.GetWorkflowExecutionHistoryResponse, error)
	}
)

// StartWorkflow starts a workflow execution
// The user can use this to start using a functor like.
// Either by
//     StartWorkflow(options, "workflowTypeName", arg1, arg2, arg3)
//     or
//     StartWorkflow(options, workflowExecuteFn, arg1, arg2, arg3)
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func (wc *workflowClient) StartWorkflow(
	ctx context.Context,
	options StartWorkflowOptions,
	workflowFunc interface{},
	args ...interface{},
) (*WorkflowExecution, error) {
	workflowID := options.ID
	if len(workflowID) == 0 {
		workflowID = uuid.NewRandom().String()
	}

	if options.TaskList == "" {
		return nil, errors.New("missing TaskList")
	}

	executionTimeout := common.Int32Ceil(options.ExecutionStartToCloseTimeout.Seconds())
	if executionTimeout <= 0 {
		return nil, errors.New("missing or invalid ExecutionStartToCloseTimeout")
	}

	decisionTaskTimeout := common.Int32Ceil(options.DecisionTaskStartToCloseTimeout.Seconds())
	if decisionTaskTimeout < 0 {
		return nil, errors.New("negative DecisionTaskStartToCloseTimeout provided")
	}
	if decisionTaskTimeout == 0 {
		decisionTaskTimeout = defaultDecisionTaskTimeoutInSecs
	}

	// Validate type and its arguments.
	workflowType, input, err := getValidatedWorkflowFunction(workflowFunc, args)
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
		Identity:                            common.StringPtr(wc.identity),
		WorkflowIdReusePolicy:               options.WorkflowIDReusePolicy.toThriftPtr(),
	}

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
		scope := wc.metricsScope.GetTaggedScope(tagWorkflowType, workflowType.Name)
		scope.Counter(metrics.WorkflowStartCounter).Inc(1)
	}

	executionInfo := &WorkflowExecution{
		ID:    workflowID,
		RunID: response.GetRunId()}
	return executionInfo, nil
}

// ExecuteWorkflow starts a workflow execution and wait until this workflow reaches the end state, such as
// workflow finished successfully or timeout.
// The user can use this to start using a functor like below and get the workflow execution result, as encoded.Value
// Either by
//     RunWorkflow(options, "workflowTypeName", arg1, arg2, arg3)
//     or
//     RunWorkflow(options, workflowExecuteFn, arg1, arg2, arg3)
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
// NOTE: the context.Context should have a fairly large timeout, since workflow execution may take a while to be finished
func (wc *workflowClient) ExecuteWorkflow(ctx context.Context, options StartWorkflowOptions, workflow interface{}, args ...interface{}) (WorkflowRun, error) {

	// start the workflow execution
	var runID string
	var workflowID string
	executionInfo, err := wc.StartWorkflow(ctx, options, workflow, args...)
	if err != nil {
		if alreadyStartedErr, ok := err.(*s.WorkflowExecutionAlreadyStartedError); ok {
			runID = alreadyStartedErr.GetRunId()
			// Assumption is that AlreadyStarted is never returned when options.ID is empty as UUID generated by
			// StartWorkflow is not going to collide ever.
			workflowID = options.ID
		} else {
			return nil, err
		}
	} else {
		runID = executionInfo.RunID
		workflowID = executionInfo.ID
	}

	iterFn := func(fnCtx context.Context, fnRunID string) HistoryEventIterator {
		return wc.GetWorkflowHistory(fnCtx, workflowID, fnRunID, true, s.HistoryEventFilterTypeCloseEvent)
	}

	return &workflowRunImpl{
		workflowFn:   workflow,
		firstRunID:   runID,
		currentRunID: runID,
		iterFn:       iterFn,
	}, nil
}

// SignalWorkflow signals a workflow in execution.
func (wc *workflowClient) SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error {
	input, err := getEncodedArg(arg)
	if err != nil {
		return err
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

// SignalWithStartWorkflow sends a signal to a running workflow.
// If the workflow is not running or not found, it starts the workflow and then sends the signal in transaction.
func (wc *workflowClient) SignalWithStartWorkflow(ctx context.Context, workflowID string, signalName string, signalArg interface{},
	options StartWorkflowOptions, workflowFunc interface{}, workflowArgs ...interface{}) (*WorkflowExecution, error) {

	signalInput, err := getEncodedArg(signalArg)
	if err != nil {
		return nil, err
	}

	if workflowID == "" {
		workflowID = uuid.NewRandom().String()
	}

	if options.TaskList == "" {
		return nil, errors.New("missing TaskList")
	}

	executionTimeout := common.Int32Ceil(options.ExecutionStartToCloseTimeout.Seconds())
	if executionTimeout <= 0 {
		return nil, errors.New("missing or invalid ExecutionStartToCloseTimeout")
	}

	decisionTaskTimeout := common.Int32Ceil(options.DecisionTaskStartToCloseTimeout.Seconds())
	if decisionTaskTimeout < 0 {
		return nil, errors.New("negative DecisionTaskStartToCloseTimeout provided")
	}
	if decisionTaskTimeout == 0 {
		decisionTaskTimeout = defaultDecisionTaskTimeoutInSecs
	}

	// Validate type and its arguments.
	workflowType, input, err := getValidatedWorkflowFunction(workflowFunc, workflowArgs)
	if err != nil {
		return nil, err
	}

	signalWithStartRequest := &s.SignalWithStartWorkflowExecutionRequest{
		Domain:       common.StringPtr(wc.domain),
		RequestId:    common.StringPtr(uuid.New()),
		WorkflowId:   common.StringPtr(workflowID),
		WorkflowType: workflowTypePtr(*workflowType),
		TaskList:     common.TaskListPtr(s.TaskList{Name: common.StringPtr(options.TaskList)}),
		Input:        input,
		ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(executionTimeout),
		TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(decisionTaskTimeout),
		SignalName:                          common.StringPtr(signalName),
		SignalInput:                         signalInput,
		Identity:                            common.StringPtr(wc.identity),
	}

	var response *s.StartWorkflowExecutionResponse

	// Start creating workflow request.
	err = backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()

			var err1 error
			response, err1 = wc.workflowService.SignalWithStartWorkflowExecution(tchCtx, signalWithStartRequest, opt...)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return nil, err
	}

	if wc.metricsScope != nil {
		scope := wc.metricsScope.GetTaggedScope(tagWorkflowType, workflowType.Name)
		scope.Counter(metrics.WorkflowSingalWithStartCounter).Inc(1)
	}

	executionInfo := &WorkflowExecution{
		ID:    options.ID,
		RunID: response.GetRunId()}
	return executionInfo, nil
}

// CancelWorkflow cancels a workflow in execution.  It allows workflow to properly clean up and gracefully close.
// workflowID is required, other parameters are optional.
// If runID is omit, it will terminate currently running workflow (if there is one) based on the workflowID.
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

// GetWorkflowHistory return a channel which contains the history events of a given workflow
func (wc *workflowClient) GetWorkflowHistory(ctx context.Context, workflowID string, runID string,
	isLongPoll bool, filterType s.HistoryEventFilterType) HistoryEventIterator {

	domain := wc.domain
	paginate := func(nexttoken []byte) (*s.GetWorkflowExecutionHistoryResponse, error) {
		request := &s.GetWorkflowExecutionHistoryRequest{
			Domain: common.StringPtr(domain),
			Execution: &s.WorkflowExecution{
				WorkflowId: common.StringPtr(workflowID),
				RunId:      getRunID(runID),
			},
			WaitForNewEvent:        common.BoolPtr(isLongPoll),
			HistoryEventFilterType: &filterType,
			NextPageToken:          nexttoken,
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
		return response, nil
	}

	return &historyEventIteratorImpl{
		paginate: paginate,
	}
}

// CompleteActivity reports activity completed. activity Execute method can return acitivity.activity.ErrResultPending to
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

// CompleteActivityById reports activity completed. Similar to CompleteActivity
// It takes domain name, workflowID, runID, activityID as arguments.
func (wc *workflowClient) CompleteActivityByID(ctx context.Context, domain, workflowID, runID, activityID string,
	result interface{}, err error) error {

	if activityID == "" || workflowID == "" || domain == "" {
		return errors.New("empty activity or workflow id or domainName")
	}

	var data []byte
	if result != nil {
		var err0 error
		data, err0 = getHostEnvironment().encodeArg(result)
		if err0 != nil {
			return err0
		}
	}

	request := convertActivityResultToRespondRequestByID(wc.identity, domain, workflowID, runID, activityID, data, err)
	return reportActivityCompleteByID(ctx, wc.workflowService, request, wc.metricsScope)
}

// RecordActivityHeartbeat records heartbeat for an activity.
func (wc *workflowClient) RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error {
	data, err := getHostEnvironment().encodeArgs(details)
	if err != nil {
		return err
	}
	return recordActivityHeartbeat(ctx, wc.workflowService, wc.identity, taskToken, data, serviceOperationRetryPolicy)
}

// RecordActivityHeartbeatByID records heartbeat for an activity.
func (wc *workflowClient) RecordActivityHeartbeatByID(ctx context.Context,
	domain, workflowID, runID, activityID string, details ...interface{}) error {
	data, err := getHostEnvironment().encodeArgs(details)
	if err != nil {
		return err
	}
	return recordActivityHeartbeatByID(ctx, wc.workflowService, wc.identity, domain, workflowID, runID, activityID, data, serviceOperationRetryPolicy)
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

// DescribeWorkflowExecution returns information about the specified workflow execution.
// The errors it can return:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *workflowClient) DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*s.DescribeWorkflowExecutionResponse, error) {
	request := &s.DescribeWorkflowExecutionRequest{
		Domain: common.StringPtr(wc.domain),
		Execution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      common.StringPtr(runID),
		},
	}
	var response *s.DescribeWorkflowExecutionResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.DescribeWorkflowExecution(tchCtx, request, opt...)
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
func (wc *workflowClient) QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (encoded.Value, error) {
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

// DescribeTaskList returns information about the target tasklist, right now this API returns the
// pollers which polled this tasklist in last few minutes.
// - tasklist name of tasklist
// - tasklistType type of tasklist, can be decision or activity
// The errors it can return:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *workflowClient) DescribeTaskList(ctx context.Context, tasklist string, tasklistType s.TaskListType) (*s.DescribeTaskListResponse, error) {
	request := &s.DescribeTaskListRequest{
		Domain:       common.StringPtr(wc.domain),
		TaskList:     &s.TaskList{Name: common.StringPtr(tasklist)},
		TaskListType: &tasklistType,
	}

	var resp *s.DescribeTaskListResponse
	err := backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			var err error
			resp, err = wc.workflowService.DescribeTaskList(tchCtx, request, opt...)
			return err
		}, serviceOperationRetryPolicy, isServiceTransientError)
	if err != nil {
		return nil, err
	}

	return resp, nil
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

// Describe a domain. The domain has 3 part of information
// DomainInfo - Which has Name, Status, Description, Owner Email
// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
// ReplicationConfiguration - replication config like clusters and active cluster name
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Describe(ctx context.Context, name string) (*s.DescribeDomainResponse, error) {
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
		return nil, err
	}
	return response, nil
}

// Update a domain.
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (dc *domainClient) Update(ctx context.Context, request *s.UpdateDomainRequest) error {
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

func (iter *historyEventIteratorImpl) HasNext() bool {
	if iter.nextEventIndex < len(iter.events) || iter.err != nil {
		return true
	} else if !iter.initialized || len(iter.nexttoken) != 0 {
		iter.initialized = true
		response, err := iter.paginate(iter.nexttoken)
		iter.nextEventIndex = 0
		if err == nil {
			iter.events = response.History.Events
			iter.nexttoken = response.NextPageToken
			iter.err = nil
		} else {
			iter.events = nil
			iter.nexttoken = nil
			iter.err = err
		}

		if iter.nextEventIndex < len(iter.events) || iter.err != nil {
			return true
		}
		return false
	}

	return false
}

func (iter *historyEventIteratorImpl) Next() (*s.HistoryEvent, error) {
	// if caller call the Next() when iteration is over, just return nil, nil
	if !iter.HasNext() {
		// debug.PrintStack()
		panic("HistoryEventIterator Next() called without checking HasNext()")
	}

	// we have cached events
	if iter.nextEventIndex < len(iter.events) {
		index := iter.nextEventIndex
		iter.nextEventIndex++
		return iter.events[index], nil
	} else if iter.err != nil {
		// we have err, clear that iter.err and return err
		err := iter.err
		iter.err = nil
		return nil, err
	}

	panic("HistoryEventIterator Next() should return either a history event or a err")
}

func (workflowRun *workflowRunImpl) GetRunID() string {
	return workflowRun.firstRunID
}

func (workflowRun *workflowRunImpl) Get(ctx context.Context, valuePtr interface{}) error {

	iter := workflowRun.iterFn(ctx, workflowRun.currentRunID)
	if !iter.HasNext() {
		panic("could not get last history event for workflow")
	}
	closeEvent, err := iter.Next()
	if err != nil {
		return err
	}

	switch closeEvent.GetEventType() {
	case s.EventTypeWorkflowExecutionCompleted:
		attributes := closeEvent.WorkflowExecutionCompletedEventAttributes
		if valuePtr == nil || attributes.Result == nil {
			return nil
		}
		rf := reflect.ValueOf(valuePtr)
		if rf.Type().Kind() != reflect.Ptr {
			return errors.New("value parameter is not a pointer")
		}
		err = deSerializeFunctionResult(workflowRun.workflowFn, attributes.Result, valuePtr)
	case s.EventTypeWorkflowExecutionFailed:
		attributes := closeEvent.WorkflowExecutionFailedEventAttributes
		err = constructError(attributes.GetReason(), attributes.Details)
	case s.EventTypeWorkflowExecutionCanceled:
		attributes := closeEvent.WorkflowExecutionCanceledEventAttributes
		err = NewCanceledError(attributes.Details)
	case s.EventTypeWorkflowExecutionTerminated:
		err = newTerminatedError()
	case s.EventTypeWorkflowExecutionTimedOut:
		attributes := closeEvent.WorkflowExecutionTimedOutEventAttributes
		err = NewTimeoutError(attributes.GetTimeoutType())
	case s.EventTypeWorkflowExecutionContinuedAsNew:
		attributes := closeEvent.WorkflowExecutionContinuedAsNewEventAttributes
		workflowRun.currentRunID = attributes.GetNewExecutionRunId()
		return workflowRun.Get(ctx, valuePtr)
	default:
		err = fmt.Errorf("Unexpected event type %s when handling workflow execution result", closeEvent.GetEventType())
	}
	return err
}

func getEncodedArg(arg interface{}) ([]byte, error) {
	var input []byte
	if arg != nil {
		var err error
		if input, err = getHostEnvironment().encodeArg(arg); err != nil {
			return nil, err
		}
	}
	return input, nil
}
