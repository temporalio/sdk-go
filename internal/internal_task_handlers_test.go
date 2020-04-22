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

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/opentracing/opentracing-go"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/temporal-proto/common"
	decisionpb "go.temporal.io/temporal-proto/decision"
	eventpb "go.temporal.io/temporal-proto/event"
	executionpb "go.temporal.io/temporal-proto/execution"
	querypb "go.temporal.io/temporal-proto/query"
	"go.temporal.io/temporal-proto/serviceerror"
	tasklistpb "go.temporal.io/temporal-proto/tasklist"
	"go.uber.org/zap"

	"go.temporal.io/temporal-proto/workflowservice"
	"go.temporal.io/temporal-proto/workflowservicemock"
)

const (
	testNamespace = "test-namespace"
)

type (
	TaskHandlersTestSuite struct {
		suite.Suite
		logger   *zap.Logger
		service  *workflowservicemock.MockWorkflowServiceClient
		registry *registry
	}
)

func registerWorkflows(r *registry) {
	r.RegisterWorkflowWithOptions(
		helloWorldWorkflowFunc,
		RegisterWorkflowOptions{Name: "HelloWorld_Workflow"},
	)
	r.RegisterWorkflowWithOptions(
		helloWorldWorkflowCancelFunc,
		RegisterWorkflowOptions{Name: "HelloWorld_WorkflowCancel"},
	)
	r.RegisterWorkflowWithOptions(
		returnPanicWorkflowFunc,
		RegisterWorkflowOptions{Name: "ReturnPanicWorkflow"},
	)
	r.RegisterWorkflowWithOptions(
		panicWorkflowFunc,
		RegisterWorkflowOptions{Name: "PanicWorkflow"},
	)
	r.RegisterWorkflowWithOptions(
		getWorkflowInfoWorkflowFunc,
		RegisterWorkflowOptions{Name: "GetWorkflowInfoWorkflow"},
	)
	r.RegisterWorkflowWithOptions(
		querySignalWorkflowFunc,
		RegisterWorkflowOptions{Name: "QuerySignalWorkflow"},
	)
	r.RegisterActivityWithOptions(
		greeterActivityFunc,
		RegisterActivityOptions{Name: "Greeter_Activity"},
	)
	r.RegisterWorkflowWithOptions(
		binaryChecksumWorkflowFunc,
		RegisterWorkflowOptions{Name: "BinaryChecksumWorkflow"},
	)
}

func returnPanicWorkflowFunc(Context, []byte) error {
	return newPanicError("panicError", "stackTrace")
}

func panicWorkflowFunc(Context, []byte) error {
	panic("panicError")
}

func getWorkflowInfoWorkflowFunc(ctx Context, expectedLastCompletionResult string) (info *WorkflowInfo, err error) {
	result := GetWorkflowInfo(ctx)
	var lastCompletionResult string
	err = getDefaultDataConverter().FromData(result.lastCompletionResult, &lastCompletionResult)
	if err != nil {
		return nil, err
	}
	if lastCompletionResult != expectedLastCompletionResult {
		return nil, errors.New("lastCompletionResult is not " + expectedLastCompletionResult)
	}
	return result, nil
}

// Test suite.
func (t *TaskHandlersTestSuite) SetupTest() {
}

func (t *TaskHandlersTestSuite) SetupSuite() {
	logger, _ := zap.NewDevelopment()
	t.logger = logger
	registerWorkflows(t.registry)
}

func TestTaskHandlersTestSuite(t *testing.T) {
	suite.Run(t, &TaskHandlersTestSuite{
		registry: newRegistry(),
	})
}

func createTestEventWorkflowExecutionCompleted(eventID int64, attr *eventpb.WorkflowExecutionCompletedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_WorkflowExecutionCompleted,
		Attributes: &eventpb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: attr}}
}

func createTestEventWorkflowExecutionStarted(eventID int64, attr *eventpb.WorkflowExecutionStartedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_WorkflowExecutionStarted,
		Attributes: &eventpb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: attr}}
}

func createTestEventLocalActivity(eventID int64, attr *eventpb.MarkerRecordedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_MarkerRecorded,
		Attributes: &eventpb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: attr}}
}

func createTestEventActivityTaskScheduled(eventID int64, attr *eventpb.ActivityTaskScheduledEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_ActivityTaskScheduled,
		Attributes: &eventpb.HistoryEvent_ActivityTaskScheduledEventAttributes{ActivityTaskScheduledEventAttributes: attr}}
}

func createTestEventActivityTaskStarted(eventID int64, attr *eventpb.ActivityTaskStartedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_ActivityTaskStarted,
		Attributes: &eventpb.HistoryEvent_ActivityTaskStartedEventAttributes{ActivityTaskStartedEventAttributes: attr}}
}

func createTestEventActivityTaskCompleted(eventID int64, attr *eventpb.ActivityTaskCompletedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_ActivityTaskCompleted,
		Attributes: &eventpb.HistoryEvent_ActivityTaskCompletedEventAttributes{ActivityTaskCompletedEventAttributes: attr}}
}

func createTestEventActivityTaskTimedOut(eventID int64, attr *eventpb.ActivityTaskTimedOutEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_ActivityTaskTimedOut,
		Attributes: &eventpb.HistoryEvent_ActivityTaskTimedOutEventAttributes{ActivityTaskTimedOutEventAttributes: attr}}
}

func createTestEventDecisionTaskScheduled(eventID int64, attr *eventpb.DecisionTaskScheduledEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_DecisionTaskScheduled,
		Attributes: &eventpb.HistoryEvent_DecisionTaskScheduledEventAttributes{DecisionTaskScheduledEventAttributes: attr}}
}

