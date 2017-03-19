package cadence

// All code in this file is private to the package.

import (
	"fmt"
	"time"

	"github.com/uber-common/bark"

	m "code.uber.internal/devexp/cadence-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/cadence-client-go.git/common"
	"errors"
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
		logger             bark.Logger
	}

	// workflowEnvironmentImpl an implementation of workflowEnvironment represents a environment for workflow execution.
	workflowEnvironmentImpl struct {
		workflowInfo              *WorkflowInfo
		workflowDefinitionFactory workflowDefinitionFactory

		scheduledActivites             map[string]resultHandler // Map of Activities(activity ID ->) and their response handlers
		waitForCancelRequestActivities map[string]bool          // Map of activity ID to whether to wait for cancelation.
		scheduledEventIDToActivityID   map[int64]string         // Mapping from scheduled event ID to activity ID
		scheduledTimers                map[string]resultHandler // Map of scheduledTimers(timer ID ->) and their response handlers
		counterID                      int32                    // To generate activity IDs
		executeDecisions               []*m.Decision            // Decisions made during the execute of the workflow
		completeHandler                completionHandler        // events completion handler
		currentReplayTime              time.Time                // Indicates current replay time of the decision.
		logger                         bark.Logger
	}
)

func newWorkflowExecutionEventHandler(workflowInfo *WorkflowInfo, workflowDefinitionFactory workflowDefinitionFactory,
	completeHandler completionHandler, logger bark.Logger) workflowExecutionEventHandler {
	context := &workflowEnvironmentImpl{
		workflowInfo:                   workflowInfo,
		workflowDefinitionFactory:      workflowDefinitionFactory,
		scheduledActivites:             make(map[string]resultHandler),
		waitForCancelRequestActivities: make(map[string]bool),
		scheduledEventIDToActivityID:   make(map[int64]string),
		scheduledTimers:                make(map[string]resultHandler),
		executeDecisions:               make([]*m.Decision, 0),
		completeHandler:                completeHandler,
		logger:                         logger}
	return &workflowExecutionEventHandlerImpl{context, nil, logger}
}

func (wc *workflowEnvironmentImpl) WorkflowInfo() *WorkflowInfo {
	return wc.workflowInfo
}

func (wc *workflowEnvironmentImpl) Complete(result []byte, err error) {
	wc.completeHandler(result, err)
}

func (wc *workflowEnvironmentImpl) GenerateSequenceID() string {
	activityID := wc.counterID
	wc.counterID++
	return fmt.Sprintf("%d", activityID)
}

func (wc *workflowEnvironmentImpl) SwapExecuteDecisions(decisions []*m.Decision) []*m.Decision {
	oldDecisions := wc.executeDecisions
	wc.executeDecisions = decisions
	return oldDecisions
}

func (wc *workflowEnvironmentImpl) CreateNewDecision(decisionType m.DecisionType) *m.Decision {
	return &m.Decision{
		DecisionType: common.DecisionTypePtr(decisionType),
	}
}

func (wc *workflowEnvironmentImpl) ExecuteActivity(parameters ExecuteActivityParameters, callback resultHandler) *activityInfo {

	scheduleTaskAttr := &m.ScheduleActivityTaskDecisionAttributes{}
	if parameters.ActivityID == nil {
		scheduleTaskAttr.ActivityId = common.StringPtr(wc.GenerateSequenceID())
	} else {
		scheduleTaskAttr.ActivityId = parameters.ActivityID
	}
	scheduleTaskAttr.ActivityType = activityTypePtr(parameters.ActivityType)
	scheduleTaskAttr.TaskList = common.TaskListPtr(m.TaskList{Name: common.StringPtr(parameters.TaskListName)})
	scheduleTaskAttr.Input = parameters.Input
	scheduleTaskAttr.ScheduleToCloseTimeoutSeconds = common.Int32Ptr(parameters.ScheduleToCloseTimeoutSeconds)
	scheduleTaskAttr.StartToCloseTimeoutSeconds = common.Int32Ptr(parameters.StartToCloseTimeoutSeconds)
	scheduleTaskAttr.ScheduleToStartTimeoutSeconds = common.Int32Ptr(parameters.ScheduleToStartTimeoutSeconds)
	scheduleTaskAttr.HeartbeatTimeoutSeconds = common.Int32Ptr(parameters.HeartbeatTimeoutSeconds)

	decision := wc.CreateNewDecision(m.DecisionType_ScheduleActivityTask)
	decision.ScheduleActivityTaskDecisionAttributes = scheduleTaskAttr

	wc.executeDecisions = append(wc.executeDecisions, decision)
	wc.scheduledActivites[scheduleTaskAttr.GetActivityId()] = callback
	wc.waitForCancelRequestActivities[scheduleTaskAttr.GetActivityId()] = parameters.WaitForCancellation
	wc.logger.Debugf("ExectueActivity: %s: Type: %v, on TaskList: %v.", scheduleTaskAttr.GetActivityId(),
		scheduleTaskAttr.GetActivityType().GetName(), scheduleTaskAttr.GetTaskList().GetName())

	return &activityInfo{activityID: scheduleTaskAttr.GetActivityId()}
}

