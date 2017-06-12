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

import (
	"fmt"

	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/util"
)

type (
	decisionState int32
	decisionType  int32

	decisionID struct {
		decisionType decisionType
		id           string
	}

	decisionStateMachine interface {
		getState() decisionState
		getID() decisionID
		isDone() bool
		getDecision() *s.Decision // return nil if there is no decision in current state
		cancel()

		handleStartedEvent()
		handleCancelInitiatedEvent()
		handleCanceledEvent()
		handleCancelFailedEvent()
		handleCompletionEvent()
		handleInitiationFailedEvent()
		handleInitiatedEvent()

		handleDecisionSent()

		setData(data interface{})
		getData() interface{}
	}

	decisionStateMachineBase struct {
		id      decisionID
		state   decisionState
		history []string
		data    interface{}
	}

	activityDecisionStateMachine struct {
		*decisionStateMachineBase
		attributes *s.ScheduleActivityTaskDecisionAttributes
	}

	timerDecisionStateMachine struct {
		*decisionStateMachineBase
		attributes *s.StartTimerDecisionAttributes
		canceled   bool
	}

	childWorkflowDecisionStateMachine struct {
		*decisionStateMachineBase
		attributes *s.StartChildWorkflowExecutionDecisionAttributes
		runID      string
	}

	naiveDecisionStateMachine struct {
		*decisionStateMachineBase
		decision *s.Decision
	}

	// only possible state transition is: CREATED->SENT->INITIATED->COMPLETED
	cancelExternalWorkflowDecisionStateMachine struct {
		*naiveDecisionStateMachine
	}

	// only possible state transition is: CREATED->SENT->COMPLETED
	markerDecisionStateMachine struct {
		*naiveDecisionStateMachine
	}

	decisionsHelper struct {
		orderedDecisions []decisionStateMachine
		decisions        map[decisionID]decisionStateMachine

		scheduledEventIDToActivityID map[int64]string
	}
)

const (
	decisionStateCreated                                decisionState = 0
	decisionStateDecisionSent                           decisionState = 1
	decisionStateCanceledBeforeInitiated                decisionState = 2
	decisionStateInitiated                              decisionState = 3
	decisionStateStarted                                decisionState = 4
	decisionStateCanceledAfterInitiated                 decisionState = 5
	decisionStateCanceledAfterStarted                   decisionState = 6
	decisionStateCancellationDecisionSent               decisionState = 7
	decisionStateCompletedAfterCancellationDecisionSent decisionState = 8
	decisionStateCompleted                              decisionState = 9
)

const (
	decisionTypeActivity         decisionType = 0
	decisionTypeChildWorkflow    decisionType = 1
	decisionTypeExternalWorkflow decisionType = 2
	decisionTypeMarker           decisionType = 3
	decisionTypeTimer            decisionType = 4
)

const (
	eventCancel           string = "cancel"
	eventDecisionSent     string = "handleDecisionSent"
	eventInitiated        string = "handleInitiatedEvent"
	eventInitiationFailed string = "handleInitiationFailedEvent"
	eventStarted          string = "handleStartedEvent"
	eventCompletion       string = "handleCompletionEvent"
	eventCancelInitiated  string = "handleCancelInitiatedEvent"
	eventCancelFailed     string = "handleCancelFailedEvent"
	eventCanceled         string = "handleCanceledEvent"
)

func (d decisionState) String() string {
	switch d {
	case decisionStateCreated:
		return "Created"
	case decisionStateDecisionSent:
		return "DecisionSent"
	case decisionStateCanceledBeforeInitiated:
		return "CanceledBeforeInitiated"
	case decisionStateInitiated:
		return "Initiated"
	case decisionStateStarted:
		return "Started"
	case decisionStateCanceledAfterInitiated:
		return "CanceledAfterInitiated"
	case decisionStateCanceledAfterStarted:
		return "CanceledAfterStarted"
	case decisionStateCancellationDecisionSent:
		return "CancellationDecisionSent"
	case decisionStateCompletedAfterCancellationDecisionSent:
		return "CompletedAfterCancellationDecisionSent"
	case decisionStateCompleted:
		return "Completed"
	default:
		return "Unknown"
	}
}

