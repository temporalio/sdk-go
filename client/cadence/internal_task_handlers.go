package cadence

// All code in this file is private to the package.

import (
	"fmt"
	"reflect"
	"time"

	"github.com/uber-common/bark"
	"github.com/uber-go/tally"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/cadence"
	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"golang.org/x/net/context"
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
		// Return List of decisions made, whether a decision is unhandled, any error.
		ProcessEvent(event *s.HistoryEvent) ([]*s.Decision, bool, error)
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
		metricsScope       tally.Scope
		ppMgr              pressurePointMgr
		logger             bark.Logger
	}

	// activityTaskHandlerImpl is the implementation of ActivityTaskHandler
	activityTaskHandlerImpl struct {
		taskListName    string
		identity        string
		implementations map[ActivityType]Activity
		service         m.TChanWorkflowService
		metricsScope    tally.Scope
		logger          bark.Logger
	}

	// history wrapper method to help information about events.
	history struct {
		workflowTask      *workflowTask
		eventsHandler     *workflowExecutionEventHandlerImpl
		currentIndex      int
		historyEventsSize int
	}
)

func newHistory(task *workflowTask, eventsHandler *workflowExecutionEventHandlerImpl) *history {
	return &history{
		workflowTask:      task,
		eventsHandler:     eventsHandler,
		currentIndex:      0,
		historyEventsSize: len(task.task.History.Events),
	}
}

// Get last non replayed event ID.
func (eh *history) LastNonReplayedID() int64 {
	if eh.workflowTask.task.PreviousStartedEventId == nil {
		return 0
	}
	return *eh.workflowTask.task.PreviousStartedEventId
}

func (eh *history) IsNextDecisionTimedOut(startIndex int) bool {
	events := eh.workflowTask.task.History.Events
	eventsSize := len(events)
	for i := startIndex; i < eventsSize; i++ {
		switch events[i].GetEventType() {
		case s.EventType_DecisionTaskCompleted:
			return false
		case s.EventType_DecisionTaskTimedOut:
			return true
		}
	}
	return false
}

func (eh *history) IsDecisionEvent(eventType s.EventType) bool {
	switch eventType {
	case s.EventType_WorkflowExecutionCompleted, s.EventType_WorkflowExecutionFailed, s.EventType_WorkflowExecutionTimedOut:
		return true
	case s.EventType_ActivityTaskScheduled, s.EventType_TimerStarted:
		return true
	default:
		return false
	}
}

func (eh *history) NextEvents() []*s.HistoryEvent {
	return eh.getNextEvents()
}

func (eh *history) getNextEvents() []*s.HistoryEvent {

	if eh.currentIndex == eh.historyEventsSize {
		return []*s.HistoryEvent{}
	}

	// Process events
	reorderedEvents := []*s.HistoryEvent{}
	history := eh.workflowTask.task.History

	// We need to re-order the events so the decider always sees in the same order.
	// For Ex: (pseudo code)
	//   ResultA := Schedule_Activity_A
	//   ResultB := Schedule_Activity_B
	//   if ResultB.IsReady() { panic error }
	//   ResultC := Schedule_Activity_C(ResultA)
	// If both A and B activities complete then we could have two different paths, Either Scheduling C (or) Panic'ing.
	// Workflow events:
	// 	Workflow_Start, DecisionStart1, DecisionComplete1, A_Schedule, B_Schedule, A_Complete,
	//      DecisionStart2, B_Complete, DecisionComplete2, C_Schedule.
	// B_Complete happened concurrent to execution of the decision(2), where C_Schedule is a result made by execution of decision(2).
	// One way to address is: Move all concurrent decisions to one after the decisions made by current decision.

	decisionStartToCompletionEvents := []*s.HistoryEvent{}
	decisionCompletionToStartEvents := []*s.HistoryEvent{}
	concurrentToDecision := true
	lastDecisionIndex := -1

OrderEvents:
	for ; eh.currentIndex < eh.historyEventsSize; eh.currentIndex++ {
		event := history.Events[eh.currentIndex]
		switch event.GetEventType() {
		case s.EventType_DecisionTaskStarted:
			if !eh.IsNextDecisionTimedOut(eh.currentIndex) {
				// Set replay clock.
				ts := time.Unix(0, event.GetTimestamp())
				eh.eventsHandler.workflowEnvironmentImpl.SetCurrentReplayTime(ts)
				eh.currentIndex++ // Sine we already processed the current event
				break OrderEvents
			}

		case s.EventType_DecisionTaskCompleted:
			concurrentToDecision = false

		case s.EventType_DecisionTaskScheduled, s.EventType_DecisionTaskTimedOut:
		// Skip

		default:
			if concurrentToDecision {
				decisionStartToCompletionEvents = append(decisionStartToCompletionEvents, event)
			} else {
				if eh.IsDecisionEvent(event.GetEventType()) {
					lastDecisionIndex = len(decisionCompletionToStartEvents)
				}
				decisionCompletionToStartEvents = append(decisionCompletionToStartEvents, event)
			}
		}
	}

	// Reorder events to correspond to the order that decider sees them.
	// The main difference is that events that were added during decision task execution
	// should be processed after events that correspond to the decisions.
	// Otherwise the replay is going to break.

	// First are events that correspond to the previous task decisions
	if lastDecisionIndex >= 0 {
		reorderedEvents = decisionCompletionToStartEvents[:lastDecisionIndex+1]
	}
	// Second are events that were added during previous task execution
	reorderedEvents = append(reorderedEvents, decisionStartToCompletionEvents...)
	// The last are events that were added after previous task completion
	if lastDecisionIndex+1 < len(decisionCompletionToStartEvents) {
		reorderedEvents = append(reorderedEvents, decisionCompletionToStartEvents[lastDecisionIndex+1:]...)
	}

	return reorderedEvents
}

