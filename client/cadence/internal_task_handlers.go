package cadence

// All code in this file is private to the package.

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/uber-common/bark"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"golang.org/x/net/context"
)

// PressurePoints
const (
	pressurePointTypeDecisionTaskStartTimeout = "decision-task-start-timeout"
	pressurePointConfigProbability            = "probability"
)

// interfaces
type (
	// workflowTaskHandler represents workflow task handlers.
	workflowTaskHandler interface {
		// Process the workflow task
		ProcessWorkflowTask(task *workflowTask, emitStack bool) (response *s.RespondDecisionTaskCompletedRequest, stackTrace string, err error)
	}

	// activityTaskHandler represents activity task handlers.
	activityTaskHandler interface {
		// Execute the activity task
		// The return interface{} can have three requests, use switch to find the type of it.
		// - RespondActivityTaskCompletedRequest
		// - RespondActivityTaskFailedRequest
		// - RespondActivityTaskCancelRequest
		Execute(context context.Context, task *activityTask) (interface{}, error)
	}

	// workflowExecutionEventHandler process a single event.
	workflowExecutionEventHandler interface {
		// Process a single event and return the assosciated decisions.
		ProcessEvent(event *s.HistoryEvent) ([]*s.Decision, error)
		StackTrace() string
		// Close for cleaning up resources on this event handler
		Close()
	}

	// workflowTask wraps a decision task.
	workflowTask struct {
		task *s.PollForDecisionTaskResponse
	}

	// activityTask wraps a activity task.
	activityTask struct {
		task *s.PollForActivityTaskResponse
	}
)

