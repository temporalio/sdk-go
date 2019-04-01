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
	"github.com/pborman/uuid"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/yarpc"
	"go.uber.org/zap"
)

// ActivityTaskHandler never returns response
type noResponseActivityTaskHandler struct {
	isExecuteCalled chan struct{}
}

func newNoResponseActivityTaskHandler() *noResponseActivityTaskHandler {
	return &noResponseActivityTaskHandler{isExecuteCalled: make(chan struct{})}
}

func (ath noResponseActivityTaskHandler) Execute(taskList string, task *m.PollForActivityTaskResponse) (interface{}, error) {
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
		service  *workflowservicetest.MockClient
	}
)

// Test suite.
func (s *WorkersTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicetest.NewMockClient(s.mockCtrl)
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

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForDecisionTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil).AnyTimes()

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
		s.service, domain, executionParameters, nil, overrides, getHostEnvironment(),
	)
	workflowWorker.Start()
	workflowWorker.Stop()

	s.Nil(ctx.Err())
}

func (s *WorkersTestSuite) TestActivityWorker() {
	domain := "testDomain"
	logger, _ := zap.NewDevelopment()

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil)
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForActivityTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger:                    logger,
	}
	overrides := &workerOverrides{activityTaskHandler: newSampleActivityTaskHandler()}
	a := &greeterActivity{}
	hostEnv := getHostEnvironment()
	hostEnv.addActivity(a.ActivityType().Name, a)
	activityWorker := newActivityWorker(
		s.service, domain, executionParameters, overrides, hostEnv, nil,
	)
	activityWorker.Start()
	activityWorker.Stop()
}

func (s *WorkersTestSuite) TestActivityWorkerStop() {
	domain := "testDomain"
	logger, _ := zap.NewDevelopment()

	pats := &m.PollForActivityTaskResponse{
		TaskToken: []byte("token"),
		WorkflowExecution: &m.WorkflowExecution{
			WorkflowId: common.StringPtr("wID"),
			RunId:      common.StringPtr("rID")},
		ActivityType:                  &m.ActivityType{Name: common.StringPtr("test")},
		ActivityId:                    common.StringPtr(uuid.New()),
		ScheduledTimestamp:            common.Int64Ptr(time.Now().UnixNano()),
		ScheduleToCloseTimeoutSeconds: common.Int32Ptr(1),
		StartedTimestamp:              common.Int64Ptr(time.Now().UnixNano()),
		StartToCloseTimeoutSeconds:    common.Int32Ptr(1),
		WorkflowType: &m.WorkflowType{
			Name: common.StringPtr("wType"),
		},
		WorkflowDomain: common.StringPtr("domain"),
	}

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil)
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), callOptions...).Return(pats, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()

	ctx, cancel := context.WithCancel(context.Background())
	stopChannel := make(chan struct{})
	executionParameters := workerExecutionParameters{
		TaskList:                        "testTaskList",
		ConcurrentPollRoutineSize:       5,
		ConcurrentActivityExecutionSize: 2,
		Logger:                          logger,
		UserContext:                     ctx,
		UserContextCancel:               cancel,
		WorkerStopTimeout:               time.Second * 2,
	}
	activityTaskHandler := newNoResponseActivityTaskHandler()
	overrides := &workerOverrides{activityTaskHandler: activityTaskHandler}
	a := &greeterActivity{}
	hostEnv := getHostEnvironment()
	hostEnv.addActivity(a.ActivityType().Name, a)
	activityWorker := newActivityWorker(
		s.service, domain, executionParameters, overrides, hostEnv, stopChannel,
	)
	activityWorker.Start()
	activityTaskHandler.BlockedOnExecuteCalled()
	go activityWorker.Stop()
	<-stopChannel
	err := ctx.Err()
	s.NoError(err)

	<-ctx.Done()
	err = ctx.Err()
	s.Error(err)
}

