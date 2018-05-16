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
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/uber-go/tally"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
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
		handled             bool
	}

	scheduledCancellation struct {
		callback resultHandler
		handled  bool
	}

	scheduledSignal struct {
		callback resultHandler
		handled  bool
	}

	// workflowEnvironmentImpl an implementation of workflowEnvironment represents a environment for workflow execution.
	workflowEnvironmentImpl struct {
		workflowInfo *WorkflowInfo

		decisionsHelper   *decisionsHelper
		sideEffectResult  map[int32][]byte
		changeVersions    map[string]Version
		pendingLaTasks    map[string]*localActivityTask
		mutableSideEffect map[string][]byte
		unstartedLaTasks  map[string]struct{}

		counterID         int32     // To generate sequence IDs for activity/timer etc.
		currentReplayTime time.Time // Indicates current replay time of the decision.
		currentLocalTime  time.Time // Local time when currentReplayTime was updated.

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

	localActivityTask struct {
		sync.Mutex
		activityID   string
		params       *executeLocalActivityParams
		callback     resultHandler
		wc           *workflowExecutionContext
		decisionTask *m.PollForDecisionTaskResponse
		canceled     bool
		cancelFunc   func()
	}

	localActivityMarkerData struct {
		ActivityID string
		ErrReason  string
		ErrJSON    string // string instead of []byte so the encoded blob is human readable
		ResultJSON string
		ReplayTime time.Time
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
		mutableSideEffect:     make(map[string][]byte),
		changeVersions:        make(map[string]Version),
		pendingLaTasks:        make(map[string]*localActivityTask),
		unstartedLaTasks:      make(map[string]struct{}),
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
		context.metricsScope = tagScope(metrics.WrapScope(&context.isReplay, scope, context),
			tagWorkflowType, workflowInfo.WorkflowType.Name)
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

func (t *localActivityTask) cancel() {
	t.Lock()
	t.canceled = true
	if t.cancelFunc != nil {
		t.cancelFunc()
	}
	t.Unlock()
}

func (s *scheduledCancellation) handle(result []byte, err error) {
	if s.handled {
		panic(fmt.Sprintf("cancellation already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (s *scheduledSignal) handle(result []byte, err error) {
	if s.handled {
		panic(fmt.Sprintf("signal already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (wc *workflowEnvironmentImpl) WorkflowInfo() *WorkflowInfo {
	return wc.workflowInfo
}

func (wc *workflowEnvironmentImpl) Complete(result []byte, err error) {
	wc.completeHandler(result, err)
}

func (wc *workflowEnvironmentImpl) RequestCancelChildWorkflow(domainName string, workflowID string) {
	// For cancellation of child workflow only, we do not use cancellation ID and run ID
	isChildWorkflowOnly := true
	cancellationID := ""
	runID := ""
	wc.decisionsHelper.requestCancelExternalWorkflowExecution(domainName, workflowID, runID, cancellationID, isChildWorkflowOnly)
}

func (wc *workflowEnvironmentImpl) RequestCancelExternalWorkflow(domainName, workflowID, runID string, callback resultHandler) {
	// for cancellation of external workflow, we have to use cancellation ID and set isChildWorkflowOnly to false
	isChildWorkflowOnly := false
	cancellationID := wc.GenerateSequenceID()
	decision := wc.decisionsHelper.requestCancelExternalWorkflowExecution(domainName, workflowID, runID, cancellationID, isChildWorkflowOnly)
	decision.setData(&scheduledCancellation{callback: callback})
}

func (wc *workflowEnvironmentImpl) SignalExternalWorkflow(domainName, workflowID, runID, signalName string,
	input []byte, _ /* THIS IS FOR TEST FRAMEWORK. DO NOT USE HERE. */ interface{}, childWorkflowOnly bool, callback resultHandler) {

	signalID := wc.GenerateSequenceID()
	decision := wc.decisionsHelper.signalExternalWorkflowExecution(domainName, workflowID, runID, signalName, input, signalID, childWorkflowOnly)
	decision.setData(&scheduledSignal{callback: callback})
}

func (wc *workflowEnvironmentImpl) RegisterCancelHandler(handler func()) {
	wc.cancelHandler = handler
}

func (wc *workflowEnvironmentImpl) ExecuteChildWorkflow(
	params executeWorkflowParams, callback resultHandler, startedHandler func(r WorkflowExecution, e error)) error {
	if params.workflowID == "" {
		params.workflowID = wc.workflowInfo.WorkflowExecution.RunID + "_" + wc.GenerateSequenceID()
	}

	attributes := &m.StartChildWorkflowExecutionDecisionAttributes{}

	attributes.Domain = params.domain
	attributes.TaskList = &m.TaskList{Name: params.taskListName}
	attributes.WorkflowId = common.StringPtr(params.workflowID)
	attributes.ExecutionStartToCloseTimeoutSeconds = params.executionStartToCloseTimeoutSeconds
	attributes.TaskStartToCloseTimeoutSeconds = params.taskStartToCloseTimeoutSeconds
	attributes.Input = params.input
	attributes.WorkflowType = workflowTypePtr(*params.workflowType)
	attributes.ChildPolicy = params.childPolicy.toThriftChildPolicyPtr()
	attributes.WorkflowIdReusePolicy = params.workflowIDReusePolicy.toThriftPtr()

	decision := wc.decisionsHelper.startChildWorkflowExecution(attributes)
	decision.setData(&scheduledChildWorkflow{
		resultCallback:      callback,
		startedCallback:     startedHandler,
		waitForCancellation: params.waitForCancellation,
	})

	wc.logger.Debug("ExecuteChildWorkflow",
		zap.String(tagChildWorkflowID, params.workflowID),
		zap.String(tagWorkflowType, params.workflowType.Name))

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

func (wc *workflowEnvironmentImpl) IsReplaying() bool {
	return wc.isReplay
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

func (wc *workflowEnvironmentImpl) ExecuteActivity(parameters executeActivityParams, callback resultHandler) *activityInfo {
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

func (wc *workflowEnvironmentImpl) ExecuteLocalActivity(params executeLocalActivityParams, callback resultHandler) *localActivityInfo {
	activityID := wc.GenerateSequenceID()
	task := &localActivityTask{activityID: activityID, params: &params, callback: callback}
	wc.pendingLaTasks[activityID] = task
	wc.unstartedLaTasks[activityID] = struct{}{}
	return &localActivityInfo{activityID: activityID}
}

func (wc *workflowEnvironmentImpl) RequestCancelLocalActivity(activityID string) {
	if task, ok := wc.pendingLaTasks[activityID]; ok {
		task.cancel()
	}
}

func (wc *workflowEnvironmentImpl) SetCurrentReplayTime(replayTime time.Time) {
	if replayTime.Before(wc.currentReplayTime) {
		return
	}
	wc.currentReplayTime = replayTime
	wc.currentLocalTime = time.Now()
}

func (wc *workflowEnvironmentImpl) Now() time.Time {
	return wc.currentReplayTime
}

func (wc *workflowEnvironmentImpl) NewTimer(d time.Duration, callback resultHandler) *timerInfo {
	if d < 0 {
		callback(nil, fmt.Errorf("negative duration provided %v", d))
		return nil
	}
	if d == 0 {
		callback(nil, nil)
		return nil
	}

	timerID := wc.GenerateSequenceID()
	startTimerAttr := &m.StartTimerDecisionAttributes{}
	startTimerAttr.TimerId = common.StringPtr(timerID)
	startTimerAttr.StartToFireTimeoutSeconds = common.Int64Ptr(common.Int64Ceil(d.Seconds()))

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
		wc.logger.Debug("SideEffect returning already calculated result.",
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

func (wc *workflowEnvironmentImpl) MutableSideEffect(id string, f func() interface{}, equals func(a, b interface{}) bool) encoded.Value {
	if result, ok := wc.mutableSideEffect[id]; ok {
		encodedResult := EncodedValue(result)
		if wc.isReplay {
			return encodedResult
		}

		newValue := f()
		if isEqualValue(newValue, result, equals) {
			return encodedResult
		}

		return wc.recordMutableSideEffect(id, encodeValue(newValue))
	}

	if wc.isReplay {
		// This should not happen
		panic("MutableSideEffect with given ID not found during replay")
	}

	return wc.recordMutableSideEffect(id, encodeValue(f()))
}

func isEqualValue(newValue interface{}, encodedOldValue []byte, equals func(a, b interface{}) bool) bool {
	if newValue == nil {
		// new value is nil
		newEncodedValue := encodeValue(nil)
		return bytes.Equal(newEncodedValue, encodedOldValue)
	}

	oldValue := decodeValue(encodedOldValue, newValue)
	return equals(newValue, oldValue)
}

func decodeValue(encodedValue EncodedValue, value interface{}) interface{} {
	// We need to decode oldValue out of encodedValue, first we need to prepare valuePtr as the same type as value
	valuePtr := reflect.New(reflect.TypeOf(value)).Interface()
	if err := encodedValue.Get(valuePtr); err != nil {
		panic(err)
	}
	decodedValue := reflect.ValueOf(valuePtr).Elem().Interface()
	return decodedValue
}

func encodeValue(value interface{}) []byte {
	blob, err := getHostEnvironment().encodeArg(value)
	if err != nil {
		panic(err)
	}
	return blob
}

func (wc *workflowEnvironmentImpl) recordMutableSideEffect(id string, data []byte) encoded.Value {
	details, err := wc.hostEnv.encodeArgs([]interface{}{id, string(data)})
	if err != nil {
		panic(err)
	}
	wc.decisionsHelper.recordMutableSideEffectMarker(id, details)
	wc.mutableSideEffect[id] = data
	return EncodedValue(data)
}

func (weh *workflowExecutionEventHandlerImpl) ProcessEvent(
	event *m.HistoryEvent,
	isReplay bool,
	isLast bool,
) (result []*m.Decision, err error) {
	if event == nil {
		return nil, errors.New("nil event provided")
	}
	defer func() {
		if p := recover(); p != nil {
			weh.metricsScope.Counter(metrics.DecisionTaskPanicCounter).Inc(1)
			topLine := fmt.Sprintf("process event for %s [panic]:", weh.workflowInfo.TaskListName)
			st := getStackTraceRaw(topLine, 7, 0)
			weh.logger.Error("ProcessEvent panic.",
				zap.String("PanicError", fmt.Sprintf("%v", p)),
				zap.String("PanicStack", st))

			weh.Complete(nil, newPanicError(p, st))
		}
	}()

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
		// Set replay clock.
		weh.SetCurrentReplayTime(time.Unix(0, event.GetTimestamp()))
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
		weh.handleRequestCancelExternalWorkflowExecutionInitiated(event)

	case m.EventTypeRequestCancelExternalWorkflowExecutionFailed:
		weh.handleRequestCancelExternalWorkflowExecutionFailed(event)

	case m.EventTypeExternalWorkflowExecutionCancelRequested:
		weh.handleExternalWorkflowExecutionCancelRequested(event)

	case m.EventTypeWorkflowExecutionContinuedAsNew:
		// No Operation.

	case m.EventTypeWorkflowExecutionSignaled:
		weh.handleWorkflowExecutionSignaled(event.WorkflowExecutionSignaledEventAttributes)

	case m.EventTypeSignalExternalWorkflowExecutionInitiated:
		signalID := string(event.SignalExternalWorkflowExecutionInitiatedEventAttributes.Control)
		weh.decisionsHelper.handleSignalExternalWorkflowExecutionInitiated(event.GetEventId(), signalID)

	case m.EventTypeSignalExternalWorkflowExecutionFailed:
		weh.handleSignalExternalWorkflowExecutionFailed(event)

	case m.EventTypeExternalWorkflowExecutionSignaled:
		weh.handleSignalExternalWorkflowExecutionCompleted(event)

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
	case localActivityMarkerName:
		return weh.handleLocalActivityMarker(attributes.Details)
	case mutableSideEffectMarkerName:
		var fixedID string
		var result string
		encodedValues.Get(&fixedID, &result)
		weh.mutableSideEffect[fixedID] = []byte(result)
		return nil
	default:
		return fmt.Errorf("unknown marker name \"%v\" for eventID \"%v\"",
			attributes.GetMarkerName(), eventID)
	}
}

func (weh *workflowExecutionEventHandlerImpl) handleLocalActivityMarker(markerData EncodedValue) error {
	var lamd localActivityMarkerData
	if err := markerData.Get(&lamd); err != nil {
		return err
	}

	weh.decisionsHelper.recordLocalActivityMarker(lamd.ActivityID, markerData)

	if la, ok := weh.pendingLaTasks[lamd.ActivityID]; ok {
		delete(weh.pendingLaTasks, lamd.ActivityID)
		delete(weh.unstartedLaTasks, lamd.ActivityID)
		if len(lamd.ErrReason) > 0 {
			la.callback(nil, constructError(lamd.ErrReason, []byte(lamd.ErrJSON)))
		} else {
			la.callback([]byte(lamd.ResultJSON), nil)
		}

		// update time
		weh.SetCurrentReplayTime(lamd.ReplayTime)

		// resume workflow execution after apply local activity result
		weh.workflowDefinition.OnDecisionTaskStarted()
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) ProcessLocalActivityResult(lar *localActivityResult) ([]*m.Decision, error) {
	// convert local activity result and error to marker data
	lamd := localActivityMarkerData{
		ActivityID: lar.task.activityID,
		ReplayTime: weh.currentReplayTime.Add(time.Now().Sub(weh.currentLocalTime)),
	}
	if lar.err != nil {
		errReason, errDetails := getErrorDetails(lar.err)
		lamd.ErrReason = errReason
		lamd.ErrJSON = string(errDetails)
	} else {
		lamd.ResultJSON = string(lar.result)
	}

	// encode marker data
	markerData, err := getHostEnvironment().encodeArg(lamd)
	if err != nil {
		return nil, err
	}

	// create marker event for local activity result
	markerEvent := &m.HistoryEvent{
		EventType: common.EventTypePtr(m.EventTypeMarkerRecorded),
		MarkerRecordedEventAttributes: &m.MarkerRecordedEventAttributes{
			MarkerName: common.StringPtr(localActivityMarkerName),
			Details:    markerData,
		},
	}

	// apply the local activity result to workflow
	return weh.ProcessEvent(markerEvent, false, false)
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

	err := fmt.Errorf("start child workflow failed: %v", attributes.GetCause())
	childWorkflow.startedCallback(WorkflowExecution{}, err)
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionStarted(event *m.HistoryEvent) error {
	attributes := event.ChildWorkflowExecutionStartedEventAttributes
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	childRunID := attributes.WorkflowExecution.GetRunId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionStarted(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}

	childWorkflowExecution := WorkflowExecution{
		ID:    childWorkflowID,
		RunID: childRunID,
	}
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
	err := newTerminatedError()
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleRequestCancelExternalWorkflowExecutionInitiated(event *m.HistoryEvent) error {
	// For cancellation of child workflow only, we do not use cancellation ID
	// for cancellation of external workflow, we have to use cancellation ID
	attribute := event.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes
	workflowID := attribute.WorkflowExecution.GetWorkflowId()
	cancellationID := string(attribute.Control)
	weh.decisionsHelper.handleRequestCancelExternalWorkflowExecutionInitiated(event.GetEventId(), workflowID, cancellationID)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleExternalWorkflowExecutionCancelRequested(event *m.HistoryEvent) error {
	// For cancellation of child workflow only, we do not use cancellation ID
	// for cancellation of external workflow, we have to use cancellation ID
	attributes := event.ExternalWorkflowExecutionCancelRequestedEventAttributes
	workflowID := attributes.WorkflowExecution.GetWorkflowId()
	isExternal, decision := weh.decisionsHelper.handleExternalWorkflowExecutionCancelRequested(attributes.GetInitiatedEventId(), workflowID)
	if isExternal {
		// for cancel external workflow, we need to set the future
		cancellation := decision.getData().(*scheduledCancellation)
		if cancellation.handled {
			return nil
		}
		cancellation.handle(nil, nil)
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleRequestCancelExternalWorkflowExecutionFailed(event *m.HistoryEvent) error {
	// For cancellation of child workflow only, we do not use cancellation ID
	// for cancellation of external workflow, we have to use cancellation ID
	attributes := event.RequestCancelExternalWorkflowExecutionFailedEventAttributes
	workflowID := attributes.WorkflowExecution.GetWorkflowId()
	isExternal, decision := weh.decisionsHelper.handleRequestCancelExternalWorkflowExecutionFailed(attributes.GetInitiatedEventId(), workflowID)
	if isExternal {
		// for cancel external workflow, we need to set the future
		cancellation := decision.getData().(*scheduledCancellation)
		if cancellation.handled {
			return nil
		}
		err := fmt.Errorf("cancel external workflow failed, %v", attributes.GetCause())
		cancellation.handle(nil, err)
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleSignalExternalWorkflowExecutionCompleted(event *m.HistoryEvent) error {
	attributes := event.ExternalWorkflowExecutionSignaledEventAttributes
	decision := weh.decisionsHelper.handleSignalExternalWorkflowExecutionCompleted(attributes.GetInitiatedEventId())
	signal := decision.getData().(*scheduledSignal)
	if signal.handled {
		return nil
	}
	signal.handle(nil, nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleSignalExternalWorkflowExecutionFailed(event *m.HistoryEvent) error {
	attributes := event.SignalExternalWorkflowExecutionFailedEventAttributes
	decision := weh.decisionsHelper.handleSignalExternalWorkflowExecutionFailed(attributes.GetInitiatedEventId())
	signal := decision.getData().(*scheduledSignal)
	if signal.handled {
		return nil
	}
	err := fmt.Errorf("signal external workflow failed, %v", attributes.GetCause())
	signal.handle(nil, err)

	return nil
}
