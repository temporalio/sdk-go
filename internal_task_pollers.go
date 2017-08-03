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

// All code in this file is private to the package.

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/uber-go/tally"
	m "go.uber.org/cadence/.gen/go/cadence"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/backoff"
	"go.uber.org/cadence/common/metrics"
	"go.uber.org/zap"
)

const (
	pollTaskServiceTimeOut = 3 * time.Minute // Server long poll is 1 * Minutes + delta

	retryServiceOperationInitialInterval    = time.Millisecond
	retryServiceOperationMaxInterval        = 4 * time.Second
	retryServiceOperationExpirationInterval = 60 * time.Second
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
		service      m.TChanWorkflowService
		taskHandler  WorkflowTaskHandler
		metricsScope tally.Scope
		logger       *zap.Logger
	}

	// activityTaskPoller implements polling/processing a workflow task
	activityTaskPoller struct {
		domain       string
		taskListName string
		identity     string
		service      m.TChanWorkflowService
		taskHandler  ActivityTaskHandler
		metricsScope tally.Scope
		logger       *zap.Logger
	}
)

func createServiceRetryPolicy() backoff.RetryPolicy {
	policy := backoff.NewExponentialRetryPolicy(retryServiceOperationInitialInterval)
	policy.SetMaximumInterval(retryServiceOperationMaxInterval)
	policy.SetExpirationInterval(retryServiceOperationExpirationInterval)
	return policy
}

func isServiceTransientError(err error) bool {
	// Retrying by default so it covers all transport errors.
	switch err.(type) {
	case *s.BadRequestError:
		return false
	case *s.EntityNotExistsError:
		return false
	case *s.WorkflowExecutionAlreadyStartedError:
		return false
	case *s.DomainAlreadyExistsError:
		return false
	}

	// s.InternalServiceError
	return true
}

func isClientSideError(err error) bool {
	// If an activity execution exceeds deadline.
	if err == context.DeadlineExceeded {
		return true
	}

	return false
}

func newWorkflowTaskPoller(taskHandler WorkflowTaskHandler, service m.TChanWorkflowService,
	domain string, params workerExecutionParameters) *workflowTaskPoller {
	return &workflowTaskPoller{
		service:      service,
		domain:       domain,
		taskListName: params.TaskList,
		identity:     params.Identity,
		taskHandler:  taskHandler,
		metricsScope: params.MetricsScope,
		logger:       params.Logger,
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

// ProcessTask processes a task
func (wtp *workflowTaskPoller) ProcessTask(task interface{}) error {
	workflowTask := task.(*workflowTask)
	if workflowTask.task == nil {
		// We didn't have task, poll might have time out.
		traceLog(func() {
			wtp.logger.Debug("Workflow task unavailable")
		})
		return nil
	}

	executionStartTime := time.Now()
	// Process the task.
	completedRequest, _, err := wtp.taskHandler.ProcessWorkflowTask(workflowTask.task, workflowTask.getHistoryPageFunc, false)
	if err != nil {
		wtp.metricsScope.Counter(metrics.DecisionExecutionFailedCounter).Inc(1)
		return err
	}
	wtp.metricsScope.Timer(metrics.DecisionExecutionLatency).Record(time.Now().Sub(executionStartTime))

	responseStartTime := time.Now()
	// Respond task completion.
	err = backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			err1 := wtp.service.RespondDecisionTaskCompleted(ctx, completedRequest)
			if err1 != nil {
				traceLog(func() {
					wtp.logger.Debug("RespondDecisionTaskCompleted failed.", zap.Error(err1))
				})
			}
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		wtp.metricsScope.Counter(metrics.DecisionResponseFailedCounter).Inc(1)
		return err
	}

	wtp.metricsScope.Timer(metrics.DecisionResponseLatency).Record(time.Now().Sub(responseStartTime))
	wtp.metricsScope.Timer(metrics.DecisionEndToEndLatency).Record(time.Now().Sub(workflowTask.pollStartTime))
	wtp.metricsScope.Counter(metrics.DecisionTaskCompletedCounter).Inc(1)

	return nil
}

// Poll for a single workflow task from the service
func (wtp *workflowTaskPoller) poll() (*workflowTask, error) {
	startTime := time.Now()
	wtp.metricsScope.Counter(metrics.DecisionPollCounter).Inc(1)

	traceLog(func() {
		wtp.logger.Debug("workflowTaskPoller::Poll")
	})
	request := &s.PollForDecisionTaskRequest{
		Domain:   common.StringPtr(wtp.domain),
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(wtp.taskListName)}),
		Identity: common.StringPtr(wtp.identity),
	}

	ctx, cancel := newTChannelContext(tchanTimeout(pollTaskServiceTimeOut), tchanRetryOption(retryNeverOptions))
	defer cancel()

	response, err := wtp.service.PollForDecisionTask(ctx, request)
	if err != nil {
		wtp.metricsScope.Counter(metrics.DecisionPollFailedCounter).Inc(1)
		return nil, err
	}

	if response == nil || len(response.GetTaskToken()) == 0 {
		wtp.metricsScope.Counter(metrics.DecisionPollNoTaskCounter).Inc(1)
		return &workflowTask{}, nil
	}

	execution := response.GetWorkflowExecution()
	iterator := newGetHistoryPageFunc(wtp.service, wtp.domain, execution, math.MaxInt64, wtp.metricsScope)
	task := &workflowTask{task: response, getHistoryPageFunc: iterator, pollStartTime: startTime}
	wtp.metricsScope.Counter(metrics.DecisionPollSucceedCounter).Inc(1)
	wtp.metricsScope.Timer(metrics.DecisionPollLatency).Record(time.Now().Sub(startTime))
	return task, nil
}

