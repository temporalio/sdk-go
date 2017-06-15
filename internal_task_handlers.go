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
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/uber-go/tally"
	m "go.uber.org/cadence/.gen/go/cadence"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/backoff"
	"go.uber.org/cadence/common/metrics"
	"go.uber.org/cadence/common/util"
	"go.uber.org/zap"
)

// interfaces
type (
	// workflowExecutionEventHandler process a single event.
	workflowExecutionEventHandler interface {
		// Process a single event and return the assosciated decisions.
		// Return List of decisions made, any error.
		ProcessEvent(event *s.HistoryEvent, isReplay bool, isLast bool) ([]*s.Decision, error)
		StackTrace() string
		// Close for cleaning up resources on this event handler
		Close()
	}

	workflowHistoryIterator func(nextToken []byte) (*s.History, []byte, error)
	// workflowTask wraps a decision task.
	workflowTask struct {
		task     *s.PollForDecisionTaskResponse
		iterator workflowHistoryIterator
	}

	// activityTask wraps a activity task.
	activityTask struct {
		task *s.PollForActivityTaskResponse
	}
)

type (
	// workflowTaskHandlerImpl is the implementation of WorkflowTaskHandler
	workflowTaskHandlerImpl struct {
		domain                string
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
		loadedEvents      []*s.HistoryEvent
		nextPageToken     []byte
		currentIndex      int
		historyEventsSize int
		next              []*s.HistoryEvent
	}
)