func createTestEventDecisionTaskStarted(eventID int64) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:   eventID,
		EventType: eventpb.EventType_DecisionTaskStarted}
}

func createTestEventWorkflowExecutionSignaled(eventID int64, signalName string) *eventpb.HistoryEvent {
	return createTestEventWorkflowExecutionSignaledWithPayload(eventID, signalName, nil)
}

func createTestEventWorkflowExecutionSignaledWithPayload(eventID int64, signalName string, payload []byte) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:   eventID,
		EventType: eventpb.EventType_WorkflowExecutionSignaled,
		Attributes: &eventpb.HistoryEvent_WorkflowExecutionSignaledEventAttributes{WorkflowExecutionSignaledEventAttributes: &eventpb.WorkflowExecutionSignaledEventAttributes{
			SignalName: signalName,
			Input:      payload,
			Identity:   "test-identity",
		}},
	}
}

func createTestEventDecisionTaskCompleted(eventID int64, attr *eventpb.DecisionTaskCompletedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_DecisionTaskCompleted,
		Attributes: &eventpb.HistoryEvent_DecisionTaskCompletedEventAttributes{DecisionTaskCompletedEventAttributes: attr}}
}

func createTestEventDecisionTaskFailed(eventID int64, attr *eventpb.DecisionTaskFailedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_DecisionTaskFailed,
		Attributes: &eventpb.HistoryEvent_DecisionTaskFailedEventAttributes{DecisionTaskFailedEventAttributes: attr}}
}

func createTestEventSignalExternalWorkflowExecutionFailed(eventID int64, attr *eventpb.SignalExternalWorkflowExecutionFailedEventAttributes) *eventpb.HistoryEvent {
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_SignalExternalWorkflowExecutionFailed,
		Attributes: &eventpb.HistoryEvent_SignalExternalWorkflowExecutionFailedEventAttributes{SignalExternalWorkflowExecutionFailedEventAttributes: attr}}
}

func createWorkflowTask(
	events []*eventpb.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
) *workflowservice.PollForDecisionTaskResponse {
	return createWorkflowTaskWithQueries(events, previousStartEventID, workflowName, nil)
}

func createWorkflowTaskWithQueries(
	events []*eventpb.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
	queries map[string]*querypb.WorkflowQuery,
) *workflowservice.PollForDecisionTaskResponse {
	eventsCopy := make([]*eventpb.HistoryEvent, len(events))
	copy(eventsCopy, events)
	return &workflowservice.PollForDecisionTaskResponse{
		PreviousStartedEventId: previousStartEventID,
		WorkflowType:           &commonpb.WorkflowType{Name: workflowName},
		History:                &eventpb.History{Events: eventsCopy},
		WorkflowExecution: &executionpb.WorkflowExecution{
			WorkflowId: "fake-workflow-id",
			RunId:      uuid.New(),
		},
		Queries: queries,
	}
}

func createQueryTask(
	events []*eventpb.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
	queryType string,
) *workflowservice.PollForDecisionTaskResponse {
	task := createWorkflowTask(events, previousStartEventID, workflowName)
	task.Query = &querypb.WorkflowQuery{
		QueryType: queryType,
	}
	return task
}

func createTestEventTimerStarted(eventID int64, id int) *eventpb.HistoryEvent {
	timerID := fmt.Sprintf("%v", id)
	attr := &eventpb.TimerStartedEventAttributes{
		TimerId:                      timerID,
		StartToFireTimeoutSeconds:    0,
		DecisionTaskCompletedEventId: 0,
	}
	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_TimerStarted,
		Attributes: &eventpb.HistoryEvent_TimerStartedEventAttributes{TimerStartedEventAttributes: attr}}
}

func createTestEventTimerFired(eventID int64, id int) *eventpb.HistoryEvent {
	timerID := fmt.Sprintf("%v", id)
	attr := &eventpb.TimerFiredEventAttributes{
		TimerId: timerID,
	}

	return &eventpb.HistoryEvent{
		EventId:    eventID,
		EventType:  eventpb.EventType_TimerFired,
		Attributes: &eventpb.HistoryEvent_TimerFiredEventAttributes{TimerFiredEventAttributes: attr}}
}

var testWorkflowTaskTasklist = "tl1"

