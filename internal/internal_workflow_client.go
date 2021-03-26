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

package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/pborman/uuid"
	"github.com/uber-go/tally"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	querypb "go.temporal.io/api/query/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/backoff"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/common/serializer"
	"go.temporal.io/sdk/internal/common/util"
	"go.temporal.io/sdk/log"
)

// Assert that structs do indeed implement the interfaces
var _ Client = (*WorkflowClient)(nil)
var _ NamespaceClient = (*namespaceClient)(nil)

const (
	defaultGetHistoryTimeout = 65 * time.Second
)

var (
	maxListArchivedWorkflowTimeout = time.Minute * 3
)

type (
	// WorkflowClient is the client for starting a workflow execution.
	WorkflowClient struct {
		workflowService    workflowservice.WorkflowServiceClient
		connectionCloser   io.Closer
		namespace          string
		registry           *registry
		logger             log.Logger
		metricsScope       tally.Scope
		identity           string
		dataConverter      converter.DataConverter
		contextPropagators []ContextPropagator
		tracer             opentracing.Tracer
	}

	// namespaceClient is the client for managing namespaces.
	namespaceClient struct {
		workflowService  workflowservice.WorkflowServiceClient
		connectionCloser io.Closer
		metricsScope     tally.Scope
		logger           log.Logger
		identity         string
	}

	// WorkflowRun represents a started non child workflow
	WorkflowRun interface {
		// GetID return workflow ID, which will be same as StartWorkflowOptions.ID if provided.
		GetID() string

		// GetRunID return the first started workflow run ID (please see below) - empty string if no such run
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
		workflowFn    interface{}
		workflowID    string
		firstRunID    string
		currentRunID  *util.OnceCell
		iterFn        func(ctx context.Context, runID string) HistoryEventIterator
		dataConverter converter.DataConverter
		registry      *registry
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
		Next() (*historypb.HistoryEvent, error)
	}

	// historyEventIteratorImpl is the implementation of HistoryEventIterator
	historyEventIteratorImpl struct {
		// whether this iterator is initialized
		initialized bool
		// local cached history events and corresponding consuming index
		nextEventIndex int
		events         []*historypb.HistoryEvent
		// token to get next page of history events
		nexttoken []byte
		// err when getting next page of history events
		err error
		// func which use a next token to get next page of history events
		paginate func(nexttoken []byte) (*workflowservice.GetWorkflowExecutionHistoryResponse, error)
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
func (wc *WorkflowClient) StartWorkflow(
	ctx context.Context,
	options StartWorkflowOptions,
	workflowFunc interface{},
	args ...interface{},
) (*WorkflowExecution, error) {
	workflowID := options.ID
	if len(workflowID) == 0 {
		workflowID = uuid.NewRandom().String()
	}

	executionTimeout := options.WorkflowExecutionTimeout
	runTimeout := options.WorkflowRunTimeout
	workflowTaskTimeout := options.WorkflowTaskTimeout

	// Validate type and its arguments.
	workflowType, input, err := getValidatedWorkflowFunction(workflowFunc, args, wc.dataConverter, wc.registry)
	if err != nil {
		return nil, err
	}

	memo, err := getWorkflowMemo(options.Memo, wc.dataConverter)
	if err != nil {
		return nil, err
	}

	searchAttr, err := serializeSearchAttributes(options.SearchAttributes)
	if err != nil {
		return nil, err
	}

	// create a workflow start span and attach it to the context object.
	// N.B. we need to finish this immediately as jaeger does not give us a way
	// to recreate a span given a span context - which means we will run into
	// issues during replay. we work around this by creating and ending the
	// workflow start span and passing in that context to the workflow. So
	// everything beginning with the StartWorkflowExecutionRequest will be
	// parented by the created start workflow span.
	ctx, span := createOpenTracingWorkflowSpan(ctx, wc.tracer, time.Now(), fmt.Sprintf("StartWorkflow-%s", workflowType.Name), workflowID)
	span.Finish()

	// get workflow headers from the context
	header := wc.getWorkflowHeader(ctx)

	// run propagators to extract information about tracing and other stuff, store in headers field
	startRequest := &workflowservice.StartWorkflowExecutionRequest{
		Namespace:                wc.namespace,
		RequestId:                uuid.New(),
		WorkflowId:               workflowID,
		WorkflowType:             &commonpb.WorkflowType{Name: workflowType.Name},
		TaskQueue:                &taskqueuepb.TaskQueue{Name: options.TaskQueue, Kind: enumspb.TASK_QUEUE_KIND_NORMAL},
		Input:                    input,
		WorkflowExecutionTimeout: &executionTimeout,
		WorkflowRunTimeout:       &runTimeout,
		WorkflowTaskTimeout:      &workflowTaskTimeout,
		Identity:                 wc.identity,
		WorkflowIdReusePolicy:    options.WorkflowIDReusePolicy,
		RetryPolicy:              convertToPBRetryPolicy(options.RetryPolicy),
		CronSchedule:             options.CronSchedule,
		Memo:                     memo,
		SearchAttributes:         searchAttr,
		Header:                   header,
	}

	var response *workflowservice.StartWorkflowExecutionResponse

	// Start creating workflow request.
	err = backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsScope(
				metrics.GetMetricsScopeForRPC(wc.metricsScope, workflowType.Name,
					metrics.NoneTagValue, options.TaskQueue)))
			defer cancel()

			var err1 error
			response, err1 = wc.workflowService.StartWorkflowExecution(grpcCtx, startRequest)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)

	if err != nil {
		return nil, err
	}

	executionInfo := &WorkflowExecution{
		ID:    workflowID,
		RunID: response.GetRunId()}
	return executionInfo, nil
}

