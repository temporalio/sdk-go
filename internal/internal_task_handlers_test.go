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
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	protocolpb "go.temporal.io/api/protocol/v1"
	querypb "go.temporal.io/api/query/v1"
	"go.temporal.io/api/sdk/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	updatepb "go.temporal.io/api/update/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/cache"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/internal/protocol"
	"go.temporal.io/sdk/log"
)

const (
	testNamespace = "test-namespace"
)

type (
	TaskHandlersTestSuite struct {
		suite.Suite
		logger    log.Logger
		service   *workflowservicemock.MockWorkflowServiceClient
		registry  *registry
		namespace string
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
	r.RegisterWorkflowWithOptions(
		helloUpdateWorkflowFunc,
		RegisterWorkflowOptions{Name: "HelloUpdate_Workflow"},
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
	err = converter.GetDefaultDataConverter().FromPayloads(result.lastCompletionResult, &lastCompletionResult)
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
	t.logger = ilog.NewDefaultLogger()
	registerWorkflows(t.registry)
	t.namespace = "default"
}

func (t *TaskHandlersTestSuite) TearDownTest() {
	if cache := *sharedWorkerCachePtr.workflowCache; cache != nil {
		cache.Clear()
	}
}

func TestTaskHandlersTestSuite(t *testing.T) {
	suite.Run(t, &TaskHandlersTestSuite{
		registry: newRegistry(),
	})
}

func createTestEventWorkflowExecutionCompleted(eventID int64, attr *historypb.WorkflowExecutionCompletedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: attr},
	}
}

func createTestEventWorkflowExecutionStarted(eventID int64, attr *historypb.WorkflowExecutionStartedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: attr},
	}
}

func createTestEventMarkerRecorded(eventID int64, attr *historypb.MarkerRecordedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: attr},
	}
}

func createTestEventActivityTaskScheduled(eventID int64, attr *historypb.ActivityTaskScheduledEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
		Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{ActivityTaskScheduledEventAttributes: attr},
	}
}

func createTestEventActivityTaskCancelRequested(eventID int64, attr *historypb.ActivityTaskCancelRequestedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED,
		Attributes: &historypb.HistoryEvent_ActivityTaskCancelRequestedEventAttributes{ActivityTaskCancelRequestedEventAttributes: attr},
	}
}

func createTestEventActivityTaskStarted(eventID int64, attr *historypb.ActivityTaskStartedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED,
		Attributes: &historypb.HistoryEvent_ActivityTaskStartedEventAttributes{ActivityTaskStartedEventAttributes: attr},
	}
}

func createTestEventActivityTaskCompleted(eventID int64, attr *historypb.ActivityTaskCompletedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
		Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{ActivityTaskCompletedEventAttributes: attr},
	}
}

func createTestEventActivityTaskTimedOut(eventID int64, attr *historypb.ActivityTaskTimedOutEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT,
		Attributes: &historypb.HistoryEvent_ActivityTaskTimedOutEventAttributes{ActivityTaskTimedOutEventAttributes: attr},
	}
}

func createTestEventWorkflowTaskScheduled(eventID int64, attr *historypb.WorkflowTaskScheduledEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED,
		Attributes: &historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes{WorkflowTaskScheduledEventAttributes: attr},
	}
}

func createTestEventWorkflowTaskStarted(eventID int64) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:   eventID,
		EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED,
	}
}

func createTestEventWorkflowExecutionSignaled(eventID int64, signalName string) *historypb.HistoryEvent {
	return createTestEventWorkflowExecutionSignaledWithPayload(eventID, signalName, nil)
}

func createTestEventWorkflowExecutionSignaledWithPayload(eventID int64, signalName string, payloads *commonpb.Payloads) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:   eventID,
		EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionSignaledEventAttributes{WorkflowExecutionSignaledEventAttributes: &historypb.WorkflowExecutionSignaledEventAttributes{
			SignalName: signalName,
			Input:      payloads,
			Identity:   "test-identity",
		}},
	}
}

func createTestEventWorkflowTaskCompleted(eventID int64, attr *historypb.WorkflowTaskCompletedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
		Attributes: &historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes{WorkflowTaskCompletedEventAttributes: attr},
	}
}

func createTestEventWorkflowTaskFailed(eventID int64, attr *historypb.WorkflowTaskFailedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED,
		Attributes: &historypb.HistoryEvent_WorkflowTaskFailedEventAttributes{WorkflowTaskFailedEventAttributes: attr},
	}
}

func createTestEventWorkflowTaskTimedOut(eventID int64, attr *historypb.WorkflowTaskTimedOutEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_TASK_TIMED_OUT,
		Attributes: &historypb.HistoryEvent_WorkflowTaskTimedOutEventAttributes{WorkflowTaskTimedOutEventAttributes: attr},
	}
}

func createTestEventSignalExternalWorkflowExecutionFailed(eventID int64, attr *historypb.SignalExternalWorkflowExecutionFailedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_FAILED,
		Attributes: &historypb.HistoryEvent_SignalExternalWorkflowExecutionFailedEventAttributes{SignalExternalWorkflowExecutionFailedEventAttributes: attr},
	}
}

func createTestEventStartChildWorkflowExecutionInitiated(eventID int64, attr *historypb.StartChildWorkflowExecutionInitiatedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED,
		Attributes: &historypb.HistoryEvent_StartChildWorkflowExecutionInitiatedEventAttributes{StartChildWorkflowExecutionInitiatedEventAttributes: attr},
	}
}

func createTestEventChildWorkflowExecutionStarted(eventID int64, attr *historypb.ChildWorkflowExecutionStartedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_STARTED,
		Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionStartedEventAttributes{ChildWorkflowExecutionStartedEventAttributes: attr},
	}
}

func createTestEventStartChildWorkflowExecutionFailed(eventID int64, attr *historypb.StartChildWorkflowExecutionFailedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_FAILED,
		Attributes: &historypb.HistoryEvent_StartChildWorkflowExecutionFailedEventAttributes{StartChildWorkflowExecutionFailedEventAttributes: attr},
	}
}

func createTestEventRequestCancelExternalWorkflowExecutionInitiated(eventID int64, attr *historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED,
		Attributes: &historypb.HistoryEvent_RequestCancelExternalWorkflowExecutionInitiatedEventAttributes{RequestCancelExternalWorkflowExecutionInitiatedEventAttributes: attr},
	}
}

func createTestEventWorkflowExecutionCancelRequested(eventID int64, attr *historypb.WorkflowExecutionCancelRequestedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCEL_REQUESTED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionCancelRequestedEventAttributes{WorkflowExecutionCancelRequestedEventAttributes: attr},
	}
}

func createTestEventExternalWorkflowExecutionCancelRequested(eventID int64, attr *historypb.ExternalWorkflowExecutionCancelRequestedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_EXTERNAL_WORKFLOW_EXECUTION_CANCEL_REQUESTED,
		Attributes: &historypb.HistoryEvent_ExternalWorkflowExecutionCancelRequestedEventAttributes{ExternalWorkflowExecutionCancelRequestedEventAttributes: attr},
	}
}

func createTestEventChildWorkflowExecutionCanceled(eventID int64, attr *historypb.ChildWorkflowExecutionCanceledEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_CANCELED,
		Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCanceledEventAttributes{ChildWorkflowExecutionCanceledEventAttributes: attr},
	}
}

func createTestEventWorkflowExecutionUpdateAdmitted(eventID int64, attr *historypb.WorkflowExecutionUpdateAdmittedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ADMITTED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionUpdateAdmittedEventAttributes{WorkflowExecutionUpdateAdmittedEventAttributes: attr},
	}
}

func createTestEventWorkflowExecutionUpdateAccepted(eventID int64, attr *historypb.WorkflowExecutionUpdateAcceptedEventAttributes) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionUpdateAcceptedEventAttributes{WorkflowExecutionUpdateAcceptedEventAttributes: attr},
	}
}

func createTestEventVersionMarker(eventID int64, workflowTaskCompletedID int64, changeID string, version Version) *historypb.HistoryEvent {
	changeIDPayload, err := converter.GetDefaultDataConverter().ToPayloads(changeID)
	if err != nil {
		panic(err)
	}

	versionPayload, err := converter.GetDefaultDataConverter().ToPayloads(version)
	if err != nil {
		panic(err)
	}

	return &historypb.HistoryEvent{
		EventId:   eventID,
		EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{
			MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName: versionMarkerName,
				Details: map[string]*commonpb.Payloads{
					versionMarkerChangeIDName: changeIDPayload,
					versionMarkerDataName:     versionPayload,
				},
				WorkflowTaskCompletedEventId: workflowTaskCompletedID,
			},
		},
	}
}

