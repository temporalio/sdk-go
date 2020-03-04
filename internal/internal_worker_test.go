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
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gogo/status"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonproto "go.temporal.io/temporal-proto/common"
	"go.temporal.io/temporal-proto/enums"
	"go.temporal.io/temporal-proto/workflowservice"
	"go.temporal.io/temporal-proto/workflowservicemock"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func testInternalWorkerRegister(r *registry) {
	r.RegisterWorkflowWithOptions(
		sampleWorkflowExecute,
		RegisterWorkflowOptions{Name: "sampleWorkflowExecute"},
	)
	r.RegisterWorkflow(testReplayWorkflow)
	r.RegisterWorkflow(testReplayWorkflowLocalActivity)
	r.RegisterWorkflow(testReplayWorkflowFromFile)
	r.RegisterWorkflow(testReplayWorkflowFromFileParent)
	r.RegisterActivityWithOptions(
		testActivity,
		RegisterActivityOptions{Name: "testActivity"},
	)
	r.RegisterActivity(testActivityByteArgs)
	r.RegisterActivityWithOptions(
		testActivityMultipleArgs,
		RegisterActivityOptions{Name: "testActivityMultipleArgs"},
	)
	r.RegisterActivity(testActivityReturnString)
	r.RegisterActivity(testActivityReturnEmptyString)
	r.RegisterActivity(testActivityReturnEmptyStruct)

	r.RegisterActivity(testActivityNoResult)
	r.RegisterActivity(testActivityNoContextArg)
	r.RegisterActivity(testActivityReturnByteArray)
	r.RegisterActivity(testActivityReturnInt)
	r.RegisterActivity(testActivityReturnNilStructPtr)
	r.RegisterActivity(testActivityReturnStructPtr)
	r.RegisterActivity(testActivityReturnNilStructPtrPtr)
	r.RegisterActivity(testActivityReturnStructPtrPtr)
}

func testInternalWorkerRegisterWithTestEnv(env *TestWorkflowEnvironment) {
	env.RegisterWorkflowWithOptions(
		sampleWorkflowExecute,
		RegisterWorkflowOptions{Name: "sampleWorkflowExecute"},
	)
	env.RegisterWorkflow(testReplayWorkflow)
	env.RegisterWorkflow(testReplayWorkflowLocalActivity)
	env.RegisterWorkflow(testReplayWorkflowFromFile)
	env.RegisterWorkflow(testReplayWorkflowFromFileParent)
	env.RegisterActivityWithOptions(
		testActivity,
		RegisterActivityOptions{Name: "testActivity"},
	)
	env.RegisterActivity(testActivityByteArgs)
	env.RegisterActivityWithOptions(
		testActivityMultipleArgs,
		RegisterActivityOptions{Name: "testActivityMultipleArgs"},
	)
	env.RegisterActivity(testActivityReturnString)
	env.RegisterActivity(testActivityReturnEmptyString)
	env.RegisterActivity(testActivityReturnEmptyStruct)

	env.RegisterActivity(testActivityNoResult)
	env.RegisterActivity(testActivityNoContextArg)
	env.RegisterActivity(testActivityReturnByteArray)
	env.RegisterActivity(testActivityReturnInt)
	env.RegisterActivity(testActivityReturnNilStructPtr)
	env.RegisterActivity(testActivityReturnStructPtr)
	env.RegisterActivity(testActivityReturnNilStructPtrPtr)
	env.RegisterActivity(testActivityReturnStructPtrPtr)
}

type internalWorkerTestSuite struct {
	suite.Suite
	mockCtrl *gomock.Controller
	service  *workflowservicemock.MockWorkflowServiceClient
	registry *registry
}

func TestInternalWorkerTestSuite(t *testing.T) {
	s := &internalWorkerTestSuite{
		registry: newRegistry(),
	}
	testInternalWorkerRegister(s.registry)
	suite.Run(t, s)
}

func (s *internalWorkerTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
}

func (s *internalWorkerTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *internalWorkerTestSuite) createLocalActivityMarkerDataForTest(activityID string) []byte {
	lamd := localActivityMarkerData{
		ActivityID: activityID,
		ReplayTime: time.Now(),
	}

	// encode marker data
	markerData, err := encodeArg(nil, lamd)
	s.NoError(err)
	return markerData
}

func getLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func testReplayWorkflow(ctx Context) error {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func testReplayWorkflowLocalActivity(ctx Context) error {
	ao := LocalActivityOptions{
		ScheduleToCloseTimeout: time.Second,
	}
	ctx = WithLocalActivityOptions(ctx, ao)
	err := ExecuteLocalActivity(ctx, testActivity).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func testReplayWorkflowFromFile(ctx Context) error {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
		WaitForCancellation:    true,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivityMultipleArgs", 2, "test", true).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func testReplayWorkflowFromFileParent(ctx Context) error {
	execution := GetWorkflowInfo(ctx).WorkflowExecution
	childID := fmt.Sprintf("child_workflow:%v", execution.RunID)
	cwo := ChildWorkflowOptions{
		WorkflowID:                   childID,
		ExecutionStartToCloseTimeout: time.Minute,
	}
	ctx = WithChildWorkflowOptions(ctx, cwo)
	var result string
	cwf := ExecuteChildWorkflow(ctx, testReplayWorkflowFromFile)
	f1 := cwf.SignalChildWorkflow(ctx, "test-signal", "test-data")
	err := f1.Get(ctx, nil)
	if err != nil {
		return err
	}
	err = cwf.Get(ctx, &result)
	if err != nil {
		return err
	}
	return nil
}

func testActivity(context.Context) error {
	return nil
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory() {
	taskList := "taskList1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonproto.WorkflowType{Name: "testReplayWorkflow"},
			TaskList:     &commonproto.TaskList{Name: taskList},
			Input:        testEncodeFunctionArgs(nil),
		}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{}),
		createTestEventActivityTaskScheduled(5, &commonproto.ActivityTaskScheduledEventAttributes{
			ActivityId:   "0",
			ActivityType: &commonproto.ActivityType{Name: "testActivity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
		}),
		createTestEventActivityTaskStarted(6, &commonproto.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 5,
		}),
		createTestEventActivityTaskCompleted(7, &commonproto.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 5,
			StartedEventId:   6,
		}),
		createTestEventDecisionTaskScheduled(8, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(9),
		createTestEventDecisionTaskCompleted(10, &commonproto.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 8,
			StartedEventId:   9,
		}),
		createTestEventWorkflowExecutionCompleted(11, &commonproto.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 10,
		}),
	}

	history := &commonproto.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflow)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity() {
	taskList := "taskList1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonproto.WorkflowType{Name: "testReplayWorkflowLocalActivity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
			Input:        testEncodeFunctionArgs(nil),
		}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{}),

		createTestEventLocalActivity(5, &commonproto.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("0"),
			DecisionTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(6, &commonproto.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 4,
		}),
	}

	history := &commonproto.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowLocalActivity)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity_Result_Mismatch() {
	taskList := "taskList1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonproto.WorkflowType{Name: "testReplayWorkflowLocalActivity"},
			TaskList:     &commonproto.TaskList{Name: taskList},
			Input:        testEncodeFunctionArgs(nil),
		}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{}),

		createTestEventLocalActivity(5, &commonproto.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("0"),
			DecisionTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(6, &commonproto.WorkflowExecutionCompletedEventAttributes{
			Result:                       []byte("some-incorrect-result"),
			DecisionTaskCompletedEventId: 4,
		}),
	}

	history := &commonproto.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflow)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.Error(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity_Activity_Type_Mismatch() {
	taskList := "taskList1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonproto.WorkflowType{Name: "go.temporal.io/temporal/internal.testReplayWorkflow"},
			TaskList:     &commonproto.TaskList{Name: taskList},
			Input:        testEncodeFunctionArgs(nil),
		}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &commonproto.DecisionTaskCompletedEventAttributes{}),

		createTestEventLocalActivity(5, &commonproto.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("0"),
			DecisionTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(6, &commonproto.WorkflowExecutionCompletedEventAttributes{
			Result:                       []byte("some-incorrect-result"),
			DecisionTaskCompletedEventId: 4,
		}),
	}

	history := &commonproto.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflow)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.Error(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistoryFromFileParent() {
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowFromFileParent)
	err := replayer.ReplayWorkflowHistoryFromJSONFile(logger, "testdata/parentWF.json")
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistoryFromFile() {
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowFromFile)
	err := replayer.ReplayWorkflowHistoryFromJSONFile(logger, "testdata/sampleHistory.json")
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) testDecisionTaskHandlerHelper(params workerExecutionParameters) {
	taskList := "taskList1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{
			TaskList: &commonproto.TaskList{Name: taskList},
			Input:    testEncodeFunctionArgs(params.DataConverter),
		}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
	}

	workflowType := "testReplayWorkflow"
	workflowID := "testID"
	runID := "testRunID"

	task := &workflowservice.PollForDecisionTaskResponse{
		WorkflowExecution:      &commonproto.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
		WorkflowType:           &commonproto.WorkflowType{Name: workflowType},
		History:                &commonproto.History{Events: testEvents},
		PreviousStartedEventId: 0,
	}

	r := newWorkflowTaskHandler(testDomain, params, nil, s.registry)
	_, err := r.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	s.NoError(err)
}

