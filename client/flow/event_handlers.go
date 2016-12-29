package flow

import (
	"fmt"

	log "github.com/Sirupsen/logrus"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
)

type (
	// completionHandler Handler to indicate completion result
	completionHandler func(result []byte, err Error)

	// workflowExecutionEventHandlerImpl handler to handle workflowExecutionEventHandler
	workflowExecutionEventHandlerImpl struct {
		*workflowContextImpl
		contextLogger      *log.Entry
		workflowDefinition WorkflowDefinition
	}

	// workflowContextImpl an implementation of WorkflowContext represents a context for workflow execution.
	workflowContextImpl struct {
		workflowInfo              *WorkflowInfo
		workflowDefinitionFactory WorkflowDefinitionFactory

		scheduledActivites           map[string]resultHandler // Map of Activities(activity ID ->) and their response handlers
		scheduledEventIDToActivityID map[int64]string         // Mapping from scheduled event ID to activity ID
		counterID                    int32                    // To generate activity IDs
		executeDecisions             []*m.Decision            // Decisions made during the execute of the workflow
		completeHandler              completionHandler        // events completion handler
		contextLogger                *log.Entry
	}
)

func newWorkflowExecutionEventHandler(workflowInfo *WorkflowInfo, workflowDefinitionFactory WorkflowDefinitionFactory,
	completeHandler completionHandler, logger *log.Entry) workflowExecutionEventHandler {
	context := &workflowContextImpl{
		workflowInfo:                 workflowInfo,
		workflowDefinitionFactory:    workflowDefinitionFactory,
		scheduledActivites:           make(map[string]resultHandler),
		scheduledEventIDToActivityID: make(map[int64]string),
		executeDecisions:             make([]*m.Decision, 0),
		completeHandler:              completeHandler,
		contextLogger:                logger}
	return &workflowExecutionEventHandlerImpl{context, logger, nil}
}

func (wc *workflowContextImpl) WorkflowInfo() *WorkflowInfo {
	return wc.workflowInfo
}

func (wc *workflowContextImpl) Complete(result []byte, err Error) {
	wc.completeHandler(result, err)
}

func (wc *workflowContextImpl) GenerateActivityID() string {
	activityID := wc.counterID
	wc.counterID++
	return fmt.Sprintf("%d", activityID)
}

func (wc *workflowContextImpl) SwapExecuteDecisions(decisions []*m.Decision) []*m.Decision {
	oldDecisions := wc.executeDecisions
	wc.executeDecisions = decisions
	return oldDecisions
}

func (wc *workflowContextImpl) CreateNewDecision(decisionType m.DecisionType) *m.Decision {
	return &m.Decision{
		DecisionType: common.DecisionTypePtr(decisionType),
	}
}

func (wc *workflowContextImpl) ExecuteActivity(parameters ExecuteActivityParameters, callback resultHandler) {
	scheduleTaskAttr := &m.ScheduleActivityTaskDecisionAttributes{}
	if parameters.ActivityID == nil {
		scheduleTaskAttr.ActivityId = common.StringPtr(wc.GenerateActivityID())
	} else {
		scheduleTaskAttr.ActivityId = parameters.ActivityID
	}
	scheduleTaskAttr.ActivityType = ActivityTypePtr(parameters.ActivityType)
	scheduleTaskAttr.TaskList = common.TaskListPtr(m.TaskList{Name: common.StringPtr(parameters.TaskListName)})
	scheduleTaskAttr.Input = parameters.Input
	scheduleTaskAttr.ScheduleToCloseTimeoutSeconds = common.Int32Ptr(parameters.ScheduleToCloseTimeoutSeconds)
	scheduleTaskAttr.StartToCloseTimeoutSeconds = common.Int32Ptr(parameters.StartToCloseTimeoutSeconds)
	scheduleTaskAttr.ScheduleToStartTimeoutSeconds = common.Int32Ptr(parameters.ScheduleToStartTimeoutSeconds)

	decision := wc.CreateNewDecision(m.DecisionType_ScheduleActivityTask)
	decision.ScheduleActivityTaskDecisionAttributes = scheduleTaskAttr

	wc.executeDecisions = append(wc.executeDecisions, decision)
	wc.scheduledActivites[scheduleTaskAttr.GetActivityId()] = callback
	wc.contextLogger.Debugf("ExectueActivity: %s: %+v", scheduleTaskAttr.GetActivityId(), scheduleTaskAttr)
}

func (weh *workflowExecutionEventHandlerImpl) ProcessEvent(event *m.HistoryEvent) ([]*m.Decision, error) {
	if event == nil {
		return nil, fmt.Errorf("nil event provided")
	}

	switch event.GetEventType() {
	case m.EventType_WorkflowExecutionStarted:
		return weh.handleWorkflowExecutionStarted(event.WorkflowExecutionStartedEventAttributes)

	case m.EventType_WorkflowExecutionCompleted:
		// No Operation
	case m.EventType_WorkflowExecutionFailed:
		// No Operation
	case m.EventType_WorkflowExecutionTimedOut:
		// TODO:
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
		return weh.handleActivityTaskCompleted(event.ActivityTaskCompletedEventAttributes)

	case m.EventType_ActivityTaskFailed:
		return weh.handleActivityTaskFailed(event.ActivityTaskFailedEventAttributes)

	case m.EventType_ActivityTaskTimedOut:
		return weh.handleActivityTaskTimedOut(event.ActivityTaskTimedOutEventAttributes)

	case m.EventType_TimerStarted:
		// TODO:
	case m.EventType_TimerFired:
		// TODO:
	default:
		return nil, fmt.Errorf("missing event handler for event type: %v", event)
	}
	return nil, nil
}

func (weh *workflowExecutionEventHandlerImpl) StackTrace() string {
	return weh.workflowDefinition.StackTrace()
}

func (weh *workflowExecutionEventHandlerImpl) Close() {
}

func (weh *workflowExecutionEventHandlerImpl) handleWorkflowExecutionStarted(
	attributes *m.WorkflowExecutionStartedEventAttributes) (decisions []*m.Decision, err error) {
	weh.workflowDefinition, err = weh.workflowDefinitionFactory(weh.workflowInfo.workflowType)
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
		return nil, fmt.Errorf("unable to find callback handler for the event: %v with activity ID: %v", attributes, activityID)
	}

	if handler != nil {
		// Invoke the callback
		handler(attributes.GetResult_(), nil)
	}
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
		return nil, fmt.Errorf("unable to find callback handler for the event: %v", attributes)
	}

	if handler != nil {
		err := &activityTaskFailedError{
			reason:  *attributes.Reason,
			details: attributes.Details}
		// Invoke the callback
		handler(nil, err)
	}
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
		return nil, fmt.Errorf("unable to find callback handler for the event: %v", attributes)
	}

	if handler != nil {
		err := &activityTaskTimeoutError{TimeoutType: attributes.GetTimeoutType()}
		// Invoke the callback
		handler(nil, err)
	}
	return weh.SwapExecuteDecisions([]*m.Decision{}), nil
}
