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
	"google.golang.org/grpc/codes"

	commonproto "github.com/temporalio/temporal-proto/common"
	"github.com/temporalio/temporal-proto/enums"
	"github.com/temporalio/temporal-proto/errordetails"
	"github.com/temporalio/temporal-proto/workflowservice"
	"github.com/temporalio/temporal-proto/workflowservicemock"
	"go.temporal.io/temporal/internal/protobufutils"

	"go.uber.org/zap"
)

const (
	testDomain = "test-domain"
)

type (
	TaskHandlersTestSuite struct {
		suite.Suite
		logger  *zap.Logger
		service *workflowservicemock.MockWorkflowServiceYARPCClient
	}
)

func init() {
	RegisterWorkflowWithOptions(
		helloWorldWorkflowFunc,
		RegisterWorkflowOptions{Name: "HelloWorld_Workflow"},
	)
	RegisterWorkflowWithOptions(
		helloWorldWorkflowCancelFunc,
		RegisterWorkflowOptions{Name: "HelloWorld_WorkflowCancel"},
	)
	RegisterWorkflowWithOptions(
		returnPanicWorkflowFunc,
		RegisterWorkflowOptions{Name: "ReturnPanicWorkflow"},
	)
	RegisterWorkflowWithOptions(
		panicWorkflowFunc,
		RegisterWorkflowOptions{Name: "PanicWorkflow"},
	)
	RegisterWorkflowWithOptions(
		getWorkflowInfoWorkflowFunc,
		RegisterWorkflowOptions{Name: "GetWorkflowInfoWorkflow"},
	)
	RegisterWorkflowWithOptions(
		querySignalWorkflowFunc,
		RegisterWorkflowOptions{Name: "QuerySignalWorkflow"},
	)
	RegisterActivityWithOptions(
		greeterActivityFunc,
		RegisterActivityOptions{Name: "Greeter_Activity"},
	)
	RegisterWorkflowWithOptions(
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
}

func TestTaskHandlersTestSuite(t *testing.T) {
	suite.Run(t, new(TaskHandlersTestSuite))
}

func createTestEventWorkflowExecutionCompleted(eventID int64, attr *commonproto.WorkflowExecutionCompletedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeWorkflowExecutionCompleted,
		Attributes: &commonproto.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: attr}}
}

func createTestEventWorkflowExecutionStarted(eventID int64, attr *commonproto.WorkflowExecutionStartedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeWorkflowExecutionStarted,
		Attributes: &commonproto.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: attr}}
}

func createTestEventLocalActivity(eventID int64, attr *commonproto.MarkerRecordedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeMarkerRecorded,
		Attributes: &commonproto.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: attr}}
}

func createTestEventActivityTaskScheduled(eventID int64, attr *commonproto.ActivityTaskScheduledEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeActivityTaskScheduled,
		Attributes: &commonproto.HistoryEvent_ActivityTaskScheduledEventAttributes{ActivityTaskScheduledEventAttributes: attr}}
}

func createTestEventActivityTaskStarted(eventID int64, attr *commonproto.ActivityTaskStartedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeActivityTaskStarted,
		Attributes: &commonproto.HistoryEvent_ActivityTaskStartedEventAttributes{ActivityTaskStartedEventAttributes: attr}}
}

func createTestEventActivityTaskCompleted(eventID int64, attr *commonproto.ActivityTaskCompletedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeActivityTaskCompleted,
		Attributes: &commonproto.HistoryEvent_ActivityTaskCompletedEventAttributes{ActivityTaskCompletedEventAttributes: attr}}
}

func createTestEventActivityTaskTimedOut(eventID int64, attr *commonproto.ActivityTaskTimedOutEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeActivityTaskTimedOut,
		Attributes: &commonproto.HistoryEvent_ActivityTaskTimedOutEventAttributes{ActivityTaskTimedOutEventAttributes: attr}}
}

