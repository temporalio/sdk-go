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
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common"
	ilog "go.temporal.io/sdk/internal/log"
)

// ActivityTaskHandler never returns response
type noResponseActivityTaskHandler struct {
	isExecuteCalled chan struct{}
}

func newNoResponseActivityTaskHandler() *noResponseActivityTaskHandler {
	return &noResponseActivityTaskHandler{isExecuteCalled: make(chan struct{})}
}

func (ath noResponseActivityTaskHandler) Execute(string, *workflowservice.PollActivityTaskQueueResponse) (interface{}, error) {
	close(ath.isExecuteCalled)
	c := make(chan struct{})
	<-c
	return nil, nil
}

func (ath noResponseActivityTaskHandler) BlockedOnExecuteCalled() error {
	<-ath.isExecuteCalled
	return nil
}

type (
	WorkersTestSuite struct {
		suite.Suite
		mockCtrl      *gomock.Controller
		service       *workflowservicemock.MockWorkflowServiceClient
		dataConverter converter.DataConverter
	}
)

// Test suite.
func (s *WorkersTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	s.dataConverter = converter.GetDefaultDataConverter()
}

func (s *WorkersTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func TestWorkersTestSuite(t *testing.T) {
	suite.Run(t, new(WorkersTestSuite))
}

func (s *WorkersTestSuite) TestWorkflowWorker() {
	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollWorkflowTaskQueueResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	ctx, cancel := context.WithCancel(context.Background())
	executionParameters := workerExecutionParameters{
		Namespace:                             DefaultNamespace,
		TaskQueue:                             "testTaskQueue",
		MaxConcurrentWorkflowTaskQueuePollers: 5,
		Logger:                                ilog.NewDefaultLogger(),
		UserContext:                           ctx,
		UserContextCancel:                     cancel,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler()}
	workflowWorker := newWorkflowWorkerInternal(s.service, executionParameters, nil, overrides, newRegistry())
	_ = workflowWorker.Start()
	workflowWorker.Stop()

	s.NoError(ctx.Err())
}

func (s *WorkersTestSuite) TestActivityWorker() {
	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollActivityTaskQueueResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()

	executionParameters := workerExecutionParameters{
		Namespace:                             DefaultNamespace,
		TaskQueue:                             "testTaskQueue",
		MaxConcurrentActivityTaskQueuePollers: 5,
		Logger:                                ilog.NewDefaultLogger(),
	}
	overrides := &workerOverrides{activityTaskHandler: newSampleActivityTaskHandler()}
	a := &greeterActivity{}
	registry := newRegistry()
	registry.addActivityWithLock(a.ActivityType().Name, a)
	activityWorker := newActivityWorker(s.service, executionParameters, overrides, registry, nil)
	_ = activityWorker.Start()
	activityWorker.Stop()
}

func (s *WorkersTestSuite) TestActivityWorkerStop() {
	now := time.Now()

	pats := &workflowservice.PollActivityTaskQueueResponse{
		Attempt:   1,
		TaskToken: []byte("token"),
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "wID",
			RunId:      "rID"},
		ActivityType:           &commonpb.ActivityType{Name: "test"},
		ActivityId:             uuid.New(),
		ScheduledTime:          &now,
		ScheduleToCloseTimeout: common.DurationPtr(1 * time.Second),
		StartedTime:            &now,
		StartToCloseTimeout:    common.DurationPtr(1 * time.Second),
		WorkflowType: &commonpb.WorkflowType{
			Name: "wType",
		},
		WorkflowNamespace: "namespace",
	}

	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(pats, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()

	stopC := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	executionParameters := workerExecutionParameters{
		Namespace:                             DefaultNamespace,
		TaskQueue:                             "testTaskQueue",
		MaxConcurrentActivityTaskQueuePollers: 5,
		ConcurrentActivityExecutionSize:       2,
		Logger:                                ilog.NewDefaultLogger(),
		UserContext:                           ctx,
		UserContextCancel:                     cancel,
		WorkerStopTimeout:                     time.Second * 2,
		WorkerStopChannel:                     stopC,
	}
	activityTaskHandler := newNoResponseActivityTaskHandler()
	overrides := &workerOverrides{activityTaskHandler: activityTaskHandler}
	a := &greeterActivity{}
	registry := newRegistry()
	registry.addActivityWithLock(a.ActivityType().Name, a)
	worker := newActivityWorker(s.service, executionParameters, overrides, registry, nil)
	_ = worker.Start()
	_ = activityTaskHandler.BlockedOnExecuteCalled()
	go worker.Stop()

	<-worker.worker.stopCh
	err := ctx.Err()
	s.NoError(err)

	<-ctx.Done()
	err = ctx.Err()
	s.Error(err)
}

func (s *WorkersTestSuite) TestPollWorkflowTaskQueue_InternalServiceError() {
	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollWorkflowTaskQueueResponse{}, serviceerror.NewInternal("")).AnyTimes()

	executionParameters := workerExecutionParameters{
		Namespace:                             DefaultNamespace,
		TaskQueue:                             "testWorkflowTaskQueue",
		MaxConcurrentWorkflowTaskQueuePollers: 5,
		Logger:                                ilog.NewNopLogger(),
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler()}
	workflowWorker := newWorkflowWorkerInternal(s.service, executionParameters, nil, overrides, newRegistry())
	_ = workflowWorker.Start()
	workflowWorker.Stop()
}