func (d decisionType) String() string {
	switch d {
	case decisionTypeActivity:
		return "Activity"
	case decisionTypeChildWorkflow:
		return "ChildWorkflow"
	case decisionTypeExternalWorkflow:
		return "ExternalWorkflow"
	case decisionTypeMarker:
		return "Marker"
	case decisionTypeTimer:
		return "Timer"
	default:
		return "Unknown"
	}
}

func (d decisionID) String() string {
	return fmt.Sprintf("DecisionType: %v, ID: %v", d.decisionType, d.id)
}

func makeDecisionID(decisionType decisionType, id string) decisionID {
	return decisionID{decisionType: decisionType, id: id}
}

func newDecisionStateMachineBase(decisionType decisionType, id string) *decisionStateMachineBase {
	return &decisionStateMachineBase{
		id:      makeDecisionID(decisionType, id),
		state:   decisionStateCreated,
		history: []string{decisionStateCreated.String()},
	}
}

func newActivityDecisionStateMachine(attributes *s.ScheduleActivityTaskDecisionAttributes) *activityDecisionStateMachine {
	base := newDecisionStateMachineBase(decisionTypeActivity, attributes.GetActivityId())
	return &activityDecisionStateMachine{
		decisionStateMachineBase: base,
		attributes:               attributes,
	}
}

func newTimerDecisionStateMachine(attributes *s.StartTimerDecisionAttributes) *timerDecisionStateMachine {
	base := newDecisionStateMachineBase(decisionTypeTimer, attributes.GetTimerId())
	return &timerDecisionStateMachine{
		decisionStateMachineBase: base,
		attributes:               attributes,
	}
}

func newChildWorkflowDecisionStateMachine(attributes *s.StartChildWorkflowExecutionDecisionAttributes) *childWorkflowDecisionStateMachine {
	base := newDecisionStateMachineBase(decisionTypeChildWorkflow, attributes.GetWorkflowId())
	return &childWorkflowDecisionStateMachine{
		decisionStateMachineBase: base,
		attributes:               attributes,
	}
}

func newNaiveDecisionStateMachine(decisionType decisionType, id string, decision *s.Decision) *naiveDecisionStateMachine {
	base := newDecisionStateMachineBase(decisionType, id)
	return &naiveDecisionStateMachine{
		decisionStateMachineBase: base,
		decision:                 decision,
	}
}

func newMarkerDecisionStateMachine(id string, attributes *s.RecordMarkerDecisionAttributes) *markerDecisionStateMachine {
	d := createNewDecision(s.DecisionType_RecordMarker)
	d.RecordMarkerDecisionAttributes = attributes
	return &markerDecisionStateMachine{
		naiveDecisionStateMachine: newNaiveDecisionStateMachine(decisionTypeMarker, id, d),
	}
}

func newCancelExternalWorkflowStateMachine(attributes *s.RequestCancelExternalWorkflowExecutionDecisionAttributes) *cancelExternalWorkflowDecisionStateMachine {
	d := createNewDecision(s.DecisionType_RequestCancelExternalWorkflowExecution)
	d.RequestCancelExternalWorkflowExecutionDecisionAttributes = attributes
	return &cancelExternalWorkflowDecisionStateMachine{
		naiveDecisionStateMachine: newNaiveDecisionStateMachine(decisionTypeExternalWorkflow, attributes.GetWorkflowId(), d),
	}
}

func (d *decisionStateMachineBase) getState() decisionState {
	return d.state
}

func (d *decisionStateMachineBase) getID() decisionID {
	return d.id
}

func (d *decisionStateMachineBase) isDone() bool {
	return d.state == decisionStateCompleted || d.state == decisionStateCompletedAfterCancellationDecisionSent
}

func (d *decisionStateMachineBase) setData(data interface{}) {
	d.data = data
}

func (d *decisionStateMachineBase) getData() interface{} {
	return d.data
}

func (d *decisionStateMachineBase) moveState(newState decisionState, event string) {
	d.history = append(d.history, event)
	d.state = newState
	d.history = append(d.history, newState.String())
}