// ExecuteWorkflow starts a workflow execution and returns a WorkflowRun that will allow you to wait until this workflow
// reaches the end state, such as workflow finished successfully or timeout.
// The user can use this to start using a functor like below and get the workflow execution result, as EncodedValue
// Either by
//     ExecuteWorkflow(options, "workflowTypeName", arg1, arg2, arg3)
//     or
//     ExecuteWorkflow(options, workflowExecuteFn, arg1, arg2, arg3)
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
// NOTE: the context.Context should have a fairly large timeout, since workflow execution may take a while to be finished
func (wc *WorkflowClient) ExecuteWorkflow(ctx context.Context, options StartWorkflowOptions, workflow interface{}, args ...interface{}) (WorkflowRun, error) {
	// start the workflow execution
	var runID string
	var workflowID string
	executionInfo, err := wc.StartWorkflow(ctx, options, workflow, args...)
	if err != nil {
		if e, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); ok {
			if options.WorkflowExecutionErrorWhenAlreadyStarted {
				return nil, err
			}
			runID = e.RunId
			workflowID = options.ID
		} else {
			return nil, err
		}
	} else {
		runID = executionInfo.RunID
		workflowID = executionInfo.ID
	}

	iterFn := func(fnCtx context.Context, fnRunID string) HistoryEventIterator {
		fnName, _ := getWorkflowFunctionName(wc.registry, workflow)
		rpcScope := metrics.GetMetricsScopeForRPC(wc.metricsScope, fnName, metrics.NoneTagValue, options.TaskQueue)
		return wc.getWorkflowHistory(fnCtx, workflowID, fnRunID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT, rpcScope)
	}

	curRunIDCell := util.PopulatedOnceCell(runID)
	return &workflowRunImpl{
		workflowFn:    workflow,
		workflowID:    workflowID,
		firstRunID:    runID,
		currentRunID:  &curRunIDCell,
		iterFn:        iterFn,
		dataConverter: wc.dataConverter,
		registry:      wc.registry,
	}, nil
}

// GetWorkflow gets a workflow execution and returns a WorkflowRun that will allow you to wait until this workflow
// reaches the end state, such as workflow finished successfully or timeout.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func (wc *WorkflowClient) GetWorkflow(ctx context.Context, workflowID string, runID string) WorkflowRun {

	iterFn := func(fnCtx context.Context, fnRunID string) HistoryEventIterator {
		return wc.GetWorkflowHistory(fnCtx, workflowID, fnRunID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
	}

	// The ID may not actually have been set - if not, we have to (lazily) ask the server for info about the workflow
	// execution and extract run id from there. This is definitely less efficient than it could be if there was a more
	// specific rpc method for this, or if there were more granular history filters - in which case it could be
	// extracted from the `iterFn` inside of `workflowRunImpl`
	var runIDCell util.OnceCell
	if runID == "" {
		fetcher := func() string {
			execData, _ := wc.DescribeWorkflowExecution(ctx, workflowID, runID)
			wei := execData.GetWorkflowExecutionInfo()
			if wei != nil {
				execution := wei.GetExecution()
				if execution != nil {
					return execution.RunId
				}
			}
			return ""
		}
		runIDCell = util.LazyOnceCell(fetcher)
	} else {
		runIDCell = util.PopulatedOnceCell(runID)
	}

	return &workflowRunImpl{
		workflowID:    workflowID,
		firstRunID:    runID,
		currentRunID:  &runIDCell,
		iterFn:        iterFn,
		dataConverter: wc.dataConverter,
		registry:      wc.registry,
	}
}