func createTestEventSideEffectMarker(eventID int64, workflowTaskCompletedID int64, sideEffectID int64, result int) *historypb.HistoryEvent {
	sideEffectIDPayload, err := converter.GetDefaultDataConverter().ToPayloads(sideEffectID)
	if err != nil {
		panic(err)
	}

	resultPayload, err := converter.GetDefaultDataConverter().ToPayloads(result)
	if err != nil {
		panic(err)
	}

	return &historypb.HistoryEvent{
		EventId:   eventID,
		EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{
			MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName: sideEffectMarkerName,
				Details: map[string]*commonpb.Payloads{
					sideEffectMarkerIDName:   sideEffectIDPayload,
					sideEffectMarkerDataName: resultPayload,
				},
				WorkflowTaskCompletedEventId: workflowTaskCompletedID,
			},
		},
	}
}

func createTestUpsertWorkflowSearchAttributesForChangeVersion(eventID int64, workflowTaskCompletedID int64, changeID string, version Version) *historypb.HistoryEvent {
	searchAttributes, _ := validateAndSerializeSearchAttributes(createSearchAttributesForChangeVersion(changeID, version, nil))

	return &historypb.HistoryEvent{
		EventId:   eventID,
		EventType: enumspb.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES,
		Attributes: &historypb.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{
			UpsertWorkflowSearchAttributesEventAttributes: &historypb.UpsertWorkflowSearchAttributesEventAttributes{
				SearchAttributes:             searchAttributes,
				WorkflowTaskCompletedEventId: workflowTaskCompletedID,
			},
		},
	}
}

func createTestProtocolMessageUpdateRequest(ID string, eventID int64, request *updatepb.Request) *protocolpb.Message {
	return &protocolpb.Message{
		Id:                 uuid.New(),
		ProtocolInstanceId: ID,
		SequencingId:       &protocolpb.Message_EventId{EventId: eventID},
		Body:               protocol.MustMarshalAny(request),
	}
}

func createWorkflowTask(
	events []*historypb.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
) *workflowservice.PollWorkflowTaskQueueResponse {
	return createWorkflowTaskWithQueries(events, previousStartEventID, workflowName, nil, true)
}

func createWorkflowTaskWithQueries(
	events []*historypb.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
	queries map[string]*querypb.WorkflowQuery,
	addEvents bool,
) *workflowservice.PollWorkflowTaskQueueResponse {
	eventsCopy := make([]*historypb.HistoryEvent, len(events))
	copy(eventsCopy, events)
	if addEvents {
		nextEventID := eventsCopy[len(eventsCopy)-1].EventId + 1
		eventsCopy = append(eventsCopy, createTestEventWorkflowTaskScheduled(nextEventID,
			&historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: "taskQueue"}}))
		eventsCopy = append(eventsCopy, createTestEventWorkflowTaskStarted(nextEventID+1))
	}
	return &workflowservice.PollWorkflowTaskQueueResponse{
		PreviousStartedEventId: previousStartEventID,
		WorkflowType:           &commonpb.WorkflowType{Name: workflowName},
		History:                &historypb.History{Events: eventsCopy},
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "fake-workflow-id",
			RunId:      uuid.New(),
		},
		Queries: queries,
	}
}

func createQueryTask(
	events []*historypb.HistoryEvent,
	previousStartEventID int64,
	workflowName string,
	queryType string,
) *workflowservice.PollWorkflowTaskQueueResponse {
	task := createWorkflowTaskWithQueries(events, previousStartEventID, workflowName, nil, false)
	task.Query = &querypb.WorkflowQuery{
		QueryType: queryType,
	}
	return task
}

func createTestEventTimerStarted(eventID int64, id int) *historypb.HistoryEvent {
	timerID := fmt.Sprintf("%v", id)
	attr := &historypb.TimerStartedEventAttributes{
		TimerId:                      timerID,
		StartToFireTimeout:           nil,
		WorkflowTaskCompletedEventId: 0,
	}
	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_TIMER_STARTED,
		Attributes: &historypb.HistoryEvent_TimerStartedEventAttributes{TimerStartedEventAttributes: attr},
	}
}

func createTestEventTimerFired(eventID int64, id int) *historypb.HistoryEvent {
	timerID := fmt.Sprintf("%v", id)
	attr := &historypb.TimerFiredEventAttributes{
		TimerId: timerID,
	}

	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_TIMER_FIRED,
		Attributes: &historypb.HistoryEvent_TimerFiredEventAttributes{TimerFiredEventAttributes: attr},
	}
}

func createTestEventTimerCanceled(eventID int64, id int) *historypb.HistoryEvent {
	timerID := fmt.Sprintf("%v", id)
	attr := &historypb.TimerCanceledEventAttributes{
		TimerId: timerID,
	}

	return &historypb.HistoryEvent{
		EventId:    eventID,
		EventType:  enumspb.EVENT_TYPE_TIMER_CANCELED,
		Attributes: &historypb.HistoryEvent_TimerCanceledEventAttributes{TimerCanceledEventAttributes: attr},
	}
}

var testWorkflowTaskTaskqueue = "tq1"

func (t *TaskHandlersTestSuite) getTestWorkerExecutionParams() workerExecutionParameters {
	cache := NewWorkerCache()
	return workerExecutionParameters{
		TaskQueue:        testWorkflowTaskTaskqueue,
		Namespace:        testNamespace,
		Identity:         "test-id-1",
		MetricsHandler:   metrics.NopHandler,
		Logger:           t.logger,
		FailureConverter: GetDefaultFailureConverter(),
		cache:            cache,
		capabilities: &workflowservice.GetSystemInfoResponse_Capabilities{
			SignalAndQueryHeader:            true,
			InternalErrorDifferentiation:    true,
			ActivityFailureIncludeHeartbeat: true,
			SupportsSchedules:               true,
			EncodedFailureAttributes:        true,
			UpsertMemo:                      true,
			EagerWorkflowStart:              true,
			SdkMetadata:                     true,
		},
	}
}

func (t *TaskHandlersTestSuite) mustWorkflowContextImpl(
	task *workflowTask,
	cm WorkflowContextManager,
) *workflowExecutionContextImpl {
	wfctx, err := cm.GetOrCreateWorkflowContext(task.task, task.historyIterator)
	t.Require().NoError(err)
	return wfctx
}

func (t *TaskHandlersTestSuite) testWorkflowTaskWorkflowExecutionStartedHelper(params workerExecutionParameters) {
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_Workflow")
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Commands))
	t.Equal(enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, response.Commands[0].GetCommandType())
	t.NotNil(response.Commands[0].GetScheduleActivityTaskCommandAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStarted() {
	params := t.getTestWorkerExecutionParams()
	t.testWorkflowTaskWorkflowExecutionStartedHelper(params)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowExecutionStartedWithDataConverter() {
	params := t.getTestWorkerExecutionParams()
	t.testWorkflowTaskWorkflowExecutionStartedHelper(params)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_BinaryChecksum() {
	taskQueue := "tq1"
	checksum1 := "chck1"
	checksum2 := "chck2"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2, BinaryChecksum: checksum1}),
		createTestEventTimerStarted(5, 5),
		createTestEventTimerFired(6, 5),
		createTestEventWorkflowTaskScheduled(7, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(8),
		createTestEventWorkflowTaskCompleted(9, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 7, BinaryChecksum: checksum2}),
		createTestEventTimerStarted(10, 10),
		createTestEventTimerFired(11, 10),
		createTestEventWorkflowTaskScheduled(12, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(13),
	}
	task := createWorkflowTask(testEvents, 8, "BinaryChecksumWorkflow")
	params := t.getTestWorkerExecutionParams()
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)

	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Commands))
	t.Equal(enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION, response.Commands[0].GetCommandType())
	checksumsPayload := response.Commands[0].GetCompleteWorkflowExecutionCommandAttributes().GetResult()
	var checksums []string
	_ = converter.GetDefaultDataConverter().FromPayloads(checksumsPayload, &checksums)
	t.Equal(3, len(checksums))
	t.Equal("chck1", checksums[0])
	t.Equal("chck2", checksums[1])
	t.Equal(getBinaryChecksum(), checksums[2])
}