func createTestEventDecisionTaskScheduled(eventID int64, attr *commonproto.DecisionTaskScheduledEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeDecisionTaskScheduled,
		Attributes: &commonproto.HistoryEvent_DecisionTaskScheduledEventAttributes{DecisionTaskScheduledEventAttributes: attr}}
}

func createTestEventDecisionTaskStarted(eventID int64) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:   eventID,
		EventType: enums.EventTypeDecisionTaskStarted}
}

func createTestEventWorkflowExecutionSignaled(eventID int64, signalName string) *commonproto.HistoryEvent {
	return createTestEventWorkflowExecutionSignaledWithPayload(eventID, signalName, nil)
}

func createTestEventWorkflowExecutionSignaledWithPayload(eventID int64, signalName string, payload []byte) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:   eventID,
		EventType: enums.EventTypeWorkflowExecutionSignaled,
		Attributes: &commonproto.HistoryEvent_WorkflowExecutionSignaledEventAttributes{WorkflowExecutionSignaledEventAttributes: &commonproto.WorkflowExecutionSignaledEventAttributes{
			SignalName: signalName,
			Input:      payload,
			Identity:   "test-identity",
		}},
	}
}

func createTestEventDecisionTaskCompleted(eventID int64, attr *commonproto.DecisionTaskCompletedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeDecisionTaskCompleted,
		Attributes: &commonproto.HistoryEvent_DecisionTaskCompletedEventAttributes{DecisionTaskCompletedEventAttributes: attr}}
}

func createTestEventDecisionTaskFailed(eventID int64, attr *commonproto.DecisionTaskFailedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeDecisionTaskFailed,
		Attributes: &commonproto.HistoryEvent_DecisionTaskFailedEventAttributes{DecisionTaskFailedEventAttributes: attr}}
}

func createTestEventSignalExternalWorkflowExecutionFailed(eventID int64, attr *commonproto.SignalExternalWorkflowExecutionFailedEventAttributes) *commonproto.HistoryEvent {
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeSignalExternalWorkflowExecutionFailed,
		Attributes: &commonproto.HistoryEvent_SignalExternalWorkflowExecutionFailedEventAttributes{SignalExternalWorkflowExecutionFailedEventAttributes: attr}}
}

func createWorkflowTask(
	events []*commonproto.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
) *workflowservice.PollForDecisionTaskResponse {
	return createWorkflowTaskWithQueries(events, previousStartEventID, workflowName, nil)
}

func createWorkflowTaskWithQueries(
	events []*commonproto.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
	queries map[string]*commonproto.WorkflowQuery,
) *workflowservice.PollForDecisionTaskResponse {
	eventsCopy := make([]*commonproto.HistoryEvent, len(events))
	copy(eventsCopy, events)
	return &workflowservice.PollForDecisionTaskResponse{
		PreviousStartedEventId: previousStartEventID,
		WorkflowType:           &commonproto.WorkflowType{Name: workflowName},
		History:                &commonproto.History{Events: eventsCopy},
		WorkflowExecution: &commonproto.WorkflowExecution{
			WorkflowId: "fake-workflow-id",
			RunId:      uuid.New(),
		},
		Queries: queries,
	}
}

func createQueryTask(
	events []*commonproto.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
	queryType string,
) *workflowservice.PollForDecisionTaskResponse {
	task := createWorkflowTask(events, previousStartEventID, workflowName)
	task.Query = &commonproto.WorkflowQuery{
		QueryType: queryType,
	}
	return task
}

func createTestEventTimerStarted(eventID int64, id int) *commonproto.HistoryEvent {
	timerID := fmt.Sprintf("%v", id)
	attr := &commonproto.TimerStartedEventAttributes{
		TimerId:                      timerID,
		StartToFireTimeoutSeconds:    0,
		DecisionTaskCompletedEventId: 0,
	}
	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeTimerStarted,
		Attributes: &commonproto.HistoryEvent_TimerStartedEventAttributes{TimerStartedEventAttributes: attr}}
}

