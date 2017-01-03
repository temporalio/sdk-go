package flow

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
	}
)

// Test suite.
func (s *TaskHandlersTestSuite) SetupTest() {
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

func createWorkflowTask(events []*m.HistoryEvent, previousStartEventID int64) *workflowTask {
	return &workflowTask{
		task: &m.PollForDecisionTaskResponse{
			PreviousStartedEventId: common.Int64Ptr(previousStartEventID),
			WorkflowType:           WorkflowTypePtr(WorkflowType{"testWorkflow"}),
			History:                &m.History{Events: events}}}
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted() {
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
	}
	logger := bark.NewLoggerFromLogrus(log.New())
	task := createWorkflowTask(testEvents, 0)
	taskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", testWorkflowDefinitionFactory, logger, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_ActivityTaskScheduled() {
	logger := bark.NewLoggerFromLogrus(log.New())
	// Schedule an activity and see if we complete workflow.
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
		createTestEventActivityTaskScheduled(2, &m.ActivityTaskScheduledEventAttributes{ActivityId: common.StringPtr("0")}),
		createTestEventActivityTaskStarted(3, &m.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(4, &m.ActivityTaskCompletedEventAttributes{ScheduledEventId: common.Int64Ptr(2)}),
	}
	task := createWorkflowTask(testEvents, 0)
	taskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", testWorkflowDefinitionFactory, logger, nil)
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
