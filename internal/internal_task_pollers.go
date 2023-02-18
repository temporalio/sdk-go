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

// All code in this file is private to the package.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gogo/protobuf/types"
	"github.com/pborman/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/common/serializer"
	"go.temporal.io/sdk/log"
)

const (
	// Server returns empty task after dynamicconfig.MatchingLongPollExpirationInterval (default is 60 seconds).
	// pollTaskServiceTimeOut should be dynamicconfig.MatchingLongPollExpirationInterval + some delta for full round trip to matching
	// because empty task should be returned before timeout is expired (expired timeout counts against SLO).
	pollTaskServiceTimeOut = 70 * time.Second

	stickyWorkflowTaskScheduleToStartTimeoutSeconds = 5

	ratioToForceCompleteWorkflowTaskComplete = 0.8
)

type (
	// taskPoller interface to poll and process for task
	taskPoller interface {
		// PollTask polls for one new task
		PollTask() (interface{}, error)
		// ProcessTask processes a task
		ProcessTask(interface{}) error
	}

	// basePoller is the base class for all poller implementations
	basePoller struct {
		metricsHandler metrics.Handler // base metric handler used for rpc calls
		stopC          <-chan struct{}
	}

	// numPollerMetric tracks the number of active pollers and publishes a metric on it.
	numPollerMetric struct {
		lock       sync.Mutex
		numPollers int32
		gauge      metrics.Gauge
	}

	// workflowTaskPoller implements polling/processing a workflow task
	workflowTaskPoller struct {
		basePoller
		namespace        string
		taskQueueName    string
		identity         string
		service          workflowservice.WorkflowServiceClient
		taskHandler      WorkflowTaskHandler
		logger           log.Logger
		dataConverter    converter.DataConverter
		failureConverter converter.FailureConverter

		stickyUUID                   string
		StickyScheduleToStartTimeout time.Duration

		pendingRegularPollCount int
		pendingStickyPollCount  int
		stickyBacklog           int64
		requestLock             sync.Mutex
		stickyCacheSize         int
		eagerActivityExecutor   *eagerActivityExecutor

		numNormalPollerMetric *numPollerMetric
		numStickyPollerMetric *numPollerMetric
	}

	// activityTaskPoller implements polling/processing a workflow task
	activityTaskPoller struct {
		basePoller
		namespace           string
		taskQueueName       string
		identity            string
		service             workflowservice.WorkflowServiceClient
		taskHandler         ActivityTaskHandler
		logger              log.Logger
		activitiesPerSecond float64
		numPollerMetric     *numPollerMetric
	}

	historyIteratorImpl struct {
		iteratorFunc   func(nextPageToken []byte) (*historypb.History, []byte, error)
		execution      *commonpb.WorkflowExecution
		nextPageToken  []byte
		namespace      string
		service        workflowservice.WorkflowServiceClient
		maxEventID     int64
		metricsHandler metrics.Handler
		taskQueue      string
	}

	localActivityTaskPoller struct {
		basePoller
		handler  *localActivityTaskHandler
		logger   log.Logger
		laTunnel *localActivityTunnel
	}

	localActivityTaskHandler struct {
		userContext        context.Context
		metricsHandler     metrics.Handler
		logger             log.Logger
		dataConverter      converter.DataConverter
		contextPropagators []ContextPropagator
		interceptors       []WorkerInterceptor
	}

	localActivityResult struct {
		result  *commonpb.Payloads
		err     error
		task    *localActivityTask
		backoff time.Duration
	}

	localActivityTunnel struct {
		taskCh   chan *localActivityTask
		resultCh chan interface{}
		stopCh   <-chan struct{}
	}
)

func newNumPollerMetric(metricsHandler metrics.Handler, pollerType string) *numPollerMetric {
	return &numPollerMetric{
		gauge: metricsHandler.WithTags(metrics.PollerTags(pollerType)).Gauge(metrics.NumPoller),
	}
}

func (npm *numPollerMetric) increment() {
	npm.lock.Lock()
	defer npm.lock.Unlock()
	npm.numPollers += 1
	npm.gauge.Update(float64(npm.numPollers))
}

func (npm *numPollerMetric) decrement() {
	npm.lock.Lock()
	defer npm.lock.Unlock()
	npm.numPollers -= 1
	npm.gauge.Update(float64(npm.numPollers))
}

func newLocalActivityTunnel(stopCh <-chan struct{}) *localActivityTunnel {
	return &localActivityTunnel{
		taskCh:   make(chan *localActivityTask, 100000),
		resultCh: make(chan interface{}),
		stopCh:   stopCh,
	}
}

func (lat *localActivityTunnel) getTask() *localActivityTask {
	select {
	case task := <-lat.taskCh:
		return task
	case <-lat.stopCh:
		return nil
	}
}

func (lat *localActivityTunnel) sendTask(task *localActivityTask) bool {
	select {
	case lat.taskCh <- task:
		return true
	case <-lat.stopCh:
		return false
	}
}

func isClientSideError(err error) bool {
	// If an activity execution exceeds deadline.
	return err == context.DeadlineExceeded
}