func (t *TaskHandlersTestSuite) testWorkflowTaskWorkflowExecutionStartedHelper(params workerExecutionParameters) {
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: testWorkflowTaskTasklist}}),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_Workflow")
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(decisionpb.DecisionType_ScheduleActivityTask, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetScheduleActivityTaskDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted() {
	params := workerExecutionParameters{
		TaskList: testWorkflowTaskTasklist,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	t.testWorkflowTaskWorkflowExecutionStartedHelper(params)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStartedWithDataConverter() {
	params := workerExecutionParameters{
		TaskList:      testWorkflowTaskTasklist,
		Identity:      "test-id-1",
		Logger:        t.logger,
		DataConverter: newTestDataConverter(),
	}
	t.testWorkflowTaskWorkflowExecutionStartedHelper(params)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_BinaryChecksum() {
	taskList := "tl1"
	checksum1 := "chck1"
	checksum2 := "chck2"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2, BinaryChecksum: checksum1}),
		createTestEventTimerStarted(5, 0),
		createTestEventTimerFired(6, 0),
		createTestEventDecisionTaskScheduled(7, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(8),
		createTestEventDecisionTaskCompleted(9, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 7, BinaryChecksum: checksum2}),
		createTestEventTimerStarted(10, 1),
		createTestEventTimerFired(11, 1),
		createTestEventDecisionTaskScheduled(12, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(13),
	}
	task := createWorkflowTask(testEvents, 8, "BinaryChecksumWorkflow")
	params := workerExecutionParameters{
		Namespace: testNamespace,
		TaskList:  taskList,
		Identity:  "test-id-1",
		Logger:    t.logger,
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)

	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(decisionpb.DecisionType_CompleteWorkflowExecution, response.Decisions[0].GetDecisionType())
	checksumsJSON := string(response.Decisions[0].GetCompleteWorkflowExecutionDecisionAttributes().Result)
	var checksums []string
	_ = json.Unmarshal([]byte(checksumsJSON), &checksums)
	t.Equal(3, len(checksums))
	t.Equal("chck1", checksums[0])
	t.Equal("chck2", checksums[1])
	t.Equal(getBinaryChecksum(), checksums[2])
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_ActivityTaskScheduled() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &eventpb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskList:     &tasklistpb.TaskList{Name: taskList},
		}),
		createTestEventActivityTaskStarted(6, &eventpb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &eventpb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventDecisionTaskStarted(8),
	}
	task := createWorkflowTask(testEvents[0:3], 0, "HelloWorld_Workflow")
	params := workerExecutionParameters{
		Namespace: testNamespace,
		TaskList:  taskList,
		Identity:  "test-id-1",
		Logger:    t.logger,
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)

	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(decisionpb.DecisionType_ScheduleActivityTask, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetScheduleActivityTaskDecisionAttributes())

	// Schedule an activity and see if we complete workflow, Having only one last decision.
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	request, err = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response = request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(decisionpb.DecisionType_CompleteWorkflowExecution, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_QueryWorkflow_Sticky() {
	// Schedule an activity and see if we complete workflow.
	taskList := "sticky-tl"
	execution := &executionpb.WorkflowExecution{
		WorkflowId: "fake-workflow-id",
		RunId:      uuid.New(),
	}
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &eventpb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskList:     &tasklistpb.TaskList{Name: taskList},
		}),
		createTestEventActivityTaskStarted(6, &eventpb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &eventpb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
	}
	params := workerExecutionParameters{
		Namespace: testNamespace,
		TaskList:  taskList,
		Identity:  "test-id-1",
		Logger:    t.logger,
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)

	// first make progress on the workflow
	task := createWorkflowTask(testEvents[0:1], 0, "HelloWorld_Workflow")
	task.StartedEventId = 1
	task.WorkflowExecution = execution
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(decisionpb.DecisionType_ScheduleActivityTask, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetScheduleActivityTaskDecisionAttributes())

	// then check the current state using query task
	task = createQueryTask([]*eventpb.HistoryEvent{}, 6, "HelloWorld_Workflow", queryType)
	task.WorkflowExecution = execution
	queryResp, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.verifyQueryResult(queryResp, "waiting-activity-result")
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_QueryWorkflow_NonSticky() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &eventpb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskList:     &tasklistpb.TaskList{Name: taskList},
		}),
		createTestEventActivityTaskStarted(6, &eventpb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &eventpb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventDecisionTaskStarted(8),
		createTestEventWorkflowExecutionSignaled(9, "test-signal"),
	}
	params := workerExecutionParameters{
		Namespace: testNamespace,
		TaskList:  taskList,
		Identity:  "test-id-1",
		Logger:    t.logger,
	}

	// query after first decision task (notice the previousStartEventID is always the last eventID for query task)
	task := createQueryTask(testEvents[0:3], 3, "HelloWorld_Workflow", queryType)
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	response, _ := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "waiting-activity-result")

	// query after activity task complete but before second decision task started
	task = createQueryTask(testEvents[0:7], 7, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	response, _ = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "waiting-activity-result")

	// query after second decision task
	task = createQueryTask(testEvents[0:8], 8, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	response, _ = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "done")

	// query after second decision task with extra events
	task = createQueryTask(testEvents[0:9], 9, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	response, _ = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "done")

	task = createQueryTask(testEvents[0:9], 9, "HelloWorld_Workflow", "invalid-query-type")
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	response, _ = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NotNil(response)
	queryResp, ok := response.(*workflowservice.RespondQueryTaskCompletedRequest)
	t.True(ok)
	t.NotNil(queryResp.ErrorMessage)
	t.Contains(queryResp.ErrorMessage, "unknown queryType")
}

func (t *TaskHandlersTestSuite) verifyQueryResult(response interface{}, expectedResult string) {
	t.NotNil(response)
	queryResp, ok := response.(*workflowservice.RespondQueryTaskCompletedRequest)
	t.True(ok)
	t.Empty(queryResp.ErrorMessage)
	t.NotNil(queryResp.QueryResult)
	encodedValue := newEncodedValue(queryResp.QueryResult, nil)
	var queryResult string
	err := encodedValue.Get(&queryResult)
	t.NoError(err)
	t.Equal(expectedResult, queryResult)
}

func (t *TaskHandlersTestSuite) TestCacheEvictionWhenErrorOccurs() {
	taskList := "taskList"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &eventpb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskList:     &tasklistpb.TaskList{Name: taskList},
		}),
	}
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	// now change the history event so it does not match to decision produced via replay
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "some-other-activity"
	task := createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
	// will fail as it can't find laTunnel in getWorkflowCache().
	newWorkflowTaskWorkerInternal(taskHandler, t.service, params, make(chan struct{}))
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)

	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "nondeterministic")

	// There should be nothing in the cache.
	t.EqualValues(getWorkflowCache().Size(), 0)
}