func createTestEventTimerFired(eventID int64, id int) *commonproto.HistoryEvent {
	timerID := fmt.Sprintf("%v", id)
	attr := &commonproto.TimerFiredEventAttributes{
		TimerId: timerID,
	}

	return &commonproto.HistoryEvent{
		EventId:    eventID,
		EventType:  enums.EventTypeTimerFired,
		Attributes: &commonproto.HistoryEvent_TimerFiredEventAttributes{TimerFiredEventAttributes: attr}}
}

var testWorkflowTaskTasklist = "tl1"

func (t *TaskHandlersTestSuite) testWorkflowTaskWorkflowExecutionStartedHelper(params workerExecutionParameters) {
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: testWorkflowTaskTasklist}}),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_Workflow")
	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(enums.DecisionTypeScheduleActivityTask, response.Decisions[0].GetDecisionType())
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

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted_WithDataConverter() {
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
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2, BinaryChecksum: checksum1}),
		createTestEventTimerStarted(5, 0),
		createTestEventTimerFired(6, 0),
		createTestEventDecisionTaskScheduled(7, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(8),
		createTestEventDecisionTaskCompleted(9, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 7, BinaryChecksum: checksum2}),
		createTestEventTimerStarted(10, 1),
		createTestEventTimerFired(11, 1),
		createTestEventDecisionTaskScheduled(12, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(13),
	}
	task := createWorkflowTask(testEvents, 8, "BinaryChecksumWorkflow")
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)

	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(enums.DecisionTypeCompleteWorkflowExecution, response.Decisions[0].GetDecisionType())
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
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &commonproto.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonproto.ActivityType{Name: "Greeter_Activity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
		}),
		createTestEventActivityTaskStarted(6, &commonproto.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &commonproto.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventDecisionTaskStarted(8),
	}
	task := createWorkflowTask(testEvents[0:3], 0, "HelloWorld_Workflow")
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)

	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(enums.DecisionTypeScheduleActivityTask, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetScheduleActivityTaskDecisionAttributes())

	// Schedule an activity and see if we complete workflow, Having only one last decision.
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	request, err = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response = request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(enums.DecisionTypeCompleteWorkflowExecution, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_QueryWorkflow_Sticky() {
	// Schedule an activity and see if we complete workflow.
	taskList := "sticky-tl"
	execution := &commonproto.WorkflowExecution{
		WorkflowId: "fake-workflow-id",
		RunId:      uuid.New(),
	}
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &commonproto.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonproto.ActivityType{Name: "Greeter_Activity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
		}),
		createTestEventActivityTaskStarted(6, &commonproto.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &commonproto.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
	}
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())

	// first make progress on the workflow
	task := createWorkflowTask(testEvents[0:1], 0, "HelloWorld_Workflow")
	task.StartedEventId = 1
	task.WorkflowExecution = execution
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(enums.DecisionTypeScheduleActivityTask, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetScheduleActivityTaskDecisionAttributes())

	// then check the current state using query task
	task = createQueryTask([]*commonproto.HistoryEvent{}, 6, "HelloWorld_Workflow", queryType)
	task.WorkflowExecution = execution
	queryResp, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.verifyQueryResult(queryResp, "waiting-activity-result")
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_QueryWorkflow_NonSticky() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &commonproto.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonproto.ActivityType{Name: "Greeter_Activity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
		}),
		createTestEventActivityTaskStarted(6, &commonproto.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &commonproto.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventDecisionTaskStarted(8),
		createTestEventWorkflowExecutionSignaled(9, "test-signal"),
	}
	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}

	// query after first decision task (notice the previousStartEventID is always the last eventID for query task)
	task := createQueryTask(testEvents[0:3], 3, "HelloWorld_Workflow", queryType)
	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	response, _ := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "waiting-activity-result")

	// query after activity task complete but before second decision task started
	task = createQueryTask(testEvents[0:7], 7, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	response, _ = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "waiting-activity-result")

	// query after second decision task
	task = createQueryTask(testEvents[0:8], 8, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	response, _ = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "done")

	// query after second decision task with extra events
	task = createQueryTask(testEvents[0:9], 9, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	response, _ = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.verifyQueryResult(response, "done")

	task = createQueryTask(testEvents[0:9], 9, "HelloWorld_Workflow", "invalid-query-type")
	taskHandler = newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
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
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &commonproto.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonproto.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
		}),
	}
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	// now change the history event so it does not match to decision produced via replay
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "some-other-activity"
	task := createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
	// will fail as it can't find laTunnel in getWorkflowCache().
	newWorkflowTaskWorkerInternal(taskHandler, t.service, testDomain, params, make(chan struct{}))
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)

	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "nondeterministic")

	// There should be nothing in the cache.
	t.EqualValues(getWorkflowCache().Size(), 0)
}