func (d *decisionStateMachineBase) failStateTransition(event string) {
	// this is when we detect illegal state transition, likely due to ill history sequence or nondeterministic decider code
	panic(fmt.Sprintf("invalid state transition: attempt to %v, %v", event, d))
}

func (d *decisionStateMachineBase) handleDecisionSent() {
	switch d.state {
	case decisionStateCreated:
		d.moveState(decisionStateDecisionSent, eventDecisionSent)
	}
}

func (d *decisionStateMachineBase) cancel() {
	switch d.state {
	case decisionStateCompleted, decisionStateCompletedAfterCancellationDecisionSent:
		// No op. This is legit. People could cancel context after timer/activity is done.
	case decisionStateCreated:
		d.moveState(decisionStateCompleted, eventCancel)
	case decisionStateDecisionSent:
		d.moveState(decisionStateCanceledBeforeInitiated, eventCancel)
	case decisionStateInitiated:
		d.moveState(decisionStateCanceledAfterInitiated, eventCancel)
	default:
		d.failStateTransition(eventCancel)
	}
}

func (d *decisionStateMachineBase) handleInitiatedEvent() {
	switch d.state {
	case decisionStateDecisionSent:
		d.moveState(decisionStateInitiated, eventInitiated)
	case decisionStateCanceledBeforeInitiated:
		d.moveState(decisionStateCanceledAfterInitiated, eventInitiated)
	default:
		d.failStateTransition(eventInitiated)
	}
}

func (d *decisionStateMachineBase) handleInitiationFailedEvent() {
	switch d.state {
	case decisionStateInitiated, decisionStateDecisionSent, decisionStateCanceledBeforeInitiated:
		d.moveState(decisionStateCompleted, eventInitiationFailed)
	default:
		d.failStateTransition(eventInitiationFailed)
	}
}

func (d *decisionStateMachineBase) handleStartedEvent() {
	d.history = append(d.history, eventStarted)
}

func (d *decisionStateMachineBase) handleCompletionEvent() {
	switch d.state {
	case decisionStateCanceledAfterInitiated, decisionStateInitiated:
		d.moveState(decisionStateCompleted, eventCompletion)
	case decisionStateCancellationDecisionSent:
		d.moveState(decisionStateCompletedAfterCancellationDecisionSent, eventCompletion)
	default:
		d.failStateTransition(eventCompletion)
	}
}

func (d *decisionStateMachineBase) handleCancelInitiatedEvent() {
	d.history = append(d.history, eventCancelInitiated)
	switch d.state {
	case decisionStateCancellationDecisionSent:
	// No state change
	default:
		d.failStateTransition(eventCancelInitiated)
	}
}

func (d *decisionStateMachineBase) handleCancelFailedEvent() {
	switch d.state {
	case decisionStateCompletedAfterCancellationDecisionSent:
		d.moveState(decisionStateCompleted, eventCancelFailed)
	default:
		d.failStateTransition(eventCancelFailed)
	}
}

func (d *decisionStateMachineBase) handleCanceledEvent() {
	switch d.state {
	case decisionStateCancellationDecisionSent:
		d.moveState(decisionStateCompleted, eventCanceled)
	default:
		d.failStateTransition(eventCanceled)
	}
}

func (d *decisionStateMachineBase) String() string {
	return fmt.Sprintf("%v, state=%v, isDone()=%v, history=%v",
		d.id, d.state, d.isDone(), d.history)
}

func (d *activityDecisionStateMachine) getDecision() *s.Decision {
	switch d.state {
	case decisionStateCreated:
		decision := createNewDecision(s.DecisionType_ScheduleActivityTask)
		decision.ScheduleActivityTaskDecisionAttributes = d.attributes
		return decision
	case decisionStateCanceledAfterInitiated:
		decision := createNewDecision(s.DecisionType_RequestCancelActivityTask)
		decision.RequestCancelActivityTaskDecisionAttributes = &s.RequestCancelActivityTaskDecisionAttributes{
			ActivityId: d.attributes.ActivityId,
		}
		return decision
	default:
		return nil
	}
}

