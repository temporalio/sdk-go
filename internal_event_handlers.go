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
	"encoding/gob"
	"errors"
	"fmt"
	"time"

	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Assert that structs do indeed implement the interfaces
var _ workflowEnvironment = (*workflowEnvironmentImpl)(nil)
var _ workflowExecutionEventHandler = (*workflowExecutionEventHandlerImpl)(nil)

type (
	// completionHandler Handler to indicate completion result
	completionHandler func(result []byte, err error)

	// workflowExecutionEventHandlerImpl handler to handle workflowExecutionEventHandler
	workflowExecutionEventHandlerImpl struct {
		*workflowEnvironmentImpl
		workflowDefinition workflowDefinition
	}

	scheduledTimer struct {
		callback resultHandler
		handled  bool
	}

	scheduledActivity struct {
		callback             resultHandler
		waitForCancelRequest bool
		handled              bool
	}

	scheduledChildWorkflow struct {
		resultCallback      resultHandler
		startedCallback     func(r WorkflowExecution, e error)
		waitForCancellation bool
		workflowExecution   *WorkflowExecution
		handled             bool
	}

	// workflowEnvironmentImpl an implementation of workflowEnvironment represents a environment for workflow execution.
	workflowEnvironmentImpl struct {
		workflowInfo              *WorkflowInfo
		workflowDefinitionFactory workflowDefinitionFactory

		decisionsHelper  *decisionsHelper
		sideEffectResult map[int32][]byte

		counterID         int32     // To generate sequence IDs for activity/timer etc.
		currentReplayTime time.Time // Indicates current replay time of the decision.

		completeHandler completionHandler               // events completion handler
		cancelHandler   func()                          // A cancel handler to be invoked on a cancel notification
		signalHandler   func(name string, input []byte) // A signal handler to be invoked on a signal event

		logger                *zap.Logger
		isReplay              bool // flag to indicate if workflow is in replay mode
		enableLoggingInReplay bool // flag to indicate if workflow should enable logging in replay mode
	}

	// wrapper around zapcore.Core that will be aware of replay
	replayAwareZapCore struct {
		zapcore.Core
		isReplay              *bool // pointer to bool that indicate if it is in replay mode
		enableLoggingInReplay *bool // pointer to bool that indicate if logging is enabled in replay mode
	}
)

var sideEffectMarkerName = "SideEffect"

func wrapLogger(isReplay *bool, enableLoggingInReplay *bool) func(zapcore.Core) zapcore.Core {
	return func(c zapcore.Core) zapcore.Core {
		return &replayAwareZapCore{c, isReplay, enableLoggingInReplay}
	}
}

func (c *replayAwareZapCore) Check(entry zapcore.Entry, checkedEntry *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if *c.isReplay && !*c.enableLoggingInReplay {
		return checkedEntry
	}
	return c.Core.Check(entry, checkedEntry)
}

func (c *replayAwareZapCore) With(fields []zapcore.Field) zapcore.Core {
	coreWithFields := c.Core.With(fields)
	return &replayAwareZapCore{coreWithFields, c.isReplay, c.enableLoggingInReplay}
}

func newWorkflowExecutionEventHandler(workflowInfo *WorkflowInfo, workflowDefinitionFactory workflowDefinitionFactory,
	completeHandler completionHandler, logger *zap.Logger, enableLoggingInReplay bool) workflowExecutionEventHandler {
	context := &workflowEnvironmentImpl{
		workflowInfo:              workflowInfo,
		workflowDefinitionFactory: workflowDefinitionFactory,
		decisionsHelper:           newDecisionsHelper(),
		sideEffectResult:          make(map[int32][]byte),
		completeHandler:           completeHandler,
		enableLoggingInReplay:     enableLoggingInReplay,
	}
	context.logger = logger.With(
		zapcore.Field{Key: tagWorkflowType, Type: zapcore.StringType, String: workflowInfo.WorkflowType.Name},
		zapcore.Field{Key: tagWorkflowID, Type: zapcore.StringType, String: workflowInfo.WorkflowExecution.ID},
		zapcore.Field{Key: tagRunID, Type: zapcore.StringType, String: workflowInfo.WorkflowExecution.RunID},
	).WithOptions(zap.WrapCore(wrapLogger(&context.isReplay, &context.enableLoggingInReplay)))

	return &workflowExecutionEventHandlerImpl{context, nil}
}