func (t *TaskHandlersTestSuite) TestWithMissingHistoryEvents() {
	taskList := "taskList"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventDecisionTaskScheduled(6, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(7),
	}
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
	}

	for _, startEventID := range []int64{0, 3} {
		taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
		task := createWorkflowTask(testEvents, startEventID, "HelloWorld_Workflow")
		// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
		// will fail as it can't find laTunnel in getWorkflowCache().
		newWorkflowTaskWorkerInternal(taskHandler, t.service, params, make(chan struct{}))
		request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)

		t.Error(err)
		t.Nil(request)
		t.Contains(err.Error(), "missing history events")

		// There should be nothing in the cache.
		t.EqualValues(getWorkflowCache().Size(), 0)
	}
}

func (t *TaskHandlersTestSuite) TestWithTruncatedHistory() {
	taskList := "taskList"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskFailed(4, &eventpb.DecisionTaskFailedEventAttributes{ScheduledEventId: 2}),
		createTestEventDecisionTaskScheduled(5, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(6),
		createTestEventDecisionTaskCompleted(7, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventActivityTaskScheduled(8, &eventpb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskList:     &tasklistpb.TaskList{Name: taskList},
		}),
	}
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
	}

	testCases := []struct {
		startedEventID         int64
		previousStartedEventID int64
		isResultErr            bool
	}{
		{0, 0, false},
		{0, 3, false},
		{10, 0, true},
		{10, 6, true},
	}

	for i, tc := range testCases {
		taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
		task := createWorkflowTask(testEvents, tc.previousStartedEventID, "HelloWorld_Workflow")
		task.StartedEventId = tc.startedEventID
		// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
		// will fail as it can't find laTunnel in getWorkflowCache().
		newWorkflowTaskWorkerInternal(taskHandler, t.service, params, make(chan struct{}))
		request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)

		if tc.isResultErr {
			t.Error(err, "testcase %v failed", i)
			t.Nil(request)
			t.Contains(err.Error(), "premature end of stream")
			t.EqualValues(getWorkflowCache().Size(), 0)
			continue
		}

		t.NoError(err, "testcase %v failed", i)
	}
}

func (t *TaskHandlersTestSuite) TestSideEffectDefer_Sticky() {
	t.testSideEffectDeferHelper(false)
}

func (t *TaskHandlersTestSuite) TestSideEffectDefer_NonSticky() {
	t.testSideEffectDeferHelper(true)
}

func (t *TaskHandlersTestSuite) testSideEffectDeferHelper(disableSticky bool) {
	value := "should not be modified"
	expectedValue := value
	doneCh := make(chan struct{})

	workflowFunc := func(ctx Context) error {
		defer func() {
			if !IsReplaying(ctx) {
				// This is an side effect op
				value = ""
			}
			close(doneCh)
		}()
		_ = Sleep(ctx, 1*time.Second)
		return nil
	}
	workflowName := fmt.Sprintf("SideEffectDeferWorkflow-Sticky=%v", disableSticky)
	t.registry.RegisterWorkflowWithOptions(
		workflowFunc,
		RegisterWorkflowOptions{Name: workflowName},
	)

	taskList := "taskList"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}

	params := workerExecutionParameters{
		Namespace:              testNamespace,
		TaskList:               taskList,
		Identity:               "test-id-1",
		Logger:                 zap.NewNop(),
		DisableStickyExecution: disableSticky,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	task := createWorkflowTask(testEvents, 0, workflowName)
	_, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.Nil(err)

	if !params.DisableStickyExecution {
		// 1. We can't set cache size in the test to 1, otherwise other tests will break.
		// 2. We need to make sure cache is empty when the test is completed,
		// So manually trigger a delete.
		getWorkflowCache().Delete(task.WorkflowExecution.GetRunId())
	}
	// Make sure the workflow coroutine has exited.
	<-doneCh
	// The side effect op should not be executed.
	t.Equal(expectedValue, value)

	// There should be nothing in the cache.
	t.EqualValues(0, getWorkflowCache().Size())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_NondeterministicDetection() {
	taskList := "taskList"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &eventpb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskList:     &tasklistpb.TaskList{Name: taskList},
		}),
	}
	task := createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	stopC := make(chan struct{})
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
		WorkerStopChannel:   stopC,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	// there should be no error as the history events matched the decisions.
	t.NoError(err)
	t.NotNil(response)

	// now change the history event so it does not match to decision produced via replay
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "some-other-activity"
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
	// will fail as it can't find laTunnel in getWorkflowCache().
	newWorkflowTaskWorkerInternal(taskHandler, t.service, params, stopC)
	request, err = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "nondeterministic")

	// now, create a new task handler with fail nondeterministic workflow policy
	// and verify that it handles the mismatching history correctly.
	params.WorkflowPanicPolicy = FailWorkflow
	failOnNondeterminismTaskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	request, err = failOnNondeterminismTaskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	// When FailWorkflow policy is set, task handler does not return an error,
	// because it will indicate non determinism in the request.
	t.NoError(err)
	// Verify that request is a RespondDecisionTaskCompleteRequest
	response, ok := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.True(ok)
	// Verify there's at least 1 decision
	// and the last last decision is to fail workflow
	// and contains proper justification.(i.e. nondeterminism).
	t.True(len(response.Decisions) > 0)
	closeDecision := response.Decisions[len(response.Decisions)-1]
	t.Equal(closeDecision.DecisionType, decisionpb.DecisionType_FailWorkflowExecution)
	t.Contains(closeDecision.GetFailWorkflowExecutionDecisionAttributes().Reason, "FailWorkflow")

	// now with different package name to activity type
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "new-package.Greeter_Activity"
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	request, err = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowReturnsPanicError() {
	taskList := "taskList"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 3, "ReturnPanicWorkflow")
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.True(ok)
	t.EqualValues(decisionpb.DecisionType_FailWorkflowExecution, r.Decisions[0].GetDecisionType())
	attr := r.Decisions[0].GetFailWorkflowExecutionDecisionAttributes()
	t.EqualValues("temporalInternal:Panic", attr.GetReason())
	details := string(attr.Details)
	t.True(strings.HasPrefix(details, "\"panicError"), details)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowPanics() {
	taskList := "taskList"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 3, "PanicWorkflow")
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondDecisionTaskFailedRequest)
	t.True(ok)
	t.EqualValues(eventpb.DecisionTaskFailedCause_WorkflowWorkerUnhandledFailure, r.Cause)
	t.EqualValues("panicError", string(r.Details))
}