func (d *activityDecisionStateMachine) handleDecisionSent() {
	switch d.state {
	case decisionStateCanceledAfterInitiated:
		d.moveState(decisionStateCancellationDecisionSent, eventDecisionSent)
	default:
		d.decisionStateMachineBase.handleDecisionSent()
	}
}

func (d *activityDecisionStateMachine) handleCancelFailedEvent() {
	switch d.state {
	case decisionStateCancellationDecisionSent:
		d.moveState(decisionStateInitiated, eventCancelFailed)
	default:
		d.decisionStateMachineBase.handleCancelFailedEvent()
	}
}

func (d *timerDecisionStateMachine) cancel() {
	d.canceled = true
	d.decisionStateMachineBase.cancel()
}

func (d *timerDecisionStateMachine) isDone() bool {
	return d.state == decisionStateCompleted || d.canceled
}

func (d *timerDecisionStateMachine) handleDecisionSent() {
	switch d.state {
	case decisionStateCanceledAfterInitiated:
		d.moveState(decisionStateCancellationDecisionSent, eventDecisionSent)
	default:
		d.decisionStateMachineBase.handleDecisionSent()
	}
}

func (d *timerDecisionStateMachine) handleCancelFailedEvent() {
	switch d.state {
	case decisionStateCancellationDecisionSent:
		d.moveState(decisionStateInitiated, eventCancelFailed)
	default:
		d.decisionStateMachineBase.handleCancelFailedEvent()
	}
}

func (d *timerDecisionStateMachine) getDecision() *s.Decision {
	switch d.state {
	case decisionStateCreated:
		decision := createNewDecision(s.DecisionType_StartTimer)
		decision.StartTimerDecisionAttributes = d.attributes
		return decision
	case decisionStateCanceledAfterInitiated:
		decision := createNewDecision(s.DecisionType_CancelTimer)
		decision.CancelTimerDecisionAttributes = &s.CancelTimerDecisionAttributes{
			TimerId: d.attributes.TimerId,
		}
		return decision
	default:
		return nil
	}
}

func (d *childWorkflowDecisionStateMachine) getDecision() *s.Decision {
	switch d.state {
	case decisionStateCreated:
		decision := createNewDecision(s.DecisionType_StartChildWorkflowExecution)
		decision.StartChildWorkflowExecutionDecisionAttributes = d.attributes
		return decision
	case decisionStateCanceledAfterStarted:
		decision := createNewDecision(s.DecisionType_RequestCancelExternalWorkflowExecution)
		decision.RequestCancelExternalWorkflowExecutionDecisionAttributes = &s.RequestCancelExternalWorkflowExecutionDecisionAttributes{
			Domain:     d.attributes.Domain,
			WorkflowId: d.attributes.WorkflowId,
			RunId:      common.StringPtr(d.runID),
		}
		return decision
	default:
		return nil
	}
}

func (d *childWorkflowDecisionStateMachine) handleDecisionSent() {
	switch d.state {
	case decisionStateCanceledAfterStarted:
		d.moveState(decisionStateCancellationDecisionSent, eventDecisionSent)
	default:
		d.decisionStateMachineBase.handleDecisionSent()
	}
}

func (d *childWorkflowDecisionStateMachine) handleStartedEvent() {
	switch d.state {
	case decisionStateInitiated:
		d.moveState(decisionStateStarted, eventStarted)
	case decisionStateCanceledAfterInitiated:
		d.moveState(decisionStateCanceledAfterStarted, eventStarted)
	default:
		d.decisionStateMachineBase.handleStartedEvent()
	}
}

func (d *childWorkflowDecisionStateMachine) handleCancelFailedEvent() {
	switch d.state {
	case decisionStateCancellationDecisionSent:
		d.moveState(decisionStateStarted, eventCancelFailed)
	default:
		d.decisionStateMachineBase.handleCancelFailedEvent()
	}
}

