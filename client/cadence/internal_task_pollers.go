package cadence

// All code in this file is private to the package.

import (
	"fmt"
	"time"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/backoff"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"github.com/uber-common/bark"
	"github.com/uber-go/tally"
	"github.com/uber/tchannel-go/thrift"
)

const (
	pollTaskServiceTimeOut    = 3 * time.Minute // Server long poll is 2 * Minutes + delta
	respondTaskServiceTimeOut = 2 * time.Second

	tagTaskListName = "taskListName"

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
		taskListName string
		identity     string
		service      m.TChanWorkflowService
		taskHandler  workflowTaskHandler
		metricsScope tally.Scope
		logger       bark.Logger
	}

	// activityTaskPoller implements polling/processing a workflow task
	activityTaskPoller struct {
		taskListName string
		identity     string
		service      m.TChanWorkflowService
		taskHandler  activityTaskHandler
		metricsScope tally.Scope
		logger       bark.Logger
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
	}

	// s.InternalServiceError
	return true
}

func newWorkflowTaskPoller(service m.TChanWorkflowService, taskListName string, identity string,
	taskHandler workflowTaskHandler, logger bark.Logger, metricsScope tally.Scope) *workflowTaskPoller {
	return &workflowTaskPoller{
		service:      service,
		taskListName: taskListName,
		identity:     identity,
		taskHandler:  taskHandler,
		metricsScope: metricsScope,
		logger:       logger,
	}
}

// PollAndProcessSingleTask process one single task
func (wtp *workflowTaskPoller) PollAndProcessSingleTask() error {
	// Get the task.
	workflowTask, err := wtp.poll()
	if err != nil {
		return err
	}
	if workflowTask.Task == nil {
		// We didn't have task, poll might have time out.
		wtp.logger.Debug("Workflow task unavailable")
		return nil
	}

	startTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if wtp.metricsScope != nil {
			wtp.metricsScope.Counter(metrics.DecisionsTotalCounter).Inc(1)
			wtp.metricsScope.Timer(metrics.DecisionsEndToEndLatency).Record(deltaTime)
		}
	}()

	// Process the task.
	completedRequest, _, err := wtp.taskHandler.ProcessWorkflowTask(workflowTask, false)
	if err != nil {
		return err
	}

	// Respond task completion.
	err = backoff.Retry(
		func() error {
			ctx, cancel := common.NewTChannelContext(respondTaskServiceTimeOut, common.RetryDefaultOptions)
			defer cancel()
			err1 := wtp.service.RespondDecisionTaskCompleted(ctx, completedRequest)
			if err1 != nil {
				wtp.logger.Debugf("RespondDecisionTaskCompleted: Failed with error: %+v", err1)
			}
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return err
	}
	return nil
}

// Poll for a single workflow task from the service
func (wtp *workflowTaskPoller) poll() (*WorkflowTask, error) {
	wtp.logger.Debug("workflowTaskPoller::Poll")
	request := &s.PollForDecisionTaskRequest{
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(wtp.taskListName)}),
		Identity: common.StringPtr(wtp.identity),
	}

	ctx, cancel := common.NewTChannelContext(pollTaskServiceTimeOut, common.RetryNeverOptions)
	defer cancel()

	response, err := wtp.service.PollForDecisionTask(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.GetTaskToken()) == 0 {
		return &WorkflowTask{}, nil
	}
	return &WorkflowTask{Task: response}, nil
}

func newActivityTaskPoller(service m.TChanWorkflowService, taskListName string, identity string,
	taskHandler activityTaskHandler, metricsScope tally.Scope, logger bark.Logger) *activityTaskPoller {
	return &activityTaskPoller{
		service:      service,
		taskListName: taskListName,
		identity:     identity,
		taskHandler:  taskHandler,
		logger:       logger,
		metricsScope: metricsScope}
}

// Poll for a single activity task from the service
func (atp *activityTaskPoller) poll() (*activityTask, error) {
	request := &s.PollForActivityTaskRequest{
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(atp.taskListName)}),
		Identity: common.StringPtr(atp.identity),
	}

	ctx, cancel := common.NewTChannelContext(pollTaskServiceTimeOut, common.RetryNeverOptions)
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

	startTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if atp.metricsScope != nil {
			atp.metricsScope.Counter(metrics.ActivitiesTotalCounter).Inc(1)
			atp.metricsScope.Timer(metrics.ActivityEndToEndLatency).Record(deltaTime)
		}
	}()

	// Process the activity task.
	// Activity execution timeout: setting it to a longer one.
	ctx, cancel := thrift.NewContext(7 * 24 * time.Hour)
	defer cancel()
	result, err := atp.taskHandler.Execute(ctx, activityTask)
	if err != nil {
		return err
	}

	// TODO: Handle Cancel of the activity after the thrift method is introduced.
	switch result.(type) {
	// Report success untill we succeed
	case *s.RespondActivityTaskCompletedRequest:
		err = backoff.Retry(
			func() error {
				ctx, cancel := common.NewTChannelContext(respondTaskServiceTimeOut, common.RetryDefaultOptions)
				defer cancel()

				err1 := atp.service.RespondActivityTaskCompleted(ctx, result.(*s.RespondActivityTaskCompletedRequest))
				if err1 != nil {
					atp.logger.Debugf("RespondActivityTaskCompleted: Failed with error: %+v", err1)
				}
				return err1
			}, serviceOperationRetryPolicy, isServiceTransientError)

		if err != nil {
			return err
		}
		// Report failure untill we succeed
	case *s.RespondActivityTaskFailedRequest:
		err = backoff.Retry(
			func() error {
				ctx, cancel := common.NewTChannelContext(respondTaskServiceTimeOut, common.RetryDefaultOptions)
				defer cancel()

				err1 := atp.service.RespondActivityTaskFailed(ctx, result.(*s.RespondActivityTaskFailedRequest))
				if err1 != nil {
					atp.logger.Debugf("RespondActivityTaskFailed: Failed with error: %+v", err1)
				}
				return err1
			}, serviceOperationRetryPolicy, isServiceTransientError)
		if err != nil {
			return err
		}
	default:
		panic(fmt.Errorf("Unhandled activity response type: %v", result))
	}
	return nil
}