func (t *TaskHandlersTestSuite) TestWithMissingHistoryEvents() {
	taskList := "taskList"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventDecisionTaskScheduled(6, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(7),
	}
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
	}

	for _, startEventID := range []int64{0, 3} {
		taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
		task := createWorkflowTask(testEvents, startEventID, "HelloWorld_Workflow")
		// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
		// will fail as it can't find laTunnel in getWorkflowCache().
		newWorkflowTaskWorkerInternal(taskHandler, t.service, testDomain, params, make(chan struct{}))
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
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskFailed(4, &commonproto.DecisionTaskFailedEventAttributes{ScheduledEventId: 2}),
		createTestEventDecisionTaskScheduled(5, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(6),
		createTestEventDecisionTaskCompleted(7, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventActivityTaskScheduled(8, &commonproto.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonproto.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
		}),
	}
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
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
		taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
		task := createWorkflowTask(testEvents, tc.previousStartedEventID, "HelloWorld_Workflow")
		task.StartedEventId = tc.startedEventID
		// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
		// will fail as it can't find laTunnel in getWorkflowCache().
		newWorkflowTaskWorkerInternal(taskHandler, t.service, testDomain, params, make(chan struct{}))
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
	RegisterWorkflowWithOptions(
		workflowFunc,
		RegisterWorkflowOptions{Name: workflowName},
	)

	taskList := "taskList"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}

	params := workerExecutionParameters{
		TaskList:               taskList,
		Identity:               "test-id-1",
		Logger:                 zap.NewNop(),
		DisableStickyExecution: disableSticky,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
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
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &commonproto.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonproto.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
		}),
	}
	task := createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	stopC := make(chan struct{})
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
		WorkerStopChannel:              stopC,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
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
	newWorkflowTaskWorkerInternal(taskHandler, t.service, testDomain, params, stopC)
	request, err = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "nondeterministic")

	// now, create a new task handler with fail nondeterministic workflow policy
	// and verify that it handles the mismatching history correctly.
	params.NonDeterministicWorkflowPolicy = NonDeterministicWorkflowPolicyFailWorkflow
	failOnNondeterminismTaskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
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
	t.Equal(closeDecision.DecisionType, enums.DecisionTypeFailWorkflowExecution)
	t.Contains(closeDecision.GetFailWorkflowExecutionDecisionAttributes().Reason, "NonDeterministicWorkflowPolicyFailWorkflow")

	// now with different package name to activity type
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "new-package.Greeter_Activity"
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	request, err = taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowReturnsPanicError() {
	taskList := "taskList"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 3, "ReturnPanicWorkflow")
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.True(ok)
	t.EqualValues(enums.DecisionTypeFailWorkflowExecution, r.Decisions[0].GetDecisionType())
	attr := r.Decisions[0].GetFailWorkflowExecutionDecisionAttributes()
	t.EqualValues("cadenceInternal:Panic", attr.GetReason())
	details := string(attr.Details)
	t.True(strings.HasPrefix(details, "\"panicError"), details)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowPanics() {
	taskList := "taskList"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 3, "PanicWorkflow")
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondDecisionTaskFailedRequest)
	t.True(ok)
	t.EqualValues(enums.DecisionTaskFailedCauseWorkflowWorkerUnhandledFailure, r.Cause)
	t.EqualValues("panicError", string(r.Details))
}

