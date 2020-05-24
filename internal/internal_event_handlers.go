// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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
	"reflect"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/opentracing/opentracing-go"
	"github.com/uber-go/tally"

	commonpb "go.temporal.io/temporal-proto/common"
	decisionpb "go.temporal.io/temporal-proto/decision"
	eventpb "go.temporal.io/temporal-proto/event"
	"go.temporal.io/temporal-proto/serviceerror"
	tasklistpb "go.temporal.io/temporal-proto/tasklist"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"go.temporal.io/temporal/internal/common"
	"go.temporal.io/temporal/internal/common/metrics"
)

const (
	queryResultSizeLimit = 2000000 // 2MB
)

// Assert that structs do indeed implement the interfaces
var _ workflowEnvironment = (*workflowEnvironmentImpl)(nil)
var _ workflowExecutionEventHandler = (*workflowExecutionEventHandlerImpl)(nil)

type (
	// completionHandler Handler to indicate completion result
	completionHandler func(result *commonpb.Payloads, err error)

	// workflowExecutionEventHandlerImpl handler to handle workflowExecutionEventHandler
	workflowExecutionEventHandlerImpl struct {
		*workflowEnvironmentImpl
		workflowDefinition WorkflowDefinition
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
		sideEffectResult  map[int64]*commonpb.Payloads
		changeVersions    map[string]Version
		pendingLaTasks    map[string]*localActivityTask
		mutableSideEffect map[string]*commonpb.Payloads
		unstartedLaTasks  map[string]struct{}
		openSessions      map[string]*SessionInfo

		currentReplayTime time.Time // Indicates current replay time of the decision.
		currentLocalTime  time.Time // Local time when currentReplayTime was updated.

		completeHandler completionHandler                           // events completion handler
		cancelHandler   func()                                      // A cancel handler to be invoked on a cancel notification
		signalHandler   func(name string, input *commonpb.Payloads) // A signal handler to be invoked on a signal event
		queryHandler    func(queryType string, queryArgs *commonpb.Payloads) (*commonpb.Payloads, error)

		logger                *zap.Logger
		isReplay              bool // flag to indicate if workflow is in replay mode
		enableLoggingInReplay bool // flag to indicate if workflow should enable logging in replay mode

		metricsScope       tally.Scope
		registry           *registry
		dataConverter      DataConverter
		contextPropagators []ContextPropagator
		tracer             opentracing.Tracer
	}

	localActivityTask struct {
		sync.Mutex
		workflowTask *workflowTask
		activityID   string
		params       *executeLocalActivityParams
		callback     laResultHandler
		wc           *workflowExecutionContextImpl
		canceled     bool
		cancelFunc   func()
		attempt      int32 // attempt starting from 0
		retryPolicy  *RetryPolicy
		expireTime   time.Time
	}

	localActivityMarkerData struct {
		ActivityID   string
		ActivityType string
		ErrReason    string
		Err          *commonpb.Payloads
		Result       *commonpb.Payloads
		ReplayTime   time.Time
		Attempt      int32         // record attempt, starting from 0.
		Backoff      time.Duration // retry backoff duration.
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
	registry *registry,
	dataConverter DataConverter,
	contextPropagators []ContextPropagator,
	tracer opentracing.Tracer,
) workflowExecutionEventHandler {
	context := &workflowEnvironmentImpl{
		workflowInfo:          workflowInfo,
		decisionsHelper:       newDecisionsHelper(),
		sideEffectResult:      make(map[int64]*commonpb.Payloads),
		mutableSideEffect:     make(map[string]*commonpb.Payloads),
		changeVersions:        make(map[string]Version),
		pendingLaTasks:        make(map[string]*localActivityTask),
		unstartedLaTasks:      make(map[string]struct{}),
		openSessions:          make(map[string]*SessionInfo),
		completeHandler:       completeHandler,
		enableLoggingInReplay: enableLoggingInReplay,
		registry:              registry,
		dataConverter:         dataConverter,
		contextPropagators:    contextPropagators,
		tracer:                tracer,
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

func (s *scheduledTimer) handle(result *commonpb.Payloads, err error) {
	if s.handled {
		panic(fmt.Sprintf("timer already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (s *scheduledActivity) handle(result *commonpb.Payloads, err error) {
	if s.handled {
		panic(fmt.Sprintf("activity already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (s *scheduledChildWorkflow) handle(result *commonpb.Payloads, err error) {
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

func (s *scheduledCancellation) handle(result *commonpb.Payloads, err error) {
	if s.handled {
		panic(fmt.Sprintf("cancellation already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (s *scheduledSignal) handle(result *commonpb.Payloads, err error) {
	if s.handled {
		panic(fmt.Sprintf("signal already handled %v", s))
	}
	s.handled = true
	s.callback(result, err)
}

func (wc *workflowEnvironmentImpl) WorkflowInfo() *WorkflowInfo {
	return wc.workflowInfo
}

func (wc *workflowEnvironmentImpl) Complete(result *commonpb.Payloads, err error) {
	wc.completeHandler(result, err)
}

func (wc *workflowEnvironmentImpl) RequestCancelChildWorkflow(namespace string, workflowID string) {
	// For cancellation of child workflow only, we do not use cancellation ID and run ID
	wc.decisionsHelper.requestCancelExternalWorkflowExecution(namespace, workflowID, "", "", true)
}

func (wc *workflowEnvironmentImpl) RequestCancelExternalWorkflow(namespace, workflowID, runID string, callback resultHandler) {
	// for cancellation of external workflow, we have to use cancellation ID and set isChildWorkflowOnly to false
	cancellationID := wc.GenerateSequenceID()
	decision := wc.decisionsHelper.requestCancelExternalWorkflowExecution(namespace, workflowID, runID, cancellationID, false)
	decision.setData(&scheduledCancellation{callback: callback})
}

func (wc *workflowEnvironmentImpl) SignalExternalWorkflow(namespace, workflowID, runID, signalName string,
	input *commonpb.Payloads, _ /* THIS IS FOR TEST FRAMEWORK. DO NOT USE HERE. */ interface{}, childWorkflowOnly bool, callback resultHandler) {

	signalID := wc.GenerateSequenceID()
	decision := wc.decisionsHelper.signalExternalWorkflowExecution(namespace, workflowID, runID, signalName, input, signalID, childWorkflowOnly)
	decision.setData(&scheduledSignal{callback: callback})
}

func (wc *workflowEnvironmentImpl) UpsertSearchAttributes(attributes map[string]interface{}) error {
	// This has to be used in workflowEnvironment implementations instead of in Workflow for testsuite mock purpose.
	attr, err := validateAndSerializeSearchAttributes(attributes)
	if err != nil {
		return err
	}

	var upsertID string
	if changeVersion, ok := attributes[TemporalChangeVersion]; ok {
		// to ensure backward compatibility on searchable GetVersion, use latest changeVersion as upsertID
		upsertID = changeVersion.([]string)[0]
	} else {
		upsertID = wc.GenerateSequenceID()
	}

	wc.decisionsHelper.upsertSearchAttributes(upsertID, attr)
	wc.updateWorkflowInfoWithSearchAttributes(attr) // this is for getInfo correctness
	return nil
}

func (wc *workflowEnvironmentImpl) updateWorkflowInfoWithSearchAttributes(attributes *commonpb.SearchAttributes) {
	wc.workflowInfo.SearchAttributes = mergeSearchAttributes(wc.workflowInfo.SearchAttributes, attributes)
}

func mergeSearchAttributes(current, upsert *commonpb.SearchAttributes) *commonpb.SearchAttributes {
	if current == nil || len(current.IndexedFields) == 0 {
		if upsert == nil || len(upsert.IndexedFields) == 0 {
			return nil
		}
		current = &commonpb.SearchAttributes{
			IndexedFields: make(map[string]*commonpb.Payload),
		}
	}

	fields := current.IndexedFields
	for k, v := range upsert.IndexedFields {
		fields[k] = v
	}
	return current
}

func validateAndSerializeSearchAttributes(attributes map[string]interface{}) (*commonpb.SearchAttributes, error) {
	if len(attributes) == 0 {
		return nil, errSearchAttributesNotSet
	}
	attr, err := serializeSearchAttributes(attributes)
	if err != nil {
		return nil, err
	}
	return attr, nil
}

func (wc *workflowEnvironmentImpl) RegisterCancelHandler(handler func()) {
	wc.cancelHandler = handler
}

func (wc *workflowEnvironmentImpl) ExecuteChildWorkflow(
	params executeWorkflowParams, callback resultHandler, startedHandler func(r WorkflowExecution, e error)) error {
	if params.workflowID == "" {
		params.workflowID = wc.workflowInfo.WorkflowExecution.RunID + "_" + wc.GenerateSequenceID()
	}
	memo, err := getWorkflowMemo(params.memo, wc.dataConverter)
	if err != nil {
		return err
	}
	searchAttr, err := serializeSearchAttributes(params.searchAttributes)
	if err != nil {
		return err
	}

	attributes := &decisionpb.StartChildWorkflowExecutionDecisionAttributes{}

	attributes.Namespace = params.namespace
	attributes.TaskList = &tasklistpb.TaskList{Name: params.taskListName}
	attributes.WorkflowId = params.workflowID
	attributes.WorkflowExecutionTimeoutSeconds = params.workflowExecutionTimeoutSeconds
	attributes.WorkflowRunTimeoutSeconds = params.workflowRunTimeoutSeconds
	attributes.WorkflowTaskTimeoutSeconds = params.workflowTaskTimeoutSeconds
	attributes.Input = params.input
	attributes.WorkflowType = &commonpb.WorkflowType{Name: params.workflowType.Name}
	attributes.WorkflowIdReusePolicy = params.workflowIDReusePolicy.toProto()
	attributes.ParentClosePolicy = params.parentClosePolicy.toProto()
	attributes.RetryPolicy = params.retryPolicy
	attributes.Header = params.header
	attributes.Memo = memo
	attributes.SearchAttributes = searchAttr
	if len(params.cronSchedule) > 0 {
		attributes.CronSchedule = params.cronSchedule
	}

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

func (wc *workflowEnvironmentImpl) RegisterSignalHandler(handler func(name string, input *commonpb.Payloads)) {
	wc.signalHandler = handler
}

func (wc *workflowEnvironmentImpl) RegisterQueryHandler(handler func(string, *commonpb.Payloads) (*commonpb.Payloads, error)) {
	wc.queryHandler = handler
}

func (wc *workflowEnvironmentImpl) GetLogger() *zap.Logger {
	return wc.logger
}

func (wc *workflowEnvironmentImpl) GetMetricsScope() tally.Scope {
	return wc.metricsScope
}

func (wc *workflowEnvironmentImpl) GetDataConverter() DataConverter {
	return wc.dataConverter
}

func (wc *workflowEnvironmentImpl) GetContextPropagators() []ContextPropagator {
	return wc.contextPropagators
}

func (wc *workflowEnvironmentImpl) IsReplaying() bool {
	return wc.isReplay
}

func (wc *workflowEnvironmentImpl) GenerateSequenceID() string {
	return getStringID(wc.GenerateSequence())
}

func (wc *workflowEnvironmentImpl) GenerateSequence() int64 {
	return wc.decisionsHelper.getNextID()
}

func (wc *workflowEnvironmentImpl) CreateNewDecision(decisionType decisionpb.DecisionType) *decisionpb.Decision {
	return &decisionpb.Decision{
		DecisionType: decisionType,
	}
}

func (wc *workflowEnvironmentImpl) ExecuteActivity(parameters executeActivityParams, callback resultHandler) *activityInfo {
	scheduleTaskAttr := &decisionpb.ScheduleActivityTaskDecisionAttributes{}
	scheduleID := wc.GenerateSequence()
	if parameters.ActivityID == "" {
		scheduleTaskAttr.ActivityId = getStringID(scheduleID)
	} else {
		scheduleTaskAttr.ActivityId = parameters.ActivityID
	}
	activityID := scheduleTaskAttr.GetActivityId()
	scheduleTaskAttr.ActivityType = &commonpb.ActivityType{Name: parameters.ActivityType.Name}
	scheduleTaskAttr.TaskList = &tasklistpb.TaskList{Name: parameters.TaskListName}
	scheduleTaskAttr.Input = parameters.Input
	scheduleTaskAttr.ScheduleToCloseTimeoutSeconds = parameters.ScheduleToCloseTimeoutSeconds
	scheduleTaskAttr.StartToCloseTimeoutSeconds = parameters.StartToCloseTimeoutSeconds
	scheduleTaskAttr.ScheduleToStartTimeoutSeconds = parameters.ScheduleToStartTimeoutSeconds
	scheduleTaskAttr.HeartbeatTimeoutSeconds = parameters.HeartbeatTimeoutSeconds
	scheduleTaskAttr.RetryPolicy = parameters.RetryPolicy
	scheduleTaskAttr.Header = parameters.Header

	decision := wc.decisionsHelper.scheduleActivityTask(scheduleID, scheduleTaskAttr)
	decision.setData(&scheduledActivity{
		callback:             callback,
		waitForCancelRequest: parameters.WaitForCancellation,
	})

	wc.logger.Debug("ExecuteActivity",
		zap.String(tagActivityID, activityID),
		zap.String(tagActivityType, scheduleTaskAttr.ActivityType.GetName()))

	return &activityInfo{
		scheduleID: wc.GenerateSequence(),
		activityID: activityID,
	}
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

func (wc *workflowEnvironmentImpl) ExecuteLocalActivity(params executeLocalActivityParams, callback laResultHandler) *localActivityInfo {
	activityID := wc.GenerateSequenceID()
	task := newLocalActivityTask(params, callback, activityID)

	wc.pendingLaTasks[activityID] = task
	wc.unstartedLaTasks[activityID] = struct{}{}
	return &localActivityInfo{activityID: activityID}
}

func newLocalActivityTask(params executeLocalActivityParams, callback laResultHandler, activityID string) *localActivityTask {
	task := &localActivityTask{
		activityID:  activityID,
		params:      &params,
		callback:    callback,
		retryPolicy: params.RetryPolicy,
		attempt:     params.Attempt,
	}

	if params.ScheduleToCloseTimeoutSeconds > 0 {
		task.expireTime = params.ScheduledTime.Add(time.Second * time.Duration(params.ScheduleToCloseTimeoutSeconds))
	}
	return task
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
	startTimerAttr := &decisionpb.StartTimerDecisionAttributes{}
	startTimerAttr.TimerId = timerID
	startTimerAttr.StartToFireTimeoutSeconds = common.Int64Ceil(d.Seconds())

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
		// GetVersion for changeID is called first time (non-replay mode), generate a marker decision for it.
		// Also upsert search attributes to enable ability to search by changeVersion.
		version = maxSupported
		wc.decisionsHelper.recordVersionMarker(changeID, version, wc.GetDataConverter())
		_ = wc.UpsertSearchAttributes(createSearchAttributesForChangeVersion(changeID, version, wc.changeVersions))
	}

	validateVersion(changeID, version, minSupported, maxSupported)
	wc.changeVersions[changeID] = version
	return version
}

func createSearchAttributesForChangeVersion(changeID string, version Version, existingChangeVersions map[string]Version) map[string]interface{} {
	return map[string]interface{}{
		TemporalChangeVersion: getChangeVersions(changeID, version, existingChangeVersions),
	}
}

func getChangeVersions(changeID string, version Version, existingChangeVersions map[string]Version) []string {
	res := []string{getChangeVersion(changeID, version)}
	for k, v := range existingChangeVersions {
		res = append(res, getChangeVersion(k, v))
	}
	return res
}

func getChangeVersion(changeID string, version Version) string {
	return fmt.Sprintf("%s-%v", changeID, version)
}

func (wc *workflowEnvironmentImpl) SideEffect(f func() (*commonpb.Payloads, error), callback resultHandler) {
	sideEffectID := wc.GenerateSequence()
	var details *commonpb.Payloads
	var result *commonpb.Payloads
	if wc.isReplay {
		var ok bool
		result, ok = wc.sideEffectResult[sideEffectID]
		if !ok {
			keys := make([]int64, 0, len(wc.sideEffectResult))
			for k := range wc.sideEffectResult {
				keys = append(keys, k)
			}
			panic(fmt.Sprintf("No cached result found for side effectID=%v. KnownSideEffects=%v",
				sideEffectID, keys))
		}
		wc.logger.Debug("SideEffect returning already calculated result.",
			zap.Int64(tagSideEffectID, sideEffectID))
		details = result
	} else {
		var err error
		result, err = f()
		if err != nil {
			callback(result, err)
			return
		}
		details, err = encodeArgs(wc.GetDataConverter(), []interface{}{sideEffectID, result})
		if err != nil {
			callback(nil, fmt.Errorf("failure encoding sideEffectID: %v", err))
			return
		}
	}

	wc.decisionsHelper.recordSideEffectMarker(sideEffectID, details)

	callback(result, nil)
	wc.logger.Debug("SideEffect Marker added", zap.Int64(tagSideEffectID, sideEffectID))
}

func (wc *workflowEnvironmentImpl) MutableSideEffect(id string, f func() interface{}, equals func(a, b interface{}) bool) Value {
	if result, ok := wc.mutableSideEffect[id]; ok {
		encodedResult := newEncodedValue(result, wc.GetDataConverter())
		if wc.isReplay {
			return encodedResult
		}

		newValue := f()
		if wc.isEqualValue(newValue, result, equals) {
			return encodedResult
		}

		return wc.recordMutableSideEffect(id, wc.encodeValue(newValue))
	}

	if wc.isReplay {
		// This should not happen
		panic(fmt.Sprintf("Non deterministic workflow code change detected. MutableSideEffect API call doesn't have a correspondent event in the workflow history. MutableSideEffect ID: %s", id))
	}

	return wc.recordMutableSideEffect(id, wc.encodeValue(f()))
}

func (wc *workflowEnvironmentImpl) isEqualValue(newValue interface{}, encodedOldValue *commonpb.Payloads, equals func(a, b interface{}) bool) bool {
	if newValue == nil {
		// new value is nil
		newEncodedValue := wc.encodeValue(nil)
		return proto.Equal(newEncodedValue, encodedOldValue)
	}

	oldValue := decodeValue(newEncodedValue(encodedOldValue, wc.GetDataConverter()), newValue)
	return equals(newValue, oldValue)
}

func decodeValue(encodedValue Value, value interface{}) interface{} {
	// We need to decode oldValue out of encodedValue, first we need to prepare valuePtr as the same type as value
	valuePtr := reflect.New(reflect.TypeOf(value)).Interface()
	if err := encodedValue.Get(valuePtr); err != nil {
		panic(err)
	}
	decodedValue := reflect.ValueOf(valuePtr).Elem().Interface()
	return decodedValue
}

func (wc *workflowEnvironmentImpl) encodeValue(value interface{}) *commonpb.Payloads {
	payload, err := wc.encodeArg(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func (wc *workflowEnvironmentImpl) encodeArg(arg interface{}) (*commonpb.Payloads, error) {
	return wc.GetDataConverter().ToData(arg)
}

func (wc *workflowEnvironmentImpl) recordMutableSideEffect(id string, data *commonpb.Payloads) Value {
	details, err := encodeArgs(wc.GetDataConverter(), []interface{}{id, data})
	if err != nil {
		panic(err)
	}
	wc.decisionsHelper.recordMutableSideEffectMarker(id, details)
	wc.mutableSideEffect[id] = data
	return newEncodedValue(data, wc.GetDataConverter())
}

func (wc *workflowEnvironmentImpl) AddSession(sessionInfo *SessionInfo) {
	wc.openSessions[sessionInfo.SessionID] = sessionInfo
}

func (wc *workflowEnvironmentImpl) RemoveSession(sessionID string) {
	delete(wc.openSessions, sessionID)
}

func (wc *workflowEnvironmentImpl) getOpenSessions() []*SessionInfo {
	openSessions := make([]*SessionInfo, 0, len(wc.openSessions))
	for _, info := range wc.openSessions {
		openSessions = append(openSessions, info)
	}
	return openSessions
}

func (wc *workflowEnvironmentImpl) GetRegistry() *registry {
	return wc.registry
}

func (weh *workflowExecutionEventHandlerImpl) ProcessEvent(
	event *eventpb.HistoryEvent,
	isReplay bool,
	isLast bool,
) (err error) {
	if event == nil {
		return errors.New("nil event provided")
	}
	defer func() {
		if p := recover(); p != nil {
			weh.metricsScope.Counter(metrics.DecisionTaskPanicCounter).Inc(1)
			topLine := fmt.Sprintf("process event for %s [panic]:", weh.workflowInfo.TaskListName)
			st := getStackTraceRaw(topLine, 7, 0)
			weh.logger.Error("ProcessEvent panic.",
				zap.String("PanicError", fmt.Sprintf("%v", p)),
				zap.String("PanicStack", st))

			weh.Complete(nil, newWorkflowPanicError(p, st))
		}
	}()

	weh.isReplay = isReplay
	traceLog(func() {
		weh.logger.Debug("ProcessEvent",
			zap.Int64(tagEventID, event.GetEventId()),
			zap.String(tagEventType, event.GetEventType().String()))
	})

	switch event.GetEventType() {
	case eventpb.EventType_WorkflowExecutionStarted:
		err = weh.handleWorkflowExecutionStarted(event.GetWorkflowExecutionStartedEventAttributes())

	case eventpb.EventType_WorkflowExecutionCompleted:
		// No Operation
	case eventpb.EventType_WorkflowExecutionFailed:
		// No Operation
	case eventpb.EventType_WorkflowExecutionTimedOut:
		// No Operation
	case eventpb.EventType_DecisionTaskScheduled:
		// No Operation
	case eventpb.EventType_DecisionTaskStarted:
		// Set replay clock.
		weh.SetCurrentReplayTime(time.Unix(0, event.GetTimestamp()))
		// Reset the counter on decision helper used for generating ID for decisions
		weh.decisionsHelper.setCurrentDecisionStartedEventID(event.GetEventId())
		weh.workflowDefinition.OnDecisionTaskStarted()

	case eventpb.EventType_DecisionTaskTimedOut:
		// No Operation
	case eventpb.EventType_DecisionTaskFailed:
		// No Operation
	case eventpb.EventType_DecisionTaskCompleted:
		// No Operation
	case eventpb.EventType_ActivityTaskScheduled:
		weh.decisionsHelper.handleActivityTaskScheduled(
			event.GetEventId(), event.GetActivityTaskScheduledEventAttributes().GetActivityId())

	case eventpb.EventType_ActivityTaskStarted:
		// No Operation

	case eventpb.EventType_ActivityTaskCompleted:
		err = weh.handleActivityTaskCompleted(event)

	case eventpb.EventType_ActivityTaskFailed:
		err = weh.handleActivityTaskFailed(event)

	case eventpb.EventType_ActivityTaskTimedOut:
		err = weh.handleActivityTaskTimedOut(event)

	case eventpb.EventType_ActivityTaskCancelRequested:
		weh.decisionsHelper.handleActivityTaskCancelRequested(
			event.GetActivityTaskCancelRequestedEventAttributes().GetScheduledEventId())

	case eventpb.EventType_ActivityTaskCanceled:
		err = weh.handleActivityTaskCanceled(event)

	case eventpb.EventType_TimerStarted:
		weh.decisionsHelper.handleTimerStarted(event.GetTimerStartedEventAttributes().GetTimerId())

	case eventpb.EventType_TimerFired:
		weh.handleTimerFired(event)

	case eventpb.EventType_TimerCanceled:
		weh.decisionsHelper.handleTimerCanceled(event.GetTimerCanceledEventAttributes().GetTimerId())

	case eventpb.EventType_CancelTimerFailed:
		weh.decisionsHelper.handleCancelTimerFailed(event.GetCancelTimerFailedEventAttributes().GetTimerId())

	case eventpb.EventType_WorkflowExecutionCancelRequested:
		weh.handleWorkflowExecutionCancelRequested()

	case eventpb.EventType_WorkflowExecutionCanceled:
		// No Operation.

	case eventpb.EventType_RequestCancelExternalWorkflowExecutionInitiated:
		_ = weh.handleRequestCancelExternalWorkflowExecutionInitiated(event)

	case eventpb.EventType_RequestCancelExternalWorkflowExecutionFailed:
		_ = weh.handleRequestCancelExternalWorkflowExecutionFailed(event)

	case eventpb.EventType_ExternalWorkflowExecutionCancelRequested:
		_ = weh.handleExternalWorkflowExecutionCancelRequested(event)

	case eventpb.EventType_WorkflowExecutionContinuedAsNew:
		// No Operation.

	case eventpb.EventType_WorkflowExecutionSignaled:
		weh.handleWorkflowExecutionSignaled(event.GetWorkflowExecutionSignaledEventAttributes())

	case eventpb.EventType_SignalExternalWorkflowExecutionInitiated:
		signalID := string(event.GetSignalExternalWorkflowExecutionInitiatedEventAttributes().Control)
		weh.decisionsHelper.handleSignalExternalWorkflowExecutionInitiated(event.GetEventId(), signalID)

	case eventpb.EventType_SignalExternalWorkflowExecutionFailed:
		_ = weh.handleSignalExternalWorkflowExecutionFailed(event)

	case eventpb.EventType_ExternalWorkflowExecutionSignaled:
		_ = weh.handleSignalExternalWorkflowExecutionCompleted(event)

	case eventpb.EventType_MarkerRecorded:
		err = weh.handleMarkerRecorded(event.GetEventId(), event.GetMarkerRecordedEventAttributes())

	case eventpb.EventType_StartChildWorkflowExecutionInitiated:
		weh.decisionsHelper.handleStartChildWorkflowExecutionInitiated(
			event.GetStartChildWorkflowExecutionInitiatedEventAttributes().GetWorkflowId())

	case eventpb.EventType_StartChildWorkflowExecutionFailed:
		err = weh.handleStartChildWorkflowExecutionFailed(event)

	case eventpb.EventType_ChildWorkflowExecutionStarted:
		err = weh.handleChildWorkflowExecutionStarted(event)

	case eventpb.EventType_ChildWorkflowExecutionCompleted:
		err = weh.handleChildWorkflowExecutionCompleted(event)

	case eventpb.EventType_ChildWorkflowExecutionFailed:
		err = weh.handleChildWorkflowExecutionFailed(event)

	case eventpb.EventType_ChildWorkflowExecutionCanceled:
		err = weh.handleChildWorkflowExecutionCanceled(event)

	case eventpb.EventType_ChildWorkflowExecutionTimedOut:
		err = weh.handleChildWorkflowExecutionTimedOut(event)

	case eventpb.EventType_ChildWorkflowExecutionTerminated:
		err = weh.handleChildWorkflowExecutionTerminated(event)

	case eventpb.EventType_UpsertWorkflowSearchAttributes:
		weh.handleUpsertWorkflowSearchAttributes(event)

	default:
		weh.logger.Error("unknown event type",
			zap.Int64(tagEventID, event.GetEventId()),
			zap.String(tagEventType, event.GetEventType().String()))
		// Do not fail to be forward compatible with new events
	}

	if err != nil {
		return err
	}

	// When replaying histories to get stack trace or current state the last event might be not
	// decision started. So always call OnDecisionTaskStarted on the last event.
	// Don't call for EventType_DecisionTaskStarted as it was already called when handling it.
	if isLast && event.GetEventType() != eventpb.EventType_DecisionTaskStarted {
		weh.workflowDefinition.OnDecisionTaskStarted()
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) ProcessQuery(queryType string, queryArgs *commonpb.Payloads) (*commonpb.Payloads, error) {
	switch queryType {
	case QueryTypeStackTrace:
		return weh.encodeArg(weh.StackTrace())
	case QueryTypeOpenSessions:
		return weh.encodeArg(weh.getOpenSessions())
	default:
		result, err := weh.queryHandler(queryType, queryArgs)
		if err != nil {
			return nil, err
		}

		if result.Size() > queryResultSizeLimit {
			weh.logger.Error("Query result size exceeds limit.",
				zap.String(tagQueryType, queryType),
				zap.String(tagWorkflowID, weh.workflowInfo.WorkflowExecution.ID),
				zap.String(tagRunID, weh.workflowInfo.WorkflowExecution.RunID))
			return nil, fmt.Errorf("query result size (%v) exceeds limit (%v)", result.Size(), queryResultSizeLimit)
		}

		return result, nil
	}
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
	attributes *eventpb.WorkflowExecutionStartedEventAttributes) (err error) {
	weh.workflowDefinition, err = weh.registry.getWorkflowDefinition(
		weh.workflowInfo.WorkflowType,
	)
	if err != nil {
		return err
	}

	// Invoke the workflow.
	weh.workflowDefinition.Execute(weh, attributes.Header, attributes.Input)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskCompleted(event *eventpb.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskClosed(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}
	activity.handle(event.GetActivityTaskCompletedEventAttributes().Result, nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskFailed(event *eventpb.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskClosed(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}

	attributes := event.GetActivityTaskFailedEventAttributes()
	err := constructError(attributes.Reason, attributes.Details, weh.GetDataConverter())
	activity.handle(nil, err)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskTimedOut(event *eventpb.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskClosed(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}

	var err error
	attributes := event.GetActivityTaskTimedOutEventAttributes()
	if len(attributes.GetLastFailureReason()) > 0 && attributes.GetTimeoutType() == commonpb.TimeoutType_StartToClose {
		// When retry activity timeout, it is possible that previous attempts got other customer timeout errors.
		// To stabilize the error type, we always return the customer error.
		// See more details of background: https://github.com/temporalio/temporal/issues/185
		err = constructError(attributes.GetLastFailureReason(), attributes.LastFailureDetails, weh.GetDataConverter())
	} else {
		details := newEncodedValues(attributes.Details, weh.GetDataConverter())
		err = NewTimeoutError(attributes.GetTimeoutType(), details)
	}
	activity.handle(nil, err)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskCanceled(event *eventpb.HistoryEvent) error {
	activityID := weh.decisionsHelper.getActivityID(event)
	decision := weh.decisionsHelper.handleActivityTaskCanceled(activityID)
	activity := decision.getData().(*scheduledActivity)
	if activity.handled {
		return nil
	}

	if decision.isDone() || !activity.waitForCancelRequest {
		// Clear this so we don't have a recursive call that while executing might call the cancel one.
		details := newEncodedValues(event.GetActivityTaskCanceledEventAttributes().Details, weh.GetDataConverter())
		err := NewCanceledError(details)
		activity.handle(nil, err)
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleTimerFired(event *eventpb.HistoryEvent) {
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

func (weh *workflowExecutionEventHandlerImpl) handleMarkerRecorded(
	eventID int64,
	attributes *eventpb.MarkerRecordedEventAttributes,
) error {
	encodedValues := newEncodedValues(attributes.Details, weh.dataConverter)
	switch attributes.GetMarkerName() {
	case sideEffectMarkerName:
		var sideEffectID int64
		var result *commonpb.Payloads
		_ = encodedValues.Get(&sideEffectID, result)
		weh.sideEffectResult[sideEffectID] = result
		return nil
	case versionMarkerName:
		var changeID string
		var version Version
		_ = encodedValues.Get(&changeID, &version)
		weh.changeVersions[changeID] = version
		weh.decisionsHelper.handleVersionMarker(eventID, changeID)
		return nil
	case localActivityMarkerName:
		return weh.handleLocalActivityMarker(attributes.Details)
	case mutableSideEffectMarkerName:
		var fixedID string
		var result *commonpb.Payloads
		_ = encodedValues.Get(&fixedID, result)
		weh.mutableSideEffect[fixedID] = result
		return nil
	default:
		return fmt.Errorf("unknown marker name \"%v\" for eventId \"%v\"",
			attributes.GetMarkerName(), eventID)
	}
}

func (weh *workflowExecutionEventHandlerImpl) handleLocalActivityMarker(markerData *commonpb.Payloads) error {
	lamd := localActivityMarkerData{}

	if err := weh.dataConverter.FromData(markerData, &lamd); err != nil {
		return err
	}

	if la, ok := weh.pendingLaTasks[lamd.ActivityID]; ok {
		if len(lamd.ActivityType) > 0 && lamd.ActivityType != la.params.ActivityType {
			// history marker mismatch to the current code.
			panicMsg := fmt.Sprintf("code execute local activity %v, but history event found %v, markerData: %v", la.params.ActivityType, lamd.ActivityType, markerData)
			panicIllegalState(panicMsg)
		}
		weh.decisionsHelper.recordLocalActivityMarker(lamd.ActivityID, markerData)
		delete(weh.pendingLaTasks, lamd.ActivityID)
		delete(weh.unstartedLaTasks, lamd.ActivityID)
		lar := &localActivityResultWrapper{}
		if len(lamd.ErrReason) > 0 {
			lar.attempt = lamd.Attempt
			lar.backoff = lamd.Backoff
			lar.err = constructError(lamd.ErrReason, lamd.Err, weh.GetDataConverter())
		} else {
			lar.result = lamd.Result
		}
		la.callback(lar)

		// update time
		weh.SetCurrentReplayTime(lamd.ReplayTime)

		// resume workflow execution after apply local activity result
		weh.workflowDefinition.OnDecisionTaskStarted()
	}

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) ProcessLocalActivityResult(lar *localActivityResult) error {
	// convert local activity result and error to marker data
	lamd := localActivityMarkerData{
		ActivityID:   lar.task.activityID,
		ActivityType: lar.task.params.ActivityType,
		ReplayTime:   weh.currentReplayTime.Add(time.Since(weh.currentLocalTime)),
		Attempt:      lar.task.attempt,
	}
	if lar.err != nil {
		errReason, errDetails := getErrorDetails(lar.err, weh.GetDataConverter())
		lamd.ErrReason = errReason
		lamd.Err = errDetails
		lamd.Backoff = lar.backoff
	} else {
		lamd.Result = lar.result
	}

	// encode marker data
	markerData, err := weh.encodeArg(lamd)
	if err != nil {
		return err
	}

	// create marker event for local activity result
	markerEvent := &eventpb.HistoryEvent{
		EventType: eventpb.EventType_MarkerRecorded,
		Attributes: &eventpb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &eventpb.MarkerRecordedEventAttributes{
			MarkerName: localActivityMarkerName,
			Details:    markerData,
		}},
	}

	// apply the local activity result to workflow
	return weh.ProcessEvent(markerEvent, false, false)
}

func (weh *workflowExecutionEventHandlerImpl) handleWorkflowExecutionSignaled(
	attributes *eventpb.WorkflowExecutionSignaledEventAttributes) {
	weh.signalHandler(attributes.GetSignalName(), attributes.Input)
}

func (weh *workflowExecutionEventHandlerImpl) handleStartChildWorkflowExecutionFailed(event *eventpb.HistoryEvent) error {
	attributes := event.GetStartChildWorkflowExecutionFailedEventAttributes()
	childWorkflowID := attributes.GetWorkflowId()
	decision := weh.decisionsHelper.handleStartChildWorkflowExecutionFailed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}

	err := serviceerror.NewWorkflowExecutionAlreadyStarted("Workflow execution already started", "", "")
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionStarted(event *eventpb.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionStartedEventAttributes()
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

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionCompleted(event *eventpb.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionCompletedEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	childWorkflow.handle(attributes.Result, nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionFailed(event *eventpb.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionFailedEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionClosed(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}

	err := constructError(attributes.GetReason(), attributes.Details, weh.GetDataConverter())
	childWorkflow.handle(nil, err)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionCanceled(event *eventpb.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionCanceledEventAttributes()
	childWorkflowID := attributes.WorkflowExecution.GetWorkflowId()
	decision := weh.decisionsHelper.handleChildWorkflowExecutionCanceled(childWorkflowID)
	childWorkflow := decision.getData().(*scheduledChildWorkflow)
	if childWorkflow.handled {
		return nil
	}
	details := newEncodedValues(attributes.Details, weh.GetDataConverter())
	err := NewCanceledError(details)
	childWorkflow.handle(nil, err)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionTimedOut(event *eventpb.HistoryEvent) error {
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

func (weh *workflowExecutionEventHandlerImpl) handleChildWorkflowExecutionTerminated(event *eventpb.HistoryEvent) error {
	attributes := event.GetChildWorkflowExecutionTerminatedEventAttributes()
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

func (weh *workflowExecutionEventHandlerImpl) handleUpsertWorkflowSearchAttributes(event *eventpb.HistoryEvent) {
	weh.updateWorkflowInfoWithSearchAttributes(event.GetUpsertWorkflowSearchAttributesEventAttributes().SearchAttributes)
}

func (weh *workflowExecutionEventHandlerImpl) handleRequestCancelExternalWorkflowExecutionInitiated(event *eventpb.HistoryEvent) error {
	// For cancellation of child workflow only, we do not use cancellation ID
	// for cancellation of external workflow, we have to use cancellation ID
	attribute := event.GetRequestCancelExternalWorkflowExecutionInitiatedEventAttributes()
	workflowID := attribute.WorkflowExecution.GetWorkflowId()
	cancellationID := string(attribute.Control)
	weh.decisionsHelper.handleRequestCancelExternalWorkflowExecutionInitiated(event.GetEventId(), workflowID, cancellationID)
	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleExternalWorkflowExecutionCancelRequested(event *eventpb.HistoryEvent) error {
	// For cancellation of child workflow only, we do not use cancellation ID
	// for cancellation of external workflow, we have to use cancellation ID
	attributes := event.GetExternalWorkflowExecutionCancelRequestedEventAttributes()
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

func (weh *workflowExecutionEventHandlerImpl) handleRequestCancelExternalWorkflowExecutionFailed(event *eventpb.HistoryEvent) error {
	// For cancellation of child workflow only, we do not use cancellation ID
	// for cancellation of external workflow, we have to use cancellation ID
	attributes := event.GetRequestCancelExternalWorkflowExecutionFailedEventAttributes()
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

func (weh *workflowExecutionEventHandlerImpl) handleSignalExternalWorkflowExecutionCompleted(event *eventpb.HistoryEvent) error {
	attributes := event.GetExternalWorkflowExecutionSignaledEventAttributes()
	decision := weh.decisionsHelper.handleSignalExternalWorkflowExecutionCompleted(attributes.GetInitiatedEventId())
	signal := decision.getData().(*scheduledSignal)
	if signal.handled {
		return nil
	}
	signal.handle(nil, nil)

	return nil
}

func (weh *workflowExecutionEventHandlerImpl) handleSignalExternalWorkflowExecutionFailed(event *eventpb.HistoryEvent) error {
	attributes := event.GetSignalExternalWorkflowExecutionFailedEventAttributes()
	decision := weh.decisionsHelper.handleSignalExternalWorkflowExecutionFailed(attributes.GetInitiatedEventId())
	signal := decision.getData().(*scheduledSignal)
	if signal.handled {
		return nil
	}

	var err error
	switch attributes.GetCause() {
	case eventpb.WorkflowExecutionFailedCause_UnknownExternalWorkflowExecution:
		err = newUnknownExternalWorkflowExecutionError()
	default:
		err = fmt.Errorf("signal external workflow failed, %v", attributes.GetCause())
	}

	signal.handle(nil, err)

	return nil
}
