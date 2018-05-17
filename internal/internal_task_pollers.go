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

// All code in this file is private to the package.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/uber-go/tally"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/cadence/internal/common/backoff"
	"go.uber.org/cadence/internal/common/metrics"
	"go.uber.org/zap"
)

const (
	pollTaskServiceTimeOut = 3 * time.Minute // Server long poll is 1 * Minutes + delta

	retryServiceOperationInitialInterval    = 200 * time.Millisecond
	retryServiceOperationMaxInterval        = 4 * time.Second
	retryServiceOperationExpirationInterval = 60 * time.Second

	stickyDecisionScheduleToStartTimeoutSeconds = 5
)

var (
	serviceOperationRetryPolicy = createServiceRetryPolicy()
)

type (
	// taskPoller interface to poll and process for task
	taskPoller interface {
		// PollTask polls for one new task
		PollTask() (interface{}, error)
		// ProcessTask processes a task
		ProcessTask(interface{}) error
	}

	// workflowTaskPoller implements polling/processing a workflow task
	workflowTaskPoller struct {
		domain       string
		taskListName string
		identity     string
		service      workflowserviceclient.Interface
		taskHandler  WorkflowTaskHandler
		metricsScope tally.Scope
		logger       *zap.Logger

		disableStickyExecution       bool
		StickyScheduleToStartTimeout time.Duration

		pendingRegularPollCount int
		pendingStickyPollCount  int
		stickyBacklog           int64
		requestLock             sync.Mutex
	}

	// activityTaskPoller implements polling/processing a workflow task
	activityTaskPoller struct {
		domain              string
		taskListName        string
		identity            string
		service             workflowserviceclient.Interface
		taskHandler         ActivityTaskHandler
		metricsScope        tally.Scope
		logger              *zap.Logger
		activitiesPerSecond float64
	}

	historyIteratorImpl struct {
		iteratorFunc  func(nextPageToken []byte) (*s.History, []byte, error)
		execution     *s.WorkflowExecution
		nextPageToken []byte
		domain        string
		service       workflowserviceclient.Interface
		metricsScope  tally.Scope
		maxEventID    int64
	}

	localActivityTaskPoller struct {
		handler      *localActivityTaskHandler
		metricsScope tally.Scope
		logger       *zap.Logger
		laTunnel     *localActivityTunnel
	}

	localActivityTaskHandler struct {
		userContext  context.Context
		metricsScope tally.Scope
		logger       *zap.Logger
	}

	localActivityResult struct {
		result []byte
		err    error
		task   *localActivityTask
	}

	localActivityTunnel struct {
		taskCh   chan *localActivityTask
		resultCh chan interface{}
	}
)

func (lat *localActivityTunnel) getTask() *localActivityTask {
	return <-lat.taskCh
}

func (lat *localActivityTunnel) sendTask(task *localActivityTask) {
	lat.taskCh <- task
}

func (lat *localActivityTunnel) deliverResult(result *localActivityResult) {
	lat.resultCh <- result
}

func createServiceRetryPolicy() backoff.RetryPolicy {
	policy := backoff.NewExponentialRetryPolicy(retryServiceOperationInitialInterval)
	policy.SetMaximumInterval(retryServiceOperationMaxInterval)
	policy.SetExpirationInterval(retryServiceOperationExpirationInterval)
	return policy
}

func isServiceTransientError(err error) bool {
	// Retrying by default so it covers all transport errors.
	switch err.(type) {
	case *s.BadRequestError,
		*s.EntityNotExistsError,
		*s.WorkflowExecutionAlreadyStartedError,
		*s.DomainAlreadyExistsError,
		*s.QueryFailedError,
		*s.CancellationAlreadyRequestedError:
		return false
	}

	// s.InternalServiceError
	// s.ServiceBusyError
	return true
}

func isClientSideError(err error) bool {
	// If an activity execution exceeds deadline.
	if err == context.DeadlineExceeded {
		return true
	}

	return false
}

func newWorkflowTaskPoller(taskHandler WorkflowTaskHandler, service workflowserviceclient.Interface,
	domain string, params workerExecutionParameters) *workflowTaskPoller {
	return &workflowTaskPoller{
		service:      metrics.NewWorkflowServiceWrapper(service, params.MetricsScope),
		domain:       domain,
		taskListName: params.TaskList,
		identity:     params.Identity,
		taskHandler:  taskHandler,
		metricsScope: params.MetricsScope,
		logger:       params.Logger,

		disableStickyExecution:       params.DisableStickyExecution,
		StickyScheduleToStartTimeout: params.StickyScheduleToStartTimeout,
	}
}