func (s *internalWorkerTestSuite) TestDecisionTaskHandler() {
	params := workerExecutionParameters{
		Identity: "identity",
		Logger:   getLogger(),
	}
	s.testDecisionTaskHandlerHelper(params)
}

func (s *internalWorkerTestSuite) TestDecisionTaskHandler_WithDataConverter() {
	params := workerExecutionParameters{
		Identity:      "identity",
		Logger:        getLogger(),
		DataConverter: newTestDataConverter(),
	}
	s.testDecisionTaskHandlerHelper(params)
}

// testSampleWorkflow
func sampleWorkflowExecute(ctx Context, input []byte) (result []byte, err error) {
	ExecuteActivity(ctx, testActivityByteArgs, input)
	ExecuteActivity(ctx, testActivityMultipleArgs, 2, []string{"test"}, true)
	return []byte("Done"), nil
}

// test activity1
func testActivityByteArgs(context.Context, []byte) ([]byte, error) {
	fmt.Println("Executing Activity1")
	return nil, nil
}

// test testActivityMultipleArgs
func testActivityMultipleArgs(context.Context, int, []string, bool) ([]byte, error) {
	fmt.Println("Executing Activity2")
	return nil, nil
}

func (s *internalWorkerTestSuite) TestCreateWorker() {
	worker := createWorkerWithThrottle(s.service, 500.0, nil)
	err := worker.Start()
	require.NoError(s.T(), err)
	time.Sleep(time.Millisecond * 200)
	worker.Stop()
}

func (s *internalWorkerTestSuite) TestCreateWorker_WithDataConverter() {
	worker := createWorkerWithDataConverter(s.service)
	err := worker.Start()
	require.NoError(s.T(), err)
	time.Sleep(time.Millisecond * 200)
	worker.Stop()
}

func (s *internalWorkerTestSuite) TestCreateWorkerRun() {
	// Create service endpoint
	mockCtrl := gomock.NewController(s.T())
	service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	worker := createWorker(service)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = worker.Run()
	}()
	time.Sleep(time.Millisecond * 200)
	p, err := os.FindProcess(os.Getpid())
	assert.NoError(s.T(), err)
	assert.NoError(s.T(), p.Signal(os.Interrupt))
	wg.Wait()
}

func (s *internalWorkerTestSuite) TestNoActivitiesOrWorkflows() {
	t := s.T()
	w := createWorker(s.service)
	w.registry = newRegistry()
	assert.Empty(t, w.registry.getRegisteredActivities())
	assert.Empty(t, w.registry.getRegisteredWorkflowTypes())
	assert.NoError(t, w.Start())
}

func (s *internalWorkerTestSuite) TestWorkerStartFailsWithInvalidDomain() {
	t := s.T()
	testCases := []struct {
		domainErr  error
		isErrFatal bool
	}{
		{status.Error(codes.NotFound, ""), true},
		{status.Error(codes.InvalidArgument, ""), true},
		{status.Error(codes.Internal, ""), false},
		{errors.New("unknown"), false},
	}

	mockCtrl := gomock.NewController(t)

	for _, tc := range testCases {
		service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)
		service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, tc.domainErr).Do(
			func(ctx context.Context, request *workflowservice.DescribeDomainRequest, opts ...grpc.CallOption) {
				// log
			}).Times(2)

		worker := createWorker(service)
		if tc.isErrFatal {
			err := worker.Start()
			assert.Error(t, err, "worker.start() MUST fail when domain is invalid")
			errC := make(chan error)
			go func() { errC <- worker.Run() }()
			select {
			case e := <-errC:
				assert.Error(t, e, "worker.Run() MUST fail when domain is invalid")
			case <-time.After(time.Second):
				assert.Fail(t, "worker.Run() MUST fail when domain is invalid")
			}
			continue
		}
		err := worker.Start()
		assert.NoError(t, err, "worker.Start() failed unexpectedly")
		worker.Stop()
	}
}

func ofPollForActivityTaskRequest(tps float64) gomock.Matcher {
	return &mockPollForActivityTaskRequest{tps: tps}
}

type mockPollForActivityTaskRequest struct {
	tps float64
}

func (m *mockPollForActivityTaskRequest) Matches(x interface{}) bool {
	v, ok := x.(*workflowservice.PollForActivityTaskRequest)
	if !ok {
		return false
	}

	if v.TaskListMetadata != nil && v.TaskListMetadata.MaxTasksPerSecond != nil {
		return v.TaskListMetadata.MaxTasksPerSecond.GetValue() == m.tps
	}

	return false
}

func (m *mockPollForActivityTaskRequest) String() string {
	return "PollForActivityTaskRequest"
}