func (t *TaskHandlersTestSuite) TestGetWorkflowInfo() {
	taskList := "taskList"
	parentID := "parentID"
	parentRunID := "parentRun"
	cronSchedule := "5 4 * * *"
	continuedRunID := uuid.New()
	parentExecution := &executionpb.WorkflowExecution{
		WorkflowId: parentID,
		RunId:      parentRunID,
	}
	parentNamespace := "parentNamespace"
	var attempt int32 = 123
	var executionTimeout int32 = 213456
	var taskTimeout int32 = 21
	workflowType := "GetWorkflowInfoWorkflow"
	lastCompletionResult, err := getDefaultDataConverter().ToData("lastCompletionData")
	t.NoError(err)
	startedEventAttributes := &eventpb.WorkflowExecutionStartedEventAttributes{
		Input:                               lastCompletionResult,
		TaskList:                            &tasklistpb.TaskList{Name: taskList},
		ParentWorkflowExecution:             parentExecution,
		CronSchedule:                        cronSchedule,
		ContinuedExecutionRunId:             continuedRunID,
		ParentWorkflowNamespace:             parentNamespace,
		Attempt:                             attempt,
		ExecutionStartToCloseTimeoutSeconds: executionTimeout,
		TaskStartToCloseTimeoutSeconds:      taskTimeout,
		LastCompletionResult:                lastCompletionResult,
	}
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, startedEventAttributes),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 3, workflowType)
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.True(ok)
	t.EqualValues(decisionpb.DecisionType_CompleteWorkflowExecution, r.Decisions[0].GetDecisionType())
	attr := r.Decisions[0].GetCompleteWorkflowExecutionDecisionAttributes()
	var result WorkflowInfo
	t.NoError(getDefaultDataConverter().FromData(attr.Result, &result))
	t.EqualValues(taskList, result.TaskListName)
	t.EqualValues(parentID, result.ParentWorkflowExecution.ID)
	t.EqualValues(parentRunID, result.ParentWorkflowExecution.RunID)
	t.EqualValues(cronSchedule, result.CronSchedule)
	t.EqualValues(continuedRunID, result.ContinuedExecutionRunID)
	t.EqualValues(parentNamespace, result.ParentWorkflowNamespace)
	t.EqualValues(attempt, result.Attempt)
	t.EqualValues(executionTimeout, result.ExecutionStartToCloseTimeoutSeconds)
	t.EqualValues(taskTimeout, result.TaskStartToCloseTimeoutSeconds)
	t.EqualValues(workflowType, result.WorkflowType.Name)
	t.EqualValues(testNamespace, result.Namespace)
}

func (t *TaskHandlersTestSuite) TestConsistentQuery_InvalidQueryTask() {
	taskList := "taskList"
	params := workerExecutionParameters{
		Namespace:           testNamespace,
		TaskList:            taskList,
		Identity:            "test-id-1",
		Logger:              zap.NewNop(),
		WorkflowPanicPolicy: BlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	task := createWorkflowTask(nil, 3, "HelloWorld_Workflow")
	task.Query = &querypb.WorkflowQuery{}
	task.Queries = map[string]*querypb.WorkflowQuery{"query_id": {}}
	newWorkflowTaskWorkerInternal(taskHandler, t.service, params, make(chan struct{}))
	// query and queries are both specified so this is an invalid task
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)

	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "invalid query decision task")

	// There should be nothing in the cache.
	t.EqualValues(getWorkflowCache().Size(), 0)
}

func (t *TaskHandlersTestSuite) TestConsistentQuery_Success() {
	taskList := "tl1"
	checksum1 := "chck1"
	numberOfSignalsToComplete, err := getDefaultDataConverter().ToData(2)
	t.NoError(err)
	signal, err := getDefaultDataConverter().ToData("signal data")
	t.NoError(err)
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{
			TaskList: &tasklistpb.TaskList{Name: taskList},
			Input:    numberOfSignalsToComplete,
		}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &eventpb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 2, BinaryChecksum: checksum1}),
		createTestEventWorkflowExecutionSignaledWithPayload(5, signalCh, signal),
		createTestEventDecisionTaskScheduled(6, &eventpb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(7),
	}

	queries := map[string]*querypb.WorkflowQuery{
		"id1": {QueryType: queryType},
		"id2": {QueryType: errQueryType},
	}
	task := createWorkflowTaskWithQueries(testEvents[0:3], 0, "QuerySignalWorkflow", queries)

	params := workerExecutionParameters{
		Namespace: testNamespace,
		TaskList:  taskList,
		Identity:  "test-id-1",
		Logger:    t.logger,
	}

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Len(response.Decisions, 0)
	expectedQueryResults := map[string]*querypb.WorkflowQueryResult{
		"id1": {
			ResultType: querypb.QueryResultType_Answered,
			Answer:     []byte(fmt.Sprintf("\"%v\"\n", startingQueryValue)),
		},
		"id2": {
			ResultType:   querypb.QueryResultType_Failed,
			ErrorMessage: queryErr,
		},
	}
	t.assertQueryResultsEqual(expectedQueryResults, response.QueryResults)

	secondTask := createWorkflowTaskWithQueries(testEvents, 3, "QuerySignalWorkflow", queries)
	secondTask.WorkflowExecution.RunId = task.WorkflowExecution.RunId
	request, err = taskHandler.ProcessWorkflowTask(&workflowTask{task: secondTask}, nil)
	response = request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Len(response.Decisions, 1)
	expectedQueryResults = map[string]*querypb.WorkflowQueryResult{
		"id1": {
			ResultType: querypb.QueryResultType_Answered,
			Answer:     []byte(fmt.Sprintf("\"%v\"\n", "signal data")),
		},
		"id2": {
			ResultType:   querypb.QueryResultType_Failed,
			ErrorMessage: queryErr,
		},
	}
	t.assertQueryResultsEqual(expectedQueryResults, response.QueryResults)

	// clean up workflow left in cache
	getWorkflowCache().Delete(task.WorkflowExecution.RunId)
}