// SignalWorkflow signals a workflow in execution.
func (wc *WorkflowClient) SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error {
	input, err := encodeArg(wc.dataConverter, arg)
	if err != nil {
		return err
	}

	request := &workflowservice.SignalWorkflowExecutionRequest{
		Namespace: wc.namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: workflowID,
			RunId:      runID,
		},
		SignalName: signalName,
		Input:      input,
		Identity:   wc.identity,
	}

	return backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			_, err := wc.workflowService.SignalWorkflowExecution(grpcCtx, request)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
}

// SignalWithStartWorkflow sends a signal to a running workflow.
// If the workflow is not running or not found, it starts the workflow and then sends the signal in transaction.
func (wc *WorkflowClient) SignalWithStartWorkflow(ctx context.Context, workflowID string, signalName string, signalArg interface{},
	options StartWorkflowOptions, workflowFunc interface{}, workflowArgs ...interface{}) (WorkflowRun, error) {

	signalInput, err := encodeArg(wc.dataConverter, signalArg)
	if err != nil {
		return nil, err
	}

	if workflowID == "" {
		workflowID = uuid.NewRandom().String()
	}

	executionTimeout := options.WorkflowExecutionTimeout
	runTimeout := options.WorkflowRunTimeout
	taskTimeout := options.WorkflowTaskTimeout

	// Validate type and its arguments.
	workflowType, input, err := getValidatedWorkflowFunction(workflowFunc, workflowArgs, wc.dataConverter, wc.registry)
	if err != nil {
		return nil, err
	}

	memo, err := getWorkflowMemo(options.Memo, wc.dataConverter)
	if err != nil {
		return nil, err
	}

	searchAttr, err := serializeSearchAttributes(options.SearchAttributes)
	if err != nil {
		return nil, err
	}

	// create a workflow start span and attach it to the context object. finish it immediately
	ctx, span := createOpenTracingWorkflowSpan(ctx, wc.tracer, time.Now(), fmt.Sprintf("SignalWithStartWorkflow-%s", workflowType.Name), workflowID)
	span.Finish()

	// get workflow headers from the context
	header := wc.getWorkflowHeader(ctx)

	signalWithStartRequest := &workflowservice.SignalWithStartWorkflowExecutionRequest{
		Namespace:                wc.namespace,
		RequestId:                uuid.New(),
		WorkflowId:               workflowID,
		WorkflowType:             &commonpb.WorkflowType{Name: workflowType.Name},
		TaskQueue:                &taskqueuepb.TaskQueue{Name: options.TaskQueue, Kind: enumspb.TASK_QUEUE_KIND_NORMAL},
		Input:                    input,
		WorkflowExecutionTimeout: &executionTimeout,
		WorkflowRunTimeout:       &runTimeout,
		WorkflowTaskTimeout:      &taskTimeout,
		SignalName:               signalName,
		SignalInput:              signalInput,
		Identity:                 wc.identity,
		RetryPolicy:              convertToPBRetryPolicy(options.RetryPolicy),
		CronSchedule:             options.CronSchedule,
		Memo:                     memo,
		SearchAttributes:         searchAttr,
		WorkflowIdReusePolicy:    options.WorkflowIDReusePolicy,
		Header:                   header,
	}

	var response *workflowservice.SignalWithStartWorkflowExecutionResponse

	// Start creating workflow request.
	err = backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()

			var err1 error
			response, err1 = wc.workflowService.SignalWithStartWorkflowExecution(grpcCtx, signalWithStartRequest)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)

	if err != nil {
		return nil, err
	}

	iterFn := func(fnCtx context.Context, fnRunID string) HistoryEventIterator {
		rpcScope := metrics.GetMetricsScopeForRPC(wc.metricsScope, workflowType.Name, metrics.NoneTagValue, options.TaskQueue)
		return wc.getWorkflowHistory(fnCtx, workflowID, fnRunID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT, rpcScope)
	}

	curRunIDCell := util.PopulatedOnceCell(response.GetRunId())
	return &workflowRunImpl{
		workflowFn:    workflowFunc,
		workflowID:    workflowID,
		firstRunID:    response.GetRunId(),
		currentRunID:  &curRunIDCell,
		iterFn:        iterFn,
		dataConverter: wc.dataConverter,
		registry:      wc.registry,
	}, nil
}

