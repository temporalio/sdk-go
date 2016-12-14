package flow

import (
	"fmt"

	"github.com/uber/tchannel-go/thrift"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/backoff"
	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
)

type (
	// workflowTaskHandlerImpl is the implementation of workflowTaskHandler
	workflowTaskHandlerImpl struct {
		taskListName       string
		identity           string
		workflowDefFactory WorkflowDefinitionFactory
		contextLogger      *log.Entry
	}

	// activityTaskHandlerImpl is the implementation of ActivityTaskHandler
	activityTaskHandlerImpl struct {
		taskListName        string
		identity            string
		activityImplFactory ActivityImplementationFactory
		service             m.TChanWorkflowService
		contextLogger       *log.Entry
	}

	// eventsHelper wrapper method to help information about events.
	eventsHelper struct {
		workflowTask *workflowTask
	}

	// activityExecutionContext an implementation of ActivityExecutionContext represents a context for workflow execution.
	activityExecutionContext struct {
		taskToken []byte
		identity  string
		service   m.TChanWorkflowService
	}

	// activityTaskFailedError wraps the details of the failure of activity
	activityTaskFailedError struct {
		reason  string
		details []byte
	}

	// activityTaskTimeoutError wraps the details of the timeout of activity
	activityTaskTimeoutError struct {
		TimeoutType m.TimeoutType
	}
)

// Error from error.Error
func (e activityTaskFailedError) Error() string {
	return fmt.Sprintf("Reason: %s, Details: %s", e.reason, e.details)
}

// Details of the error
func (e activityTaskFailedError) Details() []byte {
	return e.details
}

// Reason of the error
func (e activityTaskFailedError) Reason() string {
	return e.reason
}

// Error from error.Error
func (e activityTaskTimeoutError) Error() string {
	return fmt.Sprintf("TimeoutType: %v", e.TimeoutType)
}

// Details of the error
func (e activityTaskTimeoutError) Details() []byte {
	return nil
}

// Reason of the error
func (e activityTaskTimeoutError) Reason() string {
	return e.Error()
}

// Get last non replayed event ID.
func (eh eventsHelper) LastNonReplayedID() int64 {
	if eh.workflowTask.task.PreviousStartedEventId == nil {
		return 0
	}
	return *eh.workflowTask.task.PreviousStartedEventId
}

// newWorkflowTaskHandler returns an implementation of workflow task handler.
func newWorkflowTaskHandler(taskListName string, identity string, factory WorkflowDefinitionFactory,
	contextLogger *log.Entry) workflowTaskHandler {
	return &workflowTaskHandlerImpl{
		taskListName:       taskListName,
		identity:           identity,
		workflowDefFactory: factory,
		contextLogger:      contextLogger}
}

// ProcessWorkflowTask processes each all the events of the workflow task.
func (wth *workflowTaskHandlerImpl) ProcessWorkflowTask(workflowTask *workflowTask, emitStack bool) (result *m.RespondDecisionTaskCompletedRequest, stackTrace string, err error) {
	if workflowTask == nil {
		return nil, "", fmt.Errorf("nil workflowtask provided")
	}

	wth.contextLogger.Debugf("Processing New Workflow Task: Type=%s, PreviousStartedEventId=%d",
		workflowTask.task.GetWorkflowType().GetName(), workflowTask.task.GetPreviousStartedEventId())

	// Setup workflow Info
	workflowInfo := &WorkflowInfo{
		workflowType: *workflowTask.task.WorkflowType,
		taskListName: wth.taskListName,
		// workflowExecution
	}

	isWorkflowCompleted := false
	var completionResult []byte
	var failure Error

	completeHandler := func(result []byte, err Error) {
		completionResult = result
		failure = err
		isWorkflowCompleted = true
	}

	eventHandler := newWorkflowExecutionEventHandler(
		workflowInfo, wth.workflowDefFactory, completeHandler, wth.contextLogger)
	helperEvents := &eventsHelper{workflowTask: workflowTask}
	history := workflowTask.task.History
	decisions := []*m.Decision{}

	// Process events
	for _, event := range history.Events {
		wth.contextLogger.Debugf("ProcessWorkflowTask: Id=%d, Event=%+v", event.GetEventId(), event)
		eventDecisions, err := eventHandler.ProcessEvent(event)
		if err != nil {
			return nil, "", err
		}
		if event.GetEventId() >= helperEvents.LastNonReplayedID() {
			if eventDecisions != nil {
				decisions = append(decisions, eventDecisions...)
			}
		}
	}

	eventDecisions := wth.completeWorkflow(isWorkflowCompleted, completionResult, failure)
	if len(eventDecisions) > 0 {
		decisions = append(decisions, eventDecisions...)
	}

	// Fill the response.
	taskCompletionRequest := &m.RespondDecisionTaskCompletedRequest{
		TaskToken: workflowTask.task.TaskToken,
		Decisions: decisions,
		Identity:  common.StringPtr(wth.identity),
		// ExecutionContext:
	}
	if emitStack {
		stackTrace = eventHandler.StackTrace()
	}
	return taskCompletionRequest, stackTrace, nil
}