func (wc *workflowEnvironmentImpl) RequestCancelActivity(activityID string) {
	handler, ok := wc.scheduledActivites[activityID]
	if !ok {
		return
	}
	requestCancelAttr := &m.RequestCancelActivityTaskDecisionAttributes{
		ActivityId: common.StringPtr(activityID)}

	decision := wc.CreateNewDecision(m.DecisionType_RequestCancelActivityTask)
	decision.RequestCancelActivityTaskDecisionAttributes = requestCancelAttr
	wc.executeDecisions = append(wc.executeDecisions, decision)

	if wait, ok := wc.waitForCancelRequestActivities[activityID]; ok && !wait {
		handler(nil, NewCanceledError())
	}
	wc.logger.Debugf("RequestCancelActivity: %v.", requestCancelAttr.GetActivityId())
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
	decision := wc.CreateNewDecision(m.DecisionType_StartTimer)
	decision.StartTimerDecisionAttributes = startTimerAttr

	wc.executeDecisions = append(wc.executeDecisions, decision)
	wc.scheduledTimers[startTimerAttr.GetTimerId()] = callback
	wc.logger.Debugf("NewTimer: %s Created with a delay: %v", startTimerAttr.GetTimerId(), d)

	return &timerInfo{timerID: timerID}
}

func (wc *workflowEnvironmentImpl) RequestCancelTimer(timerID string) {
	handler, ok := wc.scheduledTimers[timerID]
	if !ok {
		return
	}
	cancelTimerAttr := &m.CancelTimerDecisionAttributes{TimerId: common.StringPtr(timerID)}
	decision := wc.CreateNewDecision(m.DecisionType_CancelTimer)
	decision.CancelTimerDecisionAttributes = cancelTimerAttr

	wc.executeDecisions = append(wc.executeDecisions, decision)

	handler(nil, NewCanceledError())
	delete(wc.scheduledTimers, timerID)

	wc.logger.Debugf("RequestCancelTimer: %v.", timerID)
}

