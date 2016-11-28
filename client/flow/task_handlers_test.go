package flow

import (
	"testing"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/common"
	log "github.com/Sirupsen/logrus"

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

func createWorkflowTask(events []*m.HistoryEvent, previousStartEventID int64) *WorkflowTask {
	return &WorkflowTask{
		task: &m.PollForDecisionTaskResponse{
			PreviousStartedEventId: common.Int64Ptr(previousStartEventID),
			WorkflowType:           common.WorkflowTypePtr(m.WorkflowType{Name: common.StringPtr("testWorkflow")}),
			History:                &m.History{Events: events}}}
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted() {
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
	}
	logger := log.WithFields(log.Fields{})
	workflowTask := createWorkflowTask(testEvents, 0)
	workflowTaskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", testWorkflowDefinitionFactory, logger)
	response, err := workflowTaskHandler.ProcessWorkflowTask(workflowTask)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_ActivityTaskScheduled() {
	logger := log.WithFields(log.Fields{})
	// Schedule an activity and see if we complete workflow.
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{}),
		createTestEventActivityTaskScheduled(2, &m.ActivityTaskScheduledEventAttributes{ActivityId: common.StringPtr("0")}),
		createTestEventActivityTaskStarted(3, &m.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(4, &m.ActivityTaskCompletedEventAttributes{ScheduledEventId: common.Int64Ptr(2)}),
	}
	workflowTask := createWorkflowTask(testEvents, 0)
	workflowTaskHandler := newWorkflowTaskHandler("taskListName", "test-id-1", testWorkflowDefinitionFactory, logger)
	response, err := workflowTaskHandler.ProcessWorkflowTask(workflowTask)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(2, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
	s.Equal(m.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[1].GetDecisionType())
	s.NotNil(response.GetDecisions()[1].GetCompleteWorkflowExecutionDecisionAttributes())

	// Schedule an activity and see if we complete workflow, Having only one last decision.
	workflowTask = createWorkflowTask(testEvents, 2)
	response, err = workflowTaskHandler.ProcessWorkflowTask(workflowTask)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetCompleteWorkflowExecutionDecisionAttributes())
}