func newHistory(task *workflowTask, eventsHandler *workflowExecutionEventHandlerImpl) *history {
	result := &history{
		workflowTask:      task,
		eventsHandler:     eventsHandler,
		loadedEvents:      task.task.GetHistory().GetEvents(),
		nextPageToken:     task.task.GetNextPageToken(),
		currentIndex:      0,
		historyEventsSize: len(task.task.History.Events),
	}

	return result
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

func (eh *history) IsNextDecisionFailed() bool {
	events := eh.workflowTask.task.History.Events
	eventsSize := len(events)
	for i := eh.currentIndex; i < eventsSize; i++ {
		switch events[i].GetEventType() {
		case s.EventType_DecisionTaskCompleted:
			return false
		case s.EventType_DecisionTaskTimedOut:
			return true
		case s.EventType_DecisionTaskFailed:
			return true
		}
	}
	return false
}

func isDecisionEvent(eventType s.EventType) bool {
	switch eventType {
	case s.EventType_WorkflowExecutionCompleted,
		s.EventType_WorkflowExecutionFailed,
		s.EventType_WorkflowExecutionCanceled,
		s.EventType_WorkflowExecutionContinuedAsNew,
		s.EventType_ActivityTaskScheduled,
		s.EventType_ActivityTaskCancelRequested,
		s.EventType_TimerStarted,
		s.EventType_TimerCanceled,
		s.EventType_MarkerRecorded,
		s.EventType_StartChildWorkflowExecutionInitiated,
		s.EventType_RequestCancelExternalWorkflowExecutionInitiated:
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
// markers result value returns marker events that currently running decision produced. They are used to
// implement SideEffect method execution without blocking on decision roundtrip.
func (eh *history) NextDecisionEvents() (result []*s.HistoryEvent, markers []*s.HistoryEvent, err error) {
	if eh.next == nil {
		eh.next, _, err = eh.nextDecisionEvents()
		if err != nil {
			return result, markers, err
		}
	}

	result = eh.next
	if len(result) > 0 {
		eh.next, markers, err = eh.nextDecisionEvents()
	}
	return result, markers, err
}

func (eh *history) nextDecisionEvents() (reorderedEvents []*s.HistoryEvent, markers []*s.HistoryEvent, err error) {
	if eh.currentIndex == len(eh.loadedEvents) && eh.nextPageToken == nil {
		return []*s.HistoryEvent{}, []*s.HistoryEvent{}, nil
	}

	// Process events

	decisionStartToCompletionEvents := []*s.HistoryEvent{}
	decisionCompletionToStartEvents := []*s.HistoryEvent{}
	var decisionStartedEvent *s.HistoryEvent
	concurrentToDecision := true
	lastDecisionIndex := -1

OrderEvents:
	for {
		// load more history events if needed
		for eh.currentIndex == len(eh.loadedEvents) {
			if eh.nextPageToken == nil {
				break OrderEvents
			}
			historyPage, token, err1 := eh.workflowTask.iterator(eh.nextPageToken)
			if err1 != nil {
				err = err1
				return
			}
			eh.nextPageToken = token
			eh.loadedEvents = append(eh.loadedEvents, historyPage.GetEvents()...)
		}

		event := eh.loadedEvents[eh.currentIndex]
		switch event.GetEventType() {
		case s.EventType_DecisionTaskStarted:
			if !eh.IsNextDecisionFailed() {
				// Set replay clock.
				ts := time.Unix(0, event.GetTimestamp())
				eh.eventsHandler.workflowEnvironmentImpl.SetCurrentReplayTime(ts)
				eh.currentIndex++ // Since we already processed the current event
				decisionStartedEvent = event
				break OrderEvents
			}

		case s.EventType_DecisionTaskCompleted:
			concurrentToDecision = false

		case s.EventType_DecisionTaskScheduled, s.EventType_DecisionTaskTimedOut, s.EventType_DecisionTaskFailed:
		// Skip
		default:
			if concurrentToDecision {
				decisionStartToCompletionEvents = append(decisionStartToCompletionEvents, event)
			} else {
				if isDecisionEvent(event.GetEventType()) {
					lastDecisionIndex = len(decisionCompletionToStartEvents)
				}
				if event.GetEventType() == s.EventType_MarkerRecorded {
					markers = append(markers, event)
				}
				decisionCompletionToStartEvents = append(decisionCompletionToStartEvents, event)
			}
		}
		eh.currentIndex++
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
	return reorderedEvents, markers, nil
}

// newWorkflowTaskHandler returns an implementation of workflow task handler.
func newWorkflowTaskHandler(factory workflowDefinitionFactory, domain string,
	params workerExecutionParameters, ppMgr pressurePointMgr) WorkflowTaskHandler {
	return &workflowTaskHandlerImpl{
		workflowDefFactory:    factory,
		domain:                domain,
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
	currentTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(currentTime)
		if wth.metricsScope != nil {
			wth.metricsScope.Timer(metrics.DecisionsExecutionLatency).Record(deltaTime)
		}
	}()

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
		ExecutionStartToCloseTimeoutSeconds: attributes.GetExecutionStartToCloseTimeoutSeconds(),
		TaskStartToCloseTimeoutSeconds:      attributes.GetTaskStartToCloseTimeoutSeconds(),
		Domain: wth.domain,
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

	startTime := time.Now()

	// Process events
ProcessEvents:
	for {
		reorderedEvents, markers, err := reorderedHistory.NextDecisionEvents()
		if err != nil {
			return nil, "", err
		}

		if len(reorderedEvents) == 0 {
			break ProcessEvents
		}
		// Markers are from the events that are produced from the current decision
		for _, m := range markers {
			_, err := eventHandler.ProcessEvent(m, true, false)
			if err != nil {
				return nil, "", err
			}
		}
		isInReplay := reorderedEvents[0].GetEventId() < reorderedHistory.LastNonReplayedID()
		for i, event := range reorderedEvents {
			isLast := !isInReplay && i == len(reorderedEvents)-1
			if isDecisionEvent(event.GetEventType()) {
				respondEvents = append(respondEvents, event)
			}

			// Any metrics.
			wth.reportAnyMetrics(event, isInReplay)

			// Any pressure points.
			err := wth.executeAnyPressurePoints(event, isInReplay)
			if err != nil {
				return nil, "", err
			}

			eventDecisions, err := eventHandler.ProcessEvent(event, isInReplay, isLast)
			if err != nil {
				return nil, "", err
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

	if _, ok := failure.(PanicError); ok {
		// Timeout the Decision instead of failing workflow.
		// TODO: Pump this stack trace on to workflow history for debuggability by exposing decision type fail to client.
		return nil, "", failure
	}

	closeDecision := wth.completeWorkflow(isWorkflowCompleted, completionResult, failure, startAttributes)
	if closeDecision != nil {
		decisions = append(decisions, closeDecision)
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

	if enableVerboseLogging {
		var buf bytes.Buffer
		for i, d := range decisions {
			buf.WriteString(fmt.Sprintf("%v: %v\n", i, util.DecisionToString(d)))
		}
		wth.logger.Debug("new_decisions",
			zap.Int("DecisionCount", len(decisions)),
			zap.String("Decisions", buf.String()))
	}

	if emitStack {
		stackTrace = eventHandler.StackTrace()
	}
	return taskCompletionRequest, stackTrace, nil
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

	case s.DecisionType_RequestCancelExternalWorkflowExecution:
		if e.GetEventType() != s.EventType_RequestCancelExternalWorkflowExecutionInitiated {
			return false
		}
		eventAttributes := e.GetRequestCancelExternalWorkflowExecutionInitiatedEventAttributes()
		decisionAttributes := d.GetRequestCancelExternalWorkflowExecutionDecisionAttributes()
		if eventAttributes.GetDomain() != decisionAttributes.GetDomain() ||
			eventAttributes.GetWorkflowExecution().GetWorkflowId() != decisionAttributes.GetWorkflowId() ||
			eventAttributes.GetWorkflowExecution().GetRunId() != decisionAttributes.GetRunId() {
			return false
		}

		return true

	case s.DecisionType_CancelWorkflowExecution:
		if e.GetEventType() != s.EventType_WorkflowExecutionCanceled {
			return false
		}
		if strictMode {
			eventAttributes := e.GetWorkflowExecutionCanceledEventAttributes()
			decisionAttributes := d.GetCancelWorkflowExecutionDecisionAttributes()
			if bytes.Compare(eventAttributes.GetDetails(), decisionAttributes.GetDetails()) != 0 {
				return false
			}
		}
		return true

	case s.DecisionType_ContinueAsNewWorkflowExecution:
		if e.GetEventType() != s.EventType_WorkflowExecutionContinuedAsNew {
			return false
		}

		return true

	case s.DecisionType_StartChildWorkflowExecution:
		if e.GetEventType() != s.EventType_StartChildWorkflowExecutionInitiated {
			return false
		}
		eventAttributes := e.GetStartChildWorkflowExecutionInitiatedEventAttributes()
		decisionAttributes := d.GetStartChildWorkflowExecutionDecisionAttributes()
		if eventAttributes.GetDomain() != decisionAttributes.GetDomain() ||
			eventAttributes.GetTaskList().GetName() != decisionAttributes.GetTaskList().GetName() ||
			eventAttributes.GetWorkflowType().GetName() != decisionAttributes.GetWorkflowType().GetName() {
			return false
		}

		return true
	}

	return false
}

func (wth *workflowTaskHandlerImpl) completeWorkflow(
	isWorkflowCompleted bool,
	completionResult []byte,
	err error,
	startAttributes *s.WorkflowExecutionStartedEventAttributes,
) *s.Decision {
	var decision *s.Decision
	if canceledErr, ok := err.(*canceledError); ok {
		// Workflow cancelled
		decision = createNewDecision(s.DecisionType_CancelWorkflowExecution)
		decision.CancelWorkflowExecutionDecisionAttributes = &s.CancelWorkflowExecutionDecisionAttributes{
			Details: canceledErr.details,
		}
	} else if contErr, ok := err.(*continueAsNewError); ok {
		// Continue as new error.
		decision = createNewDecision(s.DecisionType_ContinueAsNewWorkflowExecution)
		decision.ContinueAsNewWorkflowExecutionDecisionAttributes = &s.ContinueAsNewWorkflowExecutionDecisionAttributes{
			WorkflowType: workflowTypePtr(*contErr.options.workflowType),
			Input:        contErr.options.input,
			TaskList:     common.TaskListPtr(s.TaskList{Name: contErr.options.taskListName}),
			ExecutionStartToCloseTimeoutSeconds: contErr.options.executionStartToCloseTimeoutSeconds,
			TaskStartToCloseTimeoutSeconds:      contErr.options.taskStartToCloseTimeoutSeconds,
		}
	} else if err != nil {
		// Workflow failures
		decision = createNewDecision(s.DecisionType_FailWorkflowExecution)
		reason, details := getErrorDetails(err)
		decision.FailWorkflowExecutionDecisionAttributes = &s.FailWorkflowExecutionDecisionAttributes{
			Reason:  common.StringPtr(reason),
			Details: details,
		}
	} else if isWorkflowCompleted {
		// Workflow completion
		decision = createNewDecision(s.DecisionType_CompleteWorkflowExecution)
		decision.CompleteWorkflowExecutionDecisionAttributes = &s.CompleteWorkflowExecutionDecisionAttributes{
			Result_: completionResult,
		}
	}
	return decision
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
		metricsScope:    params.MetricsScope,
		userContext:     params.UserContext,
	}
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
	startTime := time.Now()
	defer func() {
		deltaTime := time.Now().Sub(startTime)
		if ath.metricsScope != nil {
			ath.metricsScope.Timer(metrics.ActivityExecutionLatency).Record(deltaTime)
		}
	}()

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
			ath.logger.Error("Activity panic.",
				zap.String("PanicError", fmt.Sprintf("%v", p)),
				zap.String("PanicStack", st))
			panicErr := newPanicError(p, st)
			result, err = convertActivityResultToRespondRequest(ath.identity, t.TaskToken, nil, panicErr), nil
		}
	}()

	var deadline time.Time
	scheduleToCloseDeadline := time.Unix(0, t.GetScheduledTimestamp()).Add(time.Duration(t.GetScheduleToCloseTimeoutSeconds()) * time.Second)
	startToCloseDeadline := time.Unix(0, t.GetStartedTimestamp()).Add(time.Duration(t.GetStartToCloseTimeoutSeconds()) * time.Second)
	// Minimum of the two deadlines.
	if scheduleToCloseDeadline.Before(startToCloseDeadline) {
		deadline = scheduleToCloseDeadline
	} else {
		deadline = startToCloseDeadline
	}
	ctx, dlCancelFunc := context.WithDeadline(ctx, deadline)

	output, err := activityImplementation.Execute(ctx, t.GetInput())

	dlCancelFunc()
	if <-ctx.Done(); ctx.Err() == context.DeadlineExceeded {
		return nil, ctx.Err()
	}

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
			ctx, cancel := newTChannelContext()
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