func createWorker(service *workflowservicemock.MockWorkflowServiceClient) *AggregatedWorker {
	return createWorkerWithThrottle(service, 0.0, nil)
}

func createWorkerWithThrottle(
	service *workflowservicemock.MockWorkflowServiceClient, activitiesPerSecond float64, dc DataConverter,
) *AggregatedWorker {
	domain := "testDomain"
	domainStatus := enums.DomainStatusRegistered
	domainDesc := &workflowservice.DescribeDomainResponse{
		DomainInfo: &commonproto.DomainInfo{
			Name:   domain,
			Status: domainStatus,
		},
	}
	// mocks
	service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(domainDesc, nil).Do(
		func(ctx context.Context, request *workflowservice.DescribeDomainRequest, opts ...grpc.CallOption) {
			// log
		}).AnyTimes()

	activityTask := &workflowservice.PollForActivityTaskResponse{}
	expectedActivitiesPerSecond := activitiesPerSecond
	if expectedActivitiesPerSecond == 0.0 {
		expectedActivitiesPerSecond = defaultTaskListActivitiesPerSecond
	}
	service.EXPECT().PollForActivityTask(
		gomock.Any(), ofPollForActivityTaskRequest(expectedActivitiesPerSecond), gomock.Any(),
	).Return(activityTask, nil).AnyTimes()
	service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()

	decisionTask := &workflowservice.PollForDecisionTaskResponse{}
	service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(decisionTask, nil).AnyTimes()
	service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	// Configure worker options.
	workerOptions := WorkerOptions{}
	workerOptions.WorkerActivitiesPerSecond = 20
	workerOptions.TaskListActivitiesPerSecond = activitiesPerSecond
	if dc != nil {
		workerOptions.DataConverter = dc
	}
	workerOptions.EnableSessionWorker = true

	// Start Worker.
	worker := NewWorker(
		service,
		domain,
		"testGroupName2",
		workerOptions)
	return worker
}

func createWorkerWithDataConverter(service *workflowservicemock.MockWorkflowServiceClient) *AggregatedWorker {
	return createWorkerWithThrottle(service, 0.0, newTestDataConverter())
}

func (s *internalWorkerTestSuite) testCompleteActivityHelper(opt *ClientOptions) {
	t := s.T()
	mockService := s.service
	domain := "testDomain"
	wfClient := NewClient(mockService, domain, opt)
	var completedRequest, canceledRequest, failedRequest interface{}
	mockService.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedRequest, opts ...grpc.CallOption) {
			completedRequest = request
		})
	mockService.EXPECT().RespondActivityTaskCanceled(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCanceledResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledRequest, opts ...grpc.CallOption) {
			canceledRequest = request
		})
	mockService.EXPECT().RespondActivityTaskFailed(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskFailedResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskFailedRequest, opts ...grpc.CallOption) {
			failedRequest = request
		})

	_ = wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, nil)
	require.NotNil(t, completedRequest)

	_ = wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	_ = wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, errors.New(""))
	require.NotNil(t, failedRequest)
}

func (s *internalWorkerTestSuite) TestCompleteActivity() {
	s.testCompleteActivityHelper(nil)
}

func (s *internalWorkerTestSuite) TestCompleteActivity_WithDataConverter() {
	opt := &ClientOptions{DataConverter: newTestDataConverter()}
	s.testCompleteActivityHelper(opt)
}

func (s *internalWorkerTestSuite) TestCompleteActivityById() {
	t := s.T()
	mockService := s.service
	domain := "testDomain"
	wfClient := NewClient(mockService, domain, nil)
	var completedRequest, canceledRequest, failedRequest interface{}
	mockService.EXPECT().RespondActivityTaskCompletedByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedByIDResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedByIDRequest, opts ...grpc.CallOption) {
			completedRequest = request
		})
	mockService.EXPECT().RespondActivityTaskCanceledByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCanceledByIDResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledByIDRequest, opts ...grpc.CallOption) {
			canceledRequest = request
		})
	mockService.EXPECT().RespondActivityTaskFailedByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskFailedByIDResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskFailedByIDRequest, opts ...grpc.CallOption) {
			failedRequest = request
		})

	workflowID := "wid"
	runID := ""
	activityID := "aid"

	_ = wfClient.CompleteActivityByID(context.Background(), domain, workflowID, runID, activityID, nil, nil)
	require.NotNil(t, completedRequest)

	_ = wfClient.CompleteActivityByID(context.Background(), domain, workflowID, runID, activityID, nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	_ = wfClient.CompleteActivityByID(context.Background(), domain, workflowID, runID, activityID, nil, errors.New(""))
	require.NotNil(t, failedRequest)
}

