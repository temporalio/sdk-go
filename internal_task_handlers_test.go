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
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/util"
	"go.uber.org/cadence/mocks"
	"go.uber.org/zap"
)

const (
	testDomain = "test-domain"
)

type (
	TaskHandlersTestSuite struct {
		suite.Suite
		logger *zap.Logger
	}
)

// Test suite.
func (s *TaskHandlersTestSuite) SetupTest() {
}

func (s *TaskHandlersTestSuite) SetupSuite() {
	logger, _ := zap.NewDevelopment()
	s.logger = logger
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
		WorkflowExecution: &m.WorkflowExecution{
			WorkflowId: common.StringPtr("fake-workflow-id"),
			RunId:      common.StringPtr("fake-run-id"),
		},
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted() {
	taskList := "tl1"
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
	}
	task := createWorkflowTask(testEvents, 0)
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   s.logger,
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_ActivityTaskScheduled() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
		createTestEventActivityTaskScheduled(2, &m.ActivityTaskScheduledEventAttributes{
			ActivityId:   common.StringPtr("0"),
			ActivityType: &m.ActivityType{Name: common.StringPtr("Greeter_Activity")},
			TaskList:     &m.TaskList{Name: &taskList},
		}),
		createTestEventActivityTaskStarted(3, &m.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(4, &m.ActivityTaskCompletedEventAttributes{ScheduledEventId: common.Int64Ptr(2)}),
		createTestEventDecisionTaskStarted(5),
	}
	task := createWorkflowTask(testEvents[0:1], 0)
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   s.logger,
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())

	// Schedule an activity and see if we complete workflow, Having only one last decision.
	task = createWorkflowTask(testEvents, 2)
	response, _, err = taskHandler.ProcessWorkflowTask(task, false)
	s.NoError(err)
	s.NotNil(response)
	s.Equal(1, len(response.GetDecisions()))
	s.Equal(m.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[0].GetDecisionType())
	s.NotNil(response.GetDecisions()[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_NondeterministicDetection() {
	// Schedule an activity and see if we complete workflow.
	taskList := "taskList"
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
		createTestEventActivityTaskScheduled(2, &m.ActivityTaskScheduledEventAttributes{
			ActivityId:   common.StringPtr("0"),
			ActivityType: &m.ActivityType{Name: common.StringPtr("some_random_activity")},
			TaskList:     &m.TaskList{Name: &taskList},
		}),
	}
	task := createWorkflowTask(testEvents, 2)
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   s.logger,
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	s.NotNil(err)
	s.Nil(response)
	s.Contains(err.Error(), "nondeterministic")
}

func (s *TaskHandlersTestSuite) TestWorkflowTask_CancelActivityTask() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskScheduled(2, &m.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 0)

	twdFactory := func(workflowType WorkflowType) (workflowDefinition, error) {
		return &helloWorldWorkflow{cancelActivity: true}, nil
	}

	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   s.logger,
	}
	taskHandler := newWorkflowTaskHandler(twdFactory, testDomain, params, nil)
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
	taskList := "tl1"
	testEvents := []*m.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &m.WorkflowExecutionStartedEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
		createTestEventActivityTaskScheduled(2, &m.ActivityTaskScheduledEventAttributes{ActivityId: common.StringPtr("0")}),
	}
	task := createWorkflowTask(testEvents, 0)

	pressurePoints := make(map[string]map[string]string)
	pressurePoints[pressurePointTypeActivityTaskScheduleTimeout] = map[string]string{pressurePointConfigProbability: "100"}
	ppMgr := &pressurePointMgrImpl{config: pressurePoints, logger: s.logger}

	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   s.logger,
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, ppMgr)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	s.Error(err)
	s.Nil(response)
}

func (s *TaskHandlersTestSuite) TestHeartBeat_NoError() {
	mockService := mocks.TChanWorkflowService{}
	cancelRequested := false
	heartbeatResponse := m.RecordActivityTaskHeartbeatResponse{CancelRequested: &cancelRequested}
	mockService.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).Return(&heartbeatResponse, nil)

	cadenceInvoker := &cadenceInvoker{
		identity:  "Test_Cadence_Invoker",
		service:   &mockService,
		taskToken: nil,
	}

	heartbeatErr := cadenceInvoker.Heartbeat(nil)

	s.Nil(heartbeatErr)
}

func (s *TaskHandlersTestSuite) TestHeartBeat_NilResponseWithError() {
	mockService := &mocks.TChanWorkflowService{}
	entityNotExistsError := &m.EntityNotExistsError{}
	mockService.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).Return(nil, entityNotExistsError)

	cadenceInvoker := newServiceInvoker(
		nil,
		"Test_Cadence_Invoker",
		mockService,
		func() {})

	heartbeatErr := cadenceInvoker.Heartbeat(nil)
	s.NotNil(heartbeatErr)
	_, ok := (heartbeatErr).(*m.EntityNotExistsError)
	s.True(ok, "heartbeatErr must be EntityNotExistsError.")
}

func (s *TaskHandlersTestSuite) printAllDecisions(decisions []*m.Decision) {
	for _, d := range decisions {
		s.logger.Info(util.DecisionToString(d))
	}
}
