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
	"testing"
	"time"

	"github.com/gogo/status"
	"github.com/golang/mock/gomock"
	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	commonproto "go.temporal.io/temporal-proto/common"
	"go.temporal.io/temporal-proto/enums"
	"go.temporal.io/temporal-proto/workflowservice"
	"go.temporal.io/temporal-proto/workflowservicemock"
)

// ActivityTaskHandler never returns response
type noResponseActivityTaskHandler struct {
	isExecuteCalled chan struct{}
}

func newNoResponseActivityTaskHandler() *noResponseActivityTaskHandler {
	return &noResponseActivityTaskHandler{isExecuteCalled: make(chan struct{})}
}

func (ath noResponseActivityTaskHandler) Execute(string, *workflowservice.PollForActivityTaskResponse) (interface{}, error) {
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
		mockCtrl *gomock.Controller
		service  *workflowservicemock.MockWorkflowServiceClient
	}
)

// Test suite.
func (s *WorkersTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
}

func (s *WorkersTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func TestWorkersTestSuite(t *testing.T) {
	formatter := &log.TextFormatter{}
	formatter.FullTimestamp = true
	log.SetFormatter(formatter)
	log.SetLevel(log.DebugLevel)

	suite.Run(t, new(WorkersTestSuite))
}

func (s *WorkersTestSuite) TestWorkflowWorker() {
	domain := "testDomain"
	logger, _ := zap.NewDevelopment()

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollForDecisionTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	ctx, cancel := context.WithCancel(context.Background())
	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger:                    logger,
		UserContext:               ctx,
		UserContextCancel:         cancel,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler()}
	workflowWorker := newWorkflowWorkerInternal(
		s.service, domain, executionParameters, nil, overrides, getGlobalRegistry(),
	)
	_ = workflowWorker.Start()
	workflowWorker.Stop()

	s.Nil(ctx.Err())
}

func (s *WorkersTestSuite) TestActivityWorker() {
	domain := "testDomain"
	logger, _ := zap.NewDevelopment()

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollForActivityTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger:                    logger,
	}
	overrides := &workerOverrides{activityTaskHandler: newSampleActivityTaskHandler()}
	a := &greeterActivity{}
	registry := getGlobalRegistry()
	registry.addActivity(a.ActivityType().Name, a)
	activityWorker := newActivityWorker(
		s.service, domain, executionParameters, overrides, registry, nil,
	)
	_ = activityWorker.Start()
	activityWorker.Stop()
}

func (s *WorkersTestSuite) TestActivityWorkerStop() {
	domain := "testDomain"
	logger, _ := zap.NewDevelopment()

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

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(pats, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()

	stopC := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	executionParameters := workerExecutionParameters{
		TaskList:                        "testTaskList",
		ConcurrentPollRoutineSize:       5,
		ConcurrentActivityExecutionSize: 2,
		Logger:                          logger,
		UserContext:                     ctx,
		UserContextCancel:               cancel,
		WorkerStopTimeout:               time.Second * 2,
		WorkerStopChannel:               stopC,
	}
	activityTaskHandler := newNoResponseActivityTaskHandler()
	overrides := &workerOverrides{activityTaskHandler: activityTaskHandler}
	a := &greeterActivity{}
	registry := getGlobalRegistry()
	registry.addActivity(a.ActivityType().Name, a)
	worker := newActivityWorker(
		s.service, domain, executionParameters, overrides, registry, nil,
	)
	_ = worker.Start()
	_ = activityTaskHandler.BlockedOnExecuteCalled()
	go worker.Stop()

	<-worker.worker.shutdownCh
	err := ctx.Err()
	s.NoError(err)

	<-ctx.Done()
	err = ctx.Err()
	s.Error(err)
}

func (s *WorkersTestSuite) TestPollForDecisionTask_InternalServiceError() {
	domain := "testDomain"

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollForDecisionTaskResponse{}, status.New(codes.Internal, "").Err()).AnyTimes()

	executionParameters := workerExecutionParameters{
		TaskList:                  "testDecisionTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger:                    zap.NewNop(),
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler()}
	workflowWorker := newWorkflowWorkerInternal(
		s.service, domain, executionParameters, nil, overrides, getGlobalRegistry(),
	)
	_ = workflowWorker.Start()
	workflowWorker.Stop()
}