func (s *internalWorkerTestSuite) TestRecordActivityHeartbeat() {
	domain := "testDomain"
	wfClient := NewClient(s.service, domain, nil)
	var heartbeatRequest *workflowservice.RecordActivityTaskHeartbeatRequest
	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: false}
	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(&heartbeatResponse, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			heartbeatRequest = request
		}).Times(2)

	_ = wfClient.RecordActivityHeartbeat(context.Background(), nil)
	_ = wfClient.RecordActivityHeartbeat(context.Background(), nil, "testStack", "customerObjects", 4)
	require.NotNil(s.T(), heartbeatRequest)
}

func (s *internalWorkerTestSuite) TestRecordActivityHeartbeat_WithDataConverter() {
	t := s.T()
	domain := "testDomain"
	dc := newTestDataConverter()
	opt := &ClientOptions{DataConverter: dc}
	wfClient := NewClient(s.service, domain, opt)
	var heartbeatRequest *workflowservice.RecordActivityTaskHeartbeatRequest
	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: false}
	detail1 := "testStack"
	detail2 := testStruct{"abc", 123}
	detail3 := 4
	encodedDetail, err := dc.ToData(detail1, detail2, detail3)
	require.Nil(t, err)
	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(&heartbeatResponse, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			heartbeatRequest = request
			require.Equal(t, encodedDetail, request.Details)
		}).Times(1)

	_ = wfClient.RecordActivityHeartbeat(context.Background(), nil, detail1, detail2, detail3)
	require.NotNil(t, heartbeatRequest)
}

func (s *internalWorkerTestSuite) TestRecordActivityHeartbeatByID() {
	domain := "testDomain"
	wfClient := NewClient(s.service, domain, nil)
	var heartbeatRequest *workflowservice.RecordActivityTaskHeartbeatByIDRequest
	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatByIDResponse{CancelRequested: false}
	s.service.EXPECT().RecordActivityTaskHeartbeatByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&heartbeatResponse, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatByIDRequest, opts ...grpc.CallOption) {
			heartbeatRequest = request
		}).Times(2)

	_ = wfClient.RecordActivityHeartbeatByID(context.Background(), domain, "wid", "rid", "aid")
	_ = wfClient.RecordActivityHeartbeatByID(context.Background(), domain, "wid", "rid", "aid",
		"testStack", "customerObjects", 4)
	require.NotNil(s.T(), heartbeatRequest)
}

type activitiesCallingOptionsWorkflow struct {
	t *testing.T
}

func (w activitiesCallingOptionsWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	// By functions.
	err = ExecuteActivity(ctx, testActivityByteArgs, input).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityMultipleArgs, 2, []string{"test"}, true).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoResult, 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoContextArg, 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	f := ExecuteActivity(ctx, testActivityReturnByteArray)
	var r []byte
	err = f.Get(ctx, &r)
	require.NoError(w.t, err, err)
	require.Equal(w.t, []byte("testActivity"), r)

	f = ExecuteActivity(ctx, testActivityReturnInt)
	var rInt int
	err = f.Get(ctx, &rInt)
	require.NoError(w.t, err, err)
	require.Equal(w.t, 5, rInt)

	f = ExecuteActivity(ctx, testActivityReturnString)
	var rString string
	err = f.Get(ctx, &rString)

	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString)

	f = ExecuteActivity(ctx, testActivityReturnEmptyString)
	var r2String string
	err = f.Get(ctx, &r2String)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "", r2String)

	f = ExecuteActivity(ctx, testActivityReturnEmptyStruct)
	var r2Struct testActivityResult
	err = f.Get(ctx, &r2Struct)
	require.NoError(w.t, err, err)
	require.Equal(w.t, testActivityResult{}, r2Struct)

	f = ExecuteActivity(ctx, testActivityReturnNilStructPtr)
	var rStructPtr *testActivityResult
	err = f.Get(ctx, &rStructPtr)
	require.NoError(w.t, err, err)
	require.True(w.t, rStructPtr == nil)

	f = ExecuteActivity(ctx, testActivityReturnStructPtr)
	err = f.Get(ctx, &rStructPtr)
	require.NoError(w.t, err, err)
	require.Equal(w.t, *rStructPtr, testActivityResult{Index: 10})

	f = ExecuteActivity(ctx, testActivityReturnNilStructPtrPtr)
	var rStruct2Ptr **testActivityResult
	err = f.Get(ctx, &rStruct2Ptr)
	require.NoError(w.t, err, err)
	require.True(w.t, rStruct2Ptr == nil)

	f = ExecuteActivity(ctx, testActivityReturnStructPtrPtr)
	err = f.Get(ctx, &rStruct2Ptr)
	require.NoError(w.t, err, err)
	require.True(w.t, **rStruct2Ptr == testActivityResult{Index: 10})

	// By names.
	err = ExecuteActivity(ctx, "testActivityByteArgs", input).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityMultipleArgs", 2, []string{"test"}, true).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoResult", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoContextArg", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	f = ExecuteActivity(ctx, "testActivityReturnString")
	err = f.Get(ctx, &rString)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString, rString)

	f = ExecuteActivity(ctx, "testActivityReturnEmptyString")
	var r2sString string
	err = f.Get(ctx, &r2String)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "", r2sString)

	f = ExecuteActivity(ctx, "testActivityReturnEmptyStruct")
	err = f.Get(ctx, &r2Struct)
	require.NoError(w.t, err, err)
	require.Equal(w.t, testActivityResult{}, r2Struct)

	return []byte("Done"), nil
}