func (wth *workflowTaskHandlerImpl) completeWorkflow(isWorkflowCompleted bool, completionResult []byte,
	err Error) []*m.Decision {
	decisions := []*m.Decision{}
	if err != nil {
		// Workflow failures
		failDecision := createNewDecision(m.DecisionType_FailWorkflowExecution)
		failDecision.FailWorkflowExecutionDecisionAttributes = &m.FailWorkflowExecutionDecisionAttributes{
			Reason:  common.StringPtr(err.Reason()),
			Details: err.Details(),
		}
		decisions = append(decisions, failDecision)
	} else if isWorkflowCompleted {
		// Workflow completion
		completeDecision := createNewDecision(m.DecisionType_CompleteWorkflowExecution)
		completeDecision.CompleteWorkflowExecutionDecisionAttributes = &m.CompleteWorkflowExecutionDecisionAttributes{
			Result_: completionResult,
		}
		decisions = append(decisions, completeDecision)
	}
	return decisions
}

func newActivityTaskHandler(taskListName string, identity string, factory ActivityImplementationFactory,
	service m.TChanWorkflowService, contextLogger *log.Entry) activityTaskHandler {
	return &activityTaskHandlerImpl{
		taskListName:        taskListName,
		identity:            identity,
		activityImplFactory: factory,
		service:             service,
		contextLogger:       contextLogger}
}

// Execute executes an implementation of the activity.
func (ath *activityTaskHandlerImpl) Execute(context context.Context, activityTask *activityTask) (interface{}, error) {
	ath.contextLogger.Debugf("[WorkflowID: %s] Execute Activity: %s",
		activityTask.task.GetWorkflowExecution().GetWorkflowId(), activityTask.task.GetActivityType().GetName())

	activityExecutionContext := &activityExecutionContext{
		taskToken: activityTask.task.TaskToken,
		identity:  ath.identity,
		service:   ath.service}
	activityImplementation, err := ath.activityImplFactory(*activityTask.task.GetActivityType())
	if err != nil {
		// Couldn't find the activity implementation.
		return nil, err
	}

	output, err := activityImplementation.Execute(activityExecutionContext, activityTask.task.GetInput())
	if err != nil {
		responseFailure := &m.RespondActivityTaskFailedRequest{
			TaskToken: activityTask.task.TaskToken,
			Reason:    common.StringPtr(err.Reason()),
			Details:   err.Details(),
			Identity:  common.StringPtr(ath.identity)}
		return responseFailure, nil
	}

	responseComplete := &m.RespondActivityTaskCompletedRequest{
		TaskToken: activityTask.task.TaskToken,
		Result_:   output,
		Identity:  common.StringPtr(ath.identity)}
	return responseComplete, nil
}

func (aec *activityExecutionContext) TaskToken() []byte {
	return aec.taskToken
}

func (aec *activityExecutionContext) RecordActivityHeartbeat(details []byte) error {
	request := &m.RecordActivityTaskHeartbeatRequest{
		TaskToken: aec.TaskToken(),
		Details:   details,
		Identity:  common.StringPtr(aec.identity)}

	err := backoff.Retry(
		func() error {
			ctx, cancel := thrift.NewContext(serviceTimeOut)
			defer cancel()

			// TODO: Handle the propagation of Cancel to activity.
			_, err2 := aec.service.RecordActivityTaskHeartbeat(ctx, request)
			return err2
		}, serviceOperationRetryPolicy, isServiceTransientError)
	return err
}

func createNewDecision(decisionType m.DecisionType) *m.Decision {
	return &m.Decision{
		DecisionType: common.DecisionTypePtr(decisionType),
	}
}