func (weh *workflowExecutionEventHandlerImpl) ProcessEvent(event *m.HistoryEvent) ([]*m.Decision, bool, error) {

	if event == nil {
		return nil, false, fmt.Errorf("nil event provided")
	}

	unhandledDecision := false

	switch event.GetEventType() {
	case m.EventType_WorkflowExecutionStarted:
		d, err := weh.handleWorkflowExecutionStarted(event.WorkflowExecutionStartedEventAttributes)
		return d, unhandledDecision, err

	case m.EventType_WorkflowExecutionCompleted:
	// No Operation
	case m.EventType_WorkflowExecutionFailed:
	// No Operation
	case m.EventType_WorkflowExecutionTimedOut:
	// TODO:
	case m.EventType_CompleteWorkflowExecutionFailed:
		unhandledDecision = true
	case m.EventType_DecisionTaskScheduled:
	// No Operation
	case m.EventType_DecisionTaskStarted:
	// No Operation
	case m.EventType_DecisionTaskTimedOut:
	// TODO:
	case m.EventType_DecisionTaskCompleted:
	// TODO:
	case m.EventType_ActivityTaskScheduled:
		attributes := event.ActivityTaskScheduledEventAttributes
		weh.scheduledEventIDToActivityID[event.GetEventId()] = attributes.GetActivityId()

	case m.EventType_ActivityTaskStarted:
	// No Operation
	case m.EventType_ActivityTaskCompleted:
		d, err := weh.handleActivityTaskCompleted(event.ActivityTaskCompletedEventAttributes)
		return d, unhandledDecision, err

	case m.EventType_ActivityTaskFailed:
		d, err := weh.handleActivityTaskFailed(event.ActivityTaskFailedEventAttributes)
		return d, unhandledDecision, err

	case m.EventType_ActivityTaskTimedOut:
		d, err := weh.handleActivityTaskTimedOut(event.ActivityTaskTimedOutEventAttributes)
		return d, unhandledDecision, err

	case m.EventType_ActivityTaskCancelRequested:
		// No Operation.
	case m.EventType_RequestCancelActivityTaskFailed:
		// No operation.

	case m.EventType_ActivityTaskCanceled:
		d, err := weh.handleActivityTaskCanceled(event.ActivityTaskCanceledEventAttributes)
		return d, unhandledDecision, err

	case m.EventType_TimerStarted:
		// No Operation
	case m.EventType_TimerFired:
		d, err := weh.handleTimerFired(event.TimerFiredEventAttributes)
		return d, unhandledDecision, err

	case m.EventType_TimerCanceled:
		// No Operation:
		// As we always cancel the timer immediately if asked, we don't wait for it.
	case m.EventType_CancelTimerFailed:
		// No Operation.

	default:
		return nil, unhandledDecision, fmt.Errorf("missing event handler for event type: %v", event)
	}
	return nil, unhandledDecision, nil
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
	attributes *m.WorkflowExecutionStartedEventAttributes) (decisions []*m.Decision, err error) {
	weh.workflowDefinition, err = weh.workflowDefinitionFactory(weh.workflowInfo.WorkflowType)
	if err != nil {
		return nil, err
	}

	// Invoke the workflow.
	weh.workflowDefinition.Execute(weh, attributes.Input)
	return weh.SwapExecuteDecisions([]*m.Decision{}), nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskCompleted(
	attributes *m.ActivityTaskCompletedEventAttributes) ([]*m.Decision, error) {

	activityID, ok := weh.scheduledEventIDToActivityID[attributes.GetScheduledEventId()]
	if !ok {
		return nil, fmt.Errorf("unable to find activity ID for the event: %v", attributes)
	}
	handler, ok := weh.scheduledActivites[activityID]
	if !ok {
		if wait, exist := weh.waitForCancelRequestActivities[activityID]; exist && !wait {
			return []*m.Decision{}, nil
		}
		return nil, fmt.Errorf("unable to find callback handler for the event: %v, with activity ID: %v, ok: %v",
			attributes, activityID, ok)
	}

	// Clear this so we don't have a recursive call that while executing might call the cancel one.
	delete(weh.scheduledActivites, activityID)

	// Invoke the callback
	handler(attributes.GetResult_(), nil)

	return weh.SwapExecuteDecisions([]*m.Decision{}), nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskFailed(
	attributes *m.ActivityTaskFailedEventAttributes) ([]*m.Decision, error) {

	activityID, ok := weh.scheduledEventIDToActivityID[attributes.GetScheduledEventId()]
	if !ok {
		return nil, fmt.Errorf("unable to find activity ID for the event: %v", attributes)
	}
	handler, ok := weh.scheduledActivites[activityID]
	if !ok {
		if wait, exist := weh.waitForCancelRequestActivities[activityID]; exist && !wait {
			return []*m.Decision{}, nil
		}
		return nil, fmt.Errorf("unable to find callback handler for the event: %v, with activity ID: %v, ok: %v",
			attributes, activityID, ok)
	}

	// Clear this so we don't have a recursive call that while executing might call the cancel one.
	delete(weh.scheduledActivites, activityID)

	err := NewErrorWithDetails(*attributes.Reason, attributes.Details)
	// Invoke the callback
	handler(nil, err)
	return weh.SwapExecuteDecisions([]*m.Decision{}), nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskTimedOut(
	attributes *m.ActivityTaskTimedOutEventAttributes) ([]*m.Decision, error) {

	activityID, ok := weh.scheduledEventIDToActivityID[attributes.GetScheduledEventId()]
	if !ok {
		return nil, fmt.Errorf("unable to find activity ID for the event: %v", attributes)
	}
	handler, ok := weh.scheduledActivites[activityID]
	if !ok {
		if wait, exist := weh.waitForCancelRequestActivities[activityID]; exist && !wait {
			return []*m.Decision{}, nil
		}
		return nil, fmt.Errorf("unable to find callback handler for the event: %v, with activity ID: %v, ok: %v",
			attributes, activityID, ok)
	}

	// Clear this so we don't have a recursive call that while executing might call the cancel one.
	delete(weh.scheduledActivites, activityID)

	err := NewTimeoutError(attributes.GetTimeoutType())
	// Invoke the callback
	handler(nil, err)
	return weh.SwapExecuteDecisions([]*m.Decision{}), nil
}

func (weh *workflowExecutionEventHandlerImpl) handleActivityTaskCanceled(
	attributes *m.ActivityTaskCanceledEventAttributes) ([]*m.Decision, error) {

	activityID, ok := weh.scheduledEventIDToActivityID[attributes.GetScheduledEventId()]
	if !ok {
		return nil, fmt.Errorf("unable to find activity ID for the event: %v", attributes)
	}
	handler, ok := weh.scheduledActivites[activityID]
	if !ok {
		if wait, exist := weh.waitForCancelRequestActivities[activityID]; exist && !wait {
			return []*m.Decision{}, nil
		}
		return nil, fmt.Errorf("unable to find callback handler for the event: %v, ok: %v", attributes, ok)
	}

	// Clear this so we don't have a recursive call that while executing might call the cancel one.
	delete(weh.scheduledActivites, activityID)

	err := NewCanceledErrorWithDetails(attributes.GetDetails())
	// Invoke the callback
	handler(nil, err)
	return weh.SwapExecuteDecisions([]*m.Decision{}), nil
}

func (weh *workflowExecutionEventHandlerImpl) handleTimerFired(
	attributes *m.TimerFiredEventAttributes) ([]*m.Decision, error) {
	handler, ok := weh.scheduledTimers[attributes.GetTimerId()]
	if !ok {
		weh.logger.Debugf("Unable to find the timer callback when it is fired: %v", attributes.GetTimerId())
		return []*m.Decision{}, nil
	}

	// Clear this so we don't have a recursive call that while invoking might call the cancel one.
	delete(weh.scheduledTimers, attributes.GetTimerId())

	// Invoke the callback
	handler(nil, nil)
	return weh.SwapExecuteDecisions([]*m.Decision{}), nil
}