func (t *TaskHandlersTestSuite) TestRespondsToWFTWithWorkerBinaryID() {
	taskQueue := "tq1"
	workerBuildID := "yaaaay"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_Workflow")
	params := t.getTestWorkerExecutionParams()
	params.WorkerBuildID = workerBuildID
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(workerBuildID, response.GetWorkerVersionStamp().GetBuildId())
	// clean up workflow left in cache
	params.cache.getWorkflowCache().Delete(task.WorkflowExecution.RunId)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_ActivityTaskScheduled() {
	// Schedule an activity and see if we complete workflow.
	taskQueue := "tq1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(6, &historypb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventWorkflowTaskStarted(8),
	}
	task := createWorkflowTask(testEvents[0:3], 0, "HelloWorld_Workflow")
	params := t.getTestWorkerExecutionParams()
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)

	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Commands))
	t.Equal(enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, response.Commands[0].GetCommandType())
	t.NotNil(response.Commands[0].GetScheduleActivityTaskCommandAttributes())

	// Schedule an activity and see if we complete workflow, Having only one last command.
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response = request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Commands))
	t.Equal(enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION, response.Commands[0].GetCommandType())
	t.NotNil(response.Commands[0].GetCompleteWorkflowExecutionCommandAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_QueryWorkflow_Sticky() {
	// Schedule an activity and see if we complete workflow.
	taskQueue := "sticky-tq"
	execution := &commonpb.WorkflowExecution{
		WorkflowId: "fake-workflow-id",
		RunId:      uuid.New(),
	}
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(6, &historypb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
	}
	params := t.getTestWorkerExecutionParams()
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)

	// first make progress on the workflow
	task := createWorkflowTask(testEvents[0:1], 0, "HelloWorld_Workflow")
	task.StartedEventId = 1
	task.WorkflowExecution = execution
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(1, len(response.Commands))
	t.Equal(enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, response.Commands[0].GetCommandType())
	t.NotNil(response.Commands[0].GetScheduleActivityTaskCommandAttributes())

	// then check the current state using query task
	task = createQueryTask([]*historypb.HistoryEvent{}, 6, "HelloWorld_Workflow", queryType)
	task.WorkflowExecution = execution
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	queryResp, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.NoError(err)
	t.verifyQueryResult(queryResp, "waiting-activity-result")
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_QueryWorkflow_NonSticky() {
	// Schedule an activity and see if we complete workflow.
	taskQueue := "tq1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(6, &historypb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(7, &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventWorkflowTaskStarted(8),
		createTestEventWorkflowExecutionSignaled(9, "test-signal"),
	}
	params := t.getTestWorkerExecutionParams()

	// query after first workflow task (notice the previousStartEventID is always the last eventID for query task)
	task := createQueryTask(testEvents[0:3], 3, "HelloWorld_Workflow", queryType)
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	response, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.verifyQueryResult(response, "waiting-activity-result")

	// query after activity task complete but before second workflow task started
	task = createQueryTask(testEvents[0:7], 7, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	response, _ = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.verifyQueryResult(response, "waiting-activity-result")

	// query after second workflow task
	task = createQueryTask(testEvents[0:8], 8, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	response, _ = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.verifyQueryResult(response, "done")

	// query after second workflow task with extra events
	task = createQueryTask(testEvents[0:9], 9, "HelloWorld_Workflow", queryType)
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	response, _ = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.verifyQueryResult(response, "done")

	task = createQueryTask(testEvents[0:9], 9, "HelloWorld_Workflow", "invalid-query-type")
	taskHandler = newWorkflowTaskHandler(params, nil, t.registry)
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	response, _ = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
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
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
		}),
	}
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	// now change the history event so it does not match to command produced via replay
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "some-other-activity"
	task := createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
	// will fail as it can't find laTunnel in newWorkerCache().
	newWorkflowTaskWorkerInternal(taskHandler, taskHandler, t.service, params, make(chan struct{}), nil)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)

	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "nondeterministic")

	// There should be nothing in the cache.
	t.EqualValues(params.cache.getWorkflowCache().Size(), 0)
}

func (t *TaskHandlersTestSuite) TestWithMissingHistoryEvents() {
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskStarted(7),
	}
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow
	t.Require().Equal(0, params.cache.getWorkflowCache().Size(),
		"Suite teardown should have reset cache state")

	for _, startEventID := range []int64{0, 3} {
		t.Run(fmt.Sprintf("startEventID=%v", startEventID), func() {
			taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
			task := createWorkflowTask(testEvents, startEventID, "HelloWorld_Workflow")
			// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
			// will fail as it can't find laTunnel in newWorkerCache().
			newWorkflowTaskWorkerInternal(taskHandler, taskHandler, t.service, params, make(chan struct{}), nil)
			wftask := workflowTask{task: task}
			wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
			request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
			wfctx.Unlock(err)

			t.Error(err)
			t.Nil(request)
			t.Contains(err.Error(), "missing history events")

			t.Equal(0, params.cache.getWorkflowCache().Size(), "cache should be empty")
		})
	}
}

func (t *TaskHandlersTestSuite) TestWithTruncatedHistory() {
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskFailed(4, &historypb.WorkflowTaskFailedEventAttributes{ScheduledEventId: 2}),
		createTestEventWorkflowTaskScheduled(5, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskStarted(6),
		createTestEventWorkflowTaskCompleted(7, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 5}),
		createTestEventActivityTaskScheduled(8, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
		}),
		createTestEventWorkflowTaskScheduled(9, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskStarted(10),
	}
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow

	testCases := []struct {
		startedEventID         int64
		previousStartedEventID int64
		isResultErr            bool
	}{
		{10, 6, false},
		{15, 10, true},
	}

	for i, tc := range testCases {
		cacheSize := params.cache.getWorkflowCache().Size()

		taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
		task := createWorkflowTask(testEvents, tc.previousStartedEventID, "HelloWorld_Workflow")
		// Cut the workflow task scheduled ans started events
		task.History.Events = task.History.Events[:len(task.History.Events)-2]
		task.StartedEventId = tc.startedEventID
		// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
		// will fail as it can't find laTunnel in newWorkerCache().
		newWorkflowTaskWorkerInternal(taskHandler, taskHandler, t.service, params, make(chan struct{}), nil)
		wftask := workflowTask{task: task}
		wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
		request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
		wfctx.Unlock(err)

		if tc.isResultErr {
			t.Error(err, "testcase %v failed", i)
			t.Nil(request)
			t.Contains(err.Error(), "premature end of stream")
			t.EqualValues(params.cache.getWorkflowCache().Size(), cacheSize)
			continue
		}

		t.NoError(err, "testcase %v failed", i)
		t.EqualValues(params.cache.getWorkflowCache().Size(), cacheSize+1)
	}
}

func (t *TaskHandlersTestSuite) TestSideEffectDefer() {
	t.testSideEffectDeferHelper(1)
}

func (t *TaskHandlersTestSuite) TestSideEffectDefer_NoCache() {
	t.testSideEffectDeferHelper(0)
}

func (t *TaskHandlersTestSuite) testSideEffectDeferHelper(cacheSize int) {
	value := "should not be modified"
	expectedValue := value
	doneCh := make(chan struct{})
	myWorkerCachePtr := &sharedWorkerCache{}
	var myWorkerCacheLock sync.Mutex

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
	workflowName := fmt.Sprintf("SideEffectDeferWorkflow-CacheSize=%d", cacheSize)
	t.registry.RegisterWorkflowWithOptions(
		workflowFunc,
		RegisterWorkflowOptions{Name: workflowName},
	)

	taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
	}

	params := t.getTestWorkerExecutionParams()
	params.cache = newWorkerCache(myWorkerCachePtr, &myWorkerCacheLock, cacheSize)

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	task := createWorkflowTask(testEvents, 0, workflowName)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	_, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.Nil(err)

	// Make sure the workflow coroutine has exited.
	<-doneCh
	// The side effect op should not be executed.
	t.Equal(expectedValue, value)

	// There should be nothing in the cache.
	t.EqualValues(0, params.cache.getWorkflowCache().Size())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_NondeterministicDetection() {
	taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		createTestEventActivityTaskScheduled(5, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonpb.ActivityType{Name: "pkg.Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
	}
	task := createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	stopC := make(chan struct{})
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow
	params.WorkerStopChannel = stopC

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	// there should be no error as the history events matched the commands.
	t.NoError(err)
	t.NotNil(response)

	// now change the history event so it does not match to command produced via replay
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "some-other-activity"
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	// newWorkflowTaskWorkerInternal will set the laTunnel in taskHandler, without it, ProcessWorkflowTask()
	// will fail as it can't find laTunnel in newWorkerCache().
	newWorkflowTaskWorkerInternal(taskHandler, taskHandler, t.service, params, stopC, nil)
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "nondeterministic")

	// now, create a new task handler with fail nondeterministic workflow policy
	// and verify that it handles the mismatching history correctly.
	params.WorkflowPanicPolicy = FailWorkflow
	failOnNondeterminismTaskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, failOnNondeterminismTaskHandler)
	request, err = failOnNondeterminismTaskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	// When FailWorkflow policy is set, task handler does not return an error,
	// because it will indicate non determinism in the request.
	t.NoError(err)
	// Verify that request is a RespondWorkflowTaskCompleteRequest
	response, ok := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.True(ok)
	// Verify there's at least 1 command
	// and the last last command is to fail workflow
	// and contains proper justification.(i.e. nondeterminism).
	t.True(len(response.Commands) > 0)
	closeCommand := response.Commands[len(response.Commands)-1]
	t.Equal(closeCommand.CommandType, enumspb.COMMAND_TYPE_FAIL_WORKFLOW_EXECUTION)
	t.Contains(closeCommand.GetFailWorkflowExecutionCommandAttributes().GetFailure().GetMessage(), "FailWorkflow")

	// now with different package name to activity type
	testEvents[4].GetActivityTaskScheduledEventAttributes().ActivityType.Name = "new-package.Greeter_Activity"
	task = createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	wftask = workflowTask{task: task}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.NoError(err)
	t.NotNil(request)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowReturnsPanicError() {
	taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 3, "ReturnPanicWorkflow")
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.True(ok)
	t.EqualValues(enumspb.COMMAND_TYPE_FAIL_WORKFLOW_EXECUTION, r.Commands[0].GetCommandType())
	attr := r.Commands[0].GetFailWorkflowExecutionCommandAttributes()
	t.EqualValues("panicError", attr.GetFailure().GetMessage())
	t.NotNil(attr.GetFailure().GetApplicationFailureInfo())
	t.EqualValues("PanicError", attr.GetFailure().GetApplicationFailureInfo().GetType())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_WorkflowPanics() {
	taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
	}
	task := createWorkflowTask(testEvents, 3, "PanicWorkflow")
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	_, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.Error(err)
	_, ok := err.(*workflowPanicError)
	t.True(ok)
}