// stopping returns true if worker is stopping right now
func (bp *basePoller) stopping() bool {
	select {
	case <-bp.stopC:
		return true
	default:
		return false
	}
}

// doPoll runs the given pollFunc in a separate go routine. Returns when either of the conditions are met:
// - poll succeeds, poll fails or worker is stopping
func (bp *basePoller) doPoll(pollFunc func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	if bp.stopping() {
		return nil, errStop
	}

	var err error
	var result interface{}

	doneC := make(chan struct{})
	ctx, cancel := newGRPCContext(context.Background(), grpcTimeout(pollTaskServiceTimeOut), grpcLongPoll(true))

	go func() {
		result, err = pollFunc(ctx)
		cancel()
		close(doneC)
	}()

	select {
	case <-doneC:
		return result, err
	case <-bp.stopC:
		cancel()
		return nil, errStop
	}
}

// newWorkflowTaskPoller creates a new workflow task poller which must have a one to one relationship to workflow worker
func newWorkflowTaskPoller(
	taskHandler WorkflowTaskHandler,
	service workflowservice.WorkflowServiceClient,
	params workerExecutionParameters,
) *workflowTaskPoller {
	return &workflowTaskPoller{
		basePoller:                   basePoller{metricsHandler: params.MetricsHandler, stopC: params.WorkerStopChannel},
		service:                      service,
		namespace:                    params.Namespace,
		taskQueueName:                params.TaskQueue,
		identity:                     params.Identity,
		taskHandler:                  taskHandler,
		logger:                       params.Logger,
		dataConverter:                params.DataConverter,
		failureConverter:             params.FailureConverter,
		stickyUUID:                   uuid.New(),
		StickyScheduleToStartTimeout: params.StickyScheduleToStartTimeout,
		stickyCacheSize:              params.cache.MaxWorkflowCacheSize(),
		eagerActivityExecutor:        params.eagerActivityExecutor,
		numNormalPollerMetric:        newNumPollerMetric(params.MetricsHandler, metrics.PollerTypeWorkflowTask),
		numStickyPollerMetric:        newNumPollerMetric(params.MetricsHandler, metrics.PollerTypeWorkflowStickyTask),
	}
}

// PollTask polls a new task
func (wtp *workflowTaskPoller) PollTask() (interface{}, error) {
	// Get the task.
	workflowTask, err := wtp.doPoll(wtp.poll)
	if err != nil {
		return nil, err
	}

	return workflowTask, nil
}

// ProcessTask processes a task which could be workflow task or local activity result
func (wtp *workflowTaskPoller) ProcessTask(task interface{}) error {
	if wtp.stopping() {
		return errStop
	}

	switch task := task.(type) {
	case *workflowTask:
		return wtp.processWorkflowTask(task)
	default:
		panic("unknown task type.")
	}
}

func (wtp *workflowTaskPoller) processWorkflowTask(task *workflowTask) error {
	if task.task == nil {
		// We didn't have task, poll might have timeout.
		traceLog(func() {
			wtp.logger.Debug("Workflow task unavailable")
		})
		return nil
	}

	doneCh := make(chan struct{})
	laResultCh := make(chan *localActivityResult)
	laRetryCh := make(chan *localActivityTask)
	// close doneCh so local activity worker won't get blocked forever when trying to send back result to laResultCh.
	defer close(doneCh)

	for {
		var response *workflowservice.RespondWorkflowTaskCompletedResponse
		startTime := time.Now()
		task.doneCh = doneCh
		task.laResultCh = laResultCh
		task.laRetryCh = laRetryCh
		completedRequest, resetter, err := wtp.taskHandler.ProcessWorkflowTask(
			task,
			func(response interface{}, startTime time.Time) (*workflowTask, error) {
				wtp.logger.Debug("Force RespondWorkflowTaskCompleted.", "TaskStartedEventID", task.task.GetStartedEventId())
				heartbeatResponse, err := wtp.RespondTaskCompletedWithMetrics(response, nil, task.task, startTime)
				if err != nil {
					return nil, err
				}
				if heartbeatResponse == nil || heartbeatResponse.WorkflowTask == nil {
					return nil, nil
				}
				task := wtp.toWorkflowTask(heartbeatResponse.WorkflowTask)
				task.doneCh = doneCh
				task.laResultCh = laResultCh
				task.laRetryCh = laRetryCh
				return task, nil
			},
		)
		if completedRequest == nil && err == nil {
			return nil
		}
		if _, ok := err.(workflowTaskHeartbeatError); ok {
			return err
		}
		response, err = wtp.RespondTaskCompletedWithMetrics(completedRequest, err, task.task, startTime)
		if err != nil {
			return err
		}

		if eventLevel := response.GetResetHistoryEventId(); eventLevel != 0 {
			resetter(eventLevel)
		}

		if response == nil || response.WorkflowTask == nil {
			return nil
		}

		// we are getting new workflow task, so reset the workflowTask and continue process the new one
		task = wtp.toWorkflowTask(response.WorkflowTask)
	}
}