func (t *TaskHandlersTestSuite) TestGetWorkflowInfo() {
	taskList := "taskList"
	parentID := "parentID"
	parentRunID := "parentRun"
	cronSchedule := "5 4 * * *"
	continuedRunID := uuid.New()
	parentExecution := &commonproto.WorkflowExecution{
		WorkflowId: parentID,
		RunId:      parentRunID,
	}
	parentDomain := "parentDomain"
	var attempt int32 = 123
	var executionTimeout int32 = 213456
	var taskTimeout int32 = 21
	workflowType := "GetWorkflowInfoWorkflow"
	lastCompletionResult, err := getDefaultDataConverter().ToData("lastCompletionData")
	t.NoError(err)
	startedEventAttributes := &commonproto.WorkflowExecutionStartedEventAttributes{
		Input:                               lastCompletionResult,
		TaskList:                            &commonproto.TaskList{Name: taskList},
		ParentWorkflowExecution:             parentExecution,
		CronSchedule:                        cronSchedule,
		ContinuedExecutionRunId:             continuedRunID,
		ParentWorkflowDomain:                parentDomain,
		Attempt:                             attempt,
		ExecutionStartToCloseTimeoutSeconds: executionTimeout,
		TaskStartToCloseTimeoutSeconds:      taskTimeout,
		LastCompletionResult:                lastCompletionResult,
	}
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, startedEventAttributes),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 3, workflowType)
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.True(ok)
	t.EqualValues(enums.DecisionTypeCompleteWorkflowExecution, r.Decisions[0].GetDecisionType())
	attr := r.Decisions[0].GetCompleteWorkflowExecutionDecisionAttributes()
	var result WorkflowInfo
	t.NoError(getDefaultDataConverter().FromData(attr.Result, &result))
	t.EqualValues(taskList, result.TaskListName)
	t.EqualValues(parentID, result.ParentWorkflowExecution.ID)
	t.EqualValues(parentRunID, result.ParentWorkflowExecution.RunID)
	t.EqualValues(cronSchedule, result.CronSchedule)
	t.EqualValues(continuedRunID, result.ContinuedExecutionRunID)
	t.EqualValues(parentDomain, result.ParentWorkflowDomain)
	t.EqualValues(attempt, result.Attempt)
	t.EqualValues(executionTimeout, result.ExecutionStartToCloseTimeoutSeconds)
	t.EqualValues(taskTimeout, result.TaskStartToCloseTimeoutSeconds)
	t.EqualValues(workflowType, result.WorkflowType.Name)
	t.EqualValues(testDomain, result.Domain)
}