func (t *TaskHandlersTestSuite) TestGetWorkflowInfo() {
	parentID := "parentID"
	parentRunID := "parentRun"
	cronSchedule := "5 4 * * *"
	continuedRunID := uuid.New()
	parentExecution := &commonpb.WorkflowExecution{
		WorkflowId: parentID,
		RunId:      parentRunID,
	}
	parentNamespace := "parentNamespace"
	var attempt int32 = 123
	executionTimeout := 213456 * time.Second
	runTimeout := 21098 * time.Second
	taskTimeout := 21 * time.Second
	workflowType := "GetWorkflowInfoWorkflow"
	lastCompletionResult, err := converter.GetDefaultDataConverter().ToPayloads("lastCompletionData")
	t.NoError(err)
	startedEventAttributes := &historypb.WorkflowExecutionStartedEventAttributes{
		Input:                    lastCompletionResult,
		TaskQueue:                &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
		ParentWorkflowExecution:  parentExecution,
		CronSchedule:             cronSchedule,
		ContinuedExecutionRunId:  continuedRunID,
		ParentWorkflowNamespace:  parentNamespace,
		Attempt:                  attempt,
		WorkflowExecutionTimeout: durationpb.New(executionTimeout),
		WorkflowRunTimeout:       durationpb.New(runTimeout),
		WorkflowTaskTimeout:      durationpb.New(taskTimeout),
		LastCompletionResult:     lastCompletionResult,
	}
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, startedEventAttributes),
	}
	task := createWorkflowTask(testEvents, 3, workflowType)
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.NoError(err)
	t.NotNil(request)
	r, ok := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.True(ok)
	t.EqualValues(enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION, r.Commands[0].GetCommandType())
	attr := r.Commands[0].GetCompleteWorkflowExecutionCommandAttributes()
	var result WorkflowInfo
	t.NoError(converter.GetDefaultDataConverter().FromPayloads(attr.Result, &result))
	t.EqualValues(testWorkflowTaskTaskqueue, result.TaskQueueName)
	t.EqualValues(parentID, result.ParentWorkflowExecution.ID)
	t.EqualValues(parentRunID, result.ParentWorkflowExecution.RunID)
	t.EqualValues(cronSchedule, result.CronSchedule)
	t.EqualValues(continuedRunID, result.ContinuedExecutionRunID)
	t.EqualValues(parentNamespace, result.ParentWorkflowNamespace)
	t.EqualValues(attempt, result.Attempt)
	t.EqualValues(executionTimeout, result.WorkflowExecutionTimeout)
	t.EqualValues(runTimeout, result.WorkflowRunTimeout)
	t.EqualValues(taskTimeout, result.WorkflowTaskTimeout)
	t.EqualValues(workflowType, result.WorkflowType.Name)
	t.EqualValues(testNamespace, result.Namespace)
}

func (t *TaskHandlersTestSuite) TestConsistentQuery_InvalidQueryTask() {
	params := t.getTestWorkerExecutionParams()
	params.WorkflowPanicPolicy = BlockWorkflow

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
	}
	task := createWorkflowTask(testEvents, 3, "HelloWorld_Workflow")
	task.Query = &querypb.WorkflowQuery{}
	task.Queries = map[string]*querypb.WorkflowQuery{"query_id": {}}
	newWorkflowTaskWorkerInternal(taskHandler, taskHandler, t.service, params, make(chan struct{}), nil)
	// query and queries are both specified so this is an invalid task
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)

	t.Error(err)
	t.Nil(request)
	t.Contains(err.Error(), "invalid query workflow task")

	// There should be nothing in the cache.
	t.EqualValues(params.cache.getWorkflowCache().Size(), 0)
}

func (t *TaskHandlersTestSuite) TestConsistentQuery_Success() {
	checksum1 := "chck1"
	numberOfSignalsToComplete, err := converter.GetDefaultDataConverter().ToPayloads(2)
	t.NoError(err)
	signal, err := converter.GetDefaultDataConverter().ToPayloads("signal data")
	t.NoError(err)
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
			Input:     numberOfSignalsToComplete,
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 2, BinaryChecksum: checksum1,
		}),
		createTestEventWorkflowExecutionSignaledWithPayload(5, signalCh, signal),
		createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(7),
	}

	queries := map[string]*querypb.WorkflowQuery{
		"id1": {QueryType: queryType},
		"id2": {QueryType: errQueryType},
	}
	task := createWorkflowTaskWithQueries(testEvents[0:3], 0, "QuerySignalWorkflow", queries, false)

	params := t.getTestWorkerExecutionParams()

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Len(response.Commands, 0)
	answer, _ := converter.GetDefaultDataConverter().ToPayloads(startingQueryValue)
	expectedQueryResults := map[string]*querypb.WorkflowQueryResult{
		"id1": {
			ResultType: enumspb.QUERY_RESULT_TYPE_ANSWERED,
			Answer:     answer,
		},
		"id2": {
			ResultType:   enumspb.QUERY_RESULT_TYPE_FAILED,
			ErrorMessage: queryErr,
		},
	}
	t.assertQueryResultsEqual(expectedQueryResults, response.QueryResults)

	secondTask := createWorkflowTaskWithQueries(testEvents, 3, "QuerySignalWorkflow", queries, false)
	secondTask.WorkflowExecution.RunId = task.WorkflowExecution.RunId
	wftask = workflowTask{task: secondTask}
	wfctx = t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err = taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response = request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Len(response.Commands, 1)
	answer, _ = converter.GetDefaultDataConverter().ToPayloads("signal data")
	expectedQueryResults = map[string]*querypb.WorkflowQueryResult{
		"id1": {
			ResultType: enumspb.QUERY_RESULT_TYPE_ANSWERED,
			Answer:     answer,
		},
		"id2": {
			ResultType:   enumspb.QUERY_RESULT_TYPE_FAILED,
			ErrorMessage: queryErr,
		},
	}
	t.assertQueryResultsEqual(expectedQueryResults, response.QueryResults)

	// clean up workflow left in cache
	params.cache.getWorkflowCache().Delete(task.WorkflowExecution.RunId)
}

