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
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	s "go.uber.org/cadence/.gen/go/shared"
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
func (t *TaskHandlersTestSuite) SetupTest() {
}

func (t *TaskHandlersTestSuite) SetupSuite() {
	logger, _ := zap.NewDevelopment()
	t.logger = logger
}

func TestTaskHandlersTestSuite(t *testing.T) {
	suite.Run(t, new(TaskHandlersTestSuite))
}

func createTestEventWorkflowExecutionStarted(eventID int64, attr *s.WorkflowExecutionStartedEventAttributes) *s.HistoryEvent {
	return &s.HistoryEvent{EventId: common.Int64Ptr(eventID), EventType: common.EventTypePtr(s.EventType_WorkflowExecutionStarted), WorkflowExecutionStartedEventAttributes: attr}
}

func createTestEventActivityTaskScheduled(eventID int64, attr *s.ActivityTaskScheduledEventAttributes) *s.HistoryEvent {
	return &s.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(s.EventType_ActivityTaskScheduled),
		ActivityTaskScheduledEventAttributes: attr}
}

func createTestEventActivityTaskStarted(eventID int64, attr *s.ActivityTaskStartedEventAttributes) *s.HistoryEvent {
	return &s.HistoryEvent{
		EventId:                            common.Int64Ptr(eventID),
		EventType:                          common.EventTypePtr(s.EventType_ActivityTaskStarted),
		ActivityTaskStartedEventAttributes: attr}
}

func createTestEventActivityTaskCompleted(eventID int64, attr *s.ActivityTaskCompletedEventAttributes) *s.HistoryEvent {
	return &s.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(s.EventType_ActivityTaskCompleted),
		ActivityTaskCompletedEventAttributes: attr}
}

func createTestEventDecisionTaskScheduled(eventID int64, attr *s.DecisionTaskScheduledEventAttributes) *s.HistoryEvent {
	return &s.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(s.EventType_DecisionTaskScheduled),
		DecisionTaskScheduledEventAttributes: attr}
}

func createTestEventDecisionTaskStarted(eventID int64) *s.HistoryEvent {
	return &s.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(s.EventType_DecisionTaskStarted)}
}

func createTestEventDecisionTaskCompleted(eventID int64, attr *s.DecisionTaskCompletedEventAttributes) *s.HistoryEvent {
	return &s.HistoryEvent{
		EventId:   common.Int64Ptr(eventID),
		EventType: common.EventTypePtr(s.EventType_DecisionTaskCompleted),
		DecisionTaskCompletedEventAttributes: attr}
}

