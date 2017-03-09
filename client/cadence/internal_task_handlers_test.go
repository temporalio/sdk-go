package cadence

import (
	"testing"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	log "github.com/Sirupsen/logrus"
	"github.com/uber-common/bark"

	"github.com/stretchr/testify/suite"
)

type (
	TaskHandlersTestSuite struct {
		suite.Suite
		logger bark.Logger
	}
)

// Test suite.
func (s *TaskHandlersTestSuite) SetupTest() {
}

func (s *TaskHandlersTestSuite) SetupSuite() {
	log2 := log.New()
	//log2.Level = log.DebugLevel
	s.logger = bark.NewLoggerFromLogrus(log2)
}

func TestTaskHandlersTestSuite(t *testing.T) {
	suite.Run(t, new(TaskHandlersTestSuite))
}

func createTestEventWorkflowExecutionStarted(eventID int64, attr *m.WorkflowExecutionStartedEventAttributes) *m.HistoryEvent {
	return &m.HistoryEvent{EventId: common.Int64Ptr(eventID), EventType: common.EventTypePtr(m.EventType_WorkflowExecutionStarted), WorkflowExecutionStartedEventAttributes: attr}
}

func createTestEventActivityTaskScheduled(eventID int64, attr *m.ActivityTaskScheduledEventAttributes) *m.HistoryEvent {
	return &m.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(m.EventType_ActivityTaskScheduled),
		ActivityTaskScheduledEventAttributes: attr}
}

func createTestEventActivityTaskStarted(eventID int64, attr *m.ActivityTaskStartedEventAttributes) *m.HistoryEvent {
	return &m.HistoryEvent{
		EventId:                            common.Int64Ptr(eventID),
		EventType:                          common.EventTypePtr(m.EventType_ActivityTaskStarted),
		ActivityTaskStartedEventAttributes: attr}
}

func createTestEventActivityTaskCompleted(eventID int64, attr *m.ActivityTaskCompletedEventAttributes) *m.HistoryEvent {
	return &m.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(m.EventType_ActivityTaskCompleted),
		ActivityTaskCompletedEventAttributes: attr}
}

func createTestEventDecisionTaskScheduled(eventID int64, attr *m.DecisionTaskScheduledEventAttributes) *m.HistoryEvent {
	return &m.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(m.EventType_DecisionTaskScheduled),
		DecisionTaskScheduledEventAttributes: attr}
}

func createTestEventDecisionTaskStarted(eventID int64) *m.HistoryEvent {
	return &m.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(m.EventType_DecisionTaskStarted)}
}

func createTestEventDecisionTaskCompleted(eventID int64, attr *m.DecisionTaskCompletedEventAttributes) *m.HistoryEvent {
	return &m.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(m.EventType_DecisionTaskCompleted),
		DecisionTaskCompletedEventAttributes: attr}
}

func createWorkflowTask(events []*m.HistoryEvent, previousStartEventID int64) *m.PollForDecisionTaskResponse {
	return &m.PollForDecisionTaskResponse{
		PreviousStartedEventId: common.Int64Ptr(previousStartEventID),
		WorkflowType:           workflowTypePtr(WorkflowType{"testWorkflow"}),
		History:                &m.History{Events: events},
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted() {
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
	}
	task := createWorkflowTask(testEvents, 0)
	taskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", testWorkflowDefinitionFactory, s.logger, nil, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_ActivityTaskScheduled() {
	// Schedule an activity and see if we complete workflow.
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
		createTestEventActivityTaskScheduled(2, &m.ActivityTaskScheduledEventAttributes{ActivityId: common.StringPtr("0")}),
		createTestEventActivityTaskStarted(3, &m.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(4, &m.ActivityTaskCompletedEventAttributes{ScheduledEventId: common.Int64Ptr(2)}),
	}
	task := createWorkflowTask(testEvents, 0)
	taskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", testWorkflowDefinitionFactory, s.logger, nil, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	s.NoError(err)
	s.NotNil(response)
	s.Equal(2, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
	s.Equal(m.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[1].GetDecisionType())
	s.NotNil(response.GetDecisions()[1].GetCompleteWorkflowExecutionDecisionAttributes())

	// Schedule an activity and see if we complete workflow, Having only one last decision.
	task = createWorkflowTask(testEvents, 2)
	response, _, err = taskHandler.ProcessWorkflowTask(task, false)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_CancelActivityTask() {
	// Schedule an activity and see if we complete workflow.
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
		createTestEventDecisionTaskScheduled(2, &m.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 0)

	twdFactory := func(workflowType WorkflowType) (workflowDefinition, error) {
		return &helloWorldWorkflow{cancelActivity: true}, nil
	}

	taskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", twdFactory, s.logger, nil, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	s.NoError(err)
	s.NotNil(response)
	//s.printAllDecisions(response.GetDecisions())
	s.Equal(3, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
	s.Equal(m.DecisionType_RequestCancelActivityTask, response.GetDecisions()[1].GetDecisionType())
	s.NotNil(response.GetDecisions()[1].GetRequestCancelActivityTaskDecisionAttributes())
	s.Equal(m.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[2].GetDecisionType())
	s.NotNil(response.GetDecisions()[2].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_PressurePoints() {
	// Schedule a decision activity and see if we complete workflow.
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
		createTestEventActivityTaskScheduled(2, &m.ActivityTaskScheduledEventAttributes{ActivityId: common.StringPtr("0")}),
	}
	task := createWorkflowTask(testEvents, 0)

	pressurePoints := make(map[string]map[string]string)
	pressurePoints[PressurePointTypeActivityTaskScheduleTimeout] = map[string]string{PressurePointConfigProbability: "100"}
	ppMgr := &pressurePointMgrImpl{config: pressurePoints, logger: s.logger}

	taskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", testWorkflowDefinitionFactory, s.logger, nil, ppMgr)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	s.Error(err)
	s.Nil(response)
}

func (s *TaskHandlersTestSuite) printAllDecisions(decisions []*m.Decision) {
	for i, d := range decisions {
		s.logger.Infof("Decision: %v: %+v", i, d)
	}
}