// CancelWorkflow cancels a workflow in execution.  It allows workflow to properly clean up and gracefully close.
// workflowID is required, other parameters are optional.
// If runID is omit, it will terminate currently running workflow (if there is one) based on the workflowID.
func (wc *WorkflowClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	request := &workflowservice.RequestCancelWorkflowExecutionRequest{
		Namespace: wc.namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: workflowID,
			RunId:      runID,
		},
		Identity: wc.identity,
	}

	return backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			_, err := wc.workflowService.RequestCancelWorkflowExecution(grpcCtx, request)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
}

// TerminateWorkflow terminates a workflow execution.
// workflowID is required, other parameters are optional.
// If runID is omit, it will terminate currently running workflow (if there is one) based on the workflowID.
func (wc *WorkflowClient) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error {
	datailsPayload, err := wc.dataConverter.ToPayloads(details...)
	if err != nil {
		return err
	}

	request := &workflowservice.TerminateWorkflowExecutionRequest{
		Namespace: wc.namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: workflowID,
			RunId:      runID,
		},
		Reason:   reason,
		Identity: wc.identity,
		Details:  datailsPayload,
	}

	err = backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			_, err := wc.workflowService.TerminateWorkflowExecution(grpcCtx, request)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)

	return err
}

// GetWorkflowHistory return a channel which contains the history events of a given workflow
func (wc *WorkflowClient) GetWorkflowHistory(
	ctx context.Context,
	workflowID string,
	runID string,
	isLongPoll bool,
	filterType enumspb.HistoryEventFilterType,
) HistoryEventIterator {
	return wc.getWorkflowHistory(ctx, workflowID, runID, isLongPoll, filterType, wc.metricsScope)
}

func (wc *WorkflowClient) getWorkflowHistory(
	ctx context.Context,
	workflowID string,
	runID string,
	isLongPoll bool,
	filterType enumspb.HistoryEventFilterType,
	rpcMetricsScope tally.Scope,
) HistoryEventIterator {
	namespace := wc.namespace
	paginate := func(nextToken []byte) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
		request := &workflowservice.GetWorkflowExecutionHistoryRequest{
			Namespace: namespace,
			Execution: &commonpb.WorkflowExecution{
				WorkflowId: workflowID,
				RunId:      runID,
			},
			WaitNewEvent:           isLongPoll,
			HistoryEventFilterType: filterType,
			NextPageToken:          nextToken,
			SkipArchival:           isLongPoll,
		}

		var response *workflowservice.GetWorkflowExecutionHistoryResponse
		var err error
	Loop:
		for {
			err = backoff.Retry(ctx,
				func() error {
					var err1 error
					grpcCtx, cancel := newGRPCContext(ctx,
						grpcMetricsScope(rpcMetricsScope),
						grpcLongPoll(isLongPoll),
						func(builder *grpcContextBuilder) {
							if isLongPoll {
								builder.Timeout = defaultGetHistoryTimeout
							}
						})
					defer cancel()
					response, err1 = wc.workflowService.GetWorkflowExecutionHistory(grpcCtx, request)

					if err1 != nil {
						return err1
					}

					if response.RawHistory != nil {
						history, err := serializer.DeserializeBlobDataToHistoryEvents(response.RawHistory, filterType)
						if err != nil {
							return err
						}
						response.History = history
					}
					return err1
				},
				createDynamicServiceRetryPolicy(ctx),
				func(err error) bool {
					return isServiceTransientError(err) || isEntityNotFoundFromPassive(err)
				},
			)

			if err != nil {
				return nil, err
			}
			if isLongPoll && len(response.History.Events) == 0 && len(response.NextPageToken) != 0 {
				request.NextPageToken = response.NextPageToken
				continue Loop
			}
			break Loop
		}
		return response, nil
	}

	return &historyEventIteratorImpl{
		paginate: paginate,
	}
}

func isEntityNotFoundFromPassive(err error) bool {
	if notFoundError, ok := err.(*serviceerror.NotFound); ok {
		return notFoundError.ActiveCluster != "" &&
			notFoundError.CurrentCluster != "" &&
			notFoundError.ActiveCluster != notFoundError.CurrentCluster
	}

	return false
}