func createWorkflowTask(events []*s.HistoryEvent, previousStartEventID int64) *s.PollForDecisionTaskResponse {
	return &s.PollForDecisionTaskResponse{
		PreviousStartedEventId: common.Int64Ptr(previousStartEventID),
		WorkflowType:           workflowTypePtr(WorkflowType{"testWorkflow"}),
		History:                &s.History{Events: events},
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr("fake-workflow-id"),
			RunId:      common.StringPtr("fake-run-id"),
		},
	}
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted() {
	taskList := "tl1"
	testEvents := []*s.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &s.WorkflowExecutionStartedEventAttributes{TaskList: &s.TaskList{Name: &taskList}}),
	}
	task := createWorkflowTask(testEvents, 0)
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.GetDecisions()))
	t.Equal(s.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	t.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_ActivityTaskScheduled() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*s.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &s.WorkflowExecutionStartedEventAttributes{TaskList: &s.TaskList{Name: &taskList}}),
		createTestEventActivityTaskScheduled(2, &s.ActivityTaskScheduledEventAttributes{
			ActivityId:   common.StringPtr("0"),
			ActivityType: &s.ActivityType{Name: common.StringPtr("Greeter_Activity")},
			TaskList:     &s.TaskList{Name: &taskList},
		}),
		createTestEventActivityTaskStarted(3, &s.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(4, &s.ActivityTaskCompletedEventAttributes{ScheduledEventId: common.Int64Ptr(2)}),
		createTestEventDecisionTaskStarted(5),
	}
	task := createWorkflowTask(testEvents[0:1], 0)
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.GetDecisions()))
	t.Equal(s.DecisionType_ScheduleActivityTask, response.GetDecisions()[0].GetDecisionType())
	t.NotNil(response.GetDecisions()[0].GetScheduleActivityTaskDecisionAttributes())

	// Schedule an activity and see if we complete workflow, Having only one last decision.
	task = createWorkflowTask(testEvents, 2)
	response, _, err = taskHandler.ProcessWorkflowTask(task, false)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.GetDecisions()))
	t.Equal(s.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[0].GetDecisionType())
	t.NotNil(response.GetDecisions()[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_NondeterministicDetection() {
	taskList := "taskList"
	testEvents := []*s.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &s.WorkflowExecutionStartedEventAttributes{TaskList: &s.TaskList{Name: &taskList}}),
		createTestEventActivityTaskScheduled(2, &s.ActivityTaskScheduledEventAttributes{
			ActivityId:   common.StringPtr("0"),
			ActivityType: &s.ActivityType{Name: common.StringPtr("Greeter_Activity")},
			TaskList:     &s.TaskList{Name: &taskList},
		}),
	}
	task := createWorkflowTask(testEvents, 2)
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   zap.NewNop(),
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	// there should be no error as the history events matched the decisions.
	t.NoError(err)
	t.NotNil(response)

	// now change the history event so it does not match to decision produced via replay
	testEvents[1].ActivityTaskScheduledEventAttributes.ActivityType.Name = common.StringPtr("some-other-activity")
	response, _, err = taskHandler.ProcessWorkflowTask(task, false)
	t.Error(err)
	t.Nil(response)
	t.Contains(err.Error(), "nondeterministic")
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_CancelActivityBeforeSent() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*s.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &s.WorkflowExecutionStartedEventAttributes{TaskList: &s.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskScheduled(2, &s.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 0)

	twdFactory := func(workflowType WorkflowType) (workflowDefinition, error) {
		return &helloWorldWorkflow{cancelActivity: true}, nil
	}

	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(twdFactory, testDomain, params, nil)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	t.NoError(err)
	t.NotNil(response)
	//t.printAllDecisions(response.GetDecisions())
	t.Equal(1, len(response.GetDecisions()))
	t.Equal(s.DecisionType_CompleteWorkflowExecution, response.GetDecisions()[0].GetDecisionType())
	t.NotNil(response.GetDecisions()[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_PressurePoints() {
	// Schedule a decision activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*s.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &s.WorkflowExecutionStartedEventAttributes{TaskList: &s.TaskList{Name: &taskList}}),
		createTestEventActivityTaskScheduled(2, &s.ActivityTaskScheduledEventAttributes{ActivityId: common.StringPtr("0")}),
	}
	task := createWorkflowTask(testEvents, 0)

	pressurePoints := make(map[string]map[string]string)
	pressurePoints[pressurePointTypeActivityTaskScheduleTimeout] = map[string]string{pressurePointConfigProbability: "100"}
	ppMgr := &pressurePointMgrImpl{config: pressurePoints, logger: t.logger}

	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(testWorkflowDefinitionFactory, testDomain, params, ppMgr)
	response, _, err := taskHandler.ProcessWorkflowTask(task, false)

	t.Error(err)
	t.Nil(response)
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NoError() {
	mockService := mocks.TChanWorkflowService{}
	cancelRequested := false
	heartbeatResponse := s.RecordActivityTaskHeartbeatResponse{CancelRequested: &cancelRequested}
	mockService.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).Return(&heartbeatResponse, nil)

	cadenceInvoker := &cadenceInvoker{
		identity:  "Test_Cadence_Invoker",
		service:   &mockService,
		taskToken: nil,
	}

	heartbeatErr := cadenceInvoker.Heartbeat(nil)

	t.Nil(heartbeatErr)
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NilResponseWithError() {
	mockService := &mocks.TChanWorkflowService{}
	entityNotExistsError := &s.EntityNotExistsError{}
	mockService.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).Return(nil, entityNotExistsError)

	cadenceInvoker := newServiceInvoker(
		nil,
		"Test_Cadence_Invoker",
		mockService,
		func() {})

	heartbeatErr := cadenceInvoker.Heartbeat(nil)
	t.NotNil(heartbeatErr)
	_, ok := (heartbeatErr).(*s.EntityNotExistsError)
	t.True(ok, "heartbeatErr must be EntityNotExistsError.")
}

type testActivityDeadline struct {
	d time.Duration
}

func (t testActivityDeadline) Execute(ctx context.Context, input []byte) ([]byte, error) {
	if d, _ := ctx.Deadline(); d.IsZero() {
		panic("invalid deadline provided")
	}
	if t.d != 0 {
		// Wait till deadline expires.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, nil
}
func (t testActivityDeadline) ActivityType() ActivityType {
	return ActivityType{Name: "test"}
}

type deadlineTest struct {
	activities       []activity
	ScheduleTS       time.Time
	ScheduleDuration int32
	StartTS          time.Time
	StartDuration    int32
	err              error
}

var deadlineTests = []deadlineTest{
	{[]activity{&testActivityDeadline{}}, time.Now(), 1, time.Now(), 1, nil},
	{[]activity{&testActivityDeadline{}}, time.Now(), 2, time.Now(), 1, nil},
	{[]activity{&testActivityDeadline{}}, time.Now(), 1, time.Now(), 2, nil},
	{[]activity{&testActivityDeadline{}}, time.Now().Add(-1 * time.Second), 1, time.Now(), 1, context.DeadlineExceeded},
	{[]activity{&testActivityDeadline{}}, time.Now(), 1, time.Now().Add(-1 * time.Second), 1, context.DeadlineExceeded},
	{[]activity{&testActivityDeadline{}}, time.Now().Add(-1 * time.Second), 1, time.Now().Add(-1 * time.Second), 1, context.DeadlineExceeded},
	{[]activity{&testActivityDeadline{d: 2 * time.Second}}, time.Now(), 1, time.Now(), 1, context.DeadlineExceeded},
	{[]activity{&testActivityDeadline{d: 2 * time.Second}}, time.Now(), 2, time.Now(), 1, context.DeadlineExceeded},
	{[]activity{&testActivityDeadline{d: 2 * time.Second}}, time.Now(), 1, time.Now(), 2, context.DeadlineExceeded},
}

func (t *TaskHandlersTestSuite) TestActivityExecutionDeadline() {
	mockService := &mocks.TChanWorkflowService{}

	for i, d := range deadlineTests {
		wep := workerExecutionParameters{
			Logger: t.logger,
		}
		activityHandler := newActivityTaskHandler(d.activities, mockService, wep)
		pats := &s.PollForActivityTaskResponse{
			TaskToken: []byte("token"),
			WorkflowExecution: &s.WorkflowExecution{
				WorkflowId: common.StringPtr("wID"),
				RunId:      common.StringPtr("rID")},
			ActivityType:                  &s.ActivityType{Name: common.StringPtr("test")},
			ActivityId:                    common.StringPtr(uuid.New()),
			ScheduledTimestamp:            common.Int64Ptr(d.ScheduleTS.UnixNano()),
			ScheduleToCloseTimeoutSeconds: common.Int32Ptr(d.ScheduleDuration),
			StartedTimestamp:              common.Int64Ptr(d.StartTS.UnixNano()),
			StartToCloseTimeoutSeconds:    common.Int32Ptr(d.StartDuration),
		}
		td := fmt.Sprintf("testIndex: %v, testDetails: %v", i, d)
		r, err := activityHandler.Execute(pats)
		t.logger.Info(fmt.Sprintf("test: %v, result: %v err: %v", td, r, err))
		t.Equal(d.err, err, td)
		if err != nil {
			t.Nil(r, td)
		}
	}
}

func (t *TaskHandlersTestSuite) printAllDecisions(decisions []*s.Decision) {
	for _, d := range decisions {
		t.logger.Info(util.DecisionToString(d))
	}
}