func newGetHistoryPageFunc(
	service m.TChanWorkflowService,
	domain string,
	execution *s.WorkflowExecution,
	atDecisionTaskCompletedEventID int64,
	metricsScope tally.Scope,
) func(nextPageToken []byte) (*s.History, []byte, error) {
	return func(nextPageToken []byte) (*s.History, []byte, error) {
		metricsScope.Counter(metrics.WorkflowGetHistoryCounter).Inc(1)
		startTime := time.Now()
		var resp *s.GetWorkflowExecutionHistoryResponse
		err := backoff.Retry(
			func() error {
				ctx, cancel := newTChannelContext()
				defer cancel()

				var err1 error
				resp, err1 = service.GetWorkflowExecutionHistory(ctx, &s.GetWorkflowExecutionHistoryRequest{
					Domain:        common.StringPtr(domain),
					Execution:     execution,
					NextPageToken: nextPageToken,
				})
				return err1
			}, serviceOperationRetryPolicy, isServiceTransientError)
		if err != nil {
			metricsScope.Counter(metrics.WorkflowGetHistoryFailedCounter).Inc(1)
			return nil, nil, err
		}

		metricsScope.Counter(metrics.WorkflowGetHistorySucceedCounter).Inc(1)
		metricsScope.Timer(metrics.WorkflowGetHistoryLatency).Record(time.Now().Sub(startTime))
		h := resp.GetHistory()
		size := len(h.Events)
		if size > 0 && atDecisionTaskCompletedEventID > 0 &&
			h.Events[size-1].GetEventId() > atDecisionTaskCompletedEventID {
			first := h.Events[0].GetEventId() // eventIds start from 1
			h.Events = h.Events[:atDecisionTaskCompletedEventID-first+1]
			if h.Events[len(h.Events)-1].GetEventType() != s.EventType_DecisionTaskCompleted {
				return nil, nil, fmt.Errorf("newGetHistoryPageFunc: atDecisionTaskCompletedEventID(%v) "+
					"points to event that is not DecisionTaskCompleted", atDecisionTaskCompletedEventID)
			}
			return h, nil, nil
		}
		return h, resp.GetNextPageToken(), nil
	}
}

func newActivityTaskPoller(taskHandler ActivityTaskHandler, service m.TChanWorkflowService,
	domain string, params workerExecutionParameters) *activityTaskPoller {
	return &activityTaskPoller{
		taskHandler:  taskHandler,
		service:      service,
		domain:       domain,
		taskListName: params.TaskList,
		identity:     params.Identity,
		logger:       params.Logger,
		metricsScope: params.MetricsScope}
}

// Poll for a single activity task from the service
func (atp *activityTaskPoller) poll() (*activityTask, error) {
	startTime := time.Now()

	atp.metricsScope.Counter(metrics.ActivityPollCounter).Inc(1)

	traceLog(func() {
		atp.logger.Debug("activityTaskPoller::Poll")
	})
	request := &s.PollForActivityTaskRequest{
		Domain:   common.StringPtr(atp.domain),
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(atp.taskListName)}),
		Identity: common.StringPtr(atp.identity),
	}

	ctx, cancel := newTChannelContext(tchanTimeout(pollTaskServiceTimeOut), tchanRetryOption(retryNeverOptions))
	defer cancel()

	response, err := atp.service.PollForActivityTask(ctx, request)
	if err != nil {
		atp.metricsScope.Counter(metrics.ActivityPollFailedCounter).Inc(1)
		return nil, err
	}
	if response == nil || len(response.GetTaskToken()) == 0 {
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

	executionStartTime := time.Now()
	// Process the activity task.
	request, err := atp.taskHandler.Execute(activityTask.task)
	if err != nil {
		atp.metricsScope.Counter(metrics.ActivityExecutionFailedCounter).Inc(1)
		return err
	}
	atp.metricsScope.Timer(metrics.ActivityExecutionLatency).Record(time.Now().Sub(executionStartTime))

	if request == nil {
		// this could be true when activity returns ErrActivityResultPending.
		return nil
	}

	responseStartTime := time.Now()
	reportErr := reportActivityComplete(atp.service, request, atp.metricsScope)
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

func reportActivityComplete(service m.TChanWorkflowService, request interface{}, metricsScope tally.Scope) error {
	if request == nil {
		// nothing to report
		return nil
	}

	ctx, cancel := newTChannelContext()
	defer cancel()
	var reportErr error
	switch request := request.(type) {
	case *s.RespondActivityTaskCanceledRequest:
		reportErr = backoff.Retry(
			func() error {
				return service.RespondActivityTaskCanceled(ctx, request)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	case *s.RespondActivityTaskFailedRequest:
		reportErr = backoff.Retry(
			func() error {
				return service.RespondActivityTaskFailed(ctx, request)
			}, serviceOperationRetryPolicy, isServiceTransientError)
	case *s.RespondActivityTaskCompletedRequest:
		reportErr = backoff.Retry(
			func() error {
				return service.RespondActivityTaskCompleted(ctx, request)
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

func convertActivityResultToRespondRequest(identity string, taskToken, result []byte, err error) interface{} {
	if err == ErrActivityResultPending {
		// activity result is pending and will be completed asynchronously.
		// nothing to report at this point
		return nil
	}

	if err == nil {
		return &s.RespondActivityTaskCompletedRequest{
			TaskToken: taskToken,
			Result_:   result,
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
