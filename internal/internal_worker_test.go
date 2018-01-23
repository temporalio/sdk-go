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

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/yarpc"
	"go.uber.org/zap"
)

func init() {
	RegisterWorkflowWithOptions(
		sampleWorkflowExecute,
		RegisterWorkflowOptions{Name: "sampleWorkflowExecute"},
	)
	RegisterWorkflow(testReplayWorkflow)

	RegisterActivityWithOptions(
		testActivity,
		RegisterActivityOptions{Name: "testActivity"},
	)
	RegisterActivity(testActivityByteArgs)
	RegisterActivityWithOptions(
		testActivityMultipleArgs,
		RegisterActivityOptions{Name: "testActivityMultipleArgs"},
	)
	RegisterActivity(testActivityReturnString)
	RegisterActivity(testActivityReturnEmptyString)
	RegisterActivity(testActivityReturnEmptyStruct)

	RegisterActivity(testActivityNoResult)
	RegisterActivity(testActivityNoContextArg)
	RegisterActivity(testActivityReturnByteArray)
	RegisterActivity(testActivityReturnInt)
	RegisterActivity(testActivityReturnNilStructPtr)
	RegisterActivity(testActivityReturnStructPtr)
	RegisterActivity(testActivityReturnNilStructPtrPtr)
	RegisterActivity(testActivityReturnStructPtrPtr)
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

func testActivity(ctx context.Context) error {
	return nil
}

func TestDecisionTaskHandler(t *testing.T) {
	logger := getLogger()
	taskList := "taskList1"
	testEvents := []*s.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &s.WorkflowExecutionStartedEventAttributes{
			TaskList: &s.TaskList{Name: common.StringPtr(taskList)},
			Input:    testEncodeFunctionArgs(testReplayWorkflow),
		}),
		createTestEventDecisionTaskScheduled(2, &s.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &s.DecisionTaskCompletedEventAttributes{}),
		createTestEventActivityTaskScheduled(5, &s.ActivityTaskScheduledEventAttributes{
			ActivityId:   common.StringPtr("0"),
			ActivityType: &s.ActivityType{Name: common.StringPtr("testActivity")},
			TaskList:     &s.TaskList{Name: &taskList},
		}),
		createTestEventActivityTaskStarted(6, &s.ActivityTaskStartedEventAttributes{}),
	}

	workflowType := "go.uber.org/cadence/internal.testReplayWorkflow"
	workflowID := "testID"
	runID := "testRunID"

	task := &s.PollForDecisionTaskResponse{
		WorkflowExecution:      &s.WorkflowExecution{WorkflowId: &workflowID, RunId: &runID},
		WorkflowType:           &s.WorkflowType{Name: &workflowType},
		History:                &s.History{Events: testEvents},
		PreviousStartedEventId: common.Int64Ptr(5),
	}

	params := workerExecutionParameters{
		Identity: "identity",
		Logger:   logger,
	}
	r := newWorkflowTaskHandler(testDomain, params, nil, getHostEnvironment())
	_, stackTrace, err := r.ProcessWorkflowTask(task, nil, true)
	require.NoError(t, err)
	require.NotEmpty(t, stackTrace, stackTrace)
	require.Contains(t, stackTrace, "cadence/internal.(*decodeFutureImpl).Get")
}

// testSampleWorkflow
func sampleWorkflowExecute(ctx Context, input []byte) (result []byte, err error) {
	ExecuteActivity(ctx, testActivityByteArgs, input)
	ExecuteActivity(ctx, testActivityMultipleArgs, 2, "test", true)
	return []byte("Done"), nil
}

// test activity1
func testActivityByteArgs(ctx context.Context, input []byte) ([]byte, error) {
	fmt.Println("Executing Activity1")
	return nil, nil
}

// test testActivityMultipleArgs
func testActivityMultipleArgs(ctx context.Context, arg1 int, arg2 string, arg3 bool) ([]byte, error) {
	fmt.Println("Executing Activity2")
	return nil, nil
}

