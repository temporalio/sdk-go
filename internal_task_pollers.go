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
	// taskPoller interface to poll for a single task
	taskPoller interface {
		PollAndProcessSingleTask() error
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

// PollAndProcessSingleTask process one single task
func (wtp *workflowTaskPoller) PollAndProcessSingleTask() error {
	startTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if wtp.metricsScope != nil {
			wtp.metricsScope.Counter(metrics.DecisionsTotalCounter).Inc(1)
			wtp.metricsScope.Timer(metrics.DecisionsEndToEndLatency).Record(deltaTime)
		}
	}()

	// Get the task.
	workflowTask, err := wtp.poll()
	if err != nil {
		return err
	}
	if workflowTask.task == nil {
		// We didn't have task, poll might have time out.
		wtp.logger.Debug("Workflow task unavailable")
		return nil
	}

	// Process the task.
	completedRequest, _, err := wtp.taskHandler.ProcessWorkflowTask(workflowTask.task, false)
	if err != nil {
		return err
	}

	responseStartTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(responseStartTime)
		if wtp.metricsScope != nil {
			wtp.metricsScope.Timer(metrics.DecisionsResponseLatency).Record(deltaTime)
		}
	}()

	// Respond task completion.
	err = backoff.Retry(
		func() error {
			ctx, cancel := newTChannelContext()
			defer cancel()
			err1 := wtp.service.RespondDecisionTaskCompleted(ctx, completedRequest)
			if err1 != nil {
				wtp.logger.Debug("RespondDecisionTaskCompleted failed.", zap.Error(err1))
			}
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return err
	}
	return nil
}

// Poll for a single workflow task from the service
func (wtp *workflowTaskPoller) poll() (*workflowTask, error) {
	startTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if wtp.metricsScope != nil {
			wtp.metricsScope.Timer(metrics.DecisionsPollLatency).Record(deltaTime)
		}
	}()

	if enableVerboseLogging {
		wtp.logger.Debug("workflowTaskPoller::Poll")
	}
	request := &s.PollForDecisionTaskRequest{
		Domain:   common.StringPtr(wtp.domain),
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(wtp.taskListName)}),
		Identity: common.StringPtr(wtp.identity),
	}

	ctx, cancel := newTChannelContext(tchanTimeout(pollTaskServiceTimeOut), tchanRetryOption(retryNeverOptions))
	defer cancel()

	response, err := wtp.service.PollForDecisionTask(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.GetTaskToken()) == 0 {
		return &workflowTask{}, nil
	}

	execution := response.GetWorkflowExecution()
	iterator := func(nextPageToken []byte) (*s.History, []byte, error) {
		var resp *s.GetWorkflowExecutionHistoryResponse
		err = backoff.Retry(
			func() error {
				ctx, cancel := newTChannelContext()
				defer cancel()

				var err1 error
				resp, err1 = wtp.service.GetWorkflowExecutionHistory(ctx, &s.GetWorkflowExecutionHistoryRequest{
					Domain:        common.StringPtr(wtp.domain),
					Execution:     execution,
					NextPageToken: nextPageToken,
				})
				return err1
			}, serviceOperationRetryPolicy, isServiceTransientError)
		if err != nil {
			return nil, nil, err
		}
		return resp.GetHistory(), resp.GetNextPageToken(), nil
	}

	task := &workflowTask{task: response, iterator: iterator}
	return task, nil
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
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if atp.metricsScope != nil {
			atp.metricsScope.Timer(metrics.ActivityPollLatency).Record(deltaTime)
		}
	}()

	if enableVerboseLogging {
		atp.logger.Debug("activityTaskPoller::Poll")
	}
	request := &s.PollForActivityTaskRequest{
		Domain:   common.StringPtr(atp.domain),
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(atp.taskListName)}),
		Identity: common.StringPtr(atp.identity),
	}

	ctx, cancel := newTChannelContext(tchanTimeout(pollTaskServiceTimeOut), tchanRetryOption(retryNeverOptions))
	defer cancel()

	response, err := atp.service.PollForActivityTask(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.GetTaskToken()) == 0 {
		return &activityTask{}, nil
	}
	return &activityTask{task: response}, nil
}

// PollAndProcessSingleTask process one single activity task
func (atp *activityTaskPoller) PollAndProcessSingleTask() error {
	startTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if atp.metricsScope != nil {
			atp.metricsScope.Counter(metrics.ActivitiesTotalCounter).Inc(1)
			atp.metricsScope.Timer(metrics.ActivityEndToEndLatency).Record(deltaTime)
		}
	}()

	// Get the task.
	activityTask, err := atp.poll()
	if err != nil {
		return err
	}
	if activityTask.task == nil {
		// We didn't have task, poll might have time out.
		atp.logger.Debug("Activity task unavailable")
		return nil
	}

	// Process the activity task.
	request, err := atp.taskHandler.Execute(activityTask.task)
	if err != nil {
		return err
	}

	reportErr := reportActivityComplete(atp.service, request, atp.metricsScope)
	if reportErr != nil {
		atp.logger.Debug("reportActivityComplete failed", zap.Error(reportErr))
	}

	return reportErr
}

func reportActivityComplete(service m.TChanWorkflowService, request interface{}, metricsScope tally.Scope) error {
	if request == nil {
		// nothing to report
		return nil
	}

	startTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if metricsScope != nil {
			metricsScope.Timer(metrics.ActivityResponseLatency).Record(deltaTime)
		}
	}()

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
	if _, ok := err.(CanceledError); ok || err == context.Canceled {
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
