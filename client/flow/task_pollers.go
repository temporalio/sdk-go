package flow

import (
	"fmt"
	"time"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/backoff"
	log "github.com/Sirupsen/logrus"
	"github.com/uber/tchannel-go/thrift"
)

const (
	serviceTimeOut  = 3 * time.Minute
	tagTaskListName = "taskListName"

	retryServiceOperationInitialInterval    = time.Millisecond
	retryServiceOperationMaxInterval        = 10 * time.Second
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
		taskListName  string
		identity      string
		service       m.TChanWorkflowService
		taskHandler   workflowTaskHandler
		contextLogger *log.Entry
	}

	// activityTaskPoller implements polling/processing a workflow task
	activityTaskPoller struct {
		taskListName  string
		identity      string
		service       m.TChanWorkflowService
		taskHandler   activityTaskHandler
		contextLogger *log.Entry
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
	taskHandler workflowTaskHandler, logger *log.Entry) *workflowTaskPoller {
	return &workflowTaskPoller{
		service:       service,
		taskListName:  taskListName,
		identity:      identity,
		taskHandler:   taskHandler,
		contextLogger: logger}
}

// PollAndProcessSingleTask process one single task
func (wtp *workflowTaskPoller) PollAndProcessSingleTask() error {
	// Get the task.
	workflowTask, err := wtp.poll()
	if err != nil {
		return err
	}
	if workflowTask.task == nil {
		// We didn't have task, poll might have time out.
		wtp.contextLogger.Debug("Workflow task unavailable")
		return nil
	}

	// Process the task.
	completedRequest, _, err := wtp.taskHandler.ProcessWorkflowTask(workflowTask, false)
	if err != nil {
		return err
	}

	// Respond task completion.
	err = backoff.Retry(
		func() error {
			ctx, cancel := thrift.NewContext(serviceTimeOut)
			defer cancel()
			return wtp.service.RespondDecisionTaskCompleted(ctx, completedRequest)
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return err
	}
	return nil
}

// Poll for a single workflow task from the service
func (wtp *workflowTaskPoller) poll() (*workflowTask, error) {
	wtp.contextLogger.Debug("workflowTaskPoller::Poll")
	request := &s.PollForDecisionTaskRequest{
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(wtp.taskListName)}),
		Identity: common.StringPtr(wtp.identity),
	}

	ctx, cancel := thrift.NewContext(serviceTimeOut)
	defer cancel()

	response, err := wtp.service.PollForDecisionTask(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.GetTaskToken()) == 0 {
		return &workflowTask{}, nil
	}
	return &workflowTask{task: response}, nil
}

func newActivityTaskPoller(service m.TChanWorkflowService, taskListName string, identity string,
	taskHandler activityTaskHandler, logger *log.Entry) *activityTaskPoller {
	return &activityTaskPoller{
		service:       service,
		taskListName:  taskListName,
		identity:      identity,
		taskHandler:   taskHandler,
		contextLogger: logger}
}

// Poll for a single activity task from the service
func (atp *activityTaskPoller) poll() (*activityTask, error) {
	request := &s.PollForActivityTaskRequest{
		TaskList: common.TaskListPtr(s.TaskList{Name: common.StringPtr(atp.taskListName)}),
		Identity: common.StringPtr(atp.identity),
	}

	ctx, cancel := thrift.NewContext(serviceTimeOut)
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
		atp.contextLogger.Debug("Activity task unavailable")
		return nil
	}

	// Process the activity task.
	ctx, cancel := thrift.NewContext(serviceTimeOut)
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
				ctx, cancel := thrift.NewContext(serviceTimeOut)
				defer cancel()

				return atp.service.RespondActivityTaskCompleted(ctx, result.(*s.RespondActivityTaskCompletedRequest))
			}, serviceOperationRetryPolicy, isServiceTransientError)

		if err != nil {
			return err
		}
		// Report failure untill we succeed
	case *s.RespondActivityTaskFailedRequest:
		err = backoff.Retry(
			func() error {
				ctx, cancel := thrift.NewContext(serviceTimeOut)
				defer cancel()

				return atp.service.RespondActivityTaskFailed(ctx, result.(*s.RespondActivityTaskFailedRequest))
			}, serviceOperationRetryPolicy, isServiceTransientError)
		if err != nil {
			return err
		}
	default:
		panic(fmt.Errorf("Unhandled activity response type: %v", result))
	}
	return nil
}