func (s *scheduledTimer) handle(result []byte, err error) {
	if s.handled {
		panic(fmt.Sprintf("timer already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (s *scheduledActivity) handle(result []byte, err error) {
	if s.handled {
		panic(fmt.Sprintf("activity already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (s *scheduledChildWorkflow) handle(result []byte, err error) {
	if s.handled {
		panic(fmt.Sprintf("child workflow already handled %v", s))
	}
	s.handled = true
	s.resultCallback(result, err)
}

func (wc *workflowEnvironmentImpl) WorkflowInfo() *WorkflowInfo {
	return wc.workflowInfo
}

func (wc *workflowEnvironmentImpl) Complete(result []byte, err error) {
	wc.completeHandler(result, err)
}

func (wc *workflowEnvironmentImpl) RequestCancelWorkflow(domainName, workflowID, runID string) error {
	if domainName == "" {
		return errors.New("need a valid domain, provided empty")
	}
	if workflowID == "" {
		return errors.New("need a valid workflow ID, provided empty")
	}

	isChild, decision := wc.decisionsHelper.requestCancelExternalWorkflowExecution(domainName, workflowID, runID)
	if isChild {
		// this is for child workflow
		childWorkflow := decision.getData().(*scheduledChildWorkflow)
		if childWorkflow.handled {
			return nil
		}
		if childWorkflow.workflowExecution != nil && (decision.isDone() || !childWorkflow.waitForCancellation) {
			childWorkflow.handle(nil, ErrCanceled)
		}
	}

	return nil
}

func (wc *workflowEnvironmentImpl) RegisterCancelHandler(handler func()) {
	wc.cancelHandler = handler
}

func (wc *workflowEnvironmentImpl) ExecuteChildWorkflow(
	options workflowOptions, callback resultHandler, startedHandler func(r WorkflowExecution, e error)) error {
	if options.workflowID == "" {
		options.workflowID = wc.workflowInfo.WorkflowExecution.RunID + "_" + wc.GenerateSequenceID()
	}

	attributes := m.NewStartChildWorkflowExecutionDecisionAttributes()

	attributes.Domain = options.domain
	attributes.TaskList = &m.TaskList{Name: options.taskListName}
	attributes.WorkflowId = common.StringPtr(options.workflowID)
	attributes.ExecutionStartToCloseTimeoutSeconds = options.executionStartToCloseTimeoutSeconds
	attributes.TaskStartToCloseTimeoutSeconds = options.taskStartToCloseTimeoutSeconds
	attributes.Input = options.input
	attributes.WorkflowType = workflowTypePtr(*options.workflowType)
	attributes.ChildPolicy = options.childPolicy.toThriftChildPolicyPtr()

	decision := wc.decisionsHelper.startChildWorkflowExecution(attributes)
	decision.setData(&scheduledChildWorkflow{
		resultCallback:      callback,
		startedCallback:     startedHandler,
		waitForCancellation: options.waitForCancellation,
	})

	wc.logger.Debug("ExecuteChildWorkflow",
		zap.String(tagChildWorkflowID, options.workflowID),
		zap.String(tagWorkflowType, options.workflowType.Name))

	return nil
}

func (wc *workflowEnvironmentImpl) RegisterSignalHandler(handler func(name string, input []byte)) {
	wc.signalHandler = handler
}

func (wc *workflowEnvironmentImpl) GetLogger() *zap.Logger {
	return wc.logger
}

func (wc *workflowEnvironmentImpl) GenerateSequenceID() string {
	return fmt.Sprintf("%d", wc.GenerateSequence())
}

func (wc *workflowEnvironmentImpl) GenerateSequence() int32 {
	result := wc.counterID
	wc.counterID++
	return result
}

func (wc *workflowEnvironmentImpl) CreateNewDecision(decisionType m.DecisionType) *m.Decision {
	return &m.Decision{
		DecisionType: common.DecisionTypePtr(decisionType),
	}
}

func (wc *workflowEnvironmentImpl) ExecuteActivity(parameters executeActivityParameters, callback resultHandler) *activityInfo {
	scheduleTaskAttr := &m.ScheduleActivityTaskDecisionAttributes{}
	if parameters.ActivityID == nil || *parameters.ActivityID == "" {
		scheduleTaskAttr.ActivityId = common.StringPtr(wc.GenerateSequenceID())
	} else {
		scheduleTaskAttr.ActivityId = parameters.ActivityID
	}
	activityID := scheduleTaskAttr.GetActivityId()
	scheduleTaskAttr.ActivityType = activityTypePtr(parameters.ActivityType)
	scheduleTaskAttr.TaskList = common.TaskListPtr(m.TaskList{Name: common.StringPtr(parameters.TaskListName)})
	scheduleTaskAttr.Input = parameters.Input
	scheduleTaskAttr.ScheduleToCloseTimeoutSeconds = common.Int32Ptr(parameters.ScheduleToCloseTimeoutSeconds)
	scheduleTaskAttr.StartToCloseTimeoutSeconds = common.Int32Ptr(parameters.StartToCloseTimeoutSeconds)
	scheduleTaskAttr.ScheduleToStartTimeoutSeconds = common.Int32Ptr(parameters.ScheduleToStartTimeoutSeconds)
	scheduleTaskAttr.HeartbeatTimeoutSeconds = common.Int32Ptr(parameters.HeartbeatTimeoutSeconds)

	decision := wc.decisionsHelper.scheduleActivityTask(scheduleTaskAttr)
	decision.setData(&scheduledActivity{
		callback:             callback,
		waitForCancelRequest: parameters.WaitForCancellation,
	})

	wc.logger.Debug("ExectueActivity",
		zap.String(tagActivityID, activityID),
		zap.String(tagActivityType, scheduleTaskAttr.GetActivityType().GetName()))

	return &activityInfo{activityID: activityID}
}

func (wc *workflowEnvironmentImpl) RequestCancelActivity(activityID string) {
	decision := wc.decisionsHelper.requestCancelActivityTask(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return
	}

	if decision.isDone() || !activity.waitForCancelRequest {
		activity.handle(nil, ErrCanceled)
	}

	wc.logger.Debug("RequestCancelActivity", zap.String(tagActivityID, activityID))
}

func (wc *workflowEnvironmentImpl) SetCurrentReplayTime(replayTime time.Time) {
	wc.currentReplayTime = replayTime
}

func (wc *workflowEnvironmentImpl) Now() time.Time {
	return wc.currentReplayTime
}

func (wc *workflowEnvironmentImpl) NewTimer(d time.Duration, callback resultHandler) *timerInfo {
	if d < 0 {
		callback(nil, errors.New("Invalid delayInSeconds provided"))
		return nil
	}
	if d == 0 {
		callback(nil, nil)
		return nil
	}

	timerID := wc.GenerateSequenceID()
	startTimerAttr := &m.StartTimerDecisionAttributes{}
	startTimerAttr.TimerId = common.StringPtr(timerID)
	startTimerAttr.StartToFireTimeoutSeconds = common.Int64Ptr(int64(d.Seconds()))

	decision := wc.decisionsHelper.startTimer(startTimerAttr)
	decision.setData(&scheduledTimer{callback: callback})

	wc.logger.Debug("NewTimer",
		zap.String(tagTimerID, startTimerAttr.GetTimerId()),
		zap.Duration("Duration", d))

	return &timerInfo{timerID: timerID}
}

func (wc *workflowEnvironmentImpl) RequestCancelTimer(timerID string) {
	decision := wc.decisionsHelper.cancelTimer(timerID)
	timer := decision.getData().(*scheduledTimer)
	if timer.handled {
		return
	}
	timer.handle(nil, ErrCanceled)
	wc.logger.Debug("RequestCancelTimer", zap.String(tagTimerID, timerID))
}

func (wc *workflowEnvironmentImpl) SideEffect(f func() ([]byte, error), callback resultHandler) {
	sideEffectID := wc.GenerateSequence()
	var details []byte
	var result []byte
	if wc.isReplay {
		var ok bool
		result, ok = wc.sideEffectResult[sideEffectID]
		if !ok {
			keys := make([]int32, 0, len(wc.sideEffectResult))
			for k := range wc.sideEffectResult {
				keys = append(keys, k)
			}
			panic(fmt.Sprintf("No cached result found for side effectID=%v. KnownSideEffects=%v",
				sideEffectID, keys))
		}
		wc.logger.Debug("SideEffect returning already caclulated result.",
			zap.Int32(tagSideEffectID, sideEffectID))
		details = result
	} else {
		var err error
		result, err = f()
		if err != nil {
			callback(result, err)
			return
		}
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(sideEffectID); err != nil {
			callback(nil, fmt.Errorf("failure encoding sideEffectID: %v", err))
			return
		}
		if err := enc.Encode(result); err != nil {
			callback(nil, fmt.Errorf("failure encoding side effect result: %v", err))
			return
		}
		details = buf.Bytes()
	}

	wc.decisionsHelper.recordSideEffectMarker(sideEffectID, details)

	callback(result, nil)
	wc.logger.Debug("SideEffect Marker added", zap.Int32(tagSideEffectID, sideEffectID))
}

func (weh *workflowExecutionEventHandlerImpl) ProcessEvent(
	event *m.HistoryEvent,
	isReplay bool,
	isLast bool,
) (result []*m.Decision, err error) {
	if event == nil {
		return nil, errors.New("nil event provided")
	}

	weh.isReplay = isReplay
	if enableVerboseLogging {
		weh.logger.Debug("ProcessEvent",
			zap.Int64(tagEventID, event.GetEventId()),
			zap.String(tagEventType, event.GetEventType().String()))
	}

	switch event.GetEventType() {
	case m.EventType_WorkflowExecutionStarted:
		err = weh.handleWorkflowExecutionStarted(event.WorkflowExecutionStartedEventAttributes)

	case m.EventType_WorkflowExecutionCompleted:
		// No Operation
	case m.EventType_WorkflowExecutionFailed:
		// No Operation
	case m.EventType_WorkflowExecutionTimedOut:
		// No Operation
	case m.EventType_DecisionTaskScheduled:
		// No Operation
	case m.EventType_DecisionTaskStarted:
		weh.workflowDefinition.OnDecisionTaskStarted()

	case m.EventType_DecisionTaskTimedOut:
		// No Operation
	case m.EventType_DecisionTaskFailed:
		// No Operation
	case m.EventType_DecisionTaskCompleted:
		// No Operation
	case m.EventType_ActivityTaskScheduled:
		weh.decisionsHelper.handleActivityTaskScheduled(
			event.GetEventId(), event.GetActivityTaskScheduledEventAttributes().GetActivityId())

	case m.EventType_ActivityTaskStarted:
		// No Operation

	case m.EventType_ActivityTaskCompleted:
		err = weh.handleActivityTaskCompleted(event)

	case m.EventType_ActivityTaskFailed:
		err = weh.handleActivityTaskFailed(event)

	case m.EventType_ActivityTaskTimedOut:
		err = weh.handleActivityTaskTimedOut(event)

	case m.EventType_ActivityTaskCancelRequested:
		weh.decisionsHelper.handleActivityTaskCancelRequested(
			event.GetActivityTaskCancelRequestedEventAttributes().GetActivityId())

	case m.EventType_RequestCancelActivityTaskFailed:
		weh.decisionsHelper.handleRequestCancelActivityTaskFailed(
			event.GetRequestCancelActivityTaskFailedEventAttributes().GetActivityId())

	case m.EventType_ActivityTaskCanceled:
		err = weh.handleActivityTaskCanceled(event)

	case m.EventType_TimerStarted:
		weh.decisionsHelper.handleTimerStarted(event.GetTimerStartedEventAttributes().GetTimerId())

	case m.EventType_TimerFired:
		weh.handleTimerFired(event)

	case m.EventType_TimerCanceled:
		weh.decisionsHelper.handleTimerCanceled(event.GetTimerCanceledEventAttributes().GetTimerId())

	case m.EventType_CancelTimerFailed:
		weh.decisionsHelper.handleCancelTimerFailed(event.GetCancelTimerFailedEventAttributes().GetTimerId())

	case m.EventType_WorkflowExecutionCancelRequested:
		weh.handleWorkflowExecutionCancelRequested()

	case m.EventType_WorkflowExecutionCanceled:
		// No Operation.

	case m.EventType_RequestCancelExternalWorkflowExecutionInitiated:
		weh.decisionsHelper.handleRequestCancelExternalWorkflowExecutionInitiated(
			event.GetRequestCancelExternalWorkflowExecutionInitiatedEventAttributes().GetWorkflowExecution().GetWorkflowId())

	case m.EventType_RequestCancelExternalWorkflowExecutionFailed:
		weh.decisionsHelper.handleRequestCancelExternalWorkflowExecutionFailed(
			event.GetRequestCancelExternalWorkflowExecutionFailedEventAttributes().GetWorkflowExecution().GetWorkflowId())

	case m.EventType_ExternalWorkflowExecutionCancelRequested:
		weh.decisionsHelper.handleExternalWorkflowExecutionCancelRequested(
			event.GetExternalWorkflowExecutionCancelRequestedEventAttributes().GetWorkflowExecution().GetWorkflowId())

	case m.EventType_WorkflowExecutionContinuedAsNew:
		// No Operation.

	case m.EventType_WorkflowExecutionSignaled:
		weh.handleWorkflowExecutionSignaled(event.WorkflowExecutionSignaledEventAttributes)

	case m.EventType_MarkerRecorded:
		err = weh.handleMarkerRecorded(event.GetEventId(), event.MarkerRecordedEventAttributes)

	case m.EventType_StartChildWorkflowExecutionInitiated:
		weh.decisionsHelper.handleStartChildWorkflowExecutionInitiated(
			event.GetStartChildWorkflowExecutionInitiatedEventAttributes().GetWorkflowId())

	case m.EventType_StartChildWorkflowExecutionFailed:
		err = weh.handleStartChildWorkflowExecutionFailed(event)

	case m.EventType_ChildWorkflowExecutionStarted:
		err = weh.handleChildWorkflowExecutionStarted(event)

	case m.EventType_ChildWorkflowExecutionCompleted:
		err = weh.handleChildWorkflowExecutionCompleted(event)

	case m.EventType_ChildWorkflowExecutionFailed:
		err = weh.handleChildWorkflowExecutionFailed(event)

	case m.EventType_ChildWorkflowExecutionCanceled:
		err = weh.handleChildWorkflowExecutionCanceled(event)

	case m.EventType_ChildWorkflowExecutionTimedOut:
		err = weh.handleChildWorkflowExecutionTimedOut(event)

	case m.EventType_ChildWorkflowExecutionTerminated:
		err = weh.handleChildWorkflowExecutionTerminated(event)

	default:
		weh.logger.Error("unknown event type",
			zap.Int64(tagEventID, event.GetEventId()),
			zap.String(tagEventType, event.GetEventType().String()))
		// Do not fail to be forward compatible with new events
	}

	if err != nil {
		return nil, err
	}

	// When replaying histories to get stack trace or current state the last event might be not
	// decision started. So always call OnDecisionTaskStarted on the last event.
	// Don't call for EventType_DecisionTaskStarted as it was already called when handling it.
	if isLast && event.GetEventType() != m.EventType_DecisionTaskStarted {
		weh.workflowDefinition.OnDecisionTaskStarted()
	}

	return weh.decisionsHelper.getDecisions(true), nil
}

func (weh *workflowExecutionEventHandlerImpl) StackTrace() string {
	return weh.workflowDefinition.StackTrace()
}

func (weh *workflowExecutionEventHandlerImpl) Close() {
	if weh.workflowDefinition != nil {
		weh.workflowDefinition.Close()
	}
}

func (weh *workflowExecutionEventHandlerImpl) handleWorkflowExecutionStarted(
	attributes *m.WorkflowExecutionStartedEventAttributes) (err error) {
	weh.workflowDefinition, err = weh.workflowDefinitionFactory(weh.workflowInfo.WorkflowType)
	if err != nil {
		return err
	}

	// Invoke the workflow.
	weh.workflowDefinition.Execute(weh, attributes.Input)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskCompleted(event *m.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskClosed(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}
	activity.handle(event.GetActivityTaskCompletedEventAttributes().GetResult_(), nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskFailed(event *m.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskClosed(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}

	attributes := event.GetActivityTaskFailedEventAttributes()
	err := NewErrorWithDetails(*attributes.Reason, attributes.Details)
	activity.handle(nil, err)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskTimedOut(event *m.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskClosed(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}

	attributes := event.GetActivityTaskTimedOutEventAttributes()
	var err error
	tt := attributes.GetTimeoutType()
	if tt == m.TimeoutType_HEARTBEAT {
		err = NewHeartbeatTimeoutError(attributes.GetDetails())
	} else {
		err = NewTimeoutError(attributes.GetTimeoutType())
	}
	activity.handle(nil, err)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskCanceled(event *m.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskCanceled(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}

	if decision.isDone() || !activity.waitForCancelRequest {
		// Clear this so we don't have a recursive call that while executing might call the cancel one.
		err := NewCanceledError(event.GetActivityTaskCanceledEventAttributes().GetDetails())
		activity.handle(nil, err)
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleTimerFired(event *m.HistoryEvent) {
	timerID := event.GetTimerFiredEventAttributes().GetTimerId()
	decision := weh.decisionsHelper.handleTimerClosed(timerID)
	timer := decision.getData().(*scheduledTimer)
	if timer.handled {
		return
	}

	timer.handle(nil, nil)
}

func (weh *workflowExecutionEventHandlerImpl) handleWorkflowExecutionCancelRequested() {
	weh.cancelHandler()
}

// Currently handles only side effect markers
func (weh *workflowExecutionEventHandlerImpl) handleMarkerRecorded(
	eventID int64,
	attributes *m.MarkerRecordedEventAttributes,
) error {
	if attributes.GetMarkerName() != sideEffectMarkerName {
		return fmt.Errorf("unknown marker name \"%v\" for eventID \"%v\"",
			attributes.GetMarkerName(), eventID)
	}
	dec := gob.NewDecoder(bytes.NewBuffer(attributes.GetDetails()))
	var sideEffectID int32

	if err := dec.Decode(&sideEffectID); err != nil {
		return fmt.Errorf("failure decodeing sideEffectID: %v", err)
	}
	var result []byte
	if err := dec.Decode(&result); err != nil {
		return fmt.Errorf("failure decoding side effect result: %v", err)
	}
	weh.decisionsHelper.handleSideEffectMarkerRecorded(sideEffectID)
	weh.sideEffectResult[sideEffectID] = result
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleWorkflowExecutionSignaled(
	attributes *m.WorkflowExecutionSignaledEventAttributes) {
	weh.signalHandler(attributes.GetSignalName(), attributes.GetInput())
}

func (weh *workflowExecutionEventHandlerImpl) handleStartChildWorkflowExecutionFailed(event *m.HistoryEvent) error {
	attributes := event.GetStartChildWorkflowExecutionFailedEventAttributes()
	childWorkflowID := attributes.GetWorkflowId()
	decision := weh.decisionsHelper.handleStartChildWorkflowExecutionFailed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}

	err := fmt.Errorf("ChildWorkflowFailed: %v", attributes.GetCause())
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionStarted(event *m.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionStartedEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionStarted(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}

	childWorkflowExecution := WorkflowExecution{
		ID:    childWorkflowID,
		RunID: attributes.WorkflowExecution.GetRunId(),
	}

	childWorkflow.workflowExecution = &childWorkflowExecution
	childWorkflow.startedCallback(childWorkflowExecution, nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionCompleted(event *m.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionCompletedEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	childWorkflow.handle(attributes.GetResult_(), nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionFailed(event *m.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionFailedEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}

	err := NewErrorWithDetails(attributes.GetReason(), attributes.GetDetails())
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionCanceled(event *m.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionCanceledEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionCanceled(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	err := NewCanceledError(attributes.GetDetails())
	childWorkflow.handle(nil, err)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionTimedOut(event *m.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionTimedOutEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	err := NewTimeoutError(attributes.GetTimeoutType())
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionTerminated(event *m.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionTerminatedEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	err := errors.New("terminated")
	childWorkflow.handle(nil, err)

	return nil
}