func (s *WorkersTestSuite) TestLongRunningDecisionTask() {
	localActivityCalledCount := 0
	localActivitySleep := func(duration time.Duration) error {
		time.Sleep(duration)
		localActivityCalledCount++
		return nil
	}

	doneCh := make(chan struct{})

	isWorkflowCompleted := false
	longDecisionWorkflowFn := func(ctx Context, input []byte) error {
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

	RegisterWorkflowWithOptions(
		longDecisionWorkflowFn,
		RegisterWorkflowOptions{Name: "long-running-decision-workflow-type"},
	)

	domain := "testDomain"
	taskList := "long-running-decision-tl"
	testEvents := []*commonproto.HistoryEvent{
		{
			EventId:   1,
			EventType: enums.EventTypeWorkflowExecutionStarted,
			Attributes: &commonproto.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: &commonproto.WorkflowExecutionStartedEventAttributes{
				TaskList:                            &commonproto.TaskList{Name: taskList},
				ExecutionStartToCloseTimeoutSeconds: 10,
				TaskStartToCloseTimeoutSeconds:      2,
				WorkflowType:                        &commonproto.WorkflowType{Name: "long-running-decision-workflow-type"},
			}},
		},
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   5,
			EventType: enums.EventTypeMarkerRecorded,
			Attributes: &commonproto.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &commonproto.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("0"),
				DecisionTaskCompletedEventId: 4,
			}},
		},
		createTestEventDecisionTaskScheduled(6, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(7),
		createTestEventDecisionTaskCompleted(8, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   9,
			EventType: enums.EventTypeMarkerRecorded,
			Attributes: &commonproto.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &commonproto.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("1"),
				DecisionTaskCompletedEventId: 8,
			}},
		},
		createTestEventDecisionTaskScheduled(10, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(11),
	}

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	task := &workflowservice.PollForDecisionTaskResponse{
		TaskToken: []byte("test-token"),
		WorkflowExecution: &commonproto.WorkflowExecution{
			WorkflowId: "long-running-decision-workflow-id",
			RunId:      "long-running-decision-workflow-run-id",
		},
		WorkflowType: &commonproto.WorkflowType{
			Name: "long-running-decision-workflow-type",
		},
		PreviousStartedEventId: 0,
		StartedEventId:         3,
		History:                &commonproto.History{Events: testEvents[0:3]},
		NextPageToken:          nil,
	}
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(task, nil).Times(1)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollForDecisionTaskResponse{}, status.New(codes.Internal, "").Err()).AnyTimes()

	respondCounter := 0
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *workflowservice.RespondDecisionTaskCompletedRequest, opts ...grpc.CallOption,
	) (success *workflowservice.RespondDecisionTaskCompletedResponse, err error) {
		respondCounter++
		switch respondCounter {
		case 1:
			s.Equal(1, len(request.Decisions))
			s.Equal(enums.DecisionTypeRecordMarker, request.Decisions[0].GetDecisionType())
			task.PreviousStartedEventId = 3
			task.StartedEventId = 7
			task.History.Events = testEvents[3:7]
			return &workflowservice.RespondDecisionTaskCompletedResponse{DecisionTask: task}, nil
		case 2:
			s.Equal(2, len(request.Decisions))
			s.Equal(enums.DecisionTypeRecordMarker, request.Decisions[0].GetDecisionType())
			s.Equal(enums.DecisionTypeCompleteWorkflowExecution, request.Decisions[1].GetDecisionType())
			task.PreviousStartedEventId = 7
			task.StartedEventId = 11
			task.History.Events = testEvents[7:11]
			close(doneCh)
			return nil, nil
		default:
			panic("unexpected RespondDecisionTaskCompleted")
		}
	}).Times(2)

	options := WorkerOptions{
		Logger:                zap.NewNop(),
		DisableActivityWorker: true,
		Identity:              "test-worker-identity",
	}
	worker := newAggregatedWorker(s.service, domain, taskList, options)
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
	longDecisionWorkflowFn := func(ctx Context, input []byte) error {
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

	RegisterWorkflowWithOptions(
		longDecisionWorkflowFn,
		RegisterWorkflowOptions{Name: "multiple-local-activities-workflow-type"},
	)

	domain := "testDomain"
	taskList := "multiple-local-activities-tl"
	testEvents := []*commonproto.HistoryEvent{
		{
			EventId:   1,
			EventType: enums.EventTypeWorkflowExecutionStarted,
			Attributes: &commonproto.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: &commonproto.WorkflowExecutionStartedEventAttributes{
				TaskList:                            &commonproto.TaskList{Name: taskList},
				ExecutionStartToCloseTimeoutSeconds: 10,
				TaskStartToCloseTimeoutSeconds:      3,
				WorkflowType:                        &commonproto.WorkflowType{Name: "multiple-local-activities-workflow-type"},
			}},
		},
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   5,
			EventType: enums.EventTypeMarkerRecorded,
			Attributes: &commonproto.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &commonproto.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("0"),
				DecisionTaskCompletedEventId: 4,
			}},
		},
		createTestEventDecisionTaskScheduled(6, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(7),
		createTestEventDecisionTaskCompleted(8, &commonproto.DecisionTaskCompletedEventAttributes{ScheduledEventId: 2}),
		{
			EventId:   9,
			EventType: enums.EventTypeMarkerRecorded,
			Attributes: &commonproto.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &commonproto.MarkerRecordedEventAttributes{
				MarkerName:                   localActivityMarkerName,
				Details:                      s.createLocalActivityMarkerDataForTest("1"),
				DecisionTaskCompletedEventId: 8,
			}},
		},
		createTestEventDecisionTaskScheduled(10, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: taskList}}),
		createTestEventDecisionTaskStarted(11),
	}

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	task := &workflowservice.PollForDecisionTaskResponse{
		TaskToken: []byte("test-token"),
		WorkflowExecution: &commonproto.WorkflowExecution{
			WorkflowId: "multiple-local-activities-workflow-id",
			RunId:      "multiple-local-activities-workflow-run-id",
		},
		WorkflowType: &commonproto.WorkflowType{
			Name: "multiple-local-activities-workflow-type",
		},
		PreviousStartedEventId: 0,
		StartedEventId:         3,
		History:                &commonproto.History{Events: testEvents[0:3]},
		NextPageToken:          nil,
	}
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(task, nil).Times(1)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollForDecisionTaskResponse{}, status.New(codes.Internal, "").Err()).AnyTimes()

	respondCounter := 0
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request *workflowservice.RespondDecisionTaskCompletedRequest, opts ...grpc.CallOption,
	) (success *workflowservice.RespondDecisionTaskCompletedResponse, err error) {
		respondCounter++
		switch respondCounter {
		case 1:
			s.Equal(3, len(request.Decisions))
			s.Equal(enums.DecisionTypeRecordMarker, request.Decisions[0].GetDecisionType())
			task.PreviousStartedEventId = 3
			task.StartedEventId = 7
			task.History.Events = testEvents[3:11]
			close(doneCh)
			return nil, nil
		default:
			panic("unexpected RespondDecisionTaskCompleted")
		}
	}).Times(1)

	options := WorkerOptions{
		Logger:                zap.NewNop(),
		DisableActivityWorker: true,
		Identity:              "test-worker-identity",
	}
	worker := newAggregatedWorker(s.service, domain, taskList, options)
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

func (s *WorkersTestSuite) createLocalActivityMarkerDataForTest(activityID string) []byte {
	lamd := localActivityMarkerData{
		ActivityID: activityID,
		ReplayTime: time.Now(),
	}

	// encode marker data
	markerData, err := encodeArg(nil, lamd)
	s.NoError(err)
	return markerData
}