func TestCreateWorker(t *testing.T) {
	// Create service endpoint
	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)

	worker := createWorkerWithThrottle(t, service, float64(500.0))
	err := worker.Start()
	require.NoError(t, err)
	time.Sleep(time.Millisecond * 200)
	worker.Stop()
}

func TestCreateWorkerRun(t *testing.T) {
	// Create service endpoint
	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)

	worker := createWorker(t, service)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.Run()
	}()
	time.Sleep(time.Millisecond * 200)
	p, err := os.FindProcess(os.Getpid())
	assert.NoError(t, err)
	assert.NoError(t, p.Signal(os.Interrupt))
	wg.Wait()
}

func TestNoActivitiesOrWorkflows(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)

	w := createWorker(t, service)
	aw := w.(*aggregatedWorker)
	aw.hostEnv = newHostEnvironment()
	assert.Empty(t, aw.hostEnv.getRegisteredActivities())
	assert.Empty(t, aw.hostEnv.getRegisteredWorkflowTypes())
	assert.NoError(t, aw.Start())
}

func TestWorkerStartFailsWithInvalidDomain(t *testing.T) {

	testCases := []struct {
		domainErr  error
		isErrFatal bool
	}{
		{&s.EntityNotExistsError{}, true},
		{&s.BadRequestError{}, true},
		{&s.InternalServiceError{}, false},
		{errors.New("unknown"), false},
	}

	mockCtrl := gomock.NewController(t)

	for _, tc := range testCases {
		service := workflowservicetest.NewMockClient(mockCtrl)
		service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, tc.domainErr).Do(
			func(ctx context.Context, request *s.DescribeDomainRequest, opts ...yarpc.CallOption) {
				// log
			}).Times(2)

		worker := createWorker(t, service)
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
	v, ok := x.(*s.PollForActivityTaskRequest)
	if !ok {
		return false
	}
	return *(v.TaskListMetadata.MaxTasksPerSecond) == m.tps
}

func (m *mockPollForActivityTaskRequest) String() string {
	return "PollForActivityTaskRequest"
}

func createWorker(t *testing.T, service *workflowservicetest.MockClient) Worker {
	return createWorkerWithThrottle(t, service, float64(0.0))
}

func createWorkerWithThrottle(
	t *testing.T, service *workflowservicetest.MockClient, activitiesPerSecond float64,
) Worker {
	domain := "testDomain"
	domainStatus := s.DomainStatusRegistered
	domainDesc := &s.DescribeDomainResponse{
		DomainInfo: &s.DomainInfo{
			Name:   &domain,
			Status: &domainStatus,
		},
	}
	// mocks
	service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(domainDesc, nil).Do(
		func(ctx context.Context, request *s.DescribeDomainRequest, opts ...yarpc.CallOption) {
			// log
		}).AnyTimes()

	activityTask := &s.PollForActivityTaskResponse{}
	expectedActivitiesPerSecond := activitiesPerSecond
	if expectedActivitiesPerSecond == 0.0 {
		expectedActivitiesPerSecond = _defaultTaskListActivitiesPerSecond
	}
	service.EXPECT().PollForActivityTask(
		gomock.Any(), ofPollForActivityTaskRequest(expectedActivitiesPerSecond), callOptions...,
	).Return(activityTask, nil).AnyTimes()
	service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()

	decisionTask := &s.PollForDecisionTaskResponse{}
	service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(decisionTask, nil).AnyTimes()
	service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()

	// Configure worker options.
	workerOptions := WorkerOptions{}
	workerOptions.WorkerActivitiesPerSecond = 20
	workerOptions.TaskListActivitiesPerSecond = activitiesPerSecond

	// Start Worker.
	worker := NewWorker(
		service,
		domain,
		"testGroupName2",
		workerOptions)
	return worker
}