func (s *WorkersTestSuite) TestLongRunningWorkflowTask() {
	localActivityCalledCount := 0
	localActivitySleep := func(duration time.Duration) error {
		time.Sleep(duration)
		localActivityCalledCount++
		return nil
	}

	doneCh := make(chan struct{})

	isWorkflowCompleted := false
	longWorkflowTaskWorkflowFn := func(ctx Context, input []byte) error {
		lao := LocalActivityOptions{
			ScheduleToCloseTimeout: time.Second * 2,
		}
		ctx = WithLocalActivityOptions(ctx, lao)
		err := ExecuteLocalActivity(ctx, localActivitySleep, time.Second).Get(ctx, nil)

		if err != nil {
			return err
		}

		err = ExecuteLocalActivity(ctx, localActivitySleep, time.Second).Get(ctx, nil)
		isWorkflowCompleted = true
		return err
	}

	taskQueue := "long-running-workflow-task-tq"
	testEvents := []*historypb.HistoryEvent{
		{
			EventId:   1,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
				TaskQueue:                &taskqueuepb.TaskQueue{Name: taskQueue},
				WorkflowExecutionTimeout: common.DurationPtr(10 * time.Second),
				WorkflowRunTimeout:       common.DurationPtr(10 * time.Second),
				WorkflowTaskTimeout:      common.DurationPtr(2 * time.Second),
				WorkflowType:             &commonpb.WorkflowType{Name: "long-running-workflow-task-workflow-type"},
			}},
		},
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   5,
			EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
			Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("0"),
				WorkflowTaskCompletedEventId: 4,
			}},
		},
		createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(7),
		createTestEventWorkflowTaskCompleted(8, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   9,
			EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
			Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("1"),
				WorkflowTaskCompletedEventId: 8,
			}},
		},
		createTestEventWorkflowTaskScheduled(10, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(11),
	}

	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	task := &workflowservice.PollWorkflowTaskQueueResponse{
		TaskToken: []byte("test-token"),
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "long-running-workflow-task-workflow-id",
			RunId:      "long-running-workflow-task-workflow-run-id",
		},
		WorkflowType: &commonpb.WorkflowType{
			Name: "long-running-workflow-task-workflow-type",
		},
		PreviousStartedEventId: 0,
		StartedEventId:         3,
		History:                &historypb.History{Events: testEvents[0:3]},
		NextPageToken:          nil,
	}
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(task, nil).Times(1)
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollWorkflowTaskQueueResponse{}, serviceerror.NewInternal("")).AnyTimes()
	s.service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollActivityTaskQueueResponse{}, nil).AnyTimes()

	respondCounter := 0
	s.service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *workflowservice.RespondWorkflowTaskCompletedRequest, opts ...grpc.CallOption,
	) (success *workflowservice.RespondWorkflowTaskCompletedResponse, err error) {
		respondCounter++
		switch respondCounter {
		case 1:
			s.Equal(1, len(request.Commands))
			s.Equal(enumspb.COMMAND_TYPE_RECORD_MARKER, request.Commands[0].GetCommandType())
			task.PreviousStartedEventId = 3
			task.StartedEventId = 7
			task.History.Events = testEvents[3:7]
			return &workflowservice.RespondWorkflowTaskCompletedResponse{WorkflowTask: task}, nil
		case 2:
			s.Equal(2, len(request.Commands))
			s.Equal(enumspb.COMMAND_TYPE_RECORD_MARKER, request.Commands[0].GetCommandType())
			s.Equal(enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION, request.Commands[1].GetCommandType())
			task.PreviousStartedEventId = 7
			task.StartedEventId = 11
			task.History.Events = testEvents[7:11]
			close(doneCh)
			return nil, nil
		default:
			panic("unexpected RespondWorkflowTaskCompleted")
		}
	}).Times(2)

	clientOptions := ClientOptions{
		Identity: "test-worker-identity",
	}

	client := NewServiceClient(s.service, nil, clientOptions)
	worker := NewAggregatedWorker(client, taskQueue, WorkerOptions{})
	worker.RegisterWorkflowWithOptions(
		longWorkflowTaskWorkflowFn,
		RegisterWorkflowOptions{Name: "long-running-workflow-task-workflow-type"},
	)
	worker.RegisterActivity(localActivitySleep)

	_ = worker.Start()
	// wait for test to complete
	select {
	case <-doneCh:
		break
	case <-time.After(time.Second * 4):
	}
	worker.Stop()

	s.True(isWorkflowCompleted)
	s.Equal(2, localActivityCalledCount)
}

