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

package internal

// All code in this file is private to the package.

import (
	"errors"
	"fmt"
	"time"

	"github.com/uber-go/tally"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/cadence/internal/common/metrics"
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
		workflowInfo *WorkflowInfo

		decisionsHelper  *decisionsHelper
		sideEffectResult map[int32][]byte
		changeVersions   map[string]Version

		counterID         int32     // To generate sequence IDs for activity/timer etc.
		currentReplayTime time.Time // Indicates current replay time of the decision.

		completeHandler completionHandler               // events completion handler
		cancelHandler   func()                          // A cancel handler to be invoked on a cancel notification
		signalHandler   func(name string, input []byte) // A signal handler to be invoked on a signal event
		queryHandler    func(queryType string, queryArgs []byte) ([]byte, error)

		logger                *zap.Logger
		isReplay              bool // flag to indicate if workflow is in replay mode
		enableLoggingInReplay bool // flag to indicate if workflow should enable logging in replay mode

		metricsScope tally.Scope
		hostEnv      *hostEnvImpl
	}

	// wrapper around zapcore.Core that will be aware of replay
	replayAwareZapCore struct {
		zapcore.Core
		isReplay              *bool // pointer to bool that indicate if it is in replay mode
		enableLoggingInReplay *bool // pointer to bool that indicate if logging is enabled in replay mode
	}
)

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