func (t *TaskHandlersTestSuite) assertQueryResultsEqual(expected map[string]*querypb.WorkflowQueryResult, actual map[string]*querypb.WorkflowQueryResult) {
	t.Equal(len(expected), len(actual))
	for expectedID, expectedResult := range expected {
		t.Contains(actual, expectedID)
		t.Equal(expectedResult, actual[expectedID])
	}
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_CancelActivityBeforeSent() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_WorkflowCancel")

	params := workerExecutionParameters{
		Namespace: testNamespace,
		TaskList:  taskList,
		Identity:  "test-id-1",
		Logger:    t.logger,
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(decisionpb.DecisionType_CompleteWorkflowExecution, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_PageToken() {
	// Schedule a decision activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{TaskList: &tasklistpb.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{}),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_Workflow")
	task.NextPageToken = []byte("token")

	params := workerExecutionParameters{
		Namespace: testNamespace,
		TaskList:  taskList,
		Identity:  "test-id-1",
		Logger:    t.logger,
	}

	nextEvents := []*eventpb.HistoryEvent{
		createTestEventDecisionTaskStarted(3),
	}

	historyIterator := &historyIteratorImpl{
		iteratorFunc: func(nextToken []byte) (*eventpb.History, []byte, error) {
			return &eventpb.History{Events: nextEvents}, nil, nil
		},
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task, historyIterator: historyIterator}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
}

func (t *TaskHandlersTestSuite) TestLocalActivityRetry_DecisionHeartbeatFail() {
	backoffIntervalInSeconds := int32(1)
	backoffDuration := time.Second * time.Duration(backoffIntervalInSeconds)
	workflowComplete := false

	retryLocalActivityWorkflowFunc := func(ctx Context, intput []byte) error {
		ao := LocalActivityOptions{
			ScheduleToCloseTimeout: time.Minute,
			RetryPolicy: &RetryPolicy{
				InitialInterval:    backoffDuration,
				BackoffCoefficient: 1.1,
				MaximumInterval:    time.Minute,
				ExpirationInterval: time.Minute,
			},
		}
		ctx = WithLocalActivityOptions(ctx, ao)

		err := ExecuteLocalActivity(ctx, func() error {
			return errors.New("some random error")
		}).Get(ctx, nil)
		workflowComplete = true
		return err
	}
	t.registry.RegisterWorkflowWithOptions(
		retryLocalActivityWorkflowFunc,
		RegisterWorkflowOptions{Name: "RetryLocalActivityWorkflow"},
	)

	decisionTaskStartedEvent := createTestEventDecisionTaskStarted(3)
	decisionTaskStartedEvent.Timestamp = time.Now().UnixNano()
	testEvents := []*eventpb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &eventpb.WorkflowExecutionStartedEventAttributes{
			// make sure the timeout is same as the backoff interval
			TaskStartToCloseTimeoutSeconds: backoffIntervalInSeconds,
			TaskList:                       &tasklistpb.TaskList{Name: testWorkflowTaskTasklist}},
		),
		createTestEventDecisionTaskScheduled(2, &eventpb.DecisionTaskScheduledEventAttributes{}),
		decisionTaskStartedEvent,
	}

	task := createWorkflowTask(testEvents, 0, "RetryLocalActivityWorkflow")
	stopCh := make(chan struct{})
	params := workerExecutionParameters{
		Namespace:         testNamespace,
		TaskList:          testWorkflowTaskTasklist,
		Identity:          "test-id-1",
		Logger:            t.logger,
		Tracer:            opentracing.NoopTracer{},
		WorkerStopChannel: stopCh,
	}
	defer close(stopCh)

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	laTunnel := newLocalActivityTunnel(params.WorkerStopChannel)
	taskHandlerImpl, ok := taskHandler.(*workflowTaskHandlerImpl)
	t.True(ok)
	taskHandlerImpl.laTunnel = laTunnel

	laTaskPoller := newLocalActivityPoller(params, laTunnel)
	doneCh := make(chan struct{})
	go func() {
		// laTaskPoller needs to poll the local activity and process it
		task, err := laTaskPoller.PollTask()
		t.NoError(err)
		err = laTaskPoller.ProcessTask(task)
		t.NoError(err)

		// before clearing workflow state, a reset sticky task will be sent to this chan,
		// drain the chan so that workflow state can be cleared
		<-laTunnel.resultCh

		close(doneCh)
	}()

	laResultCh := make(chan *localActivityResult)
	response, err := taskHandler.ProcessWorkflowTask(
		&workflowTask{
			task:       task,
			laResultCh: laResultCh,
		},
		func(response interface{}, startTime time.Time) (*workflowTask, error) {
			return nil, serviceerror.NewNotFound("Decision task not found.")
		})
	t.Nil(response)
	t.Error(err)

	// wait for the retry timer to fire
	time.Sleep(backoffDuration)
	t.False(workflowComplete)
	<-doneCh
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NoError() {
	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: false}
	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(&heartbeatResponse, nil)

	temporalInvoker := &temporalInvoker{
		identity:  "Test_Temporal_Invoker",
		service:   mockService,
		taskToken: nil,
	}

	heartbeatErr := temporalInvoker.Heartbeat(nil)

	t.NoError(heartbeatErr)
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NilResponseWithError() {
	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewNotFound(""))

	temporalInvoker := newServiceInvoker(
		nil,
		"Test_Temporal_Invoker",
		mockService,
		func() {},
		0,
		make(chan struct{}))

	heartbeatErr := temporalInvoker.Heartbeat(nil)
	t.NotNil(heartbeatErr)
	t.IsType(&serviceerror.NotFound{}, heartbeatErr, "heartbeatErr must be of type NotFound.")
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NilResponseWithNamespaceNotActiveError() {
	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewNamespaceNotActive("fake_namespace", "current_cluster", "active_cluster"))

	called := false
	cancelHandler := func() { called = true }

	temporalInvoker := newServiceInvoker(
		nil,
		"Test_Temporal_Invoker",
		mockService,
		cancelHandler,
		0,
		make(chan struct{}))

	heartbeatErr := temporalInvoker.Heartbeat(nil)
	t.NotNil(heartbeatErr)
	t.IsType(&serviceerror.NamespaceNotActive{}, heartbeatErr, "heartbeatErr must be of type NamespaceNotActive.")
	t.True(called)
}

type testActivityDeadline struct {
	logger *zap.Logger
	d      time.Duration
}

func (t *testActivityDeadline) Execute(ctx context.Context, _ []byte) ([]byte, error) {
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

func (t *testActivityDeadline) ActivityType() ActivityType {
	return ActivityType{Name: "test"}
}

func (t *testActivityDeadline) GetFunction() interface{} {
	return t.Execute
}

type deadlineTest struct {
	actWaitDuration  time.Duration
	ScheduleTS       time.Time
	ScheduleDuration int32
	StartTS          time.Time
	StartDuration    int32
	err              error
}

func (t *TaskHandlersTestSuite) TestActivityExecutionDeadline() {
	deadlineTests := []deadlineTest{
		{time.Duration(0), time.Now(), 3, time.Now(), 3, nil},
		{time.Duration(0), time.Now(), 4, time.Now(), 3, nil},
		{time.Duration(0), time.Now(), 3, time.Now(), 4, nil},
		{time.Duration(0), time.Now().Add(-1 * time.Second), 1, time.Now(), 1, context.DeadlineExceeded},
		{time.Duration(0), time.Now(), 1, time.Now().Add(-1 * time.Second), 1, context.DeadlineExceeded},
		{time.Duration(0), time.Now().Add(-1 * time.Second), 1, time.Now().Add(-1 * time.Second), 1, context.DeadlineExceeded},
		{1 * time.Second, time.Now(), 1, time.Now(), 1, context.DeadlineExceeded},
		{1 * time.Second, time.Now(), 2, time.Now(), 1, context.DeadlineExceeded},
		{1 * time.Second, time.Now(), 1, time.Now(), 2, context.DeadlineExceeded},
	}
	a := &testActivityDeadline{logger: t.logger}
	registry := t.registry
	registry.addActivity(a.ActivityType().Name, a)

	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	for i, d := range deadlineTests {
		a.d = d.actWaitDuration
		wep := workerExecutionParameters{
			Logger:        t.logger,
			DataConverter: getDefaultDataConverter(),
			Tracer:        opentracing.NoopTracer{},
		}
		activityHandler := newActivityTaskHandler(mockService, wep, registry)
		pats := &workflowservice.PollForActivityTaskResponse{
			TaskToken: []byte("token"),
			WorkflowExecution: &executionpb.WorkflowExecution{
				WorkflowId: "wID",
				RunId:      "rID"},
			ActivityType:                  &commonpb.ActivityType{Name: "test"},
			ActivityId:                    uuid.New(),
			ScheduledTimestamp:            d.ScheduleTS.UnixNano(),
			ScheduleToCloseTimeoutSeconds: d.ScheduleDuration,
			StartedTimestamp:              d.StartTS.UnixNano(),
			StartToCloseTimeoutSeconds:    d.StartDuration,
			WorkflowType: &commonpb.WorkflowType{
				Name: "wType",
			},
			WorkflowNamespace: "namespace",
		}
		td := fmt.Sprintf("testIndex: %v, testDetails: %v", i, d)
		r, err := activityHandler.Execute(tasklist, pats)
		t.logger.Info(fmt.Sprintf("test: %v, result: %v err: %v", td, r, err))
		t.Equal(d.err, err, td)
		if err != nil {
			t.Nil(r, td)
		}
	}
}

func activityWithWorkerStop(ctx context.Context) error {
	fmt.Println("Executing Activity with worker stop")
	workerStopCh := GetWorkerStopChannel(ctx)

	select {
	case <-workerStopCh:
		return nil
	case <-time.NewTimer(time.Second * 5).C:
		return fmt.Errorf("activity failed to handle worker stop event")
	}
}

func (t *TaskHandlersTestSuite) TestActivityExecutionWorkerStop() {
	a := &testActivityDeadline{logger: t.logger}
	registry := t.registry
	registry.addActivityFn(a.ActivityType().Name, activityWithWorkerStop)

	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)
	workerStopCh := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	wep := workerExecutionParameters{
		Logger:            t.logger,
		DataConverter:     getDefaultDataConverter(),
		UserContext:       ctx,
		UserContextCancel: cancel,
		WorkerStopChannel: workerStopCh,
		Tracer:            opentracing.NoopTracer{},
	}
	activityHandler := newActivityTaskHandler(mockService, wep, registry)
	pats := &workflowservice.PollForActivityTaskResponse{
		TaskToken: []byte("token"),
		WorkflowExecution: &executionpb.WorkflowExecution{
			WorkflowId: "wID",
			RunId:      "rID"},
		ActivityType:                  &commonpb.ActivityType{Name: "test"},
		ActivityId:                    uuid.New(),
		ScheduledTimestamp:            time.Now().UnixNano(),
		ScheduleToCloseTimeoutSeconds: 1,
		StartedTimestamp:              time.Now().UnixNano(),
		StartToCloseTimeoutSeconds:    1,
		WorkflowType: &commonpb.WorkflowType{
			Name: "wType",
		},
		WorkflowNamespace: "namespace",
	}
	close(workerStopCh)
	r, err := activityHandler.Execute(tasklist, pats)
	t.NoError(err)
	t.NotNil(r)
}

func Test_NonDeterministicCheck(t *testing.T) {
	decisionTypes := decisionpb.DecisionType_value
	require.Equal(t, 13, len(decisionTypes), "If you see this error, you are adding new decision type. "+
		"Before updating the number to make this test pass, please make sure you update isDecisionMatchEvent() method "+
		"to check the new decision type. Otherwise the replay will fail on the new decision event.")

	eventTypes := eventpb.EventType_value
	decisionEventTypeCount := 0
	for _, et := range eventTypes {
		if isDecisionEvent(eventpb.EventType(et)) {
			decisionEventTypeCount++
		}
	}
	// CancelTimer has 2 corresponding events.
	require.Equal(t, len(decisionTypes)+1, decisionEventTypeCount, "Every decision type must have one matching event type. "+
		"If you add new decision type, you need to update isDecisionEvent() method to include that new event type as well.")
}

func Test_IsDecisionMatchEvent_UpsertWorkflowSearchAttributes(t *testing.T) {
	diType := decisionpb.DecisionType_UpsertWorkflowSearchAttributes
	eType := eventpb.EventType_UpsertWorkflowSearchAttributes
	strictMode := false

	testCases := []struct {
		name     string
		decision *decisionpb.Decision
		event    *eventpb.HistoryEvent
		expected bool
	}{
		{
			name: "event type not match",
			decision: &decisionpb.Decision{
				DecisionType: diType,
				Attributes: &decisionpb.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &decisionpb.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			event:    &eventpb.HistoryEvent{},
			expected: false,
		},
		{
			name: "attributes not match",
			decision: &decisionpb.Decision{
				DecisionType: diType,
				Attributes: &decisionpb.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &decisionpb.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			event: &eventpb.HistoryEvent{
				EventType:  eType,
				Attributes: &eventpb.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &eventpb.UpsertWorkflowSearchAttributesEventAttributes{}}},
			expected: true,
		},
		{
			name: "attributes match",
			decision: &decisionpb.Decision{
				DecisionType: diType,
				Attributes: &decisionpb.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &decisionpb.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			event: &eventpb.HistoryEvent{
				EventType: eType,
				Attributes: &eventpb.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &eventpb.UpsertWorkflowSearchAttributesEventAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			expected: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, isDecisionMatchEvent(testCase.decision, testCase.event, strictMode))
		})
	}

	strictMode = true

	testCases = []struct {
		name     string
		decision *decisionpb.Decision
		event    *eventpb.HistoryEvent
		expected bool
	}{
		{
			name: "attributes not match",
			decision: &decisionpb.Decision{
				DecisionType: diType,
				Attributes: &decisionpb.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &decisionpb.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			event: &eventpb.HistoryEvent{
				EventType:  eType,
				Attributes: &eventpb.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &eventpb.UpsertWorkflowSearchAttributesEventAttributes{}}},
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, isDecisionMatchEvent(testCase.decision, testCase.event, strictMode))
		})
	}
}