// PollTask polls a new task
func (wtp *workflowTaskPoller) PollTask() (interface{}, error) {
	// Get the task.
	workflowTask, err := wtp.poll()
	if err != nil {
		return nil, err
	}

	return workflowTask, nil
}

// ProcessTask processes a task which could be workflow task or local activity result
func (wtp *workflowTaskPoller) ProcessTask(task interface{}) error {
	if lar, ok := task.(*localActivityResult); ok {
		return wtp.processLocalActivityResult(lar)
	}

	return wtp.processWorkflowTask(task.(*workflowTask))
}

func (wtp *workflowTaskPoller) processWorkflowTask(workflowTask *workflowTask) error {
	if workflowTask.task == nil {
		// We didn't have task, poll might have time out.
		traceLog(func() {
			wtp.logger.Debug("Workflow task unavailable")
		})
		return nil
	}

	executionStartTime := time.Now()
	// Process the task.
	completedRequest, _, err := wtp.taskHandler.ProcessWorkflowTask(workflowTask.task, workflowTask.historyIterator, false)
	if err != nil {
		wtp.metricsScope.Counter(metrics.DecisionExecutionFailedCounter).Inc(1)
		return err
	}
	if completedRequest == nil {
		return nil
	}
	wtp.metricsScope.Timer(metrics.DecisionExecutionLatency).Record(time.Now().Sub(executionStartTime))

	responseStartTime := time.Now()
	if err = wtp.RespondTaskCompleted(completedRequest, workflowTask.task); err != nil {
		return err
	}
	wtp.metricsScope.Timer(metrics.DecisionResponseLatency).Record(time.Now().Sub(responseStartTime))
	wtp.metricsScope.Counter(metrics.DecisionTaskCompletedCounter).Inc(1)

	return nil
}

func (wtp *workflowTaskPoller) processLocalActivityResult(lar *localActivityResult) error {
	workflowContext := lar.task.wc
	decisionStartTime := workflowContext.decisionStartTime
	decisionTask := workflowContext.currentDecisionTask
	completedRequest, err := wtp.taskHandler.(*workflowTaskHandlerImpl).ProcessLocalActivityResult(lar)
	if err != nil {
		return err
	}
	if completedRequest == nil {
		return nil
	}

	wtp.metricsScope.Timer(metrics.DecisionExecutionLatency).Record(time.Now().Sub(decisionStartTime))

	responseStartTime := time.Now()
	if err = wtp.RespondTaskCompleted(completedRequest, decisionTask); err != nil {
		return err
	}
	wtp.metricsScope.Timer(metrics.DecisionResponseLatency).Record(time.Now().Sub(responseStartTime))
	wtp.metricsScope.Counter(metrics.DecisionTaskCompletedCounter).Inc(1)
	return nil
}