func (wtp *workflowTaskPoller) RespondTaskCompletedWithMetrics(
	completedRequest interface{},
	taskErr error,
	task *workflowservice.PollWorkflowTaskQueueResponse,
	startTime time.Time,
) (response *workflowservice.RespondWorkflowTaskCompletedResponse, err error) {
	metricsHandler := wtp.metricsHandler.WithTags(metrics.WorkflowTags(task.WorkflowType.GetName()))
	if taskErr != nil {
		metricsHandler.Counter(metrics.WorkflowTaskExecutionFailureCounter).Inc(1)
		wtp.logger.Warn("Failed to process workflow task.",
			tagWorkflowType, task.WorkflowType.GetName(),
			tagWorkflowID, task.WorkflowExecution.GetWorkflowId(),
			tagRunID, task.WorkflowExecution.GetRunId(),
			tagAttempt, task.Attempt,
			tagError, taskErr)
		// convert err to WorkflowTaskFailed
		completedRequest = errorToFailWorkflowTask(task.TaskToken, taskErr, wtp.identity, wtp.dataConverter, wtp.failureConverter, wtp.namespace)
	}

	metricsHandler.Timer(metrics.WorkflowTaskExecutionLatency).Record(time.Since(startTime))

	response, err = wtp.RespondTaskCompleted(completedRequest, task)
	return
}

func (wtp *workflowTaskPoller) RespondTaskCompleted(completedRequest interface{}, task *workflowservice.PollWorkflowTaskQueueResponse) (response *workflowservice.RespondWorkflowTaskCompletedResponse, err error) {
	ctx := context.Background()
	// Respond task completion.
	grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(
		wtp.metricsHandler.WithTags(metrics.RPCTags(task.GetWorkflowType().GetName(),
			metrics.NoneTagValue, metrics.NoneTagValue))),
		defaultGrpcRetryParameters(ctx))
	defer cancel()
	switch request := completedRequest.(type) {
	case *workflowservice.RespondWorkflowTaskFailedRequest:
		// Only fail workflow task on first attempt, subsequent failure on the same workflow task will timeout.
		// This is to avoid spin on the failed workflow task. Checking Attempt not nil for older server.
		if task.GetAttempt() == 1 {
			_, err = wtp.service.RespondWorkflowTaskFailed(grpcCtx, request)
			if err != nil {
				traceLog(func() {
					wtp.logger.Debug("RespondWorkflowTaskFailed failed.", tagError, err)
				})
			}
		}
	case *workflowservice.RespondWorkflowTaskCompletedRequest:
		if request.StickyAttributes == nil && wtp.stickyCacheSize > 0 {
			request.StickyAttributes = &taskqueuepb.StickyExecutionAttributes{
				WorkerTaskQueue: &taskqueuepb.TaskQueue{
					Name: getWorkerTaskQueue(wtp.stickyUUID),
					Kind: enumspb.TASK_QUEUE_KIND_STICKY,
				},
				ScheduleToStartTimeout: &wtp.StickyScheduleToStartTimeout,
			}
		}
		eagerReserved := wtp.eagerActivityExecutor.applyToRequest(request)
		response, err = wtp.service.RespondWorkflowTaskCompleted(grpcCtx, request)
		if err != nil {
			traceLog(func() {
				wtp.logger.Debug("RespondWorkflowTaskCompleted failed.", tagError, err)
			})
		}
		wtp.eagerActivityExecutor.handleResponse(response, eagerReserved)
	case *workflowservice.RespondQueryTaskCompletedRequest:
		_, err = wtp.service.RespondQueryTaskCompleted(grpcCtx, request)
		if err != nil {
			traceLog(func() {
				wtp.logger.Debug("RespondQueryTaskCompleted failed.", tagError, err)
			})
		}
	default:
		// should not happen
		panic("unknown request type from ProcessWorkflowTask()")
	}
	return
}

func newLocalActivityPoller(
	params workerExecutionParameters,
	laTunnel *localActivityTunnel,
	interceptors []WorkerInterceptor,
) *localActivityTaskPoller {
	handler := &localActivityTaskHandler{
		userContext:        params.UserContext,
		metricsHandler:     params.MetricsHandler,
		logger:             params.Logger,
		dataConverter:      params.DataConverter,
		contextPropagators: params.ContextPropagators,
		interceptors:       interceptors,
	}
	return &localActivityTaskPoller{
		basePoller: basePoller{metricsHandler: params.MetricsHandler, stopC: params.WorkerStopChannel},
		handler:    handler,
		logger:     params.Logger,
		laTunnel:   laTunnel,
	}
}

func (latp *localActivityTaskPoller) PollTask() (interface{}, error) {
	return latp.laTunnel.getTask(), nil
}

