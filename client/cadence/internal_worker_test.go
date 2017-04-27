package cadence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/cadence-client/common"
	"github.com/uber-go/cadence-client/mocks"
	"go.uber.org/zap"
)

// Used to test registration listeners
var registeredActivities []string
var registeredWorkflows []string

func init() {
	RegisterWorkflow(sampleWorkflowExecute)
	AddWorkflowRegistrationInterceptor(func(workflowName string, workflow interface{}) (string, interface{}) {
		registeredWorkflows = append(registeredWorkflows, workflowName)
		return workflowName, workflow
	})
	RegisterWorkflow(testReplayWorkflow)

	RegisterActivity(testActivity)
	RegisterActivity(testActivityByteArgs)
	AddActivityRegistrationInterceptor(func(activityName string, activity interface{}) (string, interface{}) {
		registeredActivities = append(registeredActivities, activityName)
		return activityName, activity
	})
	RegisterActivity(testActivityMultipleArgs)
	RegisterActivity(testActivityReturnString)
	RegisterActivity(testActivityReturnEmptyString)
	RegisterActivity(testActivityReturnEmptyStruct)
}

func TestActivityRegistrationListener(t *testing.T) {
	require.Equal(t, 6, len(registeredActivities))
	expectedActivities := []string{
		"github.com/uber-go/cadence-client/client/cadence.testActivity",
		"github.com/uber-go/cadence-client/client/cadence.testActivityByteArgs",
		"github.com/uber-go/cadence-client/client/cadence.testActivityMultipleArgs",
		"github.com/uber-go/cadence-client/client/cadence.testActivityReturnString",
		"github.com/uber-go/cadence-client/client/cadence.testActivityReturnEmptyString",
		"github.com/uber-go/cadence-client/client/cadence.testActivityReturnEmptyStruct",
	}
	sort.Strings(expectedActivities)
	expected := strings.Join(expectedActivities, ",")
	sort.Strings(registeredActivities)
	registered := strings.Join(registeredActivities, ",")
	require.Equal(t, expected, registered)
}

func TestWorkflowRegistrationListener(t *testing.T) {
	require.Equal(t, 2, len(registeredWorkflows))
	expectedWorkflows := []string{
		"github.com/uber-go/cadence-client/client/cadence.sampleWorkflowExecute",
		"github.com/uber-go/cadence-client/client/cadence.testReplayWorkflow",
	}
	sort.Strings(expectedWorkflows)
	expected := strings.Join(expectedWorkflows, ",")
	sort.Strings(registeredWorkflows)
	registered := strings.Join(registeredWorkflows, ",")
	require.Equal(t, expected, registered)
}

func getLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func testReplayWorkflow(ctx Context) error {
	ctx = WithActivityOptions(ctx, NewActivityOptions().
		WithTaskList("testTaskList").
		WithScheduleToStartTimeout(time.Second).
		WithStartToCloseTimeout(time.Second).
		WithScheduleToCloseTimeout(time.Second))
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

	workflowType := "github.com/uber-go/cadence-client/client/cadence.testReplayWorkflow"
	workflowID := "testID"
	runID := "testRunID"

	task := &s.PollForDecisionTaskResponse{
		WorkflowExecution:      &s.WorkflowExecution{WorkflowId: &workflowID, RunId: &runID},
		WorkflowType:           &s.WorkflowType{Name: &workflowType},
		History:                &s.History{Events: testEvents},
		PreviousStartedEventId: common.Int64Ptr(5),
	}

	r := NewWorkflowTaskHandler("identity", logger)
	_, stackTrace, err := r.ProcessWorkflowTask(task, true)
	require.NoError(t, err)
	require.NotEmpty(t, stackTrace, stackTrace)
	require.Contains(t, stackTrace, "cadence.(*decodeFutureImpl).Get")
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
	service := new(mocks.TChanWorkflowService)
	//logger := getLogger()

	domain := "testDomain"

	workflowID := "w1"
	runID := "r1"
	activityType := "github.com/uber-go/cadence-client/client/cadence.testActivity"
	workflowType := "github.com/uber-go/cadence-client/client/cadence.sampleWorkflowExecute"
	activityID := "a1"
	taskList := "tl1"
	var startedEventID int64 = 10
	input, err := getHostEnvironment().encodeArgs([]interface{}{})
	require.NoError(t, err)

	activityTask := &s.PollForActivityTaskResponse{
		TaskToken:         []byte("taskToken1"),
		WorkflowExecution: &s.WorkflowExecution{WorkflowId: &workflowID, RunId: &runID},
		ActivityType:      &s.ActivityType{Name: &activityType},
		StartedEventId:    &startedEventID,
		Input:             input,
		ActivityId:        &activityID,
	}
	decisionTask := &s.PollForDecisionTaskResponse{
		TaskToken:              []byte("taskToken1"),
		WorkflowExecution:      &s.WorkflowExecution{WorkflowId: &workflowID, RunId: &runID},
		WorkflowType:           &s.WorkflowType{Name: &workflowType},
		StartedEventId:         &startedEventID,
		PreviousStartedEventId: &startedEventID,
		History: &s.History{
			Events: []*s.HistoryEvent{
				{WorkflowExecutionStartedEventAttributes: &s.WorkflowExecutionStartedEventAttributes{
					TaskList: &s.TaskList{Name: &taskList},
				}},
			},
		},
	}
	// mocks
	service.On("PollForActivityTask", mock.Anything, mock.Anything).Return(activityTask, nil)
	service.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(decisionTask, nil)
	service.On("RespondDecisionTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	// Configure worker options.
	workerOptions := NewWorkerOptions().SetMaxActivityExecutionRate(20)

	// Start Worker.
	worker := NewWorker(
		service,
		domain,
		"testGroupName2",
		workerOptions)
	err = worker.Start()
	require.NoError(t, err)
	time.Sleep(time.Millisecond * 200)
	worker.Stop()
	service.AssertExpectations(t)
}

func TestCompleteActivity(t *testing.T) {
	mockService := new(mocks.TChanWorkflowService)
	domain := "testDomain"
	wfClient := NewClient(mockService, domain, nil)
	var completedRequest, canceledRequest, failedRequest interface{}
	mockService.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil).Run(
		func(args mock.Arguments) {
			completedRequest = args.Get(1).(*s.RespondActivityTaskCompletedRequest)
		})
	mockService.On("RespondActivityTaskCanceled", mock.Anything, mock.Anything).Return(nil).Run(
		func(args mock.Arguments) {
			canceledRequest = args.Get(1).(*s.RespondActivityTaskCanceledRequest)
		})
	mockService.On("RespondActivityTaskFailed", mock.Anything, mock.Anything).Return(nil).Run(
		func(args mock.Arguments) {
			failedRequest = args.Get(1).(*s.RespondActivityTaskFailedRequest)
		})

	wfClient.CompleteActivity(nil, "testFunc", nil, nil)
	require.NotNil(t, completedRequest)

	wfClient.CompleteActivity(nil, "testFunc", nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	wfClient.CompleteActivity(nil, "testFunc", nil, errors.New(""))
	require.NotNil(t, failedRequest)
}

func TestRecordActivityHeartbeat(t *testing.T) {
	mockService := new(mocks.TChanWorkflowService)
	domain := "testDomain"
	wfClient := NewClient(mockService, domain, nil)
	var heartbeatRequest *s.RecordActivityTaskHeartbeatRequest
	cancelRequested := false
	heartbeatResponse := s.RecordActivityTaskHeartbeatResponse{CancelRequested: &cancelRequested}
	mockService.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).Return(&heartbeatResponse, nil).
		Run(func(args mock.Arguments) {
			heartbeatRequest = args.Get(1).(*s.RecordActivityTaskHeartbeatRequest)
		})

	wfClient.RecordActivityHeartbeat(nil)
	wfClient.RecordActivityHeartbeat(nil, "testStack", "customerObjects", 4)
	require.NotNil(t, heartbeatRequest)
}

func testEncodeFunction(t *testing.T, f interface{}, args ...interface{}) string {
	s := fnSignature{Args: args}
	input, err := getHostEnvironment().Encoder().Marshal(s)
	require.NoError(t, err, err)
	require.NotNil(t, input)

	var s2 fnSignature
	err = getHostEnvironment().Encoder().Unmarshal(input, &s2)
	require.NoError(t, err, err)

	targetArgs := []reflect.Value{}
	for _, arg := range s2.Args {
		targetArgs = append(targetArgs, reflect.ValueOf(arg))
	}
	fnValue := reflect.ValueOf(f)
	retValues := fnValue.Call(targetArgs)
	return retValues[0].Interface().(string)
}

func testEncodeWithName(t *testing.T, args ...interface{}) {
	s := fnSignature{Args: args}
	input, err := getHostEnvironment().Encoder().Marshal(s)
	require.NoError(t, err, err)
	require.NotNil(t, input)

	var s2 fnSignature
	err = getHostEnvironment().Encoder().Unmarshal(input, &s2)
	require.NoError(t, err, err)

	require.Equal(t, len(s.Args), len(s2.Args))
	require.Equal(t, s.Args, s2.Args)
}

func TestEncoder(t *testing.T) {
	testEncodeWithName(t, 2, 3)
	testEncodeWithName(t, nil)
	testEncodeWithName(t)

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
	ctx = WithActivityOptions(ctx, NewActivityOptions().
		WithTaskList("exampleTaskList").
		WithScheduleToStartTimeout(10*time.Second).
		WithStartToCloseTimeout(5*time.Second).
		WithScheduleToCloseTimeout(10*time.Second))

	// By functions.
	err = ExecuteActivity(ctx, testActivityByteArgs, input).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityMultipleArgs, 2, "test", true).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoResult, 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoContextArg, 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoError, 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoArgsAndNoResult).Get(ctx, nil)
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

	// By names.
	err = ExecuteActivity(ctx, "testActivityByteArgs", input).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityMultipleArgs", 2, "test", true).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoResult", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoContextArg", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoError", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoArgsAndNoResult").Get(ctx, nil)
	require.NoError(w.t, err, err)

	f = ExecuteActivity(ctx, "github.com/uber-go/cadence-client/client/cadence.testActivityReturnString")
	err = f.Get(ctx, &rString)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString, rString)

	f = ExecuteActivity(ctx, "github.com/uber-go/cadence-client/client/cadence.testActivityReturnEmptyString")
	var r2sString string
	err = f.Get(ctx, &r2String)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "", r2sString)

	f = ExecuteActivity(ctx, "github.com/uber-go/cadence-client/client/cadence.testActivityReturnEmptyStruct")
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