func (s *WorkersTestSuite) TestMultipleLocalActivities() {
	localActivityCalledCount := 0
	localActivitySleep := func(duration time.Duration) error {
		time.Sleep(duration)
		localActivityCalledCount++
		return nil
	}

	doneCh := make(chan struct{})

	isWorkflowCompleted := false
	longWorkflowTaskWorkflowFn := func(ctx Context, input []byte) error {
		lao := LocalActivityOptions{
			ScheduleToCloseTimeout: time.Second * 2,
		}
		ctx = WithLocalActivityOptions(ctx, lao)
		err := ExecuteLocalActivity(ctx, localActivitySleep, time.Second).Get(ctx, nil)

		if err != nil {
			return err
		}

		err = ExecuteLocalActivity(ctx, localActivitySleep, time.Second).Get(ctx, nil)
		isWorkflowCompleted = true
		return err
	}

	taskQueue := "multiple-local-activities-tq"
	testEvents := []*historypb.HistoryEvent{
		{
			EventId:   1,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
				TaskQueue:                &taskqueuepb.TaskQueue{Name: taskQueue},
				WorkflowExecutionTimeout: common.DurationPtr(10 * time.Second),
				WorkflowRunTimeout:       common.DurationPtr(10 * time.Second),
				WorkflowTaskTimeout:      common.DurationPtr(3 * time.Second),
				WorkflowType:             &commonpb.WorkflowType{Name: "multiple-local-activities-workflow-type"},
			}},
		},
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   5,
			EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
			Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("0"),
				WorkflowTaskCompletedEventId: 4,
			}},
		},
		createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(7),
		createTestEventWorkflowTaskCompleted(8, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   9,
			EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
			Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("1"),
				WorkflowTaskCompletedEventId: 8,
			}},
		},
		createTestEventWorkflowTaskScheduled(10, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(11),
	}

	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	task := &workflowservice.PollWorkflowTaskQueueResponse{
		TaskToken: []byte("test-token"),
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "multiple-local-activities-workflow-id",
			RunId:      "multiple-local-activities-workflow-run-id",
		},
		WorkflowType: &commonpb.WorkflowType{
			Name: "multiple-local-activities-workflow-type",
		},
		PreviousStartedEventId: 0,
		StartedEventId:         3,
		History:                &historypb.History{Events: testEvents[0:3]},
		NextPageToken:          nil,
	}
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(task, nil).Times(1)
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollWorkflowTaskQueueResponse{}, serviceerror.NewInternal("")).AnyTimes()
	s.service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollActivityTaskQueueResponse{}, nil).AnyTimes()

	respondCounter := 0
	s.service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *workflowservice.RespondWorkflowTaskCompletedRequest, opts ...grpc.CallOption,
	) (success *workflowservice.RespondWorkflowTaskCompletedResponse, err error) {
		respondCounter++
		switch respondCounter {
		case 1:
			s.Equal(3, len(request.Commands))
			s.Equal(enumspb.COMMAND_TYPE_RECORD_MARKER, request.Commands[0].GetCommandType())
			task.PreviousStartedEventId = 3
			task.StartedEventId = 7
			task.History.Events = testEvents[3:11]
			close(doneCh)
			return nil, nil
		default:
			panic("unexpected RespondWorkflowTaskCompleted")
		}
	}).Times(1)

	clientOptions := ClientOptions{
		Identity: "test-worker-identity",
	}

	client := NewServiceClient(s.service, nil, clientOptions)
	worker := NewAggregatedWorker(client, taskQueue, WorkerOptions{})
	worker.RegisterWorkflowWithOptions(
		longWorkflowTaskWorkflowFn,
		RegisterWorkflowOptions{Name: "multiple-local-activities-workflow-type"},
	)
	worker.RegisterActivity(localActivitySleep)

	_ = worker.Start()
	// wait for test to complete
	select {
	case <-doneCh:
		break
	case <-time.After(time.Second * 5):
	}
	worker.Stop()

	s.True(isWorkflowCompleted)
	s.Equal(2, localActivityCalledCount)
}

func (s *WorkersTestSuite) createLocalActivityMarkerDataForTest(activityID string) map[string]*commonpb.Payloads {
	lamd := localActivityMarkerData{
		ActivityID: activityID,
		ReplayTime: time.Now(),
	}

	// encode marker data
	markerData, err := s.dataConverter.ToPayloads(lamd)

	s.NoError(err)
	return map[string]*commonpb.Payloads{
		localActivityMarkerDataName: markerData,
	}
}