// test testActivityNoResult
func testActivityNoResult(context.Context, int, string) error {
	return nil
}

// test testActivityNoContextArg
func testActivityNoContextArg(int, string) error {
	return nil
}

// test testActivityReturnByteArray
func testActivityReturnByteArray() ([]byte, error) {
	return []byte("testActivity"), nil
}

// testActivityReturnInt
func testActivityReturnInt() (int, error) {
	return 5, nil
}

// testActivityReturnString
func testActivityReturnString() (string, error) {
	return "testActivity", nil
}

// testActivityReturnEmptyString
func testActivityReturnEmptyString() (string, error) {
	// Return is mocked to retrun nil from server.
	// expect to convert it to appropriate default value.
	return "", nil
}

type testActivityResult struct {
	Index int
}

// testActivityReturnEmptyStruct
func testActivityReturnEmptyStruct() (testActivityResult, error) {
	// Return is mocked to retrun nil from server.
	// expect to convert it to appropriate default value.
	return testActivityResult{}, nil
}
func testActivityReturnNilStructPtr() (*testActivityResult, error) {
	return nil, nil
}
func testActivityReturnStructPtr() (*testActivityResult, error) {
	return &testActivityResult{Index: 10}, nil
}
func testActivityReturnNilStructPtrPtr() (**testActivityResult, error) {
	return nil, nil
}
func testActivityReturnStructPtrPtr() (**testActivityResult, error) {
	r := &testActivityResult{Index: 10}
	return &r, nil
}

func TestVariousActivitySchedulingOption(t *testing.T) {
	w := &activitiesCallingOptionsWorkflow{t: t}

	testVariousActivitySchedulingOption(t, w.Execute)
	testVariousActivitySchedulingOptionWithDataConverter(t, w.Execute)
}

func testVariousActivitySchedulingOption(t *testing.T, wf interface{}) {
	ts := &WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(wf)
	testInternalWorkerRegisterWithTestEnv(env)
	env.ExecuteWorkflow(wf, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func testVariousActivitySchedulingOptionWithDataConverter(t *testing.T, wf interface{}) {
	ts := &WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{DataConverter: newTestDataConverter()})
	env.RegisterWorkflow(wf)
	testInternalWorkerRegisterWithTestEnv(env)
	env.ExecuteWorkflow(wf, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func testWorkflowSample(Context, []byte) (result []byte, err error) {
	return nil, nil
}

func testWorkflowMultipleArgs(Context, int, string, bool) (result []byte, err error) {
	return nil, nil
}

func testWorkflowNoArgs(Context) (result []byte, err error) {
	return nil, nil
}

func testWorkflowReturnInt(Context) (result int, err error) {
	return 5, nil
}

func testWorkflowReturnString(Context, int) (result string, err error) {
	return "Done", nil
}

type testWorkflowResult struct {
	V int
}

func testWorkflowReturnStruct(Context, int) (result testWorkflowResult, err error) {
	return testWorkflowResult{}, nil
}

func testWorkflowReturnStructPtr(Context, int) (result *testWorkflowResult, err error) {
	return &testWorkflowResult{}, nil
}

func testWorkflowReturnStructPtrPtr(Context, int) (result **testWorkflowResult, err error) {
	return nil, nil
}

func TestRegisterVariousWorkflowTypes(t *testing.T) {
	r := newRegistry()
	r.RegisterWorkflow(testWorkflowSample)
	r.RegisterWorkflow(testWorkflowMultipleArgs)
	r.RegisterWorkflow(testWorkflowNoArgs)
	r.RegisterWorkflow(testWorkflowReturnInt)
	r.RegisterWorkflow(testWorkflowReturnString)
	r.RegisterWorkflow(testWorkflowReturnStruct)
	r.RegisterWorkflow(testWorkflowReturnStructPtr)
	r.RegisterWorkflow(testWorkflowReturnStructPtrPtr)
}

type testErrorDetails struct {
	T string
}

func testActivityErrorWithDetailsHelper(ctx context.Context, t *testing.T, dataConverter DataConverter) {
	a1 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCustomError("testReason", "testStringDetails")
		}}
	r := newRegistry()
	encResult, e := a1.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err := deSerializeFunctionResult(a1.fn, encResult, nil, dataConverter, r)
	require.NoError(t, err)
	require.Error(t, e)
	errWD := e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	var strDetails string
	_ = errWD.Details(&strDetails)
	require.Equal(t, "testStringDetails", strDetails)

	a2 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCustomError("testReason", testErrorDetails{T: "testErrorStack"})
		}}
	encResult, e = a2.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err = deSerializeFunctionResult(a2.fn, encResult, nil, dataConverter, r)
	require.NoError(t, err)
	require.Error(t, e)
	errWD = e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	var td testErrorDetails
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack"}, td)

	a3 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult", NewCustomError("testReason", testErrorDetails{T: "testErrorStack3"})
		}}
	encResult, e = a3.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	var result string
	err = deSerializeFunctionResult(a3.fn, encResult, &result, dataConverter, r)
	require.NoError(t, err)
	require.Equal(t, "testResult", result)
	require.Error(t, e)
	errWD = e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack3"}, td)

	a4 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult4", NewCustomError("testReason", "testMultipleString", testErrorDetails{T: "testErrorStack4"})
		}}
	encResult, e = a4.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err = deSerializeFunctionResult(a3.fn, encResult, &result, dataConverter, r)
	require.NoError(t, err)
	require.Equal(t, "testResult4", result)
	require.Error(t, e)
	errWD = e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	var ed string
	_ = errWD.Details(&ed, &td)
	require.Equal(t, "testMultipleString", ed)
	require.Equal(t, testErrorDetails{T: "testErrorStack4"}, td)
}

