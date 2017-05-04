package cadence

// All code in this file is private to the package.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/cadence-client/common"
	"github.com/uber-go/cadence-client/common/backoff"
	"github.com/uber-go/cadence-client/common/metrics"
	"github.com/uber-go/cadence-client/common/util"
	"github.com/uber-go/tally"
	"go.uber.org/zap"
)

// interfaces
type (
	// workflowExecutionEventHandler process a single event.
	workflowExecutionEventHandler interface {
		// Process a single event and return the assosciated decisions.
		// Return List of decisions made, whether a decision is unhandled, any error.
		ProcessEvent(event *s.HistoryEvent, isReplay bool, isLast bool) ([]*s.Decision, bool, error)
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
	// workflowTaskHandlerImpl is the implementation of WorkflowTaskHandler
	workflowTaskHandlerImpl struct {
		workflowDefFactory    workflowDefinitionFactory
		metricsScope          tally.Scope
		ppMgr                 pressurePointMgr
		logger                *zap.Logger
		identity              string
		enableLoggingInReplay bool
	}

	// activityTaskHandlerImpl is the implementation of ActivityTaskHandler
	activityTaskHandlerImpl struct {
		taskListName    string
		identity        string
		implementations map[ActivityType]activity
		service         m.TChanWorkflowService
		metricsScope    tally.Scope
		logger          *zap.Logger
		userContext     context.Context
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

// Get workflow start attributes.
func (eh *history) GetWorkflowStartedAttr() (*s.WorkflowExecutionStartedEventAttributes, error) {
	events := eh.workflowTask.task.History.Events
	if len(events) == 0 || events[0].GetEventType() != s.EventType_WorkflowExecutionStarted {
		return nil, errors.New("unable to find WorkflowExecutionStartedEventAttributes in the history")
	}
	return events[0].WorkflowExecutionStartedEventAttributes, nil
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

// NextDecisionEvents returns events that there processed as new by the next decision.
// It also reorders events that were added to a history during outgoing decision. Without
// such reordering determinism is broken.
// For Ex: (pseudo code)
//   ResultA := Schedule_Activity_A
//   ResultB := Schedule_Activity_B
//   if ResultB.IsReady() { panic error }
//   ResultC := Schedule_Activity_C(ResultA)
// If both A and B activities complete then we could have two different paths, Either Scheduling C (or) Panic'ing.
// Workflow events:
// 	Workflow_Start, DecisionStart1, DecisionComplete1, A_Schedule, B_Schedule, A_Complete,
//      DecisionStart2, B_Complete, DecisionComplete2, C_Schedule.
// B_Complete happened concurrent to execution of the decision(2), where C_Schedule is a result made
// by execution of decision(2).
// To maintain determinism the concurrent decisions are moved to the one after the decisions made by current decision.
func (eh *history) NextDecisionEvents() []*s.HistoryEvent {
	if eh.currentIndex == eh.historyEventsSize {
		return []*s.HistoryEvent{}
	}

	// Process events
	reorderedEvents := []*s.HistoryEvent{}
	history := eh.workflowTask.task.History

	decisionStartToCompletionEvents := []*s.HistoryEvent{}
	decisionCompletionToStartEvents := []*s.HistoryEvent{}
	var decisionStartedEvent *s.HistoryEvent
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
				eh.currentIndex++ // Since we already processed the current event
				decisionStartedEvent = event
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
	if decisionStartedEvent != nil {
		reorderedEvents = append(reorderedEvents, decisionStartedEvent)
	}
	return reorderedEvents
}

// newWorkflowTaskHandler returns an implementation of workflow task handler.
func newWorkflowTaskHandler(factory workflowDefinitionFactory,
	params workerExecutionParameters, ppMgr pressurePointMgr) WorkflowTaskHandler {
	return &workflowTaskHandlerImpl{
		workflowDefFactory:    factory,
		logger:                params.Logger,
		ppMgr:                 ppMgr,
		metricsScope:          params.MetricsScope,
		identity:              params.Identity,
		enableLoggingInReplay: params.EnableLoggingInReplay,
	}
}

// ProcessWorkflowTask processes each all the events of the workflow task.
func (wth *workflowTaskHandlerImpl) ProcessWorkflowTask(
	task *s.PollForDecisionTaskResponse,
	emitStack bool,
) (result *s.RespondDecisionTaskCompletedRequest, stackTrace string, err error) {
	if task == nil {
		return nil, "", errors.New("nil workflowtask provided")
	}
	h := task.GetHistory()
	if h == nil || len(h.Events) == 0 {
		return nil, "", errors.New("nil or empty history")
	}
	event := h.Events[0]
	if h == nil {
		return nil, "", errors.New("nil first history event")
	}
	attributes := event.GetWorkflowExecutionStartedEventAttributes()
	if attributes == nil {
		return nil, "", errors.New("first history event is not WorkflowExecutionStarted")
	}
	taskList := attributes.GetTaskList()
	if taskList == nil {
		return nil, "", errors.New("nil TaskList in WorkflowExecutionStarted event")
	}

	wth.logger.Debug("Processing new workflow task.",
		zap.String(tagWorkflowType, task.GetWorkflowType().GetName()),
		zap.String(tagWorkflowID, task.GetWorkflowExecution().GetWorkflowId()),
		zap.String(tagRunID, task.GetWorkflowExecution().GetRunId()),
		zap.Int64("PreviousStartedEventId", task.GetPreviousStartedEventId()))

	// Setup workflow Info
	workflowInfo := &WorkflowInfo{
		WorkflowType: flowWorkflowTypeFrom(*task.WorkflowType),
		TaskListName: taskList.GetName(),
		WorkflowExecution: WorkflowExecution{
			ID:    *task.WorkflowExecution.WorkflowId,
			RunID: *task.WorkflowExecution.RunId,
		},
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
		workflowInfo, wth.workflowDefFactory, completeHandler, wth.logger, wth.enableLoggingInReplay)
	defer eventHandler.Close()
	reorderedHistory := newHistory(&workflowTask{task: task}, eventHandler.(*workflowExecutionEventHandlerImpl))
	decisions := []*s.Decision{}
	replayDecisions := []*s.Decision{}
	respondEvents := []*s.HistoryEvent{}
	unhandledDecision := false

	startTime := time.Now()

	// Process events
ProcessEvents:
	for {
		reorderedEvents := reorderedHistory.NextDecisionEvents()

		if len(reorderedEvents) == 0 {
			break ProcessEvents
		}
		isInReplay := reorderedEvents[0].GetEventId() < reorderedHistory.LastNonReplayedID()
		for i, event := range reorderedEvents {
			isLast := !isInReplay && i == len(reorderedEvents)-1
			if isEventTypeRespondToDecision(event.GetEventType()) {
				respondEvents = append(respondEvents, event)
			}

			// Any metrics.
			wth.reportAnyMetrics(event, isInReplay)

			// Any pressure points.
			err := wth.executeAnyPressurePoints(event, isInReplay)
			if err != nil {
				return nil, "", err
			}

			eventDecisions, unhandled, err := eventHandler.ProcessEvent(event, isInReplay, isLast)
			if err != nil {
				return nil, "", err
			}
			if unhandled {
				unhandledDecision = unhandled
			}

			if eventDecisions != nil {
				if !isInReplay {
					decisions = append(decisions, eventDecisions...)
				} else {
					replayDecisions = append(replayDecisions, eventDecisions...)
				}
			}

			if isWorkflowCompleted {
				// If workflow is already completed then we can break from processing
				// further decisions.
				break ProcessEvents
			}
		}
	}
	// check if decisions from reply matches to the history events
	if err := matchReplayWithHistory(replayDecisions, respondEvents); err != nil {
		wth.logger.Error("Replay and history mismatch.", zap.Error(err))
		return nil, "", err
	}

	startAttributes, err := reorderedHistory.GetWorkflowStartedAttr()
	if err != nil {
		wth.logger.Error("Unable to read workflow start attributes.", zap.Error(err))
		return nil, "", err
	}
	eventDecisions, err := wth.completeWorkflow(
		isWorkflowCompleted, unhandledDecision, completionResult, failure, startAttributes)
	if err != nil {
		wth.logger.Error("Complete workflow failed.", zap.Error(err))
		return nil, "", err
	}
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
		TaskToken: task.TaskToken,
		Decisions: decisions,
		Identity:  common.StringPtr(wth.identity),
		// ExecutionContext:
	}
	if emitStack {
		stackTrace = eventHandler.StackTrace()
	}
	return taskCompletionRequest, stackTrace, nil
}

// for every decision, there is one EventType respond to that decision.
func isEventTypeRespondToDecision(t s.EventType) bool {
	switch t {
	case s.EventType_ActivityTaskScheduled:
		return true
	case s.EventType_ActivityTaskCancelRequested:
		return true
	case s.EventType_TimerStarted:
		return true
	case s.EventType_TimerCanceled:
		return true
	case s.EventType_WorkflowExecutionCompleted:
		return true
	case s.EventType_WorkflowExecutionFailed:
		return true
	case s.EventType_MarkerRecorded:
		return true
	default:
		return false
	}
}

func matchReplayWithHistory(replayDecisions []*s.Decision, historyEvents []*s.HistoryEvent) error {
	for i := 0; i < len(historyEvents); i++ {
		e := historyEvents[i]
		if i >= len(replayDecisions) {
			return fmt.Errorf("nondeterministic workflow: missing replay decision for %s", util.HistoryEventToString(e))
		}
		d := replayDecisions[i]
		if !isDecisionMatchEvent(d, e, false) {
			return fmt.Errorf("nondeterministic workflow: history event is %s, replay decision is %s",
				util.HistoryEventToString(e), util.DecisionToString(d))
		}
	}
	return nil
}

func isDecisionMatchEvent(d *s.Decision, e *s.HistoryEvent, strictMode bool) bool {
	switch d.GetDecisionType() {
	case s.DecisionType_ScheduleActivityTask:
		if e.GetEventType() != s.EventType_ActivityTaskScheduled {
			return false
		}
		eventAttributes := e.GetActivityTaskScheduledEventAttributes()
		decisionAttributes := d.GetScheduleActivityTaskDecisionAttributes()

		if eventAttributes.GetActivityId() != decisionAttributes.GetActivityId() ||
			eventAttributes.GetActivityType().GetName() != decisionAttributes.GetActivityType().GetName() ||
			(strictMode && eventAttributes.GetTaskList().GetName() != decisionAttributes.GetTaskList().GetName()) ||
			(strictMode && bytes.Compare(eventAttributes.GetInput(), decisionAttributes.GetInput()) != 0) {
			return false
		}

		return true

	case s.DecisionType_RequestCancelActivityTask:
		if e.GetEventType() != s.EventType_ActivityTaskCancelRequested {
			return false
		}
		eventAttributes := e.GetActivityTaskCancelRequestedEventAttributes()
		decisionAttributes := d.GetRequestCancelActivityTaskDecisionAttributes()

		if eventAttributes.GetActivityId() != decisionAttributes.GetActivityId() {
			return false
		}

		return true

	case s.DecisionType_StartTimer:
		if e.GetEventType() != s.EventType_TimerStarted {
			return false
		}
		eventAttributes := e.GetTimerStartedEventAttributes()
		decisionAttributes := d.GetStartTimerDecisionAttributes()

		if eventAttributes.GetTimerId() != decisionAttributes.GetTimerId() ||
			eventAttributes.GetStartToFireTimeoutSeconds() != decisionAttributes.GetStartToFireTimeoutSeconds() {
			return false
		}

		return true

	case s.DecisionType_CancelTimer:
		if e.GetEventType() != s.EventType_TimerCanceled {
			return false
		}
		eventAttributes := e.GetTimerCanceledEventAttributes()
		decisionAttributes := d.GetCancelTimerDecisionAttributes()

		if eventAttributes.GetTimerId() != decisionAttributes.GetTimerId() {
			return false
		}

		return true

	case s.DecisionType_CompleteWorkflowExecution:
		if e.GetEventType() != s.EventType_WorkflowExecutionCompleted {
			return false
		}
		if strictMode {
			eventAttributes := e.GetWorkflowExecutionCompletedEventAttributes()
			decisionAttributes := d.GetCompleteWorkflowExecutionDecisionAttributes()

			if bytes.Compare(eventAttributes.GetResult_(), decisionAttributes.GetResult_()) != 0 {
				return false
			}
		}

		return true

	case s.DecisionType_FailWorkflowExecution:
		if e.GetEventType() != s.EventType_WorkflowExecutionFailed {
			return false
		}
		if strictMode {
			eventAttributes := e.GetWorkflowExecutionFailedEventAttributes()
			decisionAttributes := d.GetFailWorkflowExecutionDecisionAttributes()

			if eventAttributes.GetReason() != decisionAttributes.GetReason() ||
				bytes.Compare(eventAttributes.GetDetails(), decisionAttributes.GetDetails()) != 0 {
				return false
			}
		}

		return true

	case s.DecisionType_RecordMarker:
		if e.GetEventType() != s.EventType_MarkerRecorded {
			return false
		}
		eventAttributes := e.GetMarkerRecordedEventAttributes()
		decisionAttributes := d.GetRecordMarkerDecisionAttributes()
		if eventAttributes.GetMarkerName() != decisionAttributes.GetMarkerName() {
			return false
		}

		return true
	}

	return false
}

func (wth *workflowTaskHandlerImpl) completeWorkflow(
	isWorkflowCompleted bool, unhandledDecision bool, completionResult []byte,
	err error, startAttributes *s.WorkflowExecutionStartedEventAttributes) ([]*s.Decision, error) {
	decisions := []*s.Decision{}
	if !unhandledDecision {
		if contErr, ok := err.(*continueAsNewError); ok {
			// Continue as new error.

			// Get workflow start attributes.
			// task list name.
			var taskListName string
			if contErr.options.taskListName != nil {
				taskListName = *contErr.options.taskListName
			} else {
				taskListName = startAttributes.TaskList.GetName()
			}

			// timeouts.
			var executionStartToCloseTimeoutSeconds, taskStartToCloseTimeoutSeconds int32
			if contErr.options.executionStartToCloseTimeoutSeconds != nil {
				executionStartToCloseTimeoutSeconds = *contErr.options.executionStartToCloseTimeoutSeconds
			} else {
				executionStartToCloseTimeoutSeconds = startAttributes.GetExecutionStartToCloseTimeoutSeconds()
			}
			if contErr.options.taskStartToCloseTimeoutSeconds != nil {
				taskStartToCloseTimeoutSeconds = *contErr.options.taskStartToCloseTimeoutSeconds
			} else {
				taskStartToCloseTimeoutSeconds = startAttributes.GetTaskStartToCloseTimeoutSeconds()
			}

			continueAsNewDecision := createNewDecision(s.DecisionType_ContinueAsNewWorkflowExecution)
			continueAsNewDecision.ContinueAsNewWorkflowExecutionDecisionAttributes = &s.ContinueAsNewWorkflowExecutionDecisionAttributes{
				WorkflowType: workflowTypePtr(*contErr.options.workflowType),
				Input:        contErr.options.input,
				TaskList:     common.TaskListPtr(s.TaskList{Name: common.StringPtr(taskListName)}),
				ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(executionStartToCloseTimeoutSeconds),
				TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(taskStartToCloseTimeoutSeconds),
			}
			decisions = append(decisions, continueAsNewDecision)
		} else if err != nil {
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
	return decisions, nil
}

func (wth *workflowTaskHandlerImpl) executeAnyPressurePoints(event *s.HistoryEvent, isInReplay bool) error {
	if wth.ppMgr != nil && !reflect.ValueOf(wth.ppMgr).IsNil() && !isInReplay {
		switch event.GetEventType() {
		case s.EventType_DecisionTaskStarted:
			return wth.ppMgr.Execute(pressurePointTypeDecisionTaskStartTimeout)
		case s.EventType_ActivityTaskScheduled:
			return wth.ppMgr.Execute(pressurePointTypeActivityTaskScheduleTimeout)
		case s.EventType_ActivityTaskStarted:
			return wth.ppMgr.Execute(pressurePointTypeActivityTaskStartTimeout)
		case s.EventType_DecisionTaskCompleted:
			return wth.ppMgr.Execute(pressurePointTypeDecisionTaskCompleted)
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

func newActivityTaskHandler(activities []activity,
	service m.TChanWorkflowService, params workerExecutionParameters) ActivityTaskHandler {
	implementations := make(map[ActivityType]activity)
	for _, a := range activities {
		implementations[a.ActivityType()] = a
	}
	return &activityTaskHandlerImpl{
		taskListName:    params.TaskList,
		identity:        params.Identity,
		implementations: implementations,
		service:         service,
		logger:          params.Logger,
		metricsScope:    params.MetricsScope}
}

type cadenceInvoker struct {
	identity      string
	service       m.TChanWorkflowService
	taskToken     []byte
	cancelHandler func()
	retryPolicy   backoff.RetryPolicy
}

func (i *cadenceInvoker) Heartbeat(details []byte) error {
	err := recordActivityHeartbeat(i.service, i.identity, i.taskToken, details, i.retryPolicy)

	switch err.(type) {
	case CanceledError:
		// We are asked to cancel. inform the activity about cancellation through context.
		// We are asked to cancel. inform the activity about cancellation through context.
		i.cancelHandler()

	case *s.EntityNotExistsError:
		// We will pass these through as cancellation for now but something we can change
		// later when we have setter on cancel handler.
		i.cancelHandler()
	}

	// We don't want to bubble temporary errors to the user.
	// This error won't be return to user check RecordActivityHeartbeat().
	return err
}

func newServiceInvoker(
	taskToken []byte,
	identity string,
	service m.TChanWorkflowService,
	cancelHandler func(),
) ServiceInvoker {
	return &cadenceInvoker{
		taskToken:     taskToken,
		identity:      identity,
		service:       service,
		cancelHandler: cancelHandler,
		retryPolicy:   serviceOperationRetryPolicy,
	}
}

// Execute executes an implementation of the activity.
func (ath *activityTaskHandlerImpl) Execute(t *s.PollForActivityTaskResponse) (result interface{}, err error) {
	ath.logger.Debug("Processing new activity task",
		zap.String(tagWorkflowID, t.GetWorkflowExecution().GetWorkflowId()),
		zap.String(tagRunID, t.GetWorkflowExecution().GetRunId()),
		zap.String(tagActivityType, t.GetActivityType().GetName()))

	rootCtx := ath.userContext
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	canCtx, cancel := context.WithCancel(rootCtx)
	invoker := newServiceInvoker(t.TaskToken, ath.identity, ath.service, cancel)
	ctx := WithActivityTask(canCtx, t, invoker, ath.logger)
	activityType := *t.GetActivityType()
	activityImplementation, ok := ath.implementations[flowActivityTypeFrom(activityType)]
	if !ok {
		// Couldn't find the activity implementation.
		return nil, fmt.Errorf("No implementation for activityType=%v", activityType.GetName())
	}

	// panic handler
	defer func() {
		if p := recover(); p != nil {
			topLine := fmt.Sprintf("activity for %s [panic]:", ath.taskListName)
			st := getStackTraceRaw(topLine, 7, 0)
			ath.logger.Error("Activity panic.", zap.String("PanicStack", st))
			panicErr := newPanicError(p, st)
			result, err = convertActivityResultToRespondRequest(ath.identity, t.TaskToken, nil, panicErr), nil
		}
	}()

	output, err := activityImplementation.Execute(ctx, t.GetInput())
	return convertActivityResultToRespondRequest(ath.identity, t.TaskToken, output, err), nil
}

func createNewDecision(decisionType s.DecisionType) *s.Decision {
	return &s.Decision{
		DecisionType: common.DecisionTypePtr(decisionType),
	}
}

func recordActivityHeartbeat(
	service m.TChanWorkflowService,
	identity string,
	taskToken, details []byte,
	retryPolicy backoff.RetryPolicy,
) error {
	request := &s.RecordActivityTaskHeartbeatRequest{
		TaskToken: taskToken,
		Details:   details,
		Identity:  common.StringPtr(identity)}

	var heartbeatResponse *s.RecordActivityTaskHeartbeatResponse
	heartbeatErr := backoff.Retry(
		func() error {
			ctx, cancel := common.NewTChannelContext(respondTaskServiceTimeOut, common.RetryDefaultOptions)
			defer cancel()

			var err error
			heartbeatResponse, err = service.RecordActivityTaskHeartbeat(ctx, request)
			return err
		}, retryPolicy, isServiceTransientError)

	if heartbeatErr == nil && heartbeatResponse != nil && heartbeatResponse.GetCancelRequested() {
		return NewCanceledError()
	}

	return heartbeatErr
}