func (t *TaskHandlersTestSuite) assertQueryResultsEqual(expected map[string]*querypb.WorkflowQueryResult, actual map[string]*querypb.WorkflowQueryResult) {
	t.T().Helper()
	t.Equal(len(expected), len(actual))
	for expectedID, expectedResult := range expected {
		t.Contains(actual, expectedID)
		t.True(proto.Equal(expectedResult, actual[expectedID]),
			"expected %v = %v", expectedResult, actual[expectedID])
	}
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_CancelActivityBeforeSent() {
	// Schedule an activity and see if we complete workflow.
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_WorkflowCancel")

	params := t.getTestWorkerExecutionParams()
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Equal(3, len(response.Commands))
	t.Equal(enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, response.Commands[0].GetCommandType())
	t.Equal(enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK, response.Commands[1].GetCommandType())
	t.Equal(enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION, response.Commands[2].GetCommandType())
	t.NotNil(response.Commands[2].GetCompleteWorkflowExecutionCommandAttributes())
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_PageToken() {
	// Schedule a command activity and see if we complete workflow.
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
	}
	task := createWorkflowTask(testEvents, 0, "HelloWorld_Workflow")
	task.NextPageToken = []byte("token")

	params := t.getTestWorkerExecutionParams()

	nextEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowTaskStarted(3),
	}

	historyIterator := &historyIteratorImpl{
		iteratorFunc: func(nextToken []byte) (*historypb.History, []byte, error) {
			return &historypb.History{Events: nextEvents}, nil, nil
		},
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task, historyIterator: historyIterator}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_DuplicateMessagesPanic() {
	//taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 2,
			StartedEventId:   3,
			SdkMetadata: &sdk.WorkflowTaskCompletedMetadata{
				LangUsedFlags: []uint32{
					3,
				},
			},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(5, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 2,
			ProtocolInstanceId:               "test",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(6, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 2,
			ProtocolInstanceId:               "test",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
	}
	// createWorkflowTask add a schedule and start event
	task := createWorkflowTask(testEvents, 0, "HelloUpdate_Workflow")
	task.NextPageToken = []byte("token")
	task.PreviousStartedEventId = 14

	params := t.getTestWorkerExecutionParams()

	historyIterator := &historyIteratorImpl{
		nextPageToken: []byte("token"),
		iteratorFunc: func(nextToken []byte) (*historypb.History, []byte, error) {
			return &historypb.History{Events: nil}, nil, nil
		},
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task, historyIterator: historyIterator}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.Error(err)
	t.Nil(request)
	_, ok := err.(*workflowPanicError)
	t.True(ok)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_Messages() {
	taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 2,
			StartedEventId:   3,
			SdkMetadata: &sdk.WorkflowTaskCompletedMetadata{
				LangUsedFlags: []uint32{
					3,
				},
			},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(5, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 2,
			ProtocolInstanceId:               "test",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventActivityTaskScheduled(6, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "6",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
	}
	// createWorkflowTask add a schedule and start event
	task := createWorkflowTask(testEvents, 0, "HelloUpdate_Workflow")
	task.NextPageToken = []byte("token")
	task.PreviousStartedEventId = 15

	params := t.getTestWorkerExecutionParams()

	nextEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowTaskCompleted(9, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 7,
			StartedEventId:   8,
		}),
		createTestEventWorkflowExecutionUpdateAccepted(10, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 5,
			ProtocolInstanceId:               "test_2",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test_2",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventActivityTaskScheduled(11, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "11",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(12, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 5,
			ProtocolInstanceId:               "test_3",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test_3",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventActivityTaskScheduled(13, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "13",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowTaskScheduled(14, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(15),
	}

	historyIterator := &historyIteratorImpl{
		nextPageToken: []byte("token"),
		iteratorFunc: func(nextToken []byte) (*historypb.History, []byte, error) {
			return &historypb.History{Events: nextEvents}, nil, nil
		},
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task, historyIterator: historyIterator}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_Message_Mixed_Types() {
	// Test a workflow history with a mix of different sources of updates messages.
	taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowExecutionUpdateAdmitted(2, &historypb.WorkflowExecutionUpdateAdmittedEventAttributes{
			Request: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "admittedUpdate1",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventWorkflowExecutionUpdateAdmitted(3, &historypb.WorkflowExecutionUpdateAdmittedEventAttributes{
			Request: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "admittedUpdate2",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventWorkflowTaskScheduled(4, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(5),
		createTestEventWorkflowTaskCompleted(6, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 4,
			StartedEventId:   5,
			SdkMetadata: &sdk.WorkflowTaskCompletedMetadata{
				LangUsedFlags: []uint32{
					3,
				},
			},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(7, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 2,
			AcceptedRequestMessageId:         "admittedUpdate1/request",
			ProtocolInstanceId:               "admittedUpdate1",
		}),
		createTestEventActivityTaskScheduled(8, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "8",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(9, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 4,
			ProtocolInstanceId:               "protocolUpdate1",
			AcceptedRequestMessageId:         "protocolUpdate1/request",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "protocolUpdate1",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventActivityTaskScheduled(10, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "10",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(11, &historypb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(12, &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 8}),
		createTestEventActivityTaskStarted(13, &historypb.ActivityTaskStartedEventAttributes{}),
		createTestEventActivityTaskCompleted(14, &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 10}),
		createTestEventWorkflowExecutionUpdateAdmitted(15, &historypb.WorkflowExecutionUpdateAdmittedEventAttributes{
			Request: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "admittedUpdate3",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
	}

	task := createWorkflowTask(testEvents, 5, "HelloUpdate_Workflow")
	task.Messages = []*protocolpb.Message{
		{
			Id:                 "protocolUpdate2/request",
			ProtocolInstanceId: "protocolUpdate2",
			SequencingId: &protocolpb.Message_EventId{
				EventId: 16,
			},
			Body: protocol.MustMarshalAny(&updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "protocolUpdate2",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			}),
		},
	}

	params := t.getTestWorkerExecutionParams()
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
	t.Len(response.Commands, 6)
	t.Equal("admittedUpdate3/accept", response.Commands[0].GetProtocolMessageCommandAttributes().GetMessageId())
	t.NotNil(response.Commands[1].GetScheduleActivityTaskCommandAttributes())
	t.Equal("protocolUpdate2/accept", response.Commands[2].GetProtocolMessageCommandAttributes().GetMessageId())
	t.NotNil(response.Commands[3].GetScheduleActivityTaskCommandAttributes())
	t.Equal("admittedUpdate1/complete", response.Commands[4].GetProtocolMessageCommandAttributes().GetMessageId())
	t.Equal("protocolUpdate1/complete", response.Commands[5].GetProtocolMessageCommandAttributes().GetMessageId())

	t.Len(response.Messages, 4)
	t.Equal("admittedUpdate3", response.Messages[0].ProtocolInstanceId)
	t.Equal("protocolUpdate2", response.Messages[1].ProtocolInstanceId)
	t.Equal("admittedUpdate1", response.Messages[2].ProtocolInstanceId)
	t.Equal("protocolUpdate1", response.Messages[3].ProtocolInstanceId)
}

func (t *TaskHandlersTestSuite) TestWorkflowTask_Message_Admitted_Paged() {
	taskQueue := "taskQueue"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}),
		createTestEventWorkflowExecutionUpdateAdmitted(2, &historypb.WorkflowExecutionUpdateAdmittedEventAttributes{
			Request: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventWorkflowTaskScheduled(3, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(4),
		createTestEventWorkflowTaskCompleted(5, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 3,
			StartedEventId:   4,
			SdkMetadata: &sdk.WorkflowTaskCompletedMetadata{
				LangUsedFlags: []uint32{
					3,
				},
			},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(6, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 2,
			ProtocolInstanceId:               "test",
			AcceptedRequestMessageId:         "test/request",
		}),
		createTestEventActivityTaskScheduled(7, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "7",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
	}
	// createWorkflowTask add a schedule and start event
	task := createWorkflowTask(testEvents, 0, "HelloUpdate_Workflow")
	task.NextPageToken = []byte("token")
	task.PreviousStartedEventId = 15

	params := t.getTestWorkerExecutionParams()

	nextEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowTaskCompleted(10, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 8,
			StartedEventId:   9,
		}),
		createTestEventWorkflowExecutionUpdateAccepted(11, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 8,
			ProtocolInstanceId:               "test_2",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test_2",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventActivityTaskScheduled(12, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "12",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowExecutionUpdateAccepted(13, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 8,
			ProtocolInstanceId:               "test_3",
			AcceptedRequest: &updatepb.Request{
				Meta: &updatepb.Meta{
					UpdateId: "test_3",
				},
				Input: &updatepb.Input{
					Name: updateType,
				},
			},
		}),
		createTestEventActivityTaskScheduled(14, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "14",
			ActivityType: &commonpb.ActivityType{Name: "Greeter_Activity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowTaskScheduled(15, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(16),
	}

	historyIterator := &historyIteratorImpl{
		nextPageToken: []byte("token"),
		iteratorFunc: func(nextToken []byte) (*historypb.History, []byte, error) {
			return &historypb.History{Events: nextEvents}, nil, nil
		},
	}
	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	wftask := workflowTask{task: task, historyIterator: historyIterator}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	request, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	response := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	t.NoError(err)
	t.NotNil(response)
}

func (t *TaskHandlersTestSuite) TestLocalActivityRetry_Workflow() {
	backoffInterval := 10 * time.Millisecond
	workflowComplete := false
	laFailures := 0

	retryLocalActivityWorkflowFunc := func(ctx Context, input []byte) error {
		ao := LocalActivityOptions{
			ScheduleToCloseTimeout: time.Minute,
			RetryPolicy: &RetryPolicy{
				InitialInterval:    backoffInterval,
				BackoffCoefficient: 1.1,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    5,
			},
		}
		ctx = WithLocalActivityOptions(ctx, ao)

		err := ExecuteLocalActivity(ctx, func() error {
			if laFailures > 2 {
				return nil
			}
			laFailures++
			return errors.New("fail number " + strconv.Itoa(laFailures))
		}).Get(ctx, nil)
		workflowComplete = true
		return err
	}
	t.registry.RegisterWorkflowWithOptions(
		retryLocalActivityWorkflowFunc,
		RegisterWorkflowOptions{Name: "RetryLocalActivityWorkflow"},
	)

	workflowTaskStartedEvent := createTestEventWorkflowTaskStarted(3)
	now := time.Now()
	onesec := 5 * time.Second
	workflowTaskStartedEvent.EventTime = timestamppb.New(now)
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowTaskTimeout: durationpb.New(onesec),
			TaskQueue:           &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
		},
		),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		workflowTaskStartedEvent,
	}

	task := createWorkflowTask(testEvents, 0, "RetryLocalActivityWorkflow")
	stopCh := make(chan struct{})
	params := t.getTestWorkerExecutionParams()
	params.WorkerStopChannel = stopCh
	defer close(stopCh)

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	laTunnel := newLocalActivityTunnel(params.WorkerStopChannel)
	taskHandlerImpl, ok := taskHandler.(*workflowTaskHandlerImpl)
	t.True(ok)
	taskHandlerImpl.laTunnel = laTunnel

	laTaskPoller := newLocalActivityPoller(params, laTunnel, nil)
	go func() {
		for {
			task, _ := laTaskPoller.PollTask()
			_ = laTaskPoller.ProcessTask(task)
			// Quit after we've polled enough times
			if laFailures == 4 {
				return
			}
		}
	}()

	laResultCh := make(chan *localActivityResult)
	laRetryCh := make(chan *localActivityTask)
	wftask := workflowTask{task: task, laResultCh: laResultCh, laRetryCh: laRetryCh}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	response, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	t.NotNil(response)
	t.NoError(err)
	asWFTComplete := response.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	// There should be no non-first LA attempts since all the retries happen in one WFT
	t.Equal(uint32(0), asWFTComplete.MeteringMetadata.NonfirstLocalActivityExecutionAttempts)
	// wait long enough for wf to complete
	time.Sleep(backoffInterval * 3)
	t.True(workflowComplete)
}

func (t *TaskHandlersTestSuite) TestLocalActivityRetry_WorkflowTaskHeartbeatFail() {
	backoffInterval := 50 * time.Millisecond
	workflowComplete := false

	retryLocalActivityWorkflowFunc := func(ctx Context, input []byte) error {
		ao := LocalActivityOptions{
			ScheduleToCloseTimeout: time.Minute,
			RetryPolicy: &RetryPolicy{
				InitialInterval:    backoffInterval,
				BackoffCoefficient: 1.1,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    0,
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
		RegisterWorkflowOptions{Name: "RetryLocalActivityWorkflowHBFail"},
	)

	workflowTaskStartedEvent := createTestEventWorkflowTaskStarted(3)
	now := time.Now()
	workflowTaskStartedEvent.EventTime = timestamppb.New(now)
	// WFT timeout must be larger than the local activity backoff or the local activity is not retried
	wftTimeout := 500 * time.Millisecond
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			// make sure the timeout is same as the backoff interval
			WorkflowTaskTimeout: durationpb.New(wftTimeout),
			TaskQueue:           &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
		},
		),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		workflowTaskStartedEvent,
	}

	task := createWorkflowTask(testEvents, 0, "RetryLocalActivityWorkflowHBFail")
	stopCh := make(chan struct{})
	params := t.getTestWorkerExecutionParams()
	params.WorkerStopChannel = stopCh
	defer close(stopCh)

	taskHandler := newWorkflowTaskHandler(params, nil, t.registry)
	laTunnel := newLocalActivityTunnel(params.WorkerStopChannel)
	taskHandlerImpl, ok := taskHandler.(*workflowTaskHandlerImpl)
	t.True(ok)
	taskHandlerImpl.laTunnel = laTunnel

	laTaskPoller := newLocalActivityPoller(params, laTunnel, nil)
	doneCh := make(chan struct{})
	go func() {
		// laTaskPoller needs to poll the local activity and process it
		task, err := laTaskPoller.PollTask()
		t.NoError(err)
		err = laTaskPoller.ProcessTask(task)
		t.NoError(err)

		close(doneCh)
	}()

	laResultCh := make(chan *localActivityResult)
	wftask := workflowTask{task: task, laResultCh: laResultCh}
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	response, err := taskHandler.ProcessWorkflowTask(
		&wftask,
		wfctx,
		func(response interface{}, startTime time.Time) (*workflowTask, error) {
			return nil, serviceerror.NewNotFound("Intentional wft heartbeat error")
		})
	wfctx.Unlock(err)
	t.Nil(response)
	t.Error(err)

	// wait for the retry timer to fire
	time.Sleep(backoffInterval)
	t.False(workflowComplete)
	<-doneCh
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NoError() {
	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)
	invocationChannel := make(chan int, 2)
	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: false}
	mockService.EXPECT().
		RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(_ interface{}, _ interface{}, _ ...interface{}) { invocationChannel <- 1 }).
		Return(&heartbeatResponse, nil).
		Times(2)

	temporalInvoker := &temporalInvoker{
		identity:                  "Test_Temporal_Invoker",
		service:                   mockService,
		taskToken:                 nil,
		heartbeatThrottleInterval: time.Second,
	}

	heartbeatErr := temporalInvoker.Heartbeat(context.Background(), nil, false)
	t.NoError(heartbeatErr)

	select {
	case <-invocationChannel:
	case <-time.After(3 * time.Second):
		t.Fail("did not get expected 1st call to record heartbeat")
	}

	heartbeatErr = temporalInvoker.Heartbeat(context.Background(), nil, false)
	t.NoError(heartbeatErr)

	select {
	case <-invocationChannel:
		t.Fail("got unexpected call to record heartbeat. 2nd call should come via batch timer")
	default:
	}

	select {
	case <-invocationChannel:
	case <-time.After(3 * time.Second):
		t.Fail("did not get expected 2nd call to record heartbeat via batch timer")
	}
}

func (t *TaskHandlersTestSuite) TestHeartBeat_NilResponseWithError() {
	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewNotFound(""))

	temporalInvoker := newServiceInvoker(
		nil, "Test_Temporal_Invoker", mockService, metrics.NopHandler, func() {}, 0,
		make(chan struct{}), t.namespace)

	ctx, err := newActivityContext(context.Background(), nil, &activityEnvironment{serviceInvoker: temporalInvoker, logger: t.logger})
	t.NoError(err)

	heartbeatErr := temporalInvoker.Heartbeat(ctx, nil, false)
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
		nil, "Test_Temporal_Invoker", mockService, metrics.NopHandler, cancelHandler,
		0, make(chan struct{}), t.namespace)

	ctx, err := newActivityContext(context.Background(), nil, &activityEnvironment{serviceInvoker: temporalInvoker, logger: t.logger})
	t.NoError(err)

	heartbeatErr := temporalInvoker.Heartbeat(ctx, nil, false)
	t.NotNil(heartbeatErr)
	t.IsType(&serviceerror.NamespaceNotActive{}, heartbeatErr, "heartbeatErr must be of type NamespaceNotActive.")
	t.True(called)
}

type testActivityDeadline struct {
	logger log.Logger
	d      time.Duration
}

func (t *testActivityDeadline) Execute(ctx context.Context, _ *commonpb.Payloads) (*commonpb.Payloads, error) {
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
	ScheduleDuration time.Duration
	StartTS          time.Time
	StartDuration    time.Duration
	ActivityType     string
	err              error
}

func (t *TaskHandlersTestSuite) TestActivityExecutionDeadline() {
	deadlineTests := []deadlineTest{
		{0, time.Now(), 3 * time.Second, time.Now(), 3 * time.Second, "test", nil},
		{0, time.Now(), 4 * time.Second, time.Now(), 3 * time.Second, "test", nil},
		{0, time.Now(), 3 * time.Second, time.Now(), 4 * time.Second, "test", nil},
		{0, time.Now(), 3 * time.Second, time.Now(), 4 * time.Second, "unknown", nil},
		{0, time.Now().Add(-1 * time.Second), 1 * time.Second, time.Now(), 1 * time.Second, "test", context.DeadlineExceeded},
		{0, time.Now(), 1 * time.Second, time.Now().Add(-1 * time.Second), 1 * time.Second, "test", context.DeadlineExceeded},
		{0, time.Now().Add(-1 * time.Second), 1, time.Now().Add(-1 * time.Second), 1 * time.Second, "test", context.DeadlineExceeded},
		{1 * time.Second, time.Now(), 1 * time.Second, time.Now(), 1 * time.Second, "test", context.DeadlineExceeded},
		{1 * time.Second, time.Now(), 2 * time.Second, time.Now(), 1 * time.Second, "test", context.DeadlineExceeded},
		{1 * time.Second, time.Now(), 1 * time.Second, time.Now(), 2 * time.Second, "test", context.DeadlineExceeded},
	}
	a := &testActivityDeadline{logger: t.logger}
	registry := t.registry
	registry.addActivityWithLock(a.ActivityType().Name, a)

	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	for i, d := range deadlineTests {
		a.d = d.actWaitDuration
		wep := t.getTestWorkerExecutionParams()
		activityHandler := newActivityTaskHandler(mockService, wep, registry)
		pats := &workflowservice.PollActivityTaskQueueResponse{
			Attempt:   1,
			TaskToken: []byte("token"),
			WorkflowExecution: &commonpb.WorkflowExecution{
				WorkflowId: "wID",
				RunId:      "rID",
			},
			ActivityType:           &commonpb.ActivityType{Name: d.ActivityType},
			ActivityId:             uuid.New(),
			ScheduledTime:          timestamppb.New(d.ScheduleTS),
			ScheduleToCloseTimeout: durationpb.New(d.ScheduleDuration),
			StartedTime:            timestamppb.New(d.StartTS),
			StartToCloseTimeout:    durationpb.New(d.StartDuration),
			WorkflowType: &commonpb.WorkflowType{
				Name: "wType",
			},
			WorkflowNamespace: "namespace",
		}
		td := fmt.Sprintf("testIndex: %v, testDetails: %v", i, d)
		r, err := activityHandler.Execute(taskqueue, pats)
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
	registry.RegisterActivityWithOptions(
		activityWithWorkerStop,
		RegisterActivityOptions{Name: a.ActivityType().Name, DisableAlreadyRegisteredCheck: true},
	)

	mockCtrl := gomock.NewController(t.T())
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)
	workerStopCh := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	wep := t.getTestWorkerExecutionParams()
	wep.UserContext = ctx
	wep.UserContextCancel = cancel
	wep.WorkerStopChannel = workerStopCh
	activityHandler := newActivityTaskHandler(mockService, wep, registry)
	now := time.Now()
	pats := &workflowservice.PollActivityTaskQueueResponse{
		Attempt:   1,
		TaskToken: []byte("token"),
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "wID",
			RunId:      "rID",
		},
		ActivityType:           &commonpb.ActivityType{Name: "test"},
		ActivityId:             uuid.New(),
		ScheduledTime:          timestamppb.New(now),
		ScheduleToCloseTimeout: durationpb.New(1 * time.Second),
		StartedTime:            timestamppb.New(now),
		StartToCloseTimeout:    durationpb.New(1 * time.Second),
		WorkflowType: &commonpb.WorkflowType{
			Name: "wType",
		},
		WorkflowNamespace: "namespace",
	}
	close(workerStopCh)
	r, err := activityHandler.Execute(taskqueue, pats)
	t.NoError(err)
	t.NotNil(r)
}

func Test_NonDeterministicCheck(t *testing.T) {
	unimplementedCommands := []int32{
		int32(enumspb.COMMAND_TYPE_UNSPECIFIED),
	}
	commandTypes := enumspb.CommandType_name
	for _, cmd := range unimplementedCommands {
		delete(commandTypes, cmd)
	}

	require.Equal(t, 15, len(commandTypes), "If you see this error, you are adding new command type. "+
		"Before updating the number to make this test pass, please make sure you update isCommandMatchEvent() method "+
		"to check the new command type. Otherwise the replay will fail on the new command event.")

	eventTypes := enumspb.EventType_value
	commandEventTypeCount := 0
	for _, et := range eventTypes {
		if isCommandEvent(enumspb.EventType(et)) {
			commandEventTypeCount++
		}
	}
	// why doesn't the commandEventTypeCount equal the len(commandTypes)? There
	// was a time when every event type was created by exactly one command type
	// however that is no longer the case as ProtocolMessageCommands can create
	// multiple different event types. Currently this value is two greater than
	// the command type count because the 1 ProtocolMessageCommand type can
	// result in 3 different event types being created. As more protocols are
	// added, this number will increase.
	require.Equal(t, 17, commandEventTypeCount, "Every command type must have at least one matching event type. "+
		"If you add new command type, you need to update isCommandEvent() method to include that new event type as well.")
}

func Test_IsCommandMatchEvent_UpsertWorkflowSearchAttributes(t *testing.T) {
	diType := enumspb.COMMAND_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES
	eType := enumspb.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES

	testCases := []struct {
		name     string
		command  *commandpb.Command
		event    *historypb.HistoryEvent
		msgs     []outboxEntry
		expected bool
	}{
		{
			name: "event type not match",
			command: &commandpb.Command{
				CommandType: diType,
				Attributes: &commandpb.Command_UpsertWorkflowSearchAttributesCommandAttributes{UpsertWorkflowSearchAttributesCommandAttributes: &commandpb.UpsertWorkflowSearchAttributesCommandAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			event:    &historypb.HistoryEvent{},
			expected: false,
		},
		{
			name: "attributes not match",
			command: &commandpb.Command{
				CommandType: diType,
				Attributes: &commandpb.Command_UpsertWorkflowSearchAttributesCommandAttributes{UpsertWorkflowSearchAttributesCommandAttributes: &commandpb.UpsertWorkflowSearchAttributesCommandAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			event: &historypb.HistoryEvent{
				EventType:  eType,
				Attributes: &historypb.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &historypb.UpsertWorkflowSearchAttributesEventAttributes{}},
			},
			expected: true,
		},
		{
			name: "attributes match",
			command: &commandpb.Command{
				CommandType: diType,
				Attributes: &commandpb.Command_UpsertWorkflowSearchAttributesCommandAttributes{UpsertWorkflowSearchAttributesCommandAttributes: &commandpb.UpsertWorkflowSearchAttributesCommandAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			event: &historypb.HistoryEvent{
				EventType: eType,
				Attributes: &historypb.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes{UpsertWorkflowSearchAttributesEventAttributes: &historypb.UpsertWorkflowSearchAttributesEventAttributes{
					SearchAttributes: &commonpb.SearchAttributes{},
				}},
			},
			expected: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, isCommandMatchEvent(testCase.command, testCase.event, testCase.msgs))
		})
	}
}

func Test_ProtocolCommandEventMatching(t *testing.T) {
	msgID := t.Name() + "-msg-id"
	cmd := &commandpb.Command{
		CommandType: enumspb.COMMAND_TYPE_PROTOCOL_MESSAGE,
		Attributes: &commandpb.Command_ProtocolMessageCommandAttributes{
			ProtocolMessageCommandAttributes: &commandpb.ProtocolMessageCommandAttributes{
				MessageId: msgID,
			},
		},
	}
	for _, tc := range [...]struct {
		name  string
		event *historypb.HistoryEvent
		msgs  []outboxEntry
		want  bool
	}{
		{
			name:  "no matching message ID",
			event: nil,
			msgs: []outboxEntry{
				{msg: &protocolpb.Message{Id: "not the same msg ID"}},
				{msg: &protocolpb.Message{Id: "also not the same msg ID"}},
			},
			want: false,
		},
		{
			name:  "predicate rejects event",
			event: &historypb.HistoryEvent{},
			msgs: []outboxEntry{
				{
					msg:            &protocolpb.Message{Id: msgID},
					eventPredicate: func(*historypb.HistoryEvent) bool { return false },
				},
			},
			want: false,
		},
		{
			name:  "predicate accepts event",
			event: &historypb.HistoryEvent{},
			msgs: []outboxEntry{
				{
					msg:            &protocolpb.Message{Id: msgID},
					eventPredicate: func(*historypb.HistoryEvent) bool { return true },
				},
			},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isCommandMatchEvent(cmd, tc.event, tc.msgs))
		})
	}
}

func Test_IsSearchAttributesMatched(t *testing.T) {
	encodeString := func(str string) *commonpb.Payload {
		payload, _ := converter.GetDefaultDataConverter().ToPayload(str)
		return payload
	}

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
				IndexedFields: map[string]*commonpb.Payload{
					"key1": encodeString("1"),
					"key2": encodeString("abc"),
				},
			},
			rhs:      &commonpb.SearchAttributes{},
			expected: false,
		},
		{
			name: "match",
			lhs: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"key1": encodeString("1"),
					"key2": encodeString("abc"),
				},
			},
			rhs: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"key2": encodeString("abc"),
					"key1": encodeString("1"),
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

func Test_IsCommandMatchEvent_ModifyWorkflowProperties(t *testing.T) {
	diType := enumspb.COMMAND_TYPE_MODIFY_WORKFLOW_PROPERTIES
	eType := enumspb.EVENT_TYPE_WORKFLOW_PROPERTIES_MODIFIED

	testCases := []struct {
		name     string
		command  *commandpb.Command
		event    *historypb.HistoryEvent
		msgs     []outboxEntry
		expected bool
	}{
		{
			name: "event type not match",
			command: &commandpb.Command{
				CommandType: diType,
				Attributes: &commandpb.Command_ModifyWorkflowPropertiesCommandAttributes{
					ModifyWorkflowPropertiesCommandAttributes: &commandpb.ModifyWorkflowPropertiesCommandAttributes{
						UpsertedMemo: &commonpb.Memo{},
					},
				},
			},
			event:    &historypb.HistoryEvent{},
			expected: false,
		},
		{
			name: "attributes not match",
			command: &commandpb.Command{
				CommandType: diType,
				Attributes: &commandpb.Command_ModifyWorkflowPropertiesCommandAttributes{
					ModifyWorkflowPropertiesCommandAttributes: &commandpb.ModifyWorkflowPropertiesCommandAttributes{
						UpsertedMemo: &commonpb.Memo{},
					},
				},
			},
			event: &historypb.HistoryEvent{
				EventType: eType,
				Attributes: &historypb.HistoryEvent_WorkflowPropertiesModifiedEventAttributes{
					WorkflowPropertiesModifiedEventAttributes: &historypb.WorkflowPropertiesModifiedEventAttributes{},
				},
			},
			expected: true,
		},
		{
			name: "attributes match",
			command: &commandpb.Command{
				CommandType: diType,
				Attributes: &commandpb.Command_ModifyWorkflowPropertiesCommandAttributes{
					ModifyWorkflowPropertiesCommandAttributes: &commandpb.ModifyWorkflowPropertiesCommandAttributes{
						UpsertedMemo: &commonpb.Memo{},
					},
				},
			},
			event: &historypb.HistoryEvent{
				EventType: eType,
				Attributes: &historypb.HistoryEvent_WorkflowPropertiesModifiedEventAttributes{
					WorkflowPropertiesModifiedEventAttributes: &historypb.WorkflowPropertiesModifiedEventAttributes{
						UpsertedMemo: &commonpb.Memo{},
					},
				},
			},
			expected: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				require.Equal(
					t,
					testCase.expected,
					isCommandMatchEvent(testCase.command, testCase.event, testCase.msgs),
				)
			},
		)
	}
}