func (wtp *workflowTaskPoller) RespondTaskCompleted(completedRequest interface{}, task *s.PollForDecisionTaskResponse) (err error) {
	ctx := context.Background()
	// Respond task completion.
	err = backoff.Retry(ctx,
		func() error {
			tchCtx, cancel, opt := newChannelContext(ctx)
			defer cancel()
			var err1 error
			switch request := completedRequest.(type) {
			case *s.RespondDecisionTaskFailedRequest:
				// Only fail decision on first attempt, subsequent failure on the same decision task will timeout.
				// This is to avoid spin on the failed decision task. Checking Attempt not nil for older server.
				if task.Attempt != nil && task.GetAttempt() == 0 {
					err1 = wtp.service.RespondDecisionTaskFailed(tchCtx, request, opt...)
					if err1 != nil {
						traceLog(func() {
							wtp.logger.Debug("RespondDecisionTaskFailed failed.", zap.Error(err1))
						})
					}
				}
			case *s.RespondDecisionTaskCompletedRequest:
				if request.StickyAttributes == nil && !wtp.disableStickyExecution {
					request.StickyAttributes = &s.StickyExecutionAttributes{
						WorkerTaskList:                &s.TaskList{Name: common.StringPtr(getWorkerTaskList())},
						ScheduleToStartTimeoutSeconds: common.Int32Ptr(common.Int32Ceil(wtp.StickyScheduleToStartTimeout.Seconds())),
					}
				}
				err1 = wtp.service.RespondDecisionTaskCompleted(tchCtx, request, opt...)
				if err1 != nil {
					traceLog(func() {
						wtp.logger.Debug("RespondDecisionTaskCompleted failed.", zap.Error(err1))
					})
				}
			case *s.RespondQueryTaskCompletedRequest:
				err1 = wtp.service.RespondQueryTaskCompleted(tchCtx, request)
				if err1 != nil {
					traceLog(func() {
						wtp.logger.Debug("RespondQueryTaskCompleted failed.", zap.Error(err1))
					})
				}
			default:
				// should not happen
				panic("unknown request type from ProcessWorkflowTask()")
			}

			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		wtp.metricsScope.Counter(metrics.DecisionResponseFailedCounter).Inc(1)
		return err
	}

	return nil
}

func newLocalActivityPoller(params workerExecutionParameters, laTunnel *localActivityTunnel) *localActivityTaskPoller {
	handler := &localActivityTaskHandler{
		userContext:  params.UserContext,
		metricsScope: params.MetricsScope,
		logger:       params.Logger,
	}
	return &localActivityTaskPoller{
		handler:      handler,
		metricsScope: params.MetricsScope,
		logger:       params.Logger,
		laTunnel:     laTunnel,
	}
}

func (latp *localActivityTaskPoller) PollTask() (interface{}, error) {
	return latp.laTunnel.getTask(), nil
}

func (latp *localActivityTaskPoller) ProcessTask(task interface{}) error {
	result := latp.handler.executeLocalActivityTask(task.(*localActivityTask))
	latp.laTunnel.deliverResult(result)
	return nil
}

func (lath *localActivityTaskHandler) executeLocalActivityTask(task *localActivityTask) (result *localActivityResult) {
	lath.metricsScope.Counter(metrics.LocalActivityTotalCounter).Inc(1)
	activityType := getFunctionName(task.params.ActivityFn)
	ae := activityExecutor{name: activityType, fn: task.params.ActivityFn}

	rootCtx := lath.userContext
	if rootCtx == nil {
		rootCtx = context.Background()
	}

	ctx := context.WithValue(rootCtx, activityEnvContextKey, &activityEnvironment{
		activityType:      ActivityType{Name: activityType},
		activityID:        fmt.Sprintf("%v", task.activityID),
		workflowExecution: task.params.WorkflowInfo.WorkflowExecution,
		logger:            lath.logger,
		metricsScope:      lath.metricsScope,
		isLocalActivity:   true,
	})

	// panic handler
	defer func() {
		if p := recover(); p != nil {
			topLine := fmt.Sprintf("local activity for %s [panic]:", activityType)
			st := getStackTraceRaw(topLine, 7, 0)
			lath.logger.Error("LocalActivity panic.",
				zap.String("PanicError", fmt.Sprintf("%v", p)),
				zap.String("PanicStack", st))
			lath.metricsScope.Counter(metrics.LocalActivityPanicCounter).Inc(1)
			panicErr := newPanicError(p, st)
			result = &localActivityResult{
				task:   task,
				result: nil,
				err:    panicErr,
			}
		}
		if result.err != nil {
			lath.metricsScope.Counter(metrics.LocalActivityFailedCounter).Inc(1)
		}
	}()

	timeoutDuration := time.Duration(task.params.ScheduleToCloseTimeoutSeconds) * time.Second
	deadline := time.Now().Add(timeoutDuration)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	task.Lock()
	if task.canceled {
		task.Unlock()
		return &localActivityResult{err: ErrCanceled, task: task}
	}
	task.cancelFunc = cancel
	task.Unlock()

	var laResult []byte
	var err error
	doneCh := make(chan struct{})
	go func(ch chan struct{}) {
		laStartTime := time.Now()
		laResult, err = ae.ExecuteWithActualArgs(ctx, task.params.InputArgs)
		executionLatency := time.Now().Sub(laStartTime)
		close(ch)
		lath.metricsScope.Timer(metrics.LocalActivityExecutionLatency).Record(executionLatency)
		if executionLatency > timeoutDuration {
			// If local activity takes longer than expected timeout, the context would already be DeadlineExceeded and
			// the result would be discarded. Print a warning in this case.
			lath.logger.Warn("LocalActivity takes too long to complete.",
				zap.String("LocalActivityID", task.activityID),
				zap.String("LocalActivityType", activityType),
				zap.Int32("ScheduleToCloseTimeoutSeconds", task.params.ScheduleToCloseTimeoutSeconds),
				zap.Duration("ActualExecutionDuration", executionLatency))
		}
	}(doneCh)

Wait_Result:
	select {
	case <-ctx.Done():
		select {
		case <-doneCh:
			// double check if result is ready.
			break Wait_Result
		default:
		}

		// context is done
		if ctx.Err() == context.Canceled {
			lath.metricsScope.Counter(metrics.LocalActivityCanceledCounter).Inc(1)
			return &localActivityResult{err: ErrCanceled, task: task}
		} else if ctx.Err() == context.DeadlineExceeded {
			lath.metricsScope.Counter(metrics.LocalActivityTimeoutCounter).Inc(1)
			return &localActivityResult{err: ErrDeadlineExceeded, task: task}
		} else {
			// should not happen
			return &localActivityResult{err: NewCustomError("unexpected context done"), task: task}
		}
	case <-doneCh:
		// local activity completed
	}

	return &localActivityResult{result: laResult, err: err, task: task}
}

func (wtp *workflowTaskPoller) release(kind s.TaskListKind) {
	if wtp.disableStickyExecution {
		return
	}

	wtp.requestLock.Lock()
	if kind == s.TaskListKindSticky {
		wtp.pendingStickyPollCount--
	} else {
		wtp.pendingRegularPollCount--
	}
	wtp.requestLock.Unlock()
}

func (wtp *workflowTaskPoller) updateBacklog(taskListKind s.TaskListKind, backlogCountHint int64) {
	if taskListKind == s.TaskListKindNormal || wtp.disableStickyExecution {
		// we only care about sticky backlog for now.
		return
	}
	wtp.requestLock.Lock()
	wtp.stickyBacklog = backlogCountHint
	wtp.requestLock.Unlock()
}

// getNextPollRequest returns appropriate next poll request based on poller configuration.
// Simple rules:
// 1) if sticky execution is disabled, always poll for regular task list
// 2) otherwise:
//   2.1) if sticky task list has backlog, always prefer to process sticky task first
//   2.2) poll from the task list that has less pending requests (prefer sticky when they are the same).
// TODO: make this more smart to auto adjust based on poll latency
func (wtp *workflowTaskPoller) getNextPollRequest() (request *s.PollForDecisionTaskRequest) {
	taskListName := wtp.taskListName
	taskListKind := s.TaskListKindNormal
	if !wtp.disableStickyExecution {
		wtp.requestLock.Lock()
		if wtp.stickyBacklog > 0 || wtp.pendingStickyPollCount <= wtp.pendingRegularPollCount {
			wtp.pendingStickyPollCount++
			taskListName = getWorkerTaskList()
			taskListKind = s.TaskListKindSticky
		} else {
			wtp.pendingRegularPollCount++
		}
		wtp.requestLock.Unlock()
	}

	taskList := s.TaskList{
		Name: common.StringPtr(taskListName),
		Kind: common.TaskListKindPtr(taskListKind),
	}
	return &s.PollForDecisionTaskRequest{
		Domain:   common.StringPtr(wtp.domain),
		TaskList: common.TaskListPtr(taskList),
		Identity: common.StringPtr(wtp.identity),
	}
}

// Poll for a single workflow task from the service
func (wtp *workflowTaskPoller) poll() (*workflowTask, error) {
	startTime := time.Now()
	wtp.metricsScope.Counter(metrics.DecisionPollCounter).Inc(1)

	traceLog(func() {
		wtp.logger.Debug("workflowTaskPoller::Poll")
	})

	tchCtx, cancel, opt := newChannelContext(context.Background(), chanTimeout(pollTaskServiceTimeOut))
	defer cancel()

	request := wtp.getNextPollRequest()
	defer wtp.release(request.TaskList.GetKind())

	response, err := wtp.service.PollForDecisionTask(tchCtx, request, opt...)
	if err != nil {
		if isServiceTransientError(err) {
			wtp.metricsScope.Counter(metrics.DecisionPollTransientFailedCounter).Inc(1)
		} else {
			wtp.metricsScope.Counter(metrics.DecisionPollFailedCounter).Inc(1)
		}
		return nil, err
	}

	if response == nil || len(response.TaskToken) == 0 {
		wtp.metricsScope.Counter(metrics.DecisionPollNoTaskCounter).Inc(1)
		wtp.updateBacklog(request.TaskList.GetKind(), 0)
		return &workflowTask{}, nil
	}

	wtp.updateBacklog(request.TaskList.GetKind(), response.GetBacklogCountHint())

	historyIterator := &historyIteratorImpl{
		nextPageToken: response.NextPageToken,
		execution:     response.WorkflowExecution,
		domain:        wtp.domain,
		service:       wtp.service,
		metricsScope:  wtp.metricsScope,
		maxEventID:    response.GetStartedEventId(),
	}
	task := &workflowTask{
		task:            response,
		historyIterator: historyIterator,
		pollStartTime:   startTime,
	}
	wtp.metricsScope.Counter(metrics.DecisionPollSucceedCounter).Inc(1)
	wtp.metricsScope.Timer(metrics.DecisionPollLatency).Record(time.Now().Sub(startTime))
	return task, nil
}

func (h *historyIteratorImpl) GetNextPage() (*s.History, error) {
	if h.iteratorFunc == nil {
		h.iteratorFunc = newGetHistoryPageFunc(
			context.Background(),
			h.service,
			h.domain,
			h.execution,
			h.maxEventID,
			h.metricsScope)
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
	service workflowserviceclient.Interface,
	domain string,
	execution *s.WorkflowExecution,
	atDecisionTaskCompletedEventID int64,
	metricsScope tally.Scope,
) func(nextPageToken []byte) (*s.History, []byte, error) {
	return func(nextPageToken []byte) (*s.History, []byte, error) {
		metricsScope.Counter(metrics.WorkflowGetHistoryCounter).Inc(1)
		startTime := time.Now()
		var resp *s.GetWorkflowExecutionHistoryResponse
		err := backoff.Retry(ctx,
			func() error {
				tchCtx, cancel, opt := newChannelContext(ctx)
				defer cancel()

				var err1 error
				resp, err1 = service.GetWorkflowExecutionHistory(tchCtx, &s.GetWorkflowExecutionHistoryRequest{
					Domain:        common.StringPtr(domain),
					Execution:     execution,
					NextPageToken: nextPageToken,
				}, opt...)
				return err1
			}, serviceOperationRetryPolicy, isServiceTransientError)
		if err != nil {
			metricsScope.Counter(metrics.WorkflowGetHistoryFailedCounter).Inc(1)
			return nil, nil, err
		}

		metricsScope.Counter(metrics.WorkflowGetHistorySucceedCounter).Inc(1)
		metricsScope.Timer(metrics.WorkflowGetHistoryLatency).Record(time.Now().Sub(startTime))
		h := resp.History
		size := len(h.Events)
		if size > 0 && atDecisionTaskCompletedEventID > 0 &&
			h.Events[size-1].GetEventId() > atDecisionTaskCompletedEventID {
			first := h.Events[0].GetEventId() // eventIds start from 1
			h.Events = h.Events[:atDecisionTaskCompletedEventID-first+1]
			if h.Events[len(h.Events)-1].GetEventType() != s.EventTypeDecisionTaskCompleted {
				return nil, nil, fmt.Errorf("newGetHistoryPageFunc: atDecisionTaskCompletedEventID(%v) "+
					"points to event that is not DecisionTaskCompleted", atDecisionTaskCompletedEventID)
			}
			return h, nil, nil
		}
		return h, resp.NextPageToken, nil
	}
}

func newActivityTaskPoller(taskHandler ActivityTaskHandler, service workflowserviceclient.Interface,
	domain string, params workerExecutionParameters) *activityTaskPoller {
	return &activityTaskPoller{
		taskHandler:         taskHandler,
		service:             metrics.NewWorkflowServiceWrapper(service, params.MetricsScope),
		domain:              domain,
		taskListName:        params.TaskList,
		identity:            params.Identity,
		logger:              params.Logger,
		metricsScope:        params.MetricsScope,
		activitiesPerSecond: params.TaskListActivitiesPerSecond,
	}
}

// Poll for a single activity task from the service
func (atp *activityTaskPoller) poll() (*activityTask, error) {
	startTime := time.Now()

	atp.metricsScope.Counter(metrics.ActivityPollCounter).Inc(1)

	traceLog(func() {
		atp.logger.Debug("activityTaskPoller::Poll")
	})
	request := &s.PollForActivityTaskRequest{
		Domain:           common.StringPtr(atp.domain),
		TaskList:         common.TaskListPtr(s.TaskList{Name: common.StringPtr(atp.taskListName)}),
		Identity:         common.StringPtr(atp.identity),
		TaskListMetadata: &s.TaskListMetadata{MaxTasksPerSecond: &atp.activitiesPerSecond},
	}

	tchCtx, cancel, opt := newChannelContext(context.Background(), chanTimeout(pollTaskServiceTimeOut))
	defer cancel()

	response, err := atp.service.PollForActivityTask(tchCtx, request, opt...)
	if err != nil {
		if isServiceTransientError(err) {
			atp.metricsScope.Counter(metrics.ActivityPollTransientFailedCounter).Inc(1)
		} else {
			atp.metricsScope.Counter(metrics.ActivityPollFailedCounter).Inc(1)
		}
		return nil, err
	}
	if response == nil || len(response.TaskToken) == 0 {
		atp.metricsScope.Counter(metrics.ActivityPollNoTaskCounter).Inc(1)
		return &activityTask{}, nil
	}

	atp.metricsScope.Counter(metrics.ActivityPollSucceedCounter).Inc(1)
	atp.metricsScope.Timer(metrics.ActivityPollLatency).Record(time.Now().Sub(startTime))
	return &activityTask{task: response, pollStartTime: startTime}, nil
}

// PollTask polls a new task
func (atp *activityTaskPoller) PollTask() (interface{}, error) {
	// Get the task.
	activityTask, err := atp.poll()
	if err != nil {
		return nil, err
	}
	return activityTask, nil
}

// ProcessTask processes a new task
func (atp *activityTaskPoller) ProcessTask(task interface{}) error {
	activityTask := task.(*activityTask)
	if activityTask.task == nil {
		// We didn't have task, poll might have time out.
		traceLog(func() {
			atp.logger.Debug("Activity task unavailable")
		})
		return nil
	}

	// record tasklist queue latency
	queueLatency := time.Duration(activityTask.task.GetStartedTimestamp() - activityTask.task.GetScheduledTimestamp())
	atp.metricsScope.Timer(metrics.TaskListQueueLatency).Record(queueLatency)

	executionStartTime := time.Now()
	// Process the activity task.
	request, err := atp.taskHandler.Execute(atp.taskListName, activityTask.task)
	if err != nil {
		atp.metricsScope.Counter(metrics.ActivityExecutionFailedCounter).Inc(1)
		return err
	}
	atp.metricsScope.Timer(metrics.ActivityExecutionLatency).Record(time.Now().Sub(executionStartTime))

	if request == ErrActivityResultPending {
		return nil
	}

	responseStartTime := time.Now()
	reportErr := reportActivityComplete(context.Background(), atp.service, request, atp.metricsScope)
	if reportErr != nil {
		atp.metricsScope.Counter(metrics.ActivityResponseFailedCounter).Inc(1)
		traceLog(func() {
			atp.logger.Debug("reportActivityComplete failed", zap.Error(reportErr))
		})
		return reportErr
	}

	atp.metricsScope.Timer(metrics.ActivityResponseLatency).Record(time.Now().Sub(responseStartTime))
	atp.metricsScope.Timer(metrics.ActivityEndToEndLatency).Record(time.Now().Sub(activityTask.pollStartTime))
	return nil
}

func reportActivityComplete(ctx context.Context, service workflowserviceclient.Interface, request interface{}, metricsScope tally.Scope) error {
	if request == nil {
		// nothing to report
		return nil
	}

	tchCtx, cancel, opt := newChannelContext(ctx)
	defer cancel()
	var reportErr error
	switch request := request.(type) {
	case *s.RespondActivityTaskCanceledRequest:
		reportErr = backoff.Retry(ctx,
			func() error {
				return service.RespondActivityTaskCanceled(tchCtx, request, opt...)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	case *s.RespondActivityTaskFailedRequest:
		reportErr = backoff.Retry(ctx,
			func() error {
				return service.RespondActivityTaskFailed(tchCtx, request, opt...)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	case *s.RespondActivityTaskCompletedRequest:
		reportErr = backoff.Retry(ctx,
			func() error {
				return service.RespondActivityTaskCompleted(tchCtx, request, opt...)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	}
	if reportErr == nil {
		switch request.(type) {
		case *s.RespondActivityTaskCanceledRequest:
			metricsScope.Counter(metrics.ActivityTaskCanceledCounter).Inc(1)
		case *s.RespondActivityTaskFailedRequest:
			metricsScope.Counter(metrics.ActivityTaskFailedCounter).Inc(1)
		case *s.RespondActivityTaskCompletedRequest:
			metricsScope.Counter(metrics.ActivityTaskCompletedCounter).Inc(1)
		}
	}

	return reportErr
}

func reportActivityCompleteByID(ctx context.Context, service workflowserviceclient.Interface, request interface{}, metricsScope tally.Scope) error {
	if request == nil {
		// nothing to report
		return nil
	}

	tchCtx, cancel, opt := newChannelContext(ctx)
	defer cancel()
	var reportErr error
	switch request := request.(type) {
	case *s.RespondActivityTaskCanceledByIDRequest:
		reportErr = backoff.Retry(ctx,
			func() error {
				return service.RespondActivityTaskCanceledByID(tchCtx, request, opt...)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	case *s.RespondActivityTaskFailedByIDRequest:
		reportErr = backoff.Retry(ctx,
			func() error {
				return service.RespondActivityTaskFailedByID(tchCtx, request, opt...)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	case *s.RespondActivityTaskCompletedByIDRequest:
		reportErr = backoff.Retry(ctx,
			func() error {
				return service.RespondActivityTaskCompletedByID(tchCtx, request, opt...)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	}
	if reportErr == nil {
		switch request.(type) {
		case *s.RespondActivityTaskCanceledByIDRequest:
			metricsScope.Counter(metrics.ActivityTaskCanceledByIDCounter).Inc(1)
		case *s.RespondActivityTaskFailedByIDRequest:
			metricsScope.Counter(metrics.ActivityTaskFailedByIDCounter).Inc(1)
		case *s.RespondActivityTaskCompletedByIDRequest:
			metricsScope.Counter(metrics.ActivityTaskCompletedByIDCounter).Inc(1)
		}
	}

	return reportErr
}

func convertActivityResultToRespondRequest(identity string, taskToken, result []byte, err error) interface{} {
	if err == ErrActivityResultPending {
		// activity result is pending and will be completed asynchronously.
		// nothing to report at this point
		return ErrActivityResultPending
	}

	if err == nil {
		return &s.RespondActivityTaskCompletedRequest{
			TaskToken: taskToken,
			Result:    result,
			Identity:  common.StringPtr(identity)}
	}

	reason, details := getErrorDetails(err)
	if _, ok := err.(*CanceledError); ok || err == context.Canceled {
		return &s.RespondActivityTaskCanceledRequest{
			TaskToken: taskToken,
			Details:   details,
			Identity:  common.StringPtr(identity)}
	}

	return &s.RespondActivityTaskFailedRequest{
		TaskToken: taskToken,
		Reason:    common.StringPtr(reason),
		Details:   details,
		Identity:  common.StringPtr(identity)}
}

func convertActivityResultToRespondRequestByID(identity, domain, workflowID, runID, activityID string,
	result []byte, err error) interface{} {
	if err == ErrActivityResultPending {
		// activity result is pending and will be completed asynchronously.
		// nothing to report at this point
		return nil
	}

	if err == nil {
		return &s.RespondActivityTaskCompletedByIDRequest{
			Domain:     common.StringPtr(domain),
			WorkflowID: common.StringPtr(workflowID),
			RunID:      common.StringPtr(runID),
			ActivityID: common.StringPtr(activityID),
			Result:     result,
			Identity:   common.StringPtr(identity)}
	}

	reason, details := getErrorDetails(err)
	if _, ok := err.(*CanceledError); ok || err == context.Canceled {
		return &s.RespondActivityTaskCanceledByIDRequest{
			Domain:     common.StringPtr(domain),
			WorkflowID: common.StringPtr(workflowID),
			RunID:      common.StringPtr(runID),
			ActivityID: common.StringPtr(activityID),
			Details:    details,
			Identity:   common.StringPtr(identity)}
	}

	return &s.RespondActivityTaskFailedByIDRequest{
		Domain:     common.StringPtr(domain),
		WorkflowID: common.StringPtr(workflowID),
		RunID:      common.StringPtr(runID),
		ActivityID: common.StringPtr(activityID),
		Reason:     common.StringPtr(reason),
		Details:    details,
		Identity:   common.StringPtr(identity)}

	return nil
}