func (latp *localActivityTaskPoller) ProcessTask(task interface{}) error {
	if latp.stopping() {
		return errStop
	}

	result := latp.handler.executeLocalActivityTask(task.(*localActivityTask))
	// We need to send back the local activity result to unblock workflowTaskPoller.processWorkflowTask() which is
	// synchronously listening on the laResultCh. We also want to make sure we don't block here forever in case
	// processWorkflowTask() already returns and nobody is receiving from laResultCh. We guarantee that doneCh is closed
	// before returning from workflowTaskPoller.processWorkflowTask().
	select {
	case result.task.workflowTask.laResultCh <- result:
		return nil
	case <-result.task.workflowTask.doneCh:
		// processWorkflowTask() already returns, just drop this local activity result.
		return nil
	}
}

func (lath *localActivityTaskHandler) executeLocalActivityTask(task *localActivityTask) (result *localActivityResult) {
	workflowType := task.params.WorkflowInfo.WorkflowType.Name
	activityType := task.params.ActivityType
	metricsHandler := lath.metricsHandler.WithTags(metrics.LocalActivityTags(workflowType, activityType))

	metricsHandler.Counter(metrics.LocalActivityTotalCounter).Inc(1)

	ae := activityExecutor{name: activityType, fn: task.params.ActivityFn}
	traceLog(func() {
		lath.logger.Debug("Processing new local activity task",
			tagWorkflowID, task.params.WorkflowInfo.WorkflowExecution.ID,
			tagRunID, task.params.WorkflowInfo.WorkflowExecution.RunID,
			tagActivityType, activityType,
			tagAttempt, task.attempt,
		)
	})
	ctx, err := WithLocalActivityTask(lath.userContext, task, lath.logger, lath.metricsHandler,
		lath.dataConverter, lath.interceptors)
	if err != nil {
		return &localActivityResult{task: task, err: fmt.Errorf("failed building context: %w", err)}
	}

	// propagate context information into the local activity activity context from the headers
	ctx, err = contextWithHeaderPropagated(ctx, task.header, lath.contextPropagators)
	if err != nil {
		return &localActivityResult{task: task, err: err}
	}

	timeout := task.params.ScheduleToCloseTimeout
	if task.params.StartToCloseTimeout != 0 && task.params.StartToCloseTimeout < timeout {
		timeout = task.params.StartToCloseTimeout
	}
	timeoutDuration := timeout
	deadline := time.Now().Add(timeoutDuration)
	if task.attempt > 1 && !task.expireTime.IsZero() && task.expireTime.Before(deadline) {
		// this is attempt and expire time is before SCHEDULE_TO_CLOSE timeout
		deadline = task.expireTime
	}

	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	task.Lock()
	if task.canceled {
		task.Unlock()
		return &localActivityResult{err: ErrCanceled, task: task}
	}
	task.attemptsThisWFT += 1
	task.cancelFunc = cancel
	task.Unlock()

	var laResult *commonpb.Payloads
	doneCh := make(chan struct{})
	go func(ch chan struct{}) {
		laStartTime := time.Now()
		defer close(ch)

		// panic handler
		defer func() {
			if p := recover(); p != nil {
				topLine := fmt.Sprintf("local activity for %s [panic]:", activityType)
				st := getStackTraceRaw(topLine, 7, 0)
				lath.logger.Error("LocalActivity panic.",
					tagWorkflowID, task.params.WorkflowInfo.WorkflowExecution.ID,
					tagRunID, task.params.WorkflowInfo.WorkflowExecution.RunID,
					tagActivityType, activityType,
					tagAttempt, task.attempt,
					tagPanicError, fmt.Sprintf("%v", p),
					tagPanicStack, st)
				metricsHandler.Counter(metrics.LocalActivityErrorCounter).Inc(1)
				err = newPanicError(p, st)
			}
			if err != nil {
				metricsHandler.Counter(metrics.LocalActivityFailedCounter).Inc(1)
				metricsHandler.Counter(metrics.LocalActivityExecutionFailedCounter).Inc(1)
			}
		}()

		laResult, err = ae.ExecuteWithActualArgs(ctx, task.params.InputArgs)
		executionLatency := time.Since(laStartTime)
		metricsHandler.Timer(metrics.LocalActivityExecutionLatency).Record(executionLatency)
		if executionLatency > timeoutDuration {
			// If local activity takes longer than expected timeout, the context would already be DeadlineExceeded and
			// the result would be discarded. Print a warning in this case.
			lath.logger.Warn("LocalActivity takes too long to complete.",
				"LocalActivityID", task.activityID,
				"LocalActivityType", activityType,
				"ScheduleToCloseTimeout", task.params.ScheduleToCloseTimeout,
				"ActualExecutionDuration", executionLatency)
		}
	}(doneCh)

WaitResult:
	select {
	case <-ctx.Done():
		select {
		case <-doneCh:
			// double check if result is ready.
			break WaitResult
		default:
		}

		// context is done
		if ctx.Err() == context.Canceled {
			metricsHandler.Counter(metrics.LocalActivityCanceledCounter).Inc(1)
			metricsHandler.Counter(metrics.LocalActivityExecutionCanceledCounter).Inc(1)
			return &localActivityResult{err: ErrCanceled, task: task}
		} else if ctx.Err() == context.DeadlineExceeded {
			return &localActivityResult{err: ErrDeadlineExceeded, task: task}
		} else {
			// should not happen
			return &localActivityResult{err: NewApplicationError("unexpected context done", "", true, nil), task: task}
		}
	case <-doneCh:
		// local activity completed
	}

	if err == nil {
		metricsHandler.
			Timer(metrics.LocalActivitySucceedEndToEndLatency).
			Record(time.Since(task.params.ScheduledTime))
	}
	return &localActivityResult{result: laResult, err: err, task: task}
}