func (s *WorkersTestSuite) TestPollForDecisionTask_InternalServiceError() {
	domain := "testDomain"

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForDecisionTaskResponse{}, &m.InternalServiceError{}).AnyTimes()

	executionParameters := workerExecutionParameters{
		TaskList:                  "testDecisionTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger:                    zap.NewNop(),
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler()}
	workflowWorker := newWorkflowWorkerInternal(
		s.service, domain, executionParameters, nil, overrides, getHostEnvironment(),
	)
	workflowWorker.Start()
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
	testEvents := []*m.HistoryEvent{
		{
			EventId:   common.Int64Ptr(1),
			EventType: common.EventTypePtr(m.EventTypeWorkflowExecutionStarted),
			WorkflowExecutionStartedEventAttributes: &m.WorkflowExecutionStartedEventAttributes{
				TaskList:                            &m.TaskList{Name: &taskList},
				ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(10),
				TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(2),
				WorkflowType:                        &m.WorkflowType{Name: common.StringPtr("long-running-decision-workflow-type")},
			},
		},
		createTestEventDecisionTaskScheduled(2, &m.DecisionTaskScheduledEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &m.DecisionTaskCompletedEventAttributes{ScheduledEventId: common.Int64Ptr(2)}),
		{
			EventId:   common.Int64Ptr(5),
			EventType: common.EventTypePtr(m.EventTypeMarkerRecorded),
			MarkerRecordedEventAttributes: &m.MarkerRecordedEventAttributes{
				MarkerName:                   common.StringPtr(localActivityMarkerName),
				Details:                      s.createLocalActivityMarkerDataForTest("0"),
				DecisionTaskCompletedEventId: common.Int64Ptr(4),
			},
		},
		createTestEventDecisionTaskScheduled(6, &m.DecisionTaskScheduledEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskStarted(7),
		createTestEventDecisionTaskCompleted(8, &m.DecisionTaskCompletedEventAttributes{ScheduledEventId: common.Int64Ptr(2)}),
		{
			EventId:   common.Int64Ptr(9),
			EventType: common.EventTypePtr(m.EventTypeMarkerRecorded),
			MarkerRecordedEventAttributes: &m.MarkerRecordedEventAttributes{
				MarkerName:                   common.StringPtr(localActivityMarkerName),
				Details:                      s.createLocalActivityMarkerDataForTest("1"),
				DecisionTaskCompletedEventId: common.Int64Ptr(8),
			},
		},
		createTestEventDecisionTaskScheduled(10, &m.DecisionTaskScheduledEventAttributes{TaskList: &m.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskStarted(11),
	}

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil).AnyTimes()
	task := &m.PollForDecisionTaskResponse{
		TaskToken: []byte("test-token"),
		WorkflowExecution: &m.WorkflowExecution{
			WorkflowId: common.StringPtr("long-running-decision-workflow-id"),
			RunId:      common.StringPtr("long-running-decision-workflow-run-id"),
		},
		WorkflowType: &m.WorkflowType{
			Name: common.StringPtr("long-running-decision-workflow-type"),
		},
		PreviousStartedEventId: common.Int64Ptr(0),
		StartedEventId:         common.Int64Ptr(3),
		History:                &m.History{Events: testEvents[0:3]},
		NextPageToken:          nil,
	}
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(task, nil).Times(1)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForDecisionTaskResponse{}, &m.InternalServiceError{}).AnyTimes()

	respondCounter := 0
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).DoAndReturn(func(ctx context.Context, request *m.RespondDecisionTaskCompletedRequest, opts ...yarpc.CallOption,
	) (success *m.RespondDecisionTaskCompletedResponse, err error) {
		respondCounter++
		switch respondCounter {
		case 1:
			s.Equal(1, len(request.Decisions))
			s.Equal(m.DecisionTypeRecordMarker, request.Decisions[0].GetDecisionType())
			*task.PreviousStartedEventId = 3
			*task.StartedEventId = 7
			task.History.Events = testEvents[3:7]
			return &m.RespondDecisionTaskCompletedResponse{DecisionTask: task}, nil
		case 2:
			s.Equal(2, len(request.Decisions))
			s.Equal(m.DecisionTypeRecordMarker, request.Decisions[0].GetDecisionType())
			s.Equal(m.DecisionTypeCompleteWorkflowExecution, request.Decisions[1].GetDecisionType())
			*task.PreviousStartedEventId = 7
			*task.StartedEventId = 11
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
	worker.Start()
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