func (t *TaskHandlersTestSuite) TestConsistentQuery_InvalidQueryTask() {
	taskList := "taskList"
	params := workerExecutionParameters{
		TaskList:                       taskList,
		Identity:                       "test-id-1",
		Logger:                         zap.NewNop(),
		NonDeterministicWorkflowPolicy: NonDeterministicWorkflowPolicyBlockWorkflow,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	task := createWorkflowTask(nil, 3, "HelloWorld_Workflow")
	task.Query = &commonproto.WorkflowQuery{}
	task.Queries = map[string]*commonproto.WorkflowQuery{"query_id": {}}
	newWorkflowTaskWorkerInternal(taskHandler, t.service, testDomain, params, make(chan struct{}))
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
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{
			TaskList: &commonproto.TaskList{Name: taskList},
			Input:    numberOfSignalsToComplete,
		}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 2, BinaryChecksum: checksum1}),
		createTestEventWorkflowExecutionSignaledWithPayload(5, signalCh, signal),
		createTestEventDecisionTaskScheduled(6, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(7),
	}

	queries := map[string]*commonproto.WorkflowQuery{
		"id1": {QueryType: queryType},
		"id2": {QueryType: errQueryType},
	}
	task := createWorkflowTaskWithQueries(testEvents[0:3], 0, "QuerySignalWorkflow", queries)

	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Len(response.Decisions, 0)
	expectedQueryResults := map[string]*commonproto.WorkflowQueryResult{
		"id1": {
			ResultType: enums.QueryResultTypeAnswered,
			Answer:     []byte(fmt.Sprintf("\"%v\"\n", startingQueryValue)),
		},
		"id2": {
			ResultType:   enums.QueryResultTypeFailed,
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
	expectedQueryResults = map[string]*commonproto.WorkflowQueryResult{
		"id1": {
			ResultType: enums.QueryResultTypeAnswered,
			Answer:     []byte(fmt.Sprintf("\"%v\"\n", "signal data")),
		},
		"id2": {
			ResultType:   enums.QueryResultTypeFailed,
			ErrorMessage: queryErr,
		},
	}
	t.assertQueryResultsEqual(expectedQueryResults, response.QueryResults)

	// clean up workflow left in cache
	getWorkflowCache().Delete(task.WorkflowExecution.RunId)
}

func (t *TaskHandlersTestSuite) assertQueryResultsEqual(expected map[string]*commonproto.WorkflowQueryResult, actual map[string]*commonproto.WorkflowQueryResult) {
	t.Equal(len(expected), len(actual))
	for expectedID, expectedResult := range expected {
		t.Contains(actual, expectedID)
		t.True(expectedResult.Equal(actual[expectedID]))
	}
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_CancelActivityBeforeSent() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_WorkflowCancel")

	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}
	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	response := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Decisions))
	t.Equal(enums.DecisionTypeCompleteWorkflowExecution, response.Decisions[0].GetDecisionType())
	t.NotNil(response.Decisions[0].GetCompleteWorkflowExecutionDecisionAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_PageToken() {
	// Schedule a decision activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_Workflow")
	task.NextPageToken = []byte("token")

	params := workerExecutionParameters{
		TaskList: taskList,
		Identity: "test-id-1",
		Logger:   t.logger,
	}

	nextEvents := []*commonproto.HistoryEvent{
		createTestEventDecisionTaskStarted(3),
	}

	historyIterator := &historyIteratorImpl{
		iteratorFunc: func(nextToken []byte) (*commonproto.History, []byte, error) {
			return &commonproto.History{Events: nextEvents}, nil, nil
		},
	}
	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
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
	RegisterWorkflowWithOptions(
		retryLocalActivityWorkflowFunc,
		RegisterWorkflowOptions{Name: "RetryLocalActivityWorkflow"},
	)

	decisionTaskStartedEvent := createTestEventDecisionTaskStarted(3)
	decisionTaskStartedEvent.Timestamp = time.Now().UnixNano()
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{
			// make sure the timeout is same as the backoff interval
			TaskStartToCloseTimeoutSeconds: backoffIntervalInSeconds,
			TaskList:                       &commonproto.TaskList{Name: testWorkflowTaskTasklist}},
		),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		decisionTaskStartedEvent,
	}

	task := createWorkflowTask(testEvents, 0, "RetryLocalActivityWorkflow")
	stopCh := make(chan struct{})
	params := workerExecutionParameters{
		TaskList:          testWorkflowTaskTasklist,
		Identity:          "test-id-1",
		Logger:            t.logger,
		Tracer:            opentracing.NoopTracer{},
		WorkerStopChannel: stopCh,
	}
	defer close(stopCh)

	taskHandler := newWorkflowTaskHandler(testDomain, params, nil, getGlobalRegistry())
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
			return nil, protobufutils.NewErrorWithMessage(codes.NotFound, "Decision task not found.")
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
	mockService := workflowservicemock.NewMockWorkflowServiceYARPCClient(mockCtrl)

	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: false}
	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).Return(&heartbeatResponse, nil)

	cadenceInvoker := &cadenceInvoker{
		identity:  "Test_Cadence_Invoker",
		service:   mockService,
		taskToken: nil,
	}

	heartbeatErr := cadenceInvoker.Heartbeat(nil)

	t.Nil(heartbeatErr)
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NilResponseWithError() {
	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceYARPCClient(mockCtrl)

	entityNotExistsError := protobufutils.NewError(codes.NotFound)
	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).Return(nil, entityNotExistsError)

	cadenceInvoker := newServiceInvoker(
		nil,
		"Test_Cadence_Invoker",
		mockService,
		func() {},
		0,
		make(chan struct{}))

	heartbeatErr := cadenceInvoker.Heartbeat(nil)
	t.NotNil(heartbeatErr)
	t.Equal(codes.NotFound, protobufutils.GetCode(heartbeatErr), "heartbeatErr must have code NotFound.")
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NilResponseWithDomainNotActiveError() {
	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceYARPCClient(mockCtrl)

	domainNotActiveError := protobufutils.NewErrorWithFailure(codes.InvalidArgument, "", &errordetails.DomainNotActiveFailure{})
	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).Return(nil, domainNotActiveError)

	called := false
	cancelHandler := func() { called = true }

	cadenceInvoker := newServiceInvoker(
		nil,
		"Test_Cadence_Invoker",
		mockService,
		cancelHandler,
		0,
		make(chan struct{}))

	heartbeatErr := cadenceInvoker.Heartbeat(nil)
	t.NotNil(heartbeatErr)
	_, isDomainNotActive := protobufutils.GetFailure(heartbeatErr).(*errordetails.DomainNotActiveFailure)
	t.True(isDomainNotActive, "heartbeatErr failure must be DomainNotActiveFailure.")
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
	registry := getGlobalRegistry()
	registry.addActivity(a.ActivityType().Name, a)

	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceYARPCClient(mockCtrl)

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
			WorkflowExecution: &commonproto.WorkflowExecution{
				WorkflowId: "wID",
				RunId:      "rID"},
			ActivityType:                  &commonproto.ActivityType{Name: "test"},
			ActivityId:                    uuid.New(),
			ScheduledTimestamp:            d.ScheduleTS.UnixNano(),
			ScheduleToCloseTimeoutSeconds: d.ScheduleDuration,
			StartedTimestamp:              d.StartTS.UnixNano(),
			StartToCloseTimeoutSeconds:    d.StartDuration,
			WorkflowType: &commonproto.WorkflowType{
				Name: "wType",
			},
			WorkflowDomain: "domain",
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
	registry := getGlobalRegistry()
	registry.addActivityFn(a.ActivityType().Name, activityWithWorkerStop)

	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceYARPCClient(mockCtrl)
	workerStopCh := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	wep := workerExecutionParameters{
		Logger:            t.logger,
		DataConverter:     getDefaultDataConverter(),
		UserContext:       ctx,
		UserContextCancel: cancel,
		WorkerStopChannel: workerStopCh,
	}
	activityHandler := newActivityTaskHandler(mockService, wep, registry)
	pats := &workflowservice.PollForActivityTaskResponse{
		TaskToken: []byte("token"),
		WorkflowExecution: &commonproto.WorkflowExecution{
			WorkflowId: "wID",
			RunId:      "rID"},
		ActivityType:                  &commonproto.ActivityType{Name: "test"},
		ActivityId:                    uuid.New(),
		ScheduledTimestamp:            time.Now().UnixNano(),
		ScheduleToCloseTimeoutSeconds: 1,
		StartedTimestamp:              time.Now().UnixNano(),
		StartToCloseTimeoutSeconds:    1,
		WorkflowType: &commonproto.WorkflowType{
			Name: "wType",
		},
		WorkflowDomain: "domain",
	}
	close(workerStopCh)
	r, err := activityHandler.Execute(tasklist, pats)
	t.NoError(err)
	t.NotNil(r)
}