func (wtp *workflowTaskPoller) release(kind enumspb.TaskQueueKind) {
	if wtp.stickyCacheSize <= 0 {
		return
	}

	wtp.requestLock.Lock()
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		wtp.pendingStickyPollCount--
	} else {
		wtp.pendingRegularPollCount--
	}
	wtp.requestLock.Unlock()
}

func (wtp *workflowTaskPoller) updateBacklog(taskQueueKind enumspb.TaskQueueKind, backlogCountHint int64) {
	if taskQueueKind == enumspb.TASK_QUEUE_KIND_NORMAL || wtp.stickyCacheSize <= 0 {
		// we only care about sticky backlog for now.
		return
	}
	wtp.requestLock.Lock()
	wtp.stickyBacklog = backlogCountHint
	wtp.requestLock.Unlock()
}

// getNextPollRequest returns appropriate next poll request based on poller configuration.
// Simple rules:
//  1. if sticky execution is disabled, always poll for regular task queue
//  2. otherwise:
//     2.1) if sticky task queue has backlog, always prefer to process sticky task first
//     2.2) poll from the task queue that has less pending requests (prefer sticky when they are the same).
//
// TODO: make this more smart to auto adjust based on poll latency
func (wtp *workflowTaskPoller) getNextPollRequest() (request *workflowservice.PollWorkflowTaskQueueRequest) {
	taskQueueName := wtp.taskQueueName
	taskQueueKind := enumspb.TASK_QUEUE_KIND_NORMAL
	if wtp.stickyCacheSize > 0 {
		wtp.requestLock.Lock()
		if wtp.stickyBacklog > 0 || wtp.pendingStickyPollCount <= wtp.pendingRegularPollCount {
			wtp.pendingStickyPollCount++
			taskQueueName = getWorkerTaskQueue(wtp.stickyUUID)
			taskQueueKind = enumspb.TASK_QUEUE_KIND_STICKY
		} else {
			wtp.pendingRegularPollCount++
		}
		wtp.requestLock.Unlock()
	}

	taskQueue := &taskqueuepb.TaskQueue{
		Name: taskQueueName,
		Kind: taskQueueKind,
	}
	return &workflowservice.PollWorkflowTaskQueueRequest{
		Namespace:      wtp.namespace,
		TaskQueue:      taskQueue,
		Identity:       wtp.identity,
		BinaryChecksum: getBinaryChecksum(),
	}
}

// Poll the workflow task queue and update the num_poller metric
func (wtp *workflowTaskPoller) pollWorkflowTaskQueue(ctx context.Context, request *workflowservice.PollWorkflowTaskQueueRequest) (*workflowservice.PollWorkflowTaskQueueResponse, error) {
	if request.TaskQueue.GetKind() == enumspb.TASK_QUEUE_KIND_NORMAL {
		wtp.numNormalPollerMetric.increment()
		defer wtp.numNormalPollerMetric.decrement()
	} else {
		wtp.numStickyPollerMetric.increment()
		defer wtp.numStickyPollerMetric.decrement()
	}

	return wtp.service.PollWorkflowTaskQueue(ctx, request)
}

// Poll for a single workflow task from the service
func (wtp *workflowTaskPoller) poll(ctx context.Context) (interface{}, error) {
	traceLog(func() {
		wtp.logger.Debug("workflowTaskPoller::Poll")
	})

	request := wtp.getNextPollRequest()
	defer wtp.release(request.TaskQueue.GetKind())

	response, err := wtp.pollWorkflowTaskQueue(ctx, request)
	if err != nil {
		wtp.updateBacklog(request.TaskQueue.GetKind(), 0)
		return nil, err
	}

	if response == nil || len(response.TaskToken) == 0 {
		// Emit using base scope as no workflow type information is available in the case of empty poll
		wtp.metricsHandler.Counter(metrics.WorkflowTaskQueuePollEmptyCounter).Inc(1)
		wtp.updateBacklog(request.TaskQueue.GetKind(), 0)
		return &workflowTask{}, nil
	}

	wtp.updateBacklog(request.TaskQueue.GetKind(), response.GetBacklogCountHint())

	task := wtp.toWorkflowTask(response)
	traceLog(func() {
		var firstEventID int64 = -1
		if response.History != nil && len(response.History.Events) > 0 {
			firstEventID = response.History.Events[0].GetEventId()
		}
		wtp.logger.Debug("workflowTaskPoller::Poll Succeed",
			"StartedEventID", response.GetStartedEventId(),
			"Attempt", response.GetAttempt(),
			"FirstEventID", firstEventID,
			"IsQueryTask", response.Query != nil)
	})

	metricsHandler := wtp.metricsHandler.WithTags(metrics.WorkflowTags(response.WorkflowType.GetName()))
	metricsHandler.Counter(metrics.WorkflowTaskQueuePollSucceedCounter).Inc(1)

	scheduleToStartLatency := common.TimeValue(response.GetStartedTime()).Sub(common.TimeValue(response.GetScheduledTime()))
	metricsHandler.Timer(metrics.WorkflowTaskScheduleToStartLatency).Record(scheduleToStartLatency)
	return task, nil
}