func newWorkflowExecutionEventHandler(
	workflowInfo *WorkflowInfo,
	completeHandler completionHandler,
	logger *zap.Logger,
	enableLoggingInReplay bool,
	scope tally.Scope,
	hostEnv *hostEnvImpl,
) workflowExecutionEventHandler {
	context := &workflowEnvironmentImpl{
		workflowInfo:          workflowInfo,
		decisionsHelper:       newDecisionsHelper(),
		sideEffectResult:      make(map[int32][]byte),
		changeVersions:        make(map[string]Version),
		completeHandler:       completeHandler,
		enableLoggingInReplay: enableLoggingInReplay,
		hostEnv:               hostEnv,
	}
	context.logger = logger.With(
		zapcore.Field{Key: tagWorkflowType, Type: zapcore.StringType, String: workflowInfo.WorkflowType.Name},
		zapcore.Field{Key: tagWorkflowID, Type: zapcore.StringType, String: workflowInfo.WorkflowExecution.ID},
		zapcore.Field{Key: tagRunID, Type: zapcore.StringType, String: workflowInfo.WorkflowExecution.RunID},
	).WithOptions(zap.WrapCore(wrapLogger(&context.isReplay, &context.enableLoggingInReplay)))

	if scope != nil {
		context.metricsScope = metrics.WrapScope(&context.isReplay, scope, context)
	}

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

	attributes := &m.StartChildWorkflowExecutionDecisionAttributes{}

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

func (wc *workflowEnvironmentImpl) RegisterQueryHandler(handler func(string, []byte) ([]byte, error)) {
	wc.queryHandler = handler
}

func (wc *workflowEnvironmentImpl) GetLogger() *zap.Logger {
	return wc.logger
}

func (wc *workflowEnvironmentImpl) GetMetricsScope() tally.Scope {
	return wc.metricsScope
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

	wc.logger.Debug("ExecuteActivity",
		zap.String(tagActivityID, activityID),
		zap.String(tagActivityType, scheduleTaskAttr.ActivityType.GetName()))

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

func validateVersion(changeID string, version, minSupported, maxSupported Version) {
	if version < minSupported {
		panic(fmt.Sprintf("Workflow code removed support of version %v. "+
			"for \"%v\" changeID. The oldest supported version is %v",
			version, changeID, minSupported))
	}
	if version > maxSupported {
		panic(fmt.Sprintf("Workflow code is too old to support version %v "+
			"for \"%v\" changeID. The maximum supported version is %v",
			version, changeID, maxSupported))
	}
}

func (wc *workflowEnvironmentImpl) GetVersion(changeID string, minSupported, maxSupported Version) Version {
	if version, ok := wc.changeVersions[changeID]; ok {
		validateVersion(changeID, version, minSupported, maxSupported)
		return version
	}

	var version Version
	if wc.isReplay {
		// GetVersion for changeID is called first time in replay mode, use DefaultVersion
		version = DefaultVersion
	} else {
		// GetVersion for changeID is called first time (non-replay mode), we need to generate a marker decision for it.
		version = maxSupported
		wc.decisionsHelper.recordVersionMarker(changeID, version)
	}

	validateVersion(changeID, version, minSupported, maxSupported)
	wc.changeVersions[changeID] = version
	return version
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
		details, err = wc.hostEnv.encodeArgs([]interface{}{sideEffectID, result})
		if err != nil {
			callback(nil, fmt.Errorf("failure encoding sideEffectID: %v", err))
			return
		}
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
	traceLog(func() {
		weh.logger.Debug("ProcessEvent",
			zap.Int64(tagEventID, event.GetEventId()),
			zap.String(tagEventType, event.GetEventType().String()))
	})

	switch event.GetEventType() {
	case m.EventTypeWorkflowExecutionStarted:
		err = weh.handleWorkflowExecutionStarted(event.WorkflowExecutionStartedEventAttributes)

	case m.EventTypeWorkflowExecutionCompleted:
		// No Operation
	case m.EventTypeWorkflowExecutionFailed:
		// No Operation
	case m.EventTypeWorkflowExecutionTimedOut:
		// No Operation
	case m.EventTypeDecisionTaskScheduled:
		// No Operation
	case m.EventTypeDecisionTaskStarted:
		weh.workflowDefinition.OnDecisionTaskStarted()

	case m.EventTypeDecisionTaskTimedOut:
		// No Operation
	case m.EventTypeDecisionTaskFailed:
		// No Operation
	case m.EventTypeDecisionTaskCompleted:
		// No Operation
	case m.EventTypeActivityTaskScheduled:
		weh.decisionsHelper.handleActivityTaskScheduled(
			event.GetEventId(), event.ActivityTaskScheduledEventAttributes.GetActivityId())

	case m.EventTypeActivityTaskStarted:
		// No Operation

	case m.EventTypeActivityTaskCompleted:
		err = weh.handleActivityTaskCompleted(event)

	case m.EventTypeActivityTaskFailed:
		err = weh.handleActivityTaskFailed(event)

	case m.EventTypeActivityTaskTimedOut:
		err = weh.handleActivityTaskTimedOut(event)

	case m.EventTypeActivityTaskCancelRequested:
		weh.decisionsHelper.handleActivityTaskCancelRequested(
			event.ActivityTaskCancelRequestedEventAttributes.GetActivityId())

	case m.EventTypeRequestCancelActivityTaskFailed:
		weh.decisionsHelper.handleRequestCancelActivityTaskFailed(
			event.RequestCancelActivityTaskFailedEventAttributes.GetActivityId())

	case m.EventTypeActivityTaskCanceled:
		err = weh.handleActivityTaskCanceled(event)

	case m.EventTypeTimerStarted:
		weh.decisionsHelper.handleTimerStarted(event.TimerStartedEventAttributes.GetTimerId())

	case m.EventTypeTimerFired:
		weh.handleTimerFired(event)

	case m.EventTypeTimerCanceled:
		weh.decisionsHelper.handleTimerCanceled(event.TimerCanceledEventAttributes.GetTimerId())

	case m.EventTypeCancelTimerFailed:
		weh.decisionsHelper.handleCancelTimerFailed(event.CancelTimerFailedEventAttributes.GetTimerId())

	case m.EventTypeWorkflowExecutionCancelRequested:
		weh.handleWorkflowExecutionCancelRequested()

	case m.EventTypeWorkflowExecutionCanceled:
		// No Operation.

	case m.EventTypeRequestCancelExternalWorkflowExecutionInitiated:
		weh.decisionsHelper.handleRequestCancelExternalWorkflowExecutionInitiated(
			event.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes.WorkflowExecution.GetWorkflowId())

	case m.EventTypeRequestCancelExternalWorkflowExecutionFailed:
		weh.decisionsHelper.handleRequestCancelExternalWorkflowExecutionFailed(
			event.RequestCancelExternalWorkflowExecutionFailedEventAttributes.WorkflowExecution.GetWorkflowId())

	case m.EventTypeExternalWorkflowExecutionCancelRequested:
		weh.decisionsHelper.handleExternalWorkflowExecutionCancelRequested(
			event.ExternalWorkflowExecutionCancelRequestedEventAttributes.WorkflowExecution.GetWorkflowId())

	case m.EventTypeWorkflowExecutionContinuedAsNew:
		// No Operation.

	case m.EventTypeWorkflowExecutionSignaled:
		weh.handleWorkflowExecutionSignaled(event.WorkflowExecutionSignaledEventAttributes)

	case m.EventTypeMarkerRecorded:
		err = weh.handleMarkerRecorded(event.GetEventId(), event.MarkerRecordedEventAttributes)

	case m.EventTypeStartChildWorkflowExecutionInitiated:
		weh.decisionsHelper.handleStartChildWorkflowExecutionInitiated(
			event.StartChildWorkflowExecutionInitiatedEventAttributes.GetWorkflowId())

	case m.EventTypeStartChildWorkflowExecutionFailed:
		err = weh.handleStartChildWorkflowExecutionFailed(event)

	case m.EventTypeChildWorkflowExecutionStarted:
		err = weh.handleChildWorkflowExecutionStarted(event)

	case m.EventTypeChildWorkflowExecutionCompleted:
		err = weh.handleChildWorkflowExecutionCompleted(event)

	case m.EventTypeChildWorkflowExecutionFailed:
		err = weh.handleChildWorkflowExecutionFailed(event)

	case m.EventTypeChildWorkflowExecutionCanceled:
		err = weh.handleChildWorkflowExecutionCanceled(event)

	case m.EventTypeChildWorkflowExecutionTimedOut:
		err = weh.handleChildWorkflowExecutionTimedOut(event)

	case m.EventTypeChildWorkflowExecutionTerminated:
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
	if isLast && event.GetEventType() != m.EventTypeDecisionTaskStarted {
		weh.workflowDefinition.OnDecisionTaskStarted()
	}

	return weh.decisionsHelper.getDecisions(true), nil
}

func (weh *workflowExecutionEventHandlerImpl) ProcessQuery(queryType string, queryArgs []byte) ([]byte, error) {
	if queryType == QueryTypeStackTrace {
		return getHostEnvironment().encodeArg(weh.StackTrace())
	}
	return weh.queryHandler(queryType, queryArgs)
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
	weh.workflowDefinition, err = weh.hostEnv.getWorkflowDefinition(
		weh.workflowInfo.WorkflowType,
	)
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
	activity.handle(event.ActivityTaskCompletedEventAttributes.Result, nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskFailed(event *m.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskClosed(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}

	attributes := event.ActivityTaskFailedEventAttributes
	err := constructError(*attributes.Reason, attributes.Details)
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

	attributes := event.ActivityTaskTimedOutEventAttributes
	var err error
	tt := attributes.GetTimeoutType()
	if tt == m.TimeoutTypeHeartbeat {
		err = NewHeartbeatTimeoutError(attributes.Details)
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
		err := NewCanceledError(event.ActivityTaskCanceledEventAttributes.Details)
		activity.handle(nil, err)
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleTimerFired(event *m.HistoryEvent) {
	timerID := event.TimerFiredEventAttributes.GetTimerId()
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

func (weh *workflowExecutionEventHandlerImpl) handleMarkerRecorded(
	eventID int64,
	attributes *m.MarkerRecordedEventAttributes,
) error {
	encodedValues := EncodedValues(attributes.Details)
	switch attributes.GetMarkerName() {
	case sideEffectMarkerName:
		var sideEffectID int32
		var result []byte
		encodedValues.Get(&sideEffectID, &result)
		weh.sideEffectResult[sideEffectID] = result
		return nil
	case versionMarkerName:
		var changeID string
		var version Version
		encodedValues.Get(&changeID, &version)
		weh.changeVersions[changeID] = version
		return nil
	default:
		return fmt.Errorf("unknown marker name \"%v\" for eventID \"%v\"",
			attributes.GetMarkerName(), eventID)
	}
}

func (weh *workflowExecutionEventHandlerImpl) handleWorkflowExecutionSignaled(
	attributes *m.WorkflowExecutionSignaledEventAttributes) {
	weh.signalHandler(attributes.GetSignalName(), attributes.Input)
}

func (weh *workflowExecutionEventHandlerImpl) handleStartChildWorkflowExecutionFailed(event *m.HistoryEvent) error {
	attributes := event.StartChildWorkflowExecutionFailedEventAttributes
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
	attributes := event.ChildWorkflowExecutionStartedEventAttributes
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
	attributes := event.ChildWorkflowExecutionCompletedEventAttributes
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	childWorkflow.handle(attributes.Result, nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionFailed(event *m.HistoryEvent) error {
	attributes := event.ChildWorkflowExecutionFailedEventAttributes
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}

	err := constructError(attributes.GetReason(), attributes.Details)
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionCanceled(event *m.HistoryEvent) error {
	attributes := event.ChildWorkflowExecutionCanceledEventAttributes
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionCanceled(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	err := NewCanceledError(attributes.Details)
	childWorkflow.handle(nil, err)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionTimedOut(event *m.HistoryEvent) error {
	attributes := event.ChildWorkflowExecutionTimedOutEventAttributes
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
	attributes := event.ChildWorkflowExecutionTerminatedEventAttributes
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