func TestCompleteActivity(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockService := workflowservicetest.NewMockClient(mockCtrl)

	domain := "testDomain"
	wfClient := NewClient(mockService, domain, nil)
	var completedRequest, canceledRequest, failedRequest interface{}
	mockService.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).Do(
		func(ctx context.Context, request *s.RespondActivityTaskCompletedRequest, opts ...yarpc.CallOption) {
			completedRequest = request
		})
	mockService.EXPECT().RespondActivityTaskCanceled(gomock.Any(), gomock.Any(), callOptions...).Return(nil).Do(
		func(ctx context.Context, request *s.RespondActivityTaskCanceledRequest, opts ...yarpc.CallOption) {
			canceledRequest = request
		})
	mockService.EXPECT().RespondActivityTaskFailed(gomock.Any(), gomock.Any(), callOptions...).Return(nil).Do(
		func(ctx context.Context, request *s.RespondActivityTaskFailedRequest, opts ...yarpc.CallOption) {
			failedRequest = request
		})

	wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, nil)
	require.NotNil(t, completedRequest)

	wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, errors.New(""))
	require.NotNil(t, failedRequest)
}

func TestCompleteActivityById(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockService := workflowservicetest.NewMockClient(mockCtrl)

	domain := "testDomain"
	wfClient := NewClient(mockService, domain, nil)
	var completedRequest, canceledRequest, failedRequest interface{}
	mockService.EXPECT().RespondActivityTaskCompletedByID(gomock.Any(), gomock.Any(), callOptions...).Return(nil).Do(
		func(ctx context.Context, request *s.RespondActivityTaskCompletedByIDRequest, opts ...yarpc.CallOption) {
			completedRequest = request
		})
	mockService.EXPECT().RespondActivityTaskCanceledByID(gomock.Any(), gomock.Any(), callOptions...).Return(nil).Do(
		func(ctx context.Context, request *s.RespondActivityTaskCanceledByIDRequest, opts ...yarpc.CallOption) {
			canceledRequest = request
		})
	mockService.EXPECT().RespondActivityTaskFailedByID(gomock.Any(), gomock.Any(), callOptions...).Return(nil).Do(
		func(ctx context.Context, request *s.RespondActivityTaskFailedByIDRequest, opts ...yarpc.CallOption) {
			failedRequest = request
		})

	workflowID := "wid"
	runID := ""
	activityID := "aid"

	wfClient.CompleteActivityByID(context.Background(), domain, workflowID, runID, activityID, nil, nil)
	require.NotNil(t, completedRequest)

	wfClient.CompleteActivityByID(context.Background(), domain, workflowID, runID, activityID, nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	wfClient.CompleteActivityByID(context.Background(), domain, workflowID, runID, activityID, nil, errors.New(""))
	require.NotNil(t, failedRequest)
}

func TestRecordActivityHeartbeat(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockService := workflowservicetest.NewMockClient(mockCtrl)

	domain := "testDomain"
	wfClient := NewClient(mockService, domain, nil)
	var heartbeatRequest *s.RecordActivityTaskHeartbeatRequest
	cancelRequested := false
	heartbeatResponse := s.RecordActivityTaskHeartbeatResponse{CancelRequested: &cancelRequested}
	mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).Return(&heartbeatResponse, nil).
		Do(func(ctx context.Context, request *s.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
			heartbeatRequest = request
		}).Times(2)

	wfClient.RecordActivityHeartbeat(context.Background(), nil)
	wfClient.RecordActivityHeartbeat(context.Background(), nil, "testStack", "customerObjects", 4)
	require.NotNil(t, heartbeatRequest)
}

func testEncodeFunction(t *testing.T, f interface{}, args ...interface{}) string {
	input, err := getHostEnvironment().Encoder().Marshal(args)
	require.NoError(t, err, err)

	var result []interface{}
	for _, v := range args {
		arg := reflect.New(reflect.TypeOf(v)).Interface()
		result = append(result, arg)
	}
	err = getHostEnvironment().Encoder().Unmarshal(input, result)
	require.NoError(t, err, err)

	targetArgs := []reflect.Value{}
	for _, arg := range result {
		targetArgs = append(targetArgs, reflect.ValueOf(arg).Elem())
	}
	fnValue := reflect.ValueOf(f)
	retValues := fnValue.Call(targetArgs)
	return retValues[0].Interface().(string)
}