func (d *childWorkflowDecisionStateMachine) cancel() {
	switch d.state {
	case decisionStateStarted:
		d.moveState(decisionStateCanceledAfterStarted, eventCancel)
	default:
		d.decisionStateMachineBase.cancel()
	}
}

func (d *childWorkflowDecisionStateMachine) handleCanceledEvent() {
	switch d.state {
	case decisionStateStarted:
		d.moveState(decisionStateCompleted, eventCanceled)
	default:
		d.decisionStateMachineBase.handleCanceledEvent()
	}
}

func (d *childWorkflowDecisionStateMachine) handleCompletionEvent() {
	switch d.state {
	case decisionStateStarted, decisionStateCanceledAfterStarted:
		d.moveState(decisionStateCompleted, eventCompletion)
	default:
		d.decisionStateMachineBase.handleCompletionEvent()
	}
}

func (d *naiveDecisionStateMachine) getDecision() *s.Decision {
	switch d.state {
	case decisionStateCreated:
		return d.decision
	default:
		return nil
	}
}

func (d *naiveDecisionStateMachine) cancel() {
	panic("unsupported operation")
}

func (d *naiveDecisionStateMachine) handleCompletionEvent() {
	panic("unsupported operation")
}

func (d *naiveDecisionStateMachine) handleInitiatedEvent() {
	panic("unsupported operation")
}

func (d *naiveDecisionStateMachine) handleInitiationFailedEvent() {
	panic("unsupported operation")
}

func (d *naiveDecisionStateMachine) handleStartedEvent() {
	panic("unsupported operation")
}

func (d *naiveDecisionStateMachine) handleCanceledEvent() {
	panic("unsupported operation")
}

func (d *naiveDecisionStateMachine) handleCancelFailedEvent() {
	panic("unsupported operation")
}

func (d *naiveDecisionStateMachine) handleCancelInitiatedEvent() {
	panic("unsupported operation")
}

func (d *cancelExternalWorkflowDecisionStateMachine) handleInitiatedEvent() {
	switch d.state {
	case decisionStateDecisionSent:
		d.moveState(decisionStateInitiated, eventInitiated)
	default:
		d.failStateTransition(eventInitiated)
	}
}

func (d *cancelExternalWorkflowDecisionStateMachine) handleCompletionEvent() {
	switch d.state {
	case decisionStateInitiated:
		d.moveState(decisionStateCompleted, eventCompletion)
	default:
		d.failStateTransition(eventCompletion)
	}
}

func (d *markerDecisionStateMachine) handleCompletionEvent() {
	// Marker decision transit from SENT to COMPLETED on EventType_MarkerRecorded event
	switch d.state {
	case decisionStateDecisionSent:
		d.moveState(decisionStateCompleted, eventInitiated)
	default:
		d.failStateTransition(eventInitiated)
	}
}

func newDecisionsHelper() *decisionsHelper {
	return &decisionsHelper{
		decisions: make(map[decisionID]decisionStateMachine),

		scheduledEventIDToActivityID: make(map[int64]string),
	}
}

func (h *decisionsHelper) getDecision(id decisionID) decisionStateMachine {
	decision, ok := h.decisions[id]
	if !ok {
		panicMsg := fmt.Sprintf("unknown decision %v, possible causes are nondeterministic workflow definition code"+
			" or incompatible change in the workflow definition", id)
		panic(panicMsg)
	}
	return decision
}

func (h *decisionsHelper) addDecision(decision decisionStateMachine) {
	h.orderedDecisions = append(h.orderedDecisions, decision)
	h.decisions[decision.getID()] = decision
}

func (h *decisionsHelper) scheduleActivityTask(attributes *s.ScheduleActivityTaskDecisionAttributes) decisionStateMachine {
	decision := newActivityDecisionStateMachine(attributes)
	h.addDecision(decision)
	return decision
}

func (h *decisionsHelper) requestCancelActivityTask(activityID string) decisionStateMachine {
	id := makeDecisionID(decisionTypeActivity, activityID)
	decision := h.getDecision(id)
	decision.cancel()
	return decision
}

func (h *decisionsHelper) handleActivityTaskClosed(activityID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeActivity, activityID))
	decision.handleCompletionEvent()
	return decision
}