type (
	// workflowTaskHandlerImpl is the implementation of workflowTaskHandler
	workflowTaskHandlerImpl struct {
		taskListName       string
		identity           string
		workflowDefFactory workflowDefinitionFactory
		reporter           metrics.Reporter
		pressurePoints     map[string]map[string]string
		logger             bark.Logger
	}

	// activityTaskHandlerImpl is the implementation of ActivityTaskHandler
	activityTaskHandlerImpl struct {
		taskListName    string
		identity        string
		implementations map[ActivityType]Activity
		service         m.TChanWorkflowService
		reporter        metrics.Reporter
		logger          bark.Logger
	}

	// eventsHelper wrapper method to help information about events.
	eventsHelper struct {
		workflowTask *workflowTask
	}

	// activityTaskFailedError wraps the details of the failure of activity
	activityTaskFailedError struct {
		reason  string
		details []byte
	}

	// activityTaskTimeoutError wraps the details of the timeout of activity
	activityTaskTimeoutError struct {
		TimeoutType s.TimeoutType
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
func newWorkflowTaskHandler(taskListName string, identity string, factory workflowDefinitionFactory,
	logger bark.Logger, reporter metrics.Reporter, pressurePoints map[string]map[string]string) workflowTaskHandler {
	return &workflowTaskHandlerImpl{
		taskListName:       taskListName,
		identity:           identity,
		workflowDefFactory: factory,
		logger:             logger,
		pressurePoints:     pressurePoints,
		reporter:           reporter}
}

// ProcessWorkflowTask processes each all the events of the workflow task.
func (wth *workflowTaskHandlerImpl) ProcessWorkflowTask(workflowTask *workflowTask, emitStack bool) (result *s.RespondDecisionTaskCompletedRequest, stackTrace string, err error) {
	if workflowTask == nil {
		return nil, "", fmt.Errorf("nil workflowtask provided")
	}

	wth.logger.Debugf("Processing New Workflow Task: Type=%s, PreviousStartedEventId=%d",
		workflowTask.task.GetWorkflowType().GetName(), workflowTask.task.GetPreviousStartedEventId())

	// Setup workflow Info
	workflowInfo := &WorkflowInfo{
		WorkflowType: flowWorkflowTypeFrom(*workflowTask.task.WorkflowType),
		TaskListName: wth.taskListName,
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
		workflowInfo, wth.workflowDefFactory, completeHandler, wth.logger)
	helperEvents := &eventsHelper{workflowTask: workflowTask}
	history := workflowTask.task.History
	decisions := []*s.Decision{}

	startTime := time.Now()

	// Process events
	for _, event := range history.Events {
		wth.logger.Debugf("ProcessWorkflowTask: Id=%d, Event=%+v", event.GetEventId(), event)

		isInReplay := event.GetEventId() < helperEvents.LastNonReplayedID()

		// Any metrics.
		if wth.reporter != nil && !isInReplay {
			switch event.GetEventType() {
			case s.EventType_DecisionTaskTimedOut:
				wth.reporter.IncCounter(metrics.DecisionsTimeoutCounter, nil, 1)
			}
		}

		// Any pressure points.
		if wth.pressurePoints != nil && !isInReplay {
			switch event.GetEventType() {
			case s.EventType_DecisionTaskStarted:
				if config, ok := wth.pressurePoints[pressurePointTypeDecisionTaskStartTimeout]; ok {
					if value, ok2 := config[pressurePointConfigProbability]; ok2 {
						if probablity, err := strconv.Atoi(value); err == nil {
							if rand.Int31n(100) < int32(probablity) {
								// Drop the task.
								wth.logger.Debugf("ProcessWorkflowTask: Dropping task with probability:%d, Id=%d, Event=%+v",
									probablity, event.GetEventId(), event)
								return nil, "", fmt.Errorf("pressurepoint configured")
							}
						}
					}
				}
			}
		}

		eventDecisions, err := eventHandler.ProcessEvent(event)
		if err != nil {
			return nil, "", err
		}

		if !isInReplay {
			if eventDecisions != nil {
				decisions = append(decisions, eventDecisions...)
			}
		}
	}

	eventDecisions := wth.completeWorkflow(isWorkflowCompleted, completionResult, failure)
	if len(eventDecisions) > 0 {
		decisions = append(decisions, eventDecisions...)

		if wth.reporter != nil {
			wth.reporter.IncCounter(metrics.WorkflowsCompletionTotalCounter, nil, 1)
			elapsed := time.Now().Sub(startTime)
			wth.reporter.RecordTimer(metrics.WorkflowEndToEndLatency, nil, elapsed)
		}
	}

	// Fill the response.
	taskCompletionRequest := &s.RespondDecisionTaskCompletedRequest{
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
	err Error) []*s.Decision {
	decisions := []*s.Decision{}
	if err != nil {
		// Workflow failures
		failDecision := createNewDecision(s.DecisionType_FailWorkflowExecution)
		failDecision.FailWorkflowExecutionDecisionAttributes = &s.FailWorkflowExecutionDecisionAttributes{
			Reason:  common.StringPtr(err.Reason()),
			Details: err.Details(),
		}
		decisions = append(decisions, failDecision)
	} else if isWorkflowCompleted {
		// Workflow completion
		completeDecision := createNewDecision(s.DecisionType_CompleteWorkflowExecution)
		completeDecision.CompleteWorkflowExecutionDecisionAttributes = &s.CompleteWorkflowExecutionDecisionAttributes{
			Result_: completionResult,
		}
		decisions = append(decisions, completeDecision)
	}
	return decisions
}

func newActivityTaskHandler(taskListName string, identity string, activities []Activity,
	service m.TChanWorkflowService, logger bark.Logger, reporter metrics.Reporter) activityTaskHandler {
	implementations := make(map[ActivityType]Activity)
	for _, a := range activities {
		implementations[a.ActivityType()] = a
	}
	return &activityTaskHandlerImpl{
		taskListName:    taskListName,
		identity:        identity,
		implementations: implementations,
		service:         service,
		logger:          logger,
		reporter:        reporter}
}

// Execute executes an implementation of the activity.
func (ath *activityTaskHandlerImpl) Execute(ctx context.Context, activityTask *activityTask) (interface{}, error) {
	t := activityTask.task
	ath.logger.Debugf("[WorkflowID: %s] Execute Activity: %s",
		t.GetWorkflowExecution().GetWorkflowId(), t.GetActivityType().GetName())

	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		taskToken:    t.TaskToken,
		identity:     ath.identity,
		service:      ath.service,
		activityType: ActivityType{Name: *t.ActivityType.Name},
		activityID:   *t.ActivityId,
		workflowExecution: WorkflowExecution{
			RunID:      *t.WorkflowExecution.RunId,
			WorkflowID: *t.WorkflowExecution.WorkflowId},
	})
	activityType := *t.GetActivityType()
	activityImplementation, ok := ath.implementations[flowActivityTypeFrom(activityType)]
	if !ok {
		// Couldn't find the activity implementation.
		return nil, fmt.Errorf("No implementation for activityType=%v", activityType)
	}

	output, err := activityImplementation.Execute(ctx, t.GetInput())
	if err != nil {
		responseFailure := &s.RespondActivityTaskFailedRequest{
			TaskToken: t.TaskToken,
			Reason:    common.StringPtr(err.Reason()),
			Details:   err.Details(),
			Identity:  common.StringPtr(ath.identity)}
		return responseFailure, nil
	}

	responseComplete := &s.RespondActivityTaskCompletedRequest{
		TaskToken: t.TaskToken,
		Result_:   output,
		Identity:  common.StringPtr(ath.identity)}
	return responseComplete, nil
}

func createNewDecision(decisionType s.DecisionType) *s.Decision {
	return &s.Decision{
		DecisionType: common.DecisionTypePtr(decisionType),
	}
}