func TestEncoder(t *testing.T) {
	getHostEnvironment().Encoder().Register(new(emptyCtx))
	// Two param functor.
	f1 := func(ctx Context, r []byte) string {
		return "result"
	}
	r1 := testEncodeFunction(t, f1, new(emptyCtx), []byte("test"))
	require.Equal(t, r1, "result")
	// No parameters.
	f2 := func() string {
		return "empty-result"
	}
	r2 := testEncodeFunction(t, f2)
	require.Equal(t, r2, "empty-result")
	// Nil parameter.
	f3 := func(r []byte) string {
		return "nil-result"
	}
	r3 := testEncodeFunction(t, f3, []byte(""))
	require.Equal(t, r3, "nil-result")
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

	err = ExecuteActivity(ctx, testActivityMultipleArgs, 2, "test", true).Get(ctx, nil)
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
	err = ExecuteActivity(ctx, "go.uber.org/cadence/internal.testActivityByteArgs", input).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityMultipleArgs", 2, "test", true).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "go.uber.org/cadence/internal.testActivityNoResult", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "go.uber.org/cadence/internal.testActivityNoContextArg", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	f = ExecuteActivity(ctx, "go.uber.org/cadence/internal.testActivityReturnString")
	err = f.Get(ctx, &rString)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString, rString)

	f = ExecuteActivity(ctx, "go.uber.org/cadence/internal.testActivityReturnEmptyString")
	var r2sString string
	err = f.Get(ctx, &r2String)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "", r2sString)

	f = ExecuteActivity(ctx, "go.uber.org/cadence/internal.testActivityReturnEmptyStruct")
	err = f.Get(ctx, &r2Struct)
	require.NoError(w.t, err, err)
	require.Equal(w.t, testActivityResult{}, r2Struct)

	return []byte("Done"), nil
}

// test testActivityNoResult
func testActivityNoResult(ctx context.Context, arg1 int, arg2 string) error {
	return nil
}