func Test_IsMemoMatched(t *testing.T) {
	encodeString := func(str string) *commonpb.Payload {
		payload, _ := converter.GetDefaultDataConverter().ToPayload(str)
		return payload
	}

	testCases := []struct {
		name     string
		lhs      *commonpb.Memo
		rhs      *commonpb.Memo
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
			rhs:      &commonpb.Memo{},
			expected: false,
		},
		{
			name:     "right nil",
			lhs:      &commonpb.Memo{},
			rhs:      nil,
			expected: false,
		},
		{
			name: "not match",
			lhs: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString("1"),
					"key2": encodeString("abc"),
				},
			},
			rhs:      &commonpb.Memo{},
			expected: false,
		},
		{
			name: "match",
			lhs: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString("1"),
					"key2": encodeString("abc"),
				},
			},
			rhs: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{
					"key2": encodeString("abc"),
					"key1": encodeString("1"),
				},
			},
			expected: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				require.Equal(t, testCase.expected, isMemoMatched(testCase.lhs, testCase.rhs))
			},
		)
	}
}

func TestHeartbeatThrottleInterval(t *testing.T) {
	assertInterval := func(timeoutSec, defaultIntervalSec, maxIntervalSec, expectedSec int) {
		a := &activityTaskHandlerImpl{
			defaultHeartbeatThrottleInterval: time.Duration(defaultIntervalSec) * time.Second,
			maxHeartbeatThrottleInterval:     time.Duration(maxIntervalSec) * time.Second,
		}
		require.Equal(t, time.Duration(expectedSec)*time.Second,
			a.getHeartbeatThrottleInterval(time.Duration(timeoutSec)*time.Second))
	}

	// Use 80% of timeout
	assertInterval(5, 2, 10, 4)
	// Use default if no timeout
	assertInterval(0, 2, 10, 2)
	// Use default of 30s if no timeout or default
	assertInterval(0, 0, 50, 30)
	// Use max if 80% of timeout is too large
	assertInterval(14, 2, 10, 10)
	// Default max to 60 if not set
	assertInterval(5000, 2, 0, 60)
}