// CompleteActivity reports activity completed. activity Execute method can return activity.ErrResultPending to
// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
// should be called when that activity is completed with the actual result and error. If err is nil, activity task
// completed event will be reported; if err is CanceledError, activity task canceled event will be reported; otherwise,
// activity task failed event will be reported.
func (wc *WorkflowClient) CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error {
	if taskToken == nil {
		return errors.New("invalid task token provided")
	}

	var data *commonpb.Payloads
	if result != nil {
		var err0 error
		data, err0 = encodeArg(wc.dataConverter, result)
		if err0 != nil {
			return err0
		}
	}
	request := convertActivityResultToRespondRequest(wc.identity, taskToken, data, err, wc.dataConverter, wc.namespace)
	return reportActivityComplete(ctx, wc.workflowService, request, wc.metricsScope)
}

// CompleteActivityByID reports activity completed. Similar to CompleteActivity
// It takes namespace name, workflowID, runID, activityID as arguments.
func (wc *WorkflowClient) CompleteActivityByID(ctx context.Context, namespace, workflowID, runID, activityID string,
	result interface{}, err error) error {

	if activityID == "" || workflowID == "" || namespace == "" {
		return errors.New("empty activity or workflow id or namespace")
	}

	var data *commonpb.Payloads
	if result != nil {
		var err0 error
		data, err0 = encodeArg(wc.dataConverter, result)
		if err0 != nil {
			return err0
		}
	}

	request := convertActivityResultToRespondRequestByID(wc.identity, namespace, workflowID, runID, activityID, data, err, wc.dataConverter)
	return reportActivityCompleteByID(ctx, wc.workflowService, request, wc.metricsScope)
}

// RecordActivityHeartbeat records heartbeat for an activity.
func (wc *WorkflowClient) RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error {
	data, err := encodeArgs(wc.dataConverter, details)
	if err != nil {
		return err
	}
	return recordActivityHeartbeat(ctx, wc.workflowService, wc.metricsScope, wc.identity, taskToken, data)
}

// RecordActivityHeartbeatByID records heartbeat for an activity.
func (wc *WorkflowClient) RecordActivityHeartbeatByID(ctx context.Context,
	namespace, workflowID, runID, activityID string, details ...interface{}) error {
	data, err := encodeArgs(wc.dataConverter, details)
	if err != nil {
		return err
	}
	return recordActivityHeartbeatByID(ctx, wc.workflowService, wc.metricsScope, wc.identity, namespace, workflowID, runID, activityID, data)
}

// ListClosedWorkflow gets closed workflow executions based on request filters
// The errors it can throw:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *WorkflowClient) ListClosedWorkflow(ctx context.Context, request *workflowservice.ListClosedWorkflowExecutionsRequest) (*workflowservice.ListClosedWorkflowExecutionsResponse, error) {
	if request.GetNamespace() == "" {
		request.Namespace = wc.namespace
	}
	var response *workflowservice.ListClosedWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.ListClosedWorkflowExecutions(grpcCtx, request)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// ListOpenWorkflow gets open workflow executions based on request filters
// The errors it can throw:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *WorkflowClient) ListOpenWorkflow(ctx context.Context, request *workflowservice.ListOpenWorkflowExecutionsRequest) (*workflowservice.ListOpenWorkflowExecutionsResponse, error) {
	if request.GetNamespace() == "" {
		request.Namespace = wc.namespace
	}
	var response *workflowservice.ListOpenWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.ListOpenWorkflowExecutions(grpcCtx, request)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// ListWorkflow implementation
func (wc *WorkflowClient) ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	if request.GetNamespace() == "" {
		request.Namespace = wc.namespace
	}
	var response *workflowservice.ListWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.ListWorkflowExecutions(grpcCtx, request)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// ListArchivedWorkflow implementation