// test testActivityNoContextArg
func testActivityNoContextArg(arg1 int, arg2 string) error {
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
	ts := &WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	w := &activitiesCallingOptionsWorkflow{t: t}
	RegisterWorkflow(w.Execute)
	env.ExecuteWorkflow(w.Execute, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func testWorkflowSample(ctx Context, input []byte) (result []byte, err error) {
	return nil, nil
}

func testWorkflowMultipleArgs(ctx Context, arg1 int, arg2 string, arg3 bool) (result []byte, err error) {
	return nil, nil
}

func testWorkflowNoArgs(ctx Context) (result []byte, err error) {
	return nil, nil
}

func testWorkflowReturnInt(ctx Context) (result int, err error) {
	return 5, nil
}

func testWorkflowReturnString(ctx Context, arg1 int) (result string, err error) {
	return "Done", nil
}

type testWorkflowResult struct {
	V int
}

func testWorkflowReturnStruct(ctx Context, arg1 int) (result testWorkflowResult, err error) {
	return testWorkflowResult{}, nil
}

func testWorkflowReturnStructPtr(ctx Context, arg1 int) (result *testWorkflowResult, err error) {
	return &testWorkflowResult{}, nil
}

func testWorkflowReturnStructPtrPtr(ctx Context, arg1 int) (result **testWorkflowResult, err error) {
	return nil, nil
}

func TestRegisterVariousWorkflowTypes(t *testing.T) {
	RegisterWorkflow(testWorkflowSample)
	RegisterWorkflow(testWorkflowMultipleArgs)
	RegisterWorkflow(testWorkflowNoArgs)
	RegisterWorkflow(testWorkflowReturnInt)
	RegisterWorkflow(testWorkflowReturnString)
	RegisterWorkflow(testWorkflowReturnStruct)
	RegisterWorkflow(testWorkflowReturnStructPtr)
	RegisterWorkflow(testWorkflowReturnStructPtrPtr)
}

type testErrorDetails struct {
	T string
}

func TestActivityErrorWithDetails(t *testing.T) {
	a1 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCustomError("testReason", "testStringDetails")
		}}
	encResult, e := a1.Execute(context.Background(), testEncodeFunctionArgs(a1.fn, 1))

	err := deSerializeFunctionResult(a1.fn, encResult, nil)
	require.NoError(t, err)
	require.Error(t, e)
	errWD := e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	var strDetails string
	errWD.Details(&strDetails)
	require.Equal(t, "testStringDetails", strDetails)

	a2 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCustomError("testReason", testErrorDetails{T: "testErrorStack"})
		}}
	encResult, e = a2.Execute(context.Background(), testEncodeFunctionArgs(a2.fn, 1))
	err = deSerializeFunctionResult(a2.fn, encResult, nil)
	require.NoError(t, err)
	require.Error(t, e)
	errWD = e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	var td testErrorDetails
	errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack"}, td)

	a3 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult", NewCustomError("testReason", testErrorDetails{T: "testErrorStack3"})
		}}
	encResult, e = a3.Execute(context.Background(), testEncodeFunctionArgs(a3.fn, 1))
	var result string
	err = deSerializeFunctionResult(a3.fn, encResult, &result)
	require.NoError(t, err)
	require.Equal(t, "testResult", result)
	require.Error(t, e)
	errWD = e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack3"}, td)

	a4 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult4", NewCustomError("testReason", "testMultipleString", testErrorDetails{T: "testErrorStack4"})
		}}
	encResult, e = a4.Execute(context.Background(), testEncodeFunctionArgs(a4.fn, 1))
	err = deSerializeFunctionResult(a3.fn, encResult, &result)
	require.NoError(t, err)
	require.Equal(t, "testResult4", result)
	require.Error(t, e)
	errWD = e.(*CustomError)
	require.Equal(t, "testReason", errWD.Reason())
	var ed string
	errWD.Details(&ed, &td)
	require.Equal(t, "testMultipleString", ed)
	require.Equal(t, testErrorDetails{T: "testErrorStack4"}, td)
}

func TestActivityCancelledError(t *testing.T) {
	a1 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCanceledError("testCancelStringDetails")
		}}
	encResult, e := a1.Execute(context.Background(), testEncodeFunctionArgs(a1.fn, 1))
	err := deSerializeFunctionResult(a1.fn, encResult, nil)
	require.NoError(t, err)
	require.Error(t, e)
	errWD := e.(*CanceledError)
	var strDetails string
	errWD.Details(&strDetails)
	require.Equal(t, "testCancelStringDetails", strDetails)

	a2 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCanceledError(testErrorDetails{T: "testCancelErrorStack"})
		}}
	encResult, e = a2.Execute(context.Background(), testEncodeFunctionArgs(a2.fn, 1))
	err = deSerializeFunctionResult(a2.fn, encResult, nil)
	require.NoError(t, err)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	var td testErrorDetails
	errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testCancelErrorStack"}, td)

	a3 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult", NewCanceledError(testErrorDetails{T: "testErrorStack3"})
		}}
	encResult, e = a3.Execute(context.Background(), testEncodeFunctionArgs(a2.fn, 1))
	var r string
	err = deSerializeFunctionResult(a3.fn, encResult, &r)
	require.NoError(t, err)
	require.Equal(t, "testResult", r)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack3"}, td)

	a4 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult4", NewCanceledError("testMultipleString", testErrorDetails{T: "testErrorStack4"})
		}}
	encResult, e = a4.Execute(context.Background(), testEncodeFunctionArgs(a2.fn, 1))
	err = deSerializeFunctionResult(a3.fn, encResult, &r)
	require.NoError(t, err)
	require.Equal(t, "testResult4", r)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	var ed string
	errWD.Details(&ed, &td)
	require.Equal(t, "testMultipleString", ed)
	require.Equal(t, testErrorDetails{T: "testErrorStack4"}, td)
}