func (wtp *workflowTaskPoller) toWorkflowTask(response *workflowservice.PollWorkflowTaskQueueResponse) *workflowTask {
	historyIterator := &historyIteratorImpl{
		execution:      response.WorkflowExecution,
		nextPageToken:  response.NextPageToken,
		namespace:      wtp.namespace,
		service:        wtp.service,
		maxEventID:     response.GetStartedEventId(),
		metricsHandler: wtp.metricsHandler,
		taskQueue:      wtp.taskQueueName,
	}
	task := &workflowTask{
		task:            response,
		historyIterator: historyIterator,
	}
	return task
}

func (h *historyIteratorImpl) GetNextPage() (*historypb.History, error) {
	if h.iteratorFunc == nil {
		h.iteratorFunc = newGetHistoryPageFunc(
			context.Background(),
			h.service,
			h.namespace,
			h.execution,
			h.maxEventID,
			h.metricsHandler,
			h.taskQueue,
		)
	}

	history, token, err := h.iteratorFunc(h.nextPageToken)
	if err != nil {
		return nil, err
	}
	h.nextPageToken = token
	return history, nil
}

func (h *historyIteratorImpl) Reset() {
	h.nextPageToken = nil
}

func (h *historyIteratorImpl) HasNextPage() bool {
	return h.nextPageToken != nil
}

func newGetHistoryPageFunc(
	ctx context.Context,
	service workflowservice.WorkflowServiceClient,
	namespace string,
	execution *commonpb.WorkflowExecution,
	atWorkflowTaskCompletedEventID int64,
	metricsHandler metrics.Handler,
	taskQueue string,
) func(nextPageToken []byte) (*historypb.History, []byte, error) {
	return func(nextPageToken []byte) (*historypb.History, []byte, error) {
		var resp *workflowservice.GetWorkflowExecutionHistoryResponse
		grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(
			metricsHandler.WithTags(metrics.RPCTags(metrics.NoneTagValue, metrics.NoneTagValue, taskQueue))),
			defaultGrpcRetryParameters(ctx))
		defer cancel()

		resp, err := service.GetWorkflowExecutionHistory(grpcCtx, &workflowservice.GetWorkflowExecutionHistoryRequest{
			Namespace:     namespace,
			Execution:     execution,
			NextPageToken: nextPageToken,
		})
		if err != nil {
			return nil, nil, err
		}

		var h *historypb.History

		if resp.RawHistory != nil {
			h, err = serializer.DeserializeBlobDataToHistoryEvents(resp.RawHistory, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			if err != nil {
				return nil, nil, nil
			}
		} else {
			h = resp.History
		}

		size := len(h.Events)
		if size > 0 && atWorkflowTaskCompletedEventID > 0 &&
			h.Events[size-1].GetEventId() > atWorkflowTaskCompletedEventID {
			first := h.Events[0].GetEventId() // eventIds start from 1
			h.Events = h.Events[:atWorkflowTaskCompletedEventID-first+1]
			if h.Events[len(h.Events)-1].GetEventType() != enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
				return nil, nil, fmt.Errorf("newGetHistoryPageFunc: atWorkflowTaskCompletedEventID(%v) "+
					"points to event that is not WorkflowTaskCompleted", atWorkflowTaskCompletedEventID)
			}
			return h, nil, nil
		}
		return h, resp.NextPageToken, nil
	}
}

func newActivityTaskPoller(taskHandler ActivityTaskHandler, service workflowservice.WorkflowServiceClient, params workerExecutionParameters) *activityTaskPoller {
	return &activityTaskPoller{
		basePoller:          basePoller{metricsHandler: params.MetricsHandler, stopC: params.WorkerStopChannel},
		taskHandler:         taskHandler,
		service:             service,
		namespace:           params.Namespace,
		taskQueueName:       params.TaskQueue,
		identity:            params.Identity,
		logger:              params.Logger,
		activitiesPerSecond: params.TaskQueueActivitiesPerSecond,
		numPollerMetric:     newNumPollerMetric(params.MetricsHandler, metrics.PollerTypeActivityTask),
	}
}

// Poll the activity task queue and update the num_poller metric
func (atp *activityTaskPoller) pollActivityTaskQueue(ctx context.Context, request *workflowservice.PollActivityTaskQueueRequest) (*workflowservice.PollActivityTaskQueueResponse, error) {
	atp.numPollerMetric.increment()
	defer atp.numPollerMetric.decrement()

	return atp.service.PollActivityTaskQueue(ctx, request)
}