func TestActivityErrorWithDetails(t *testing.T) {
	testActivityErrorWithDetailsHelper(context.Background(), t, nil)
}

func TestActivityErrorWithDetails_WithDataConverter(t *testing.T) {
	dc := newTestDataConverter()
	ctx := context.WithValue(context.Background(), activityEnvContextKey, &activityEnvironment{dataConverter: dc})
	testActivityErrorWithDetailsHelper(ctx, t, dc)
}

func testActivityCancelledErrorHelper(ctx context.Context, t *testing.T, dataConverter DataConverter) {
	registry := newRegistry()
	a1 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCanceledError("testCancelStringDetails")
		}}
	encResult, e := a1.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err := deSerializeFunctionResult(a1.fn, encResult, nil, dataConverter, registry)
	require.NoError(t, err)
	require.Error(t, e)
	errWD := e.(*CanceledError)
	var strDetails string
	_ = errWD.Details(&strDetails)
	require.Equal(t, "testCancelStringDetails", strDetails)

	a2 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCanceledError(testErrorDetails{T: "testCancelErrorStack"})
		}}
	encResult, e = a2.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err = deSerializeFunctionResult(a2.fn, encResult, nil, dataConverter, registry)
	require.NoError(t, err)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	var td testErrorDetails
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testCancelErrorStack"}, td)

	a3 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult", NewCanceledError(testErrorDetails{T: "testErrorStack3"})
		}}
	encResult, e = a3.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	var r string
	err = deSerializeFunctionResult(a3.fn, encResult, &r, dataConverter, registry)
	require.NoError(t, err)
	require.Equal(t, "testResult", r)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack3"}, td)

	a4 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult4", NewCanceledError("testMultipleString", testErrorDetails{T: "testErrorStack4"})
		}}
	encResult, e = a4.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err = deSerializeFunctionResult(a3.fn, encResult, &r, dataConverter, registry)
	require.NoError(t, err)
	require.Equal(t, "testResult4", r)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	var ed string
	_ = errWD.Details(&ed, &td)
	require.Equal(t, "testMultipleString", ed)
	require.Equal(t, testErrorDetails{T: "testErrorStack4"}, td)
}

func TestActivityCancelledError(t *testing.T) {
	testActivityCancelledErrorHelper(context.Background(), t, nil)
}

func TestActivityCancelledError_WithDataConverter(t *testing.T) {
	dc := newTestDataConverter()
	ctx := context.WithValue(context.Background(), activityEnvContextKey, &activityEnvironment{dataConverter: dc})
	testActivityCancelledErrorHelper(ctx, t, dc)
}