// newWorkflowTaskHandler returns an implementation of workflow task handler.
func newWorkflowTaskHandler(taskListName string, identity string, factory workflowDefinitionFactory,
	logger bark.Logger, metricsScope tally.Scope, ppMgr pressurePointMgr) workflowTaskHandler {
	return &workflowTaskHandlerImpl{
		taskListName:       taskListName,
		identity:           identity,
		workflowDefFactory: factory,
		logger:             logger,
		ppMgr:              ppMgr,
		metricsScope:       metricsScope}
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
	var failure error

	completeHandler := func(result []byte, err error) {
		completionResult = result
		failure = err
		isWorkflowCompleted = true
	}

	eventHandler := newWorkflowExecutionEventHandler(
		workflowInfo, wth.workflowDefFactory, completeHandler, wth.logger)
	history := newHistory(workflowTask, eventHandler.(*workflowExecutionEventHandlerImpl))
	decisions := []*s.Decision{}
	unhandledDecision := false

	startTime := time.Now()

	// Process events
ProcessEvents:
	for {
		reorderedEvents := history.NextEvents()
		if len(reorderedEvents) == 0 {
			break ProcessEvents
		}

		for _, event := range reorderedEvents {
			wth.logger.Debugf("ProcessWorkflowTask: Id=%d, Event=%+v", event.GetEventId(), event)

			isInReplay := event.GetEventId() < history.LastNonReplayedID()

			// Any metrics.
			wth.reportAnyMetrics(event, isInReplay)

			// Any pressure points.
			err := wth.executeAnyPressurePoints(event, isInReplay)
			if err != nil {
				return nil, "", err
			}

			eventDecisions, unhandled, err := eventHandler.ProcessEvent(event)
			if err != nil {
				return nil, "", err
			}
			if unhandled {
				unhandledDecision = unhandled
			}

			if !isInReplay {
				if eventDecisions != nil {
					decisions = append(decisions, eventDecisions...)
				}
			}

			if isWorkflowCompleted {
				// If workflow is already completed then we can break from processing
				// further decisions.
				break ProcessEvents
			}
		}
	}

	eventDecisions := wth.completeWorkflow(isWorkflowCompleted, unhandledDecision, completionResult, failure)
	if len(eventDecisions) > 0 {
		decisions = append(decisions, eventDecisions...)

		if wth.metricsScope != nil {
			wth.metricsScope.Counter(metrics.WorkflowsCompletionTotalCounter).Inc(1)
			elapsed := time.Now().Sub(startTime)
			wth.metricsScope.Timer(metrics.WorkflowEndToEndLatency).Record(elapsed)
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

func (wth *workflowTaskHandlerImpl) completeWorkflow(isWorkflowCompleted bool, unhandledDecision bool, completionResult []byte,
	err error) []*s.Decision {
	decisions := []*s.Decision{}
	if !unhandledDecision {
		if err != nil {
			// Workflow failures
			failDecision := createNewDecision(s.DecisionType_FailWorkflowExecution)
			reason, details := getErrorDetails(err)
			failDecision.FailWorkflowExecutionDecisionAttributes = &s.FailWorkflowExecutionDecisionAttributes{
				Reason:  common.StringPtr(reason),
				Details: details,
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
	}
	return decisions
}

func (wth *workflowTaskHandlerImpl) executeAnyPressurePoints(event *s.HistoryEvent, isInReplay bool) error {
	if wth.ppMgr != nil && !reflect.ValueOf(wth.ppMgr).IsNil() && !isInReplay {
		switch event.GetEventType() {
		case s.EventType_DecisionTaskStarted:
			return wth.ppMgr.Execute(PressurePointTypeDecisionTaskStartTimeout)
		case s.EventType_ActivityTaskScheduled:
			return wth.ppMgr.Execute(PressurePointTypeActivityTaskScheduleTimeout)
		case s.EventType_ActivityTaskStarted:
			return wth.ppMgr.Execute(PressurePointTypeActivityTaskStartTimeout)
		case s.EventType_DecisionTaskCompleted:
			return wth.ppMgr.Execute(PressurePointTypeDecisionTaskCompleted)
		}
	}
	return nil
}

func (wth *workflowTaskHandlerImpl) reportAnyMetrics(event *s.HistoryEvent, isInReplay bool) {
	if wth.metricsScope != nil && !isInReplay {
		switch event.GetEventType() {
		case s.EventType_DecisionTaskTimedOut:
			wth.metricsScope.Counter(metrics.DecisionsTimeoutCounter).Inc(1)
		}
	}
}

func newActivityTaskHandler(taskListName string, identity string, activities []Activity,
	service m.TChanWorkflowService, logger bark.Logger, metricsScope tally.Scope) activityTaskHandler {
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
		metricsScope:    metricsScope}
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
			RunID: *t.WorkflowExecution.RunId,
			ID:    *t.WorkflowExecution.WorkflowId},
	})
	activityType := *t.GetActivityType()
	activityImplementation, ok := ath.implementations[flowActivityTypeFrom(activityType)]
	if !ok {
		// Couldn't find the activity implementation.
		return nil, fmt.Errorf("No implementation for activityType=%v", activityType.GetName())
	}

	output, err := activityImplementation.Execute(ctx, t.GetInput())
	if err != nil {
		reason, details := getErrorDetails(err)
		responseFailure := &s.RespondActivityTaskFailedRequest{
			TaskToken: t.TaskToken,
			Reason:    common.StringPtr(reason),
			Details:   details,
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