func TestActivityExecutionVariousTypes(t *testing.T) {
	a1 := activityExecutor{
		fn: func(ctx context.Context, arg1 string) (*testWorkflowResult, error) {
			return &testWorkflowResult{V: 1}, nil
		}}
	encResult, e := a1.Execute(context.Background(), testEncodeFunctionArgs(a1.fn, "test"))
	require.NoError(t, e)
	var r *testWorkflowResult
	err := deSerializeFunctionResult(a1.fn, encResult, &r)
	require.NoError(t, err)
	require.Equal(t, 1, r.V)

	a2 := activityExecutor{
		fn: func(ctx context.Context, arg1 *testWorkflowResult) (*testWorkflowResult, error) {
			return &testWorkflowResult{V: 2}, nil
		}}
	encResult, e = a2.Execute(context.Background(), testEncodeFunctionArgs(a2.fn, r))
	require.NoError(t, e)
	err = deSerializeFunctionResult(a2.fn, encResult, &r)
	require.NoError(t, err)
	require.Equal(t, 2, r.V)
}

type encodingTest struct {
	encoding encoding
	input    []interface{}
}

var encodingTests = []encodingTest{
	{&gobEncoding{}, []interface{}{"test"}},
	{&gobEncoding{}, []interface{}{"test1", "test2"}},
	{&gobEncoding{}, []interface{}{"test1", 1, false}},
	{&gobEncoding{}, []interface{}{"test1", testWorkflowResult{V: 10}, false}},
	{&gobEncoding{}, []interface{}{"test2", &testWorkflowResult{V: 20}, 4}},
}

// duplicate of GetHostEnvironment().registerType()
func testRegisterType(enc encoding, v interface{}) error {
	t := reflect.Indirect(reflect.ValueOf(v)).Type()
	if t.Kind() == reflect.Interface || t.Kind() == reflect.Ptr {
		return nil
	}
	arg := reflect.Zero(t).Interface()
	return enc.Register(arg)
}

func TestGobEncoding(t *testing.T) {
	for _, et := range encodingTests {
		for _, obj := range et.input {
			err := testRegisterType(et.encoding, obj)
			require.NoError(t, err, err)
		}
		data, err := et.encoding.Marshal(et.input)
		require.NoError(t, err, err)

		var result []interface{}
		for _, v := range et.input {
			arg := reflect.New(reflect.TypeOf(v)).Interface()
			result = append(result, arg)
		}
		err = et.encoding.Unmarshal(data, result)
		require.NoError(t, err, err)

		for i := 0; i < len(et.input); i++ {
			vat := reflect.ValueOf(result[i]).Elem().Interface()
			require.Equal(t, et.input[i], vat)
		}
	}
}

/*
var testWorkflowID1 = s.WorkflowExecution{WorkflowId: common.StringPtr("testWID"), RunId: common.StringPtr("runID")}
var testWorkflowID2 = s.WorkflowExecution{WorkflowId: common.StringPtr("testWID2"), RunId: common.StringPtr("runID2")}
var thriftEncodingTests = []encodingTest{
	{&thriftEncoding{}, []interface{}{&testWorkflowID1}},
	{&thriftEncoding{}, []interface{}{&testWorkflowID1, &testWorkflowID2}},
	{&thriftEncoding{}, []interface{}{&testWorkflowID1, &testWorkflowID2, &testWorkflowID1}},
}

// TODO: Disable until thriftrw encoding support is added to cadence client.(follow up change)
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
func testEncodeFunctionArgs(workflowFunc interface{}, args ...interface{}) []byte {
	err := getHostEnvironment().RegisterFnType(reflect.TypeOf(workflowFunc))
	if err != nil {
		fmt.Println(err)
		panic("Failed to register function types")
	}
	input, err := getHostEnvironment().encodeArgs(args)
	if err != nil {
		fmt.Println(err)
		panic("Failed to encode arguments")
	}
	return input
}