func Test_IsSearchAttributesMatched(t *testing.T) {
	testCases := []struct {
		name     string
		lhs      *commonpb.SearchAttributes
		rhs      *commonpb.SearchAttributes
		expected bool
	}{
		{
			name:     "both nil",
			lhs:      nil,
			rhs:      nil,
			expected: true,
		},
		{
			name:     "left nil",
			lhs:      nil,
			rhs:      &commonpb.SearchAttributes{},
			expected: false,
		},
		{
			name:     "right nil",
			lhs:      &commonpb.SearchAttributes{},
			rhs:      nil,
			expected: false,
		},
		{
			name: "not match",
			lhs: &commonpb.SearchAttributes{
				IndexedFields: map[string][]byte{
					"key1": []byte("1"),
					"key2": []byte("abc"),
				},
			},
			rhs:      &commonpb.SearchAttributes{},
			expected: false,
		},
		{
			name: "match",
			lhs: &commonpb.SearchAttributes{
				IndexedFields: map[string][]byte{
					"key1": []byte("1"),
					"key2": []byte("abc"),
				},
			},
			rhs: &commonpb.SearchAttributes{
				IndexedFields: map[string][]byte{
					"key2": []byte("abc"),
					"key1": []byte("1"),
				},
			},
			expected: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, isSearchAttributesMatched(testCase.lhs, testCase.rhs))
		})
	}
}