func testActivityExecutionVariousTypesHelper(ctx context.Context, t *testing.T, dataConverter DataConverter) {
	registry := newRegistry()
	a1 := activityExecutor{
		fn: func(ctx context.Context, arg1 string) (*testWorkflowResult, error) {
			return &testWorkflowResult{V: 1}, nil
		}}
	encResult, e := a1.Execute(ctx, testEncodeFunctionArgs(dataConverter, "test"))
	require.NoError(t, e)
	var r *testWorkflowResult
	err := deSerializeFunctionResult(a1.fn, encResult, &r, dataConverter, registry)
	require.NoError(t, err)
	require.Equal(t, 1, r.V)

	a2 := activityExecutor{
		fn: func(ctx context.Context, arg1 *testWorkflowResult) (*testWorkflowResult, error) {
			return &testWorkflowResult{V: 2}, nil
		}}
	encResult, e = a2.Execute(ctx, testEncodeFunctionArgs(dataConverter, r))
	require.NoError(t, e)
	err = deSerializeFunctionResult(a2.fn, encResult, &r, dataConverter, registry)
	require.NoError(t, err)
	require.Equal(t, 2, r.V)
}

func TestActivityExecutionVariousTypes(t *testing.T) {
	testActivityExecutionVariousTypesHelper(context.Background(), t, nil)
}

func TestActivityExecutionVariousTypes_WithDataConverter(t *testing.T) {
	dc := newTestDataConverter()
	ctx := context.WithValue(context.Background(), activityEnvContextKey, &activityEnvironment{
		dataConverter: dc,
	})
	testActivityExecutionVariousTypesHelper(ctx, t, dc)
}

func TestActivityNilArgs(t *testing.T) {
	nilErr := errors.New("nils")
	activityFn := func(name string, idx int, strptr *string) error {
		if name == "" && idx == 0 && strptr == nil {
			return nilErr
		}
		return nil
	}

	args := []interface{}{nil, nil, nil}
	_, err := getValidatedActivityFunction(activityFn, args, newRegistry())
	require.NoError(t, err)

	data, _ := encodeArgs(nil, args)
	reflectArgs, err := decodeArgs(nil, reflect.TypeOf(activityFn), data)
	require.NoError(t, err)

	reflectResults := reflect.ValueOf(activityFn).Call(reflectArgs)
	require.Equal(t, nilErr, reflectResults[0].Interface())
}

/*
type encodingTest struct {
	encoding encoding
	input    []interface{}
}

var testWorkflowID1 = s.WorkflowExecution{WorkflowId: "testWID", RunId: "runID"}
var testWorkflowID2 = s.WorkflowExecution{WorkflowId: "testWID2", RunId: "runID2"}
var thriftEncodingTests = []encodingTest{
	{&thriftEncoding{}, []interface{}{&testWorkflowID1}},
	{&thriftEncoding{}, []interface{}{&testWorkflowID1, &testWorkflowID2}},
	{&thriftEncoding{}, []interface{}{&testWorkflowID1, &testWorkflowID2, &testWorkflowID1}},
}

// TODO: Disable until thriftrw encoding support is added to temporal client.(follow up change)
func _TestThriftEncoding(t *testing.T) {
	// Success tests.
	for _, et := range thriftEncodingTests {
		data, err := et.encoding.Marshal(et.input)
		require.NoError(t, err)

		var result []interface{}
		for _, v := range et.input {
			arg := reflect.New(reflect.ValueOf(v).Type()).Interface()
			result = append(result, arg)
		}
		err = et.encoding.Unmarshal(data, result)
		require.NoError(t, err)

		for i := 0; i < len(et.input); i++ {
			vat := reflect.ValueOf(result[i]).Elem().Interface()
			require.Equal(t, et.input[i], vat)
		}
	}

	// Failure tests.
	enc := &thriftEncoding{}
	_, err := enc.Marshal([]interface{}{testWorkflowID1})
	require.Contains(t, err.Error(), "pointer to thrift.TStruct type is required")

	err = enc.Unmarshal([]byte("dummy"), []interface{}{testWorkflowID1})
	require.Contains(t, err.Error(), "pointer to pointer thrift.TStruct type is required")

	err = enc.Unmarshal([]byte("dummy"), []interface{}{&testWorkflowID1})
	require.Contains(t, err.Error(), "pointer to pointer thrift.TStruct type is required")

	_, err = enc.Marshal([]interface{}{testWorkflowID1, &testWorkflowID2})
	require.Contains(t, err.Error(), "pointer to thrift.TStruct type is required")

	err = enc.Unmarshal([]byte("dummy"), []interface{}{testWorkflowID1, &testWorkflowID2})
	require.Contains(t, err.Error(), "pointer to pointer thrift.TStruct type is required")
}
*/

// Encode function args
func testEncodeFunctionArgs(dataConverter DataConverter, args ...interface{}) []byte {
	input, err := encodeArgs(dataConverter, args)
	if err != nil {
		fmt.Println(err)
		panic("Failed to encode arguments")
	}
	return input
}