type MockHistoryIterator struct {
	HistoryIterator
	GetNextPageImpl func() (*historypb.History, error)
	ResetImpl       func()
	HasNextPageImpl func() bool
}

func (mhi MockHistoryIterator) GetNextPage() (*historypb.History, error) {
	return mhi.GetNextPageImpl()
}

func (mhi MockHistoryIterator) Reset() {
	mhi.ResetImpl()
}

func (mhi MockHistoryIterator) HasNextPage() bool {
	return mhi.HasNextPageImpl()
}

func TestResetIfDestroyedTaskPrep(t *testing.T) {
	historyAcceptedMsgID := t.Name() + "-historyAcceptedMsgID"
	// a plausible full history that includes an update accepted event to also
	// test for lookahead event inference
	fullHist := &historypb.History{
		Events: []*historypb.HistoryEvent{
			createTestEventWorkflowExecutionStarted(1,
				&historypb.WorkflowExecutionStartedEventAttributes{
					TaskQueue: &taskqueuepb.TaskQueue{Name: t.Name() + "-queue"},
				}),
			createTestEventWorkflowTaskScheduled(2, nil),
			createTestEventWorkflowTaskStarted(3),
			createTestEventWorkflowTaskCompleted(4, nil),
			createTestEventWorkflowExecutionUpdateAccepted(5, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
				ProtocolInstanceId:       "123",
				AcceptedRequestMessageId: historyAcceptedMsgID,
				AcceptedRequest:          &updatepb.Request{},
			}),
		},
	}

	// start the task out with a partial history to verify that we reset the
	// history iterator back to the start
	taskHist := &historypb.History{
		Events: []*historypb.HistoryEvent{
			createTestEventWorkflowTaskScheduled(6, nil),
			createTestEventWorkflowTaskStarted(7),
		},
	}

	// iterator implementation uses partial history until HistoryIterator.Reset
	// is called
	iterHist := taskHist

	histIter := MockHistoryIterator{
		ResetImpl: func() {
			// if impl calls reset, switch to full history
			iterHist = fullHist
		},

		GetNextPageImpl: func() (*historypb.History, error) {
			return iterHist, nil
		},
	}
	cache := cache.NewLRU(1)
	// values of these fields are not important to the test but some of these
	// pointers are dereferenced as part of constructing a new event handler so
	// they need to be non-nil
	weci := &workflowExecutionContextImpl{
		workflowInfo: &WorkflowInfo{
			WorkflowExecution: WorkflowExecution{},
			WorkflowType:      WorkflowType{Name: t.Name()},
		},
		wth: &workflowTaskHandlerImpl{
			metricsHandler: metrics.NopHandler,
			logger:         ilog.NewNopLogger(),
			cache: &WorkerCache{
				sharedCache: &sharedWorkerCache{workflowCache: &cache},
			},
		},
	}

	// assertion helper for use below
	requireContainsMsgWithID := func(t *testing.T, msgs []*protocolpb.Message, id string) {
		t.Helper()
		for _, msg := range msgs {
			if msg.GetId() == id {
				return
			}
		}
		require.FailNow(t, "expected message not found",
			"message with id %q not found in %v", id, msgs)
	}

	wftNewMsgID := t.Name() + "-wftNewMsgID"
	t.Run("cache miss", func(t *testing.T) {
		task := &workflowservice.PollWorkflowTaskQueueResponse{
			History:  taskHist,
			Messages: []*protocolpb.Message{{Id: wftNewMsgID}},
		}

		require.EqualValues(t, 0, cache.Size())
		// cache is empty so this should miss and build a new context with a
		// full history
		_, err := weci.wth.GetOrCreateWorkflowContext(task, histIter)

		require.NoError(t, err)
		require.Len(t, task.History.Events, len(fullHist.Events),
			"expected task to be mutated to carry full WF history (all events)")
		requireContainsMsgWithID(t, task.Messages, wftNewMsgID)
	})
	t.Run("cache hit but destroyed", func(t *testing.T) {
		task := &workflowservice.PollWorkflowTaskQueueResponse{
			History:  taskHist,
			Messages: []*protocolpb.Message{{Id: wftNewMsgID}},
		}

		// trick the execution context into thinking it has been destroyed
		weci.eventHandler = nil
		err := weci.resetStateIfDestroyed(task, histIter)

		require.NoError(t, err)
		require.Len(t, task.History.Events, len(fullHist.Events),
			"expected task to be mutated to carry full WF history (all events)")
		requireContainsMsgWithID(t, task.Messages, wftNewMsgID)
	})
}