// Poll for a single activity task from the service
func (atp *activityTaskPoller) poll(ctx context.Context) (interface{}, error) {
	traceLog(func() {
		atp.logger.Debug("activityTaskPoller::Poll")
	})
	request := &workflowservice.PollActivityTaskQueueRequest{
		Namespace:         atp.namespace,
		TaskQueue:         &taskqueuepb.TaskQueue{Name: atp.taskQueueName, Kind: enumspb.TASK_QUEUE_KIND_NORMAL},
		Identity:          atp.identity,
		TaskQueueMetadata: &taskqueuepb.TaskQueueMetadata{MaxTasksPerSecond: &types.DoubleValue{Value: atp.activitiesPerSecond}},
	}

	response, err := atp.pollActivityTaskQueue(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.TaskToken) == 0 {
		// No activity info is available on empty poll.  Emit using base scope.
		atp.metricsHandler.Counter(metrics.ActivityPollNoTaskCounter).Inc(1)
		return &activityTask{}, nil
	}

	workflowType := response.WorkflowType.GetName()
	activityType := response.ActivityType.GetName()
	metricsHandler := atp.metricsHandler.WithTags(metrics.ActivityTags(workflowType, activityType, atp.taskQueueName))

	scheduleToStartLatency := common.TimeValue(response.GetStartedTime()).Sub(common.TimeValue(response.GetCurrentAttemptScheduledTime()))
	metricsHandler.Timer(metrics.ActivityScheduleToStartLatency).Record(scheduleToStartLatency)

	return &activityTask{task: response}, nil
}

// PollTask polls a new task
func (atp *activityTaskPoller) PollTask() (interface{}, error) {
	// Get the task.
	activityTask, err := atp.doPoll(atp.poll)
	if err != nil {
		return nil, err
	}
	return activityTask, nil
}

// ProcessTask processes a new task
func (atp *activityTaskPoller) ProcessTask(task interface{}) error {
	if atp.stopping() {
		return errStop
	}

	activityTask := task.(*activityTask)
	if activityTask.task == nil {
		// We didn't have task, poll might have timeout.
		traceLog(func() {
			atp.logger.Debug("Activity task unavailable")
		})
		return nil
	}

	workflowType := activityTask.task.WorkflowType.GetName()
	activityType := activityTask.task.ActivityType.GetName()
	activityMetricsHandler := atp.metricsHandler.WithTags(metrics.ActivityTags(workflowType, activityType, atp.taskQueueName))

	executionStartTime := time.Now()
	// Process the activity task.
	request, err := atp.taskHandler.Execute(atp.taskQueueName, activityTask.task)
	// err is returned in case of internal failure, such as unable to propagate context or context timeout.
	if err != nil {
		activityMetricsHandler.Counter(metrics.ActivityExecutionFailedCounter).Inc(1)
		return err
	}
	// in case if activity execution failed, request should be of type RespondActivityTaskFailedRequest
	if _, ok := request.(*workflowservice.RespondActivityTaskFailedRequest); ok {
		activityMetricsHandler.Counter(metrics.ActivityExecutionFailedCounter).Inc(1)
	}
	activityMetricsHandler.Timer(metrics.ActivityExecutionLatency).Record(time.Since(executionStartTime))

	if request == ErrActivityResultPending {
		return nil
	}

	rpcMetricsHandler := atp.metricsHandler.WithTags(metrics.RPCTags(workflowType, activityType, metrics.NoneTagValue))
	reportErr := reportActivityComplete(context.Background(), atp.service, request, rpcMetricsHandler)
	if reportErr != nil {
		traceLog(func() {
			atp.logger.Debug("reportActivityComplete failed", tagError, reportErr)
		})
		return reportErr
	}

	activityMetricsHandler.
		Timer(metrics.ActivitySucceedEndToEndLatency).
		Record(time.Since(common.TimeValue(activityTask.task.GetScheduledTime())))
	return nil
}

func reportActivityComplete(
	ctx context.Context,
	service workflowservice.WorkflowServiceClient,
	request interface{},
	rpcMetricsHandler metrics.Handler,
) error {
	if request == nil {
		// nothing to report
		return nil
	}

	var reportErr error
	switch request := request.(type) {
	case *workflowservice.RespondActivityTaskCanceledRequest:
		grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(rpcMetricsHandler),
			defaultGrpcRetryParameters(ctx))
		defer cancel()
		_, err := service.RespondActivityTaskCanceled(grpcCtx, request)
		reportErr = err
	case *workflowservice.RespondActivityTaskFailedRequest:
		grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(rpcMetricsHandler), defaultGrpcRetryParameters(ctx))
		defer cancel()
		_, err := service.RespondActivityTaskFailed(grpcCtx, request)
		reportErr = err
	case *workflowservice.RespondActivityTaskCompletedRequest:
		grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(rpcMetricsHandler),
			defaultGrpcRetryParameters(ctx))
		defer cancel()
		_, err := service.RespondActivityTaskCompleted(grpcCtx, request)
		reportErr = err
	}
	return reportErr
}