func (wc *WorkflowClient) ListArchivedWorkflow(ctx context.Context, request *workflowservice.ListArchivedWorkflowExecutionsRequest) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error) {
	if request.GetNamespace() == "" {
		request.Namespace = wc.namespace
	}
	var response *workflowservice.ListArchivedWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			timeout := maxListArchivedWorkflowTimeout
			now := time.Now()
			if ctx != nil {
				if expiration, ok := ctx.Deadline(); ok && expiration.After(now) {
					timeout = expiration.Sub(now)
					if timeout > maxListArchivedWorkflowTimeout {
						timeout = maxListArchivedWorkflowTimeout
					} else if timeout < minRPCTimeout {
						timeout = minRPCTimeout
					}
				}
			}
			grpcCtx, cancel := newGRPCContext(ctx, grpcTimeout(timeout))
			defer cancel()
			response, err1 = wc.workflowService.ListArchivedWorkflowExecutions(grpcCtx, request)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// ScanWorkflow implementation
func (wc *WorkflowClient) ScanWorkflow(ctx context.Context, request *workflowservice.ScanWorkflowExecutionsRequest) (*workflowservice.ScanWorkflowExecutionsResponse, error) {
	if request.GetNamespace() == "" {
		request.Namespace = wc.namespace
	}
	var response *workflowservice.ScanWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.ScanWorkflowExecutions(grpcCtx, request)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// CountWorkflow implementation
func (wc *WorkflowClient) CountWorkflow(ctx context.Context, request *workflowservice.CountWorkflowExecutionsRequest) (*workflowservice.CountWorkflowExecutionsResponse, error) {
	if request.GetNamespace() == "" {
		request.Namespace = wc.namespace
	}
	var response *workflowservice.CountWorkflowExecutionsResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.CountWorkflowExecutions(grpcCtx, request)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// GetSearchAttributes implementation
func (wc *WorkflowClient) GetSearchAttributes(ctx context.Context) (*workflowservice.GetSearchAttributesResponse, error) {
	var response *workflowservice.GetSearchAttributesResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.GetSearchAttributes(grpcCtx, &workflowservice.GetSearchAttributesRequest{})
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
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
func (wc *WorkflowClient) DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	request := &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: wc.namespace,
		Execution: &commonpb.WorkflowExecution{
			WorkflowId: workflowID,
			RunId:      runID,
		},
	}
	var response *workflowservice.DescribeWorkflowExecutionResponse
	err := backoff.Retry(ctx,
		func() error {
			var err1 error
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			response, err1 = wc.workflowService.DescribeWorkflowExecution(grpcCtx, request)
			return err1
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// QueryWorkflow queries a given workflow execution
// workflowID and queryType are required, other parameters are optional.
// - workflow ID of the workflow.
// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
// - taskQueue can be default(empty string). If empty string then it will pick the taskQueue of the running execution of that workflow ID.
// - queryType is the type of the query.
// - args... are the optional query parameters.
// The errors it can return:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
//  - QueryFailError
func (wc *WorkflowClient) QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (converter.EncodedValue, error) {
	queryWorkflowWithOptionsRequest := &QueryWorkflowWithOptionsRequest{
		WorkflowID: workflowID,
		RunID:      runID,
		QueryType:  queryType,
		Args:       args,
	}
	result, err := wc.QueryWorkflowWithOptions(ctx, queryWorkflowWithOptionsRequest)
	if err != nil {
		return nil, err
	}
	return result.QueryResult, nil
}

// QueryWorkflowWithOptionsRequest is the request to QueryWorkflowWithOptions
type QueryWorkflowWithOptionsRequest struct {
	// WorkflowID is a required field indicating the workflow which should be queried.
	WorkflowID string

	// RunID is an optional field used to identify a specific run of the queried workflow.
	// If RunID is not provided the latest run will be used.
	RunID string

	// QueryType is a required field which specifies the query you want to run.
	// By default, temporal supports "__stack_trace" as a standard query type, which will return string value
	// representing the call stack of the target workflow. The target workflow could also setup different query handler to handle custom query types.
	// See comments at workflow.SetQueryHandler(ctx Context, queryType string, handler interface{}) for more details on how to setup query handler within the target workflow.
	QueryType string

	// Args is an optional field used to identify the arguments passed to the query.
	Args []interface{}

	// QueryRejectCondition is an optional field used to reject queries based on workflow state.
	// QUERY_REJECT_CONDITION_NONE indicates that query should not be rejected.
	// QUERY_REJECT_CONDITION_NOT_OPEN indicates that query should be rejected if workflow is not open.
	// QUERY_REJECT_CONDITION_NOT_COMPLETED_CLEANLY indicates that query should be rejected if workflow did not complete cleanly (e.g. terminated, canceled timeout etc...).
	QueryRejectCondition enumspb.QueryRejectCondition
}

// QueryWorkflowWithOptionsResponse is the response to QueryWorkflowWithOptions
type QueryWorkflowWithOptionsResponse struct {
	// QueryResult contains the result of executing the query.
	// This will only be set if the query was completed successfully and not rejected.
	QueryResult converter.EncodedValue

	// QueryRejected contains information about the query rejection.
	QueryRejected *querypb.QueryRejected
}

// QueryWorkflowWithOptions queries a given workflow execution and returns the query result synchronously.
// See QueryWorkflowWithOptionsRequest and QueryWorkflowWithOptionsResult for more information.
// The errors it can return:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
//  - QueryFailError
func (wc *WorkflowClient) QueryWorkflowWithOptions(ctx context.Context, request *QueryWorkflowWithOptionsRequest) (*QueryWorkflowWithOptionsResponse, error) {
	var input *commonpb.Payloads
	if len(request.Args) > 0 {
		var err error
		if input, err = encodeArgs(wc.dataConverter, request.Args); err != nil {
			return nil, err
		}
	}
	req := &workflowservice.QueryWorkflowRequest{
		Namespace: wc.namespace,
		Execution: &commonpb.WorkflowExecution{
			WorkflowId: request.WorkflowID,
			RunId:      request.RunID,
		},
		Query: &querypb.WorkflowQuery{
			QueryType: request.QueryType,
			QueryArgs: input,
		},
		QueryRejectCondition: request.QueryRejectCondition,
	}

	var resp *workflowservice.QueryWorkflowResponse
	err := backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			var err error
			resp, err = wc.workflowService.QueryWorkflow(grpcCtx, req)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}

	if resp.QueryRejected != nil {
		return &QueryWorkflowWithOptionsResponse{
			QueryRejected: resp.QueryRejected,
			QueryResult:   nil,
		}, nil
	}
	return &QueryWorkflowWithOptionsResponse{
		QueryRejected: nil,
		QueryResult:   newEncodedValue(resp.QueryResult, wc.dataConverter),
	}, nil
}

// DescribeTaskQueue returns information about the target taskqueue, right now this API returns the
// pollers which polled this taskqueue in last few minutes.
// - taskqueue name of taskqueue
// - taskqueueType type of taskqueue, can be workflow or activity
// The errors it can return:
//  - BadRequestError
//  - InternalServiceError
//  - EntityNotExistError
func (wc *WorkflowClient) DescribeTaskQueue(ctx context.Context, taskQueue string, taskQueueType enumspb.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error) {
	request := &workflowservice.DescribeTaskQueueRequest{
		Namespace:     wc.namespace,
		TaskQueue:     &taskqueuepb.TaskQueue{Name: taskQueue, Kind: enumspb.TASK_QUEUE_KIND_NORMAL},
		TaskQueueType: taskQueueType,
	}

	var resp *workflowservice.DescribeTaskQueueResponse
	err := backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			var err error
			resp, err = wc.workflowService.DescribeTaskQueue(grpcCtx, request)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// Close client and clean up underlying resources.
func (wc *WorkflowClient) Close() {
	if wc.connectionCloser == nil {
		return
	}
	if err := wc.connectionCloser.Close(); err != nil {
		wc.logger.Warn("unable to close connection", tagError, err)
	}
}

func (wc *WorkflowClient) getWorkflowHeader(ctx context.Context) *commonpb.Header {
	header := &commonpb.Header{
		Fields: make(map[string]*commonpb.Payload),
	}
	writer := NewHeaderWriter(header)
	for _, ctxProp := range wc.contextPropagators {
		_ = ctxProp.Inject(ctx, writer)
	}
	return header
}

// Register a namespace with temporal server
// The errors it can throw:
//	- NamespaceAlreadyExistsError
//	- BadRequestError
//	- InternalServiceError
func (nc *namespaceClient) Register(ctx context.Context, request *workflowservice.RegisterNamespaceRequest) error {
	return backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			var err error
			_, err = nc.workflowService.RegisterNamespace(grpcCtx, request)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
}

// Describe a namespace. The namespace has 3 part of information
// NamespaceInfo - Which has Name, Status, Description, Owner Email
// NamespaceConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
// ReplicationConfiguration - replication config like clusters and active cluster name
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (nc *namespaceClient) Describe(ctx context.Context, namespace string) (*workflowservice.DescribeNamespaceResponse, error) {
	request := &workflowservice.DescribeNamespaceRequest{
		Namespace: namespace,
	}

	var response *workflowservice.DescribeNamespaceResponse
	err := backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			var err error
			response, err = nc.workflowService.DescribeNamespace(grpcCtx, request)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Update a namespace.
// The errors it can throw:
//	- EntityNotExistsError
//	- BadRequestError
//	- InternalServiceError
func (nc *namespaceClient) Update(ctx context.Context, request *workflowservice.UpdateNamespaceRequest) error {
	return backoff.Retry(ctx,
		func() error {
			grpcCtx, cancel := newGRPCContext(ctx)
			defer cancel()
			_, err := nc.workflowService.UpdateNamespace(grpcCtx, request)
			return err
		}, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
}

// Close client and clean up underlying resources.
func (nc *namespaceClient) Close() {
	if nc.connectionCloser == nil {
		return
	}
	if err := nc.connectionCloser.Close(); err != nil {
		nc.logger.Warn("unable to close connection", tagError, err)
	}
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

func (iter *historyEventIteratorImpl) Next() (*historypb.HistoryEvent, error) {
	// if caller call the Next() when iteration is over, just return nil, nil
	if !iter.HasNext() {
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
	return workflowRun.currentRunID.Get()
}

func (workflowRun *workflowRunImpl) GetID() string {
	return workflowRun.workflowID
}

func (workflowRun *workflowRunImpl) Get(ctx context.Context, valuePtr interface{}) error {

	iter := workflowRun.iterFn(ctx, workflowRun.currentRunID.Get())
	if !iter.HasNext() {
		panic("could not get last history event for workflow")
	}
	closeEvent, err := iter.Next()
	if err != nil {
		return err
	}

	switch closeEvent.GetEventType() {
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		attributes := closeEvent.GetWorkflowExecutionCompletedEventAttributes()
		if valuePtr == nil || attributes.Result == nil {
			return nil
		}
		rf := reflect.ValueOf(valuePtr)
		if rf.Type().Kind() != reflect.Ptr {
			return errors.New("value parameter is not a pointer")
		}
		return workflowRun.dataConverter.FromPayloads(attributes.Result, valuePtr)
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		attributes := closeEvent.GetWorkflowExecutionFailedEventAttributes()
		err = ConvertFailureToError(attributes.GetFailure(), workflowRun.dataConverter)
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
		attributes := closeEvent.GetWorkflowExecutionCanceledEventAttributes()
		details := newEncodedValues(attributes.Details, workflowRun.dataConverter)
		err = NewCanceledError(details)
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
		err = newTerminatedError()
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
		err = NewTimeoutError("Workflow timeout", enumspb.TIMEOUT_TYPE_START_TO_CLOSE, nil)
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
		attributes := closeEvent.GetWorkflowExecutionContinuedAsNewEventAttributes()
		curRunID := util.PopulatedOnceCell(attributes.GetNewExecutionRunId())
		workflowRun.currentRunID = &curRunID
		return workflowRun.Get(ctx, valuePtr)
	default:
		return fmt.Errorf("unexpected event type %s when handling workflow execution result", closeEvent.GetEventType())
	}

	fnName, _ := getWorkflowFunctionName(workflowRun.registry, workflowRun.workflowFn)
	err = NewWorkflowExecutionError(
		workflowRun.workflowID,
		workflowRun.currentRunID.Get(),
		fnName,
		err)

	return err
}

func getWorkflowMemo(input map[string]interface{}, dc converter.DataConverter) (*commonpb.Memo, error) {
	if input == nil {
		return nil, nil
	}

	memo := make(map[string]*commonpb.Payload)
	for k, v := range input {
		// TODO (shtin): use dc here???
		memoBytes, err := converter.GetDefaultDataConverter().ToPayload(v)
		if err != nil {
			return nil, fmt.Errorf("encode workflow memo error: %v", err.Error())
		}
		memo[k] = memoBytes
	}
	return &commonpb.Memo{Fields: memo}, nil
}

func serializeSearchAttributes(input map[string]interface{}) (*commonpb.SearchAttributes, error) {
	if input == nil {
		return nil, nil
	}

	attr := make(map[string]*commonpb.Payload)
	for k, v := range input {
		attrBytes, err := converter.GetDefaultDataConverter().ToPayload(v)
		if err != nil {
			return nil, fmt.Errorf("encode search attribute [%s] error: %v", k, err)
		}
		attr[k] = attrBytes
	}
	return &commonpb.SearchAttributes{IndexedFields: attr}, nil
}