func (h *decisionsHelper) handleActivityTaskScheduled(scheduledEventID int64, activityID string) {
	h.scheduledEventIDToActivityID[scheduledEventID] = activityID
	decision := h.getDecision(makeDecisionID(decisionTypeActivity, activityID))
	decision.handleInitiatedEvent()
}

func (h *decisionsHelper) handleActivityTaskCancelRequested(activityID string) {
	decision := h.getDecision(makeDecisionID(decisionTypeActivity, activityID))
	decision.handleCancelInitiatedEvent()
}

func (h *decisionsHelper) handleActivityTaskCanceled(activityID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeActivity, activityID))
	decision.handleCanceledEvent()
	return decision
}

func (h *decisionsHelper) handleRequestCancelActivityTaskFailed(activityID string) {
	decision := h.getDecision(makeDecisionID(decisionTypeActivity, activityID))
	decision.handleCancelFailedEvent()
}

func (h *decisionsHelper) getActivityID(event *s.HistoryEvent) string {
	var scheduledEventID int64 = -1
	switch event.GetEventType() {
	case s.EventType_ActivityTaskCanceled:
		scheduledEventID = event.GetActivityTaskCanceledEventAttributes().GetScheduledEventId()
	case s.EventType_ActivityTaskCompleted:
		scheduledEventID = event.GetActivityTaskCompletedEventAttributes().GetScheduledEventId()
	case s.EventType_ActivityTaskFailed:
		scheduledEventID = event.GetActivityTaskFailedEventAttributes().GetScheduledEventId()
	case s.EventType_ActivityTaskTimedOut:
		scheduledEventID = event.GetActivityTaskTimedOutEventAttributes().GetScheduledEventId()
	default:
		panic(fmt.Sprintf("unexpected event type %v", event.GetEventType()))
	}

	activityID, ok := h.scheduledEventIDToActivityID[scheduledEventID]
	if !ok {
		panic(fmt.Sprintf("unable to find activity ID for the event %v", util.HistoryEventToString(event)))
	}
	return activityID
}

func (h *decisionsHelper) recordSideEffectMarker(sideEffectID int32, data []byte) decisionStateMachine {
	markerID := fmt.Sprintf("%v_%v", sideEffectMarkerName, sideEffectID)
	attributes := &s.RecordMarkerDecisionAttributes{
		MarkerName: common.StringPtr(sideEffectMarkerName),
		Details:    data,
	}
	decision := newMarkerDecisionStateMachine(markerID, attributes)
	h.addDecision(decision)
	return decision
}

func (h *decisionsHelper) handleSideEffectMarkerRecorded(sideEffectID int32) decisionStateMachine {
	markerID := fmt.Sprintf("%v_%v", sideEffectMarkerName, sideEffectID)
	decision := h.getDecision(makeDecisionID(decisionTypeMarker, markerID))
	decision.handleCompletionEvent()
	return decision
}

func (h *decisionsHelper) startChildWorkflowExecution(attributes *s.StartChildWorkflowExecutionDecisionAttributes) decisionStateMachine {
	decision := newChildWorkflowDecisionStateMachine(attributes)
	h.addDecision(decision)
	return decision
}

func (h *decisionsHelper) handleStartChildWorkflowExecutionInitiated(workflowID string) {
	decision := h.getDecision(makeDecisionID(decisionTypeChildWorkflow, workflowID))
	decision.handleInitiatedEvent()
}

func (h *decisionsHelper) handleStartChildWorkflowExecutionFailed(workflowID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeChildWorkflow, workflowID))
	decision.handleInitiationFailedEvent()
	return decision
}

func (h *decisionsHelper) requestCancelExternalWorkflowExecution(domain, workflowID, runID string) (bool, decisionStateMachine) {
	decision, ok := h.decisions[makeDecisionID(decisionTypeChildWorkflow, workflowID)]
	if ok {
		// this is for child workflow
		decision.(*childWorkflowDecisionStateMachine).runID = runID
		decision.cancel()
		return true, decision
	}

	// this is for external non-child workflow
	attributes := &s.RequestCancelExternalWorkflowExecutionDecisionAttributes{
		Domain:     common.StringPtr(domain),
		WorkflowId: common.StringPtr(workflowID),
		RunId:      common.StringPtr(runID),
	}
	decision = newCancelExternalWorkflowStateMachine(attributes)
	h.addDecision(decision)

	return false, decision
}