func reportActivityCompleteByID(
	ctx context.Context,
	service workflowservice.WorkflowServiceClient,
	request interface{},
	rpcMetricsHandler metrics.Handler,
) error {
	if request == nil {
		// nothing to report
		return nil
	}

	var reportErr error
	switch request := request.(type) {
	case *workflowservice.RespondActivityTaskCanceledByIdRequest:
		grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(rpcMetricsHandler),
			defaultGrpcRetryParameters(ctx))
		defer cancel()
		_, err := service.RespondActivityTaskCanceledById(grpcCtx, request)
		reportErr = err
	case *workflowservice.RespondActivityTaskFailedByIdRequest:
		grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(rpcMetricsHandler),
			defaultGrpcRetryParameters(ctx))
		defer cancel()
		_, err := service.RespondActivityTaskFailedById(grpcCtx, request)
		reportErr = err
	case *workflowservice.RespondActivityTaskCompletedByIdRequest:
		grpcCtx, cancel := newGRPCContext(ctx, grpcMetricsHandler(rpcMetricsHandler),
			defaultGrpcRetryParameters(ctx))
		defer cancel()
		_, err := service.RespondActivityTaskCompletedById(grpcCtx, request)
		reportErr = err
	}
	return reportErr
}

func convertActivityResultToRespondRequest(
	identity string,
	taskToken []byte,
	result *commonpb.Payloads,
	err error,
	dataConverter converter.DataConverter,
	failureConverter converter.FailureConverter,
	namespace string,
	cancelAllowed bool,
) interface{} {
	if err == ErrActivityResultPending {
		// activity result is pending and will be completed asynchronously.
		// nothing to report at this point
		return ErrActivityResultPending
	}

	if err == nil {
		return &workflowservice.RespondActivityTaskCompletedRequest{
			TaskToken: taskToken,
			Result:    result,
			Identity:  identity,
			Namespace: namespace,
		}
	}

	// Only respond with canceled if allowed
	if cancelAllowed {
		var canceledErr *CanceledError
		if errors.As(err, &canceledErr) {
			return &workflowservice.RespondActivityTaskCanceledRequest{
				TaskToken: taskToken,
				Details:   convertErrDetailsToPayloads(canceledErr.details, dataConverter),
				Identity:  identity,
				Namespace: namespace,
			}
		}
		if errors.Is(err, context.Canceled) {
			return &workflowservice.RespondActivityTaskCanceledRequest{
				TaskToken: taskToken,
				Identity:  identity,
				Namespace: namespace,
			}
		}
	}

	// If a canceled error is returned but it wasn't allowed, we have to wrap in
	// an unexpected-cancel application error
	if _, isCanceledErr := err.(*CanceledError); isCanceledErr {
		err = fmt.Errorf("unexpected activity cancel error: %w", err)
	}

	return &workflowservice.RespondActivityTaskFailedRequest{
		TaskToken: taskToken,
		Failure:   failureConverter.ErrorToFailure(err),
		Identity:  identity,
		Namespace: namespace,
	}
}

func convertActivityResultToRespondRequestByID(
	identity string,
	namespace string,
	workflowID string,
	runID string,
	activityID string,
	result *commonpb.Payloads,
	err error,
	dataConverter converter.DataConverter,
	failureConverter converter.FailureConverter,
	cancelAllowed bool,
) interface{} {
	if err == ErrActivityResultPending {
		// activity result is pending and will be completed asynchronously.
		// nothing to report at this point
		return nil
	}

	if err == nil {
		return &workflowservice.RespondActivityTaskCompletedByIdRequest{
			Namespace:  namespace,
			WorkflowId: workflowID,
			RunId:      runID,
			ActivityId: activityID,
			Result:     result,
			Identity:   identity,
		}
	}

	// Only respond with canceled if allowed
	if cancelAllowed {
		var canceledErr *CanceledError
		if errors.As(err, &canceledErr) {
			return &workflowservice.RespondActivityTaskCanceledByIdRequest{
				Namespace:  namespace,
				WorkflowId: workflowID,
				RunId:      runID,
				ActivityId: activityID,
				Details:    convertErrDetailsToPayloads(canceledErr.details, dataConverter),
				Identity:   identity,
			}
		}
		if errors.Is(err, context.Canceled) {
			return &workflowservice.RespondActivityTaskCanceledByIdRequest{
				Namespace:  namespace,
				WorkflowId: workflowID,
				RunId:      runID,
				ActivityId: activityID,
				Identity:   identity,
			}
		}
	}

	// If a canceled error is returned but it wasn't allowed, we have to wrap in
	// an unexpected-cancel application error
	if _, isCanceledErr := err.(*CanceledError); isCanceledErr {
		err = fmt.Errorf("unexpected activity cancel error: %w", err)
	}

	return &workflowservice.RespondActivityTaskFailedByIdRequest{
		Namespace:  namespace,
		WorkflowId: workflowID,
		RunId:      runID,
		ActivityId: activityID,
		Failure:    failureConverter.ErrorToFailure(err),
		Identity:   identity,
	}
}