// test testActivityNoError
func testActivityNoError(arg1 int, arg2 string) {
	return
}

// test testActivityNoError
func testActivityNoArgsAndNoResult() {
	return
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

type testActivityResult struct{}

// testActivityReturnEmptyStruct
func testActivityReturnEmptyStruct() (testActivityResult, error) {
	// Return is mocked to retrun nil from server.
	// expect to convert it to appropriate default value.
	return testActivityResult{}, nil
}

func TestVariousActivitySchedulingOption(t *testing.T) {
	w := newWorkflowDefinition(&activitiesCallingOptionsWorkflow{t: t})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	cbProcessor := newAsyncTestCallbackProcessor()

	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		params := args.Get(0).(executeActivityParameters)
		var r []byte
		if strings.Contains(params.ActivityType.Name, "testActivityReturnByteArray") {
			r = testEncodeFunctionResult([]byte("testActivity"))
		} else if strings.Contains(params.ActivityType.Name, "testActivityReturnInt") {
			r = testEncodeFunctionResult(5)
		} else if strings.Contains(params.ActivityType.Name, "testActivityReturnString") {
			r = testEncodeFunctionResult("testActivity")
		} else if strings.Contains(params.ActivityType.Name, "testActivityReturnEmptyString") ||
			strings.Contains(params.ActivityType.Name, "testActivityReturnEmptyStruct") {
			r = nil
		} else {
			r = testEncodeFunctionResult([]byte("test"))
		}
		callback := args.Get(1).(resultHandler)
		cbProcessor.Add(callback, r, nil)
	}).Times(20)

	ctx.On("Complete", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		if args.Get(1) != nil {
			err := args.Get(1).(ErrorWithDetails)
			var details []byte
			err.Details(&details)
			fmt.Printf("ErrorWithDetails: %v, Stack: %v \n", err.Reason(), string(details))
		}
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte(""))

	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)
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
			return NewErrorWithDetails("testReason", "testStringDetails")
		}}
	encResult, e := a1.Execute(context.Background(), testEncodeFunctionArgs(a1.fn, 1))

	err := deSerializeFunctionResult(a1.fn, encResult, nil)
	require.NoError(t, err)
	require.Error(t, e)
	errWD := e.(ErrorWithDetails)
	require.Equal(t, "testReason", errWD.Reason())
	var strDetails string
	errWD.Details(&strDetails)
	require.Equal(t, "testStringDetails", strDetails)

	a2 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewErrorWithDetails("testReason", testErrorDetails{T: "testErrorStack"})
		}}
	encResult, e = a2.Execute(context.Background(), testEncodeFunctionArgs(a2.fn, 1))
	err = deSerializeFunctionResult(a2.fn, encResult, nil)
	require.NoError(t, err)
	require.Error(t, e)
	errWD = e.(ErrorWithDetails)
	require.Equal(t, "testReason", errWD.Reason())
	var td testErrorDetails
	errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack"}, td)

	a3 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult", NewErrorWithDetails("testReason", testErrorDetails{T: "testErrorStack3"})
		}}
	encResult, e = a3.Execute(context.Background(), testEncodeFunctionArgs(a3.fn, 1))
	var result string
	err = deSerializeFunctionResult(a3.fn, encResult, &result)
	require.NoError(t, err)
	require.Equal(t, "testResult", result)
	require.Error(t, e)
	errWD = e.(ErrorWithDetails)
	require.Equal(t, "testReason", errWD.Reason())
	errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack3"}, td)

	a4 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult4", NewErrorWithDetails("testReason", "testMultipleString", testErrorDetails{T: "testErrorStack4"})
		}}
	encResult, e = a4.Execute(context.Background(), testEncodeFunctionArgs(a4.fn, 1))
	err = deSerializeFunctionResult(a3.fn, encResult, &result)
	require.NoError(t, err)
	require.Equal(t, "testResult4", result)
	require.Error(t, e)
	errWD = e.(ErrorWithDetails)
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
	errWD := e.(CanceledError)
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
	errWD = e.(CanceledError)
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
	errWD = e.(CanceledError)
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
	errWD = e.(CanceledError)
	var ed string
	errWD.Details(&ed, &td)
	require.Equal(t, "testMultipleString", ed)
	require.Equal(t, testErrorDetails{T: "testErrorStack4"}, td)
}

// Encode function result.
func testEncodeFunctionResult(r interface{}) []byte {
	result, err := getHostEnvironment().encodeArg(r)
	if err != nil {
		fmt.Println(err)
		panic("Failed to encode")
	}
	return result
}

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
