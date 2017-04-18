package cadence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"sort"

	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/uber-common/bark"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/cadence-client/common"
	"github.com/uber-go/cadence-client/mocks"
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
}

func TestActivityRegistrationListener(t *testing.T) {
	require.Equal(t, 4, len(registeredActivities))
	expectedActivities := []string{
		"github.com/uber-go/cadence-client/client/cadence.testActivity",
		"github.com/uber-go/cadence-client/client/cadence.testActivityByteArgs",
		"github.com/uber-go/cadence-client/client/cadence.testActivityMultipleArgs",
		"github.com/uber-go/cadence-client/client/cadence.testActivityReturnString",
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

func getLogger() bark.Logger {
	formatter := &log.TextFormatter{}
	formatter.FullTimestamp = true
	log1 := log.New()
	//log1.Level = log.DebugLevel
	log1.Formatter = formatter
	return bark.NewLoggerFromLogrus(log1)
}

func testReplayWorkflow(ctx Context) error {
	ctx = WithActivityOptions(ctx, NewActivityOptions().
		WithTaskList("testTaskList").
		WithScheduleToStartTimeout(time.Second).
		WithStartToCloseTimeout(time.Second).
		WithScheduleToCloseTimeout(time.Second))
	_, err := ExecuteActivity(ctx, "testActivity")
	if err != nil {
		getLogger().Errorf("activity failed with error: %v", err)
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
	require.Contains(t, stackTrace, "cadence.ExecuteActivity")
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
	logger := getLogger()

	domain := "testDomain"

	workflowID := "w1"
	runID := "r1"
	activityType := "github.com/uber-go/cadence-client/client/cadence.testActivity"
	workflowType := "github.com/uber-go/cadence-client/client/cadence.sampleWorkflowExecute"
	activityID := "a1"
	taskList := "tl1"
	var startedEventID int64 = 10
	input, err := marshalFunctionArgs([]interface{}{})
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
	workerOptions := NewWorkerOptions().SetLogger(logger).SetMaxActivityExecutionRate(20)

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

	wfClient.CompleteActivity(nil, nil, nil)
	require.NotNil(t, completedRequest)

	wfClient.CompleteActivity(nil, nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	wfClient.CompleteActivity(nil, nil, errors.New(""))
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

	wfClient.RecordActivityHeartbeat(nil, nil)
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
	_, err = ExecuteActivity(ctx, testActivityByteArgs, input)
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, testActivityMultipleArgs, 2, "test", true)
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, testActivityNoResult, 2, "test")
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, testActivityNoContextArg, 2, "test")
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, testActivityNoError, 2, "test")
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, testActivityNoArgsAndNoResult)
	require.NoError(w.t, err, err)

	r, err := ExecuteActivity(ctx, testActivityReturnByteArray)
	require.NoError(w.t, err, err)
	require.Equal(w.t, []byte("testActivity"), r.([]byte))

	rInt, err := ExecuteActivity(ctx, testActivityReturnInt)
	require.NoError(w.t, err, err)
	require.Equal(w.t, 5, rInt.(int))

	rString, err := ExecuteActivity(ctx, testActivityReturnString)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString.(string))

	// By names.
	_, err = ExecuteActivity(ctx, "testActivityByteArgs", input)
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, "testActivityMultipleArgs", 2, "test", true)
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, "testActivityNoResult", 2, "test")
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, "testActivityNoContextArg", 2, "test")
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, "testActivityNoError", 2, "test")
	require.NoError(w.t, err, err)

	_, err = ExecuteActivity(ctx, "testActivityNoArgsAndNoResult")
	require.NoError(w.t, err, err)

	rString, err = ExecuteActivity(ctx, "github.com/uber-go/cadence-client/client/cadence.testActivityReturnString")
	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString.(string), rString)

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
		} else {
			r = testEncodeFunctionResult([]byte("test"))
		}
		callback := args.Get(1).(resultHandler)
		cbProcessor.Add(callback, r, nil)
	}).Times(16)

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

func TestRegisterVariousWorkflowTypes(t *testing.T) {
	RegisterWorkflow(testWorkflowSample)
	RegisterWorkflow(testWorkflowMultipleArgs)
	RegisterWorkflow(testWorkflowNoArgs)
	RegisterWorkflow(testWorkflowReturnInt)
	RegisterWorkflow(testWorkflowReturnString)
	RegisterWorkflow(testWorkflowReturnStruct)
	// TODO: Gob doesn't resolve pointers to full package hence conflicts with out pointer registration
	//err = RegisterWorkflow(testWorkflowReturnStructPtr)
	//require.NoError(t, err)
}

// Encode function result.
func testEncodeFunctionResult(r interface{}) []byte {
	if err := getHostEnvironment().Encoder().Register(r); err != nil {
		fmt.Println(err)
		panic("Failed to register")
	}
	fr := fnReturnSignature{Ret: r}
	result, err := getHostEnvironment().Encoder().Marshal(fr)
	if err != nil {
		fmt.Println(err)
		panic("Failed to Marshal")
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
	input, err := marshalFunctionArgs(args)
	if err != nil {
		fmt.Println(err)
		panic("Failed to encode arguments")
	}
	return input
}