func Test_NonDeterministicCheck(t *testing.T) {
	decisionTypes := enums.DecisionType_value
	require.Equal(t, 13, len(decisionTypes), "If you see this error, you are adding new decision type. "+
		"Before updating the number to make this test pass, please make sure you update isDecisionMatchEvent() method "+
		"to check the new decision type. Otherwise the replay will fail on the new decision event.")

	eventTypes := enums.EventType_value
	decisionEventTypeCount := 0
	for _, et := range eventTypes {
		if isDecisionEvent(enums.EventType(et)) {
			decisionEventTypeCount++
		}
	}
	// CancelTimer has 2 corresponding events.
	require.Equal(t, len(decisionTypes)+1, decisionEventTypeCount, "Every decision type must have one matching event type. "+
		"If you add new decision type, you need to update isDecisionEvent() method to include that new event type as well.")
}

func Test_IsDecisionMatchEvent_UpsertWorkflowSearchAttributes(t *testing.T) {
	diType := enums.DecisionTypeUpsertWorkflowSearchAttributes
	eType := enums.EventTypeUpsertWorkflowSearchAttributes
	strictMode := false

	testCases := []struct {
		name     string
		decision *commonproto.Decision
		event    *commonproto.HistoryEvent
		expected bool
	}{
		{
			name: "event type not match",
			decision: &commonproto.Decision{
				DecisionType: diType,
				Attributes: &commonproto.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &commonproto.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonproto.SearchAttributes{},
				}},
			},
			event:    &commonproto.HistoryEvent{},
			expected: false,
		},
		{
			name: "attributes not match",
			decision: &commonproto.Decision{
				DecisionType: diType,
				Attributes: &commonproto.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &commonproto.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonproto.SearchAttributes{},
				}},
			},
			event: &commonproto.HistoryEvent{
				EventType:  eType,
				Attributes: &commonproto.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &commonproto.UpsertWorkflowSearchAttributesEventAttributes{}}},
			expected: true,
		},
		{
			name: "attributes match",
			decision: &commonproto.Decision{
				DecisionType: diType,
				Attributes: &commonproto.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &commonproto.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonproto.SearchAttributes{},
				}},
			},
			event: &commonproto.HistoryEvent{
				EventType: eType,
				Attributes: &commonproto.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &commonproto.UpsertWorkflowSearchAttributesEventAttributes{
					SearchAttributes: &commonproto.SearchAttributes{},
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
		decision *commonproto.Decision
		event    *commonproto.HistoryEvent
		expected bool
	}{
		{
			name: "attributes not match",
			decision: &commonproto.Decision{
				DecisionType: diType,
				Attributes: &commonproto.Decision_UpsertWorkflowSearchAttributesDecisionAttributes{UpsertWorkflowSearchAttributesDecisionAttributes: &commonproto.UpsertWorkflowSearchAttributesDecisionAttributes{
					SearchAttributes: &commonproto.SearchAttributes{},
				}},
			},
			event: &commonproto.HistoryEvent{
				EventType:  eType,
				Attributes: &commonproto.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &commonproto.UpsertWorkflowSearchAttributesEventAttributes{}}},
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
		lhs      *commonproto.SearchAttributes
		rhs      *commonproto.SearchAttributes
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
			rhs:      &commonproto.SearchAttributes{},
			expected: false,
		},
		{
			name:     "right nil",
			lhs:      &commonproto.SearchAttributes{},
			rhs:      nil,
			expected: false,
		},
		{
			name: "not match",
			lhs: &commonproto.SearchAttributes{
				IndexedFields: map[string][]byte{
					"key1": []byte("1"),
					"key2": []byte("abc"),
				},
			},
			rhs:      &commonproto.SearchAttributes{},
			expected: false,
		},
		{
			name: "match",
			lhs: &commonproto.SearchAttributes{
				IndexedFields: map[string][]byte{
					"key1": []byte("1"),
					"key2": []byte("abc"),
				},
			},
			rhs: &commonproto.SearchAttributes{
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