func (h *decisionsHelper) handleRequestCancelExternalWorkflowExecutionInitiated(workflowID string) {
	decision, ok := h.decisions[makeDecisionID(decisionTypeChildWorkflow, workflowID)]
	if ok {
		// for child workflow
		decision.handleCancelInitiatedEvent()
	} else {
		// for non-child external workflow
		decision = h.getDecision(makeDecisionID(decisionTypeExternalWorkflow, workflowID))
		decision.handleInitiatedEvent()
	}
}

func (h *decisionsHelper) handleExternalWorkflowExecutionCancelRequested(workflowID string) {
	decision, ok := h.decisions[makeDecisionID(decisionTypeChildWorkflow, workflowID)]
	// no state change for child workflow, it is still in CancellationDecisionSent
	if !ok {
		// for non-child external workflow, this is the end of the decision state
		decision = h.getDecision(makeDecisionID(decisionTypeExternalWorkflow, workflowID))
		decision.handleCompletionEvent()
	}
}

func (h *decisionsHelper) handleRequestCancelExternalWorkflowExecutionFailed(workflowID string) {
	decision, ok := h.decisions[makeDecisionID(decisionTypeChildWorkflow, workflowID)]
	if ok {
		// for child workflow
		decision.handleCancelFailedEvent()
	} else {
		// for non-child external workflow, this is the end of the decision state
		decision = h.getDecision(makeDecisionID(decisionTypeExternalWorkflow, workflowID))
		decision.handleCompletionEvent()
	}
}

func (h *decisionsHelper) startTimer(attributes *s.StartTimerDecisionAttributes) decisionStateMachine {
	decision := newTimerDecisionStateMachine(attributes)
	h.addDecision(decision)
	return decision
}

func (h *decisionsHelper) cancelTimer(timerID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeTimer, timerID))
	decision.cancel()
	return decision
}

func (h *decisionsHelper) handleTimerClosed(timerID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeTimer, timerID))
	decision.handleCompletionEvent()
	return decision
}

func (h *decisionsHelper) handleTimerStarted(timerID string) {
	decision := h.getDecision(makeDecisionID(decisionTypeTimer, timerID))
	decision.handleInitiatedEvent()
}

func (h *decisionsHelper) handleTimerCanceled(timerID string) {
	decision := h.getDecision(makeDecisionID(decisionTypeTimer, timerID))
	decision.handleCanceledEvent()
}

func (h *decisionsHelper) handleCancelTimerFailed(timerID string) {
	decision := h.getDecision(makeDecisionID(decisionTypeTimer, timerID))
	decision.handleCancelFailedEvent()
}

func (h *decisionsHelper) handleChildWorkflowExecutionStarted(workflowID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeChildWorkflow, workflowID))
	decision.handleStartedEvent()
	return decision
}

func (h *decisionsHelper) handleChildWorkflowExecutionClosed(workflowID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeChildWorkflow, workflowID))
	decision.handleCompletionEvent()
	return decision
}

func (h *decisionsHelper) handleChildWorkflowExecutionCanceled(workflowID string) decisionStateMachine {
	decision := h.getDecision(makeDecisionID(decisionTypeChildWorkflow, workflowID))
	decision.handleCanceledEvent()
	return decision
}

func (h *decisionsHelper) getDecisions(markAsSent bool) []*s.Decision {
	var result []*s.Decision
	for _, d := range h.orderedDecisions {
		decision := d.getDecision()
		if decision != nil {
			result = append(result, decision)
		}
	}

	if markAsSent {
		h.handleDecisionsSent()
	}

	return result
}

func (h *decisionsHelper) handleDecisionsSent() {
	for _, d := range h.orderedDecisions {
		d.handleDecisionSent()
	}
}
