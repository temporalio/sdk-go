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
	"fmt"
	"reflect"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/uber-go/tally"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
	"go.uber.org/zap"
)

type (
	// EncodedValues is a type alias used to encapsulate/extract encoded arguments from workflow/activity.
	EncodedValues []byte

	// WorkflowTestSuite is the test suite to run unit tests for workflow/activity.
	WorkflowTestSuite struct {
		logger *zap.Logger
		scope  tally.Scope
	}

	// TestWorkflowEnvironment is the environment that you use to test workflow
	TestWorkflowEnvironment struct {
		mock.Mock
		impl *testWorkflowEnvironmentImpl
	}

	// TestActivityEnvironment is the environment that you use to test activity
	TestActivityEnvironment struct {
		impl *testWorkflowEnvironmentImpl
	}

	// MockCallWrapper is a wrapper to mock.Call. It offers the ability to wait on workflow's clock instead of wall clock.
	MockCallWrapper struct {
		call *mock.Call
		env  *TestWorkflowEnvironment

		runFn        func(args mock.Arguments)
		waitDuration time.Duration
	}
)

// Get extract data from encoded data to desired value type. valuePtr is pointer to the actual value type.
func (b EncodedValues) Get(valuePtr ...interface{}) error {
	if b == nil {
		return ErrNoData
	}
	return getHostEnvironment().decode(b, valuePtr)
}

// HasValues return whether there are values encoded.
func (b EncodedValues) HasValues() bool {
	return b != nil
}

// NewTestWorkflowEnvironment creates a new instance of TestWorkflowEnvironment. Use the returned TestWorkflowEnvironment
// to run your workflow in the test environment.
func (s *WorkflowTestSuite) NewTestWorkflowEnvironment() *TestWorkflowEnvironment {
	return &TestWorkflowEnvironment{impl: newTestWorkflowEnvironmentImpl(s)}
}

// NewTestActivityEnvironment creates a new instance of TestActivityEnvironment. Use the returned TestActivityEnvironment
// to run your activity in the test environment.
func (s *WorkflowTestSuite) NewTestActivityEnvironment() *TestActivityEnvironment {
	return &TestActivityEnvironment{impl: newTestWorkflowEnvironmentImpl(s)}
}

// SetLogger sets the logger for this WorkflowTestSuite. If you don't set logger, test suite will create a default logger
// with Debug level logging enabled.
func (s *WorkflowTestSuite) SetLogger(logger *zap.Logger) {
	s.logger = logger
}

// GetLogger gets the logger for this WorkflowTestSuite.
func (s *WorkflowTestSuite) GetLogger() *zap.Logger {
	return s.logger
}

// SetMetricsScope sets the metrics scope for this WorkflowTestSuite. If you don't set scope, test suite will use
// tally.NoopScope
func (s *WorkflowTestSuite) SetMetricsScope(scope tally.Scope) {
	s.scope = scope
}

// ExecuteActivity executes an activity. The tested activity will be executed synchronously in the calling goroutinue.
// Caller should use encoded.Value.Get() to extract strong typed result value.
func (t *TestActivityEnvironment) ExecuteActivity(activityFn interface{}, args ...interface{}) (encoded.Value, error) {
	return t.impl.executeActivity(activityFn, args...)
}

// ExecuteLocalActivity executes a local activity. The tested activity will be executed synchronously in the calling goroutinue.
// Caller should use encoded.Value.Get() to extract strong typed result value.
func (t *TestActivityEnvironment) ExecuteLocalActivity(activityFn interface{}, args ...interface{}) (encoded.Value, error) {
	return t.impl.executeLocalActivity(activityFn, args...)
}

// SetWorkerOptions sets the WorkerOptions that will be use by TestActivityEnvironment. TestActivityEnvironment will
// use options of Identity, MetricsScope and BackgroundActivityContext on the WorkerOptions. Other options are ignored.
// Note: WorkerOptions is defined in internal package, use public type worker.Options instead.
func (t *TestActivityEnvironment) SetWorkerOptions(options WorkerOptions) *TestActivityEnvironment {
	t.impl.setWorkerOptions(options)
	return t
}

// SetStartTime sets the start time of the workflow. This is optional, default start time will be the wall clock time when
// workflow starts. Start time is the workflow.Now(ctx) time at the beginning of the workflow.
func (t *TestWorkflowEnvironment) SetStartTime(startTime time.Time) {
	t.impl.startTime = startTime
}

// OnActivity setup a mock call for activity. Parameter activity must be activity function (func) or activity name (string).
// You must call Return() with appropriate parameters on the returned *MockCallWrapper instance. The supplied parameters to
// the Return() call should either be a function that has exact same signature as the mocked activity, or it should be
// mock values with the same types as the mocked activity function returns.
// Example: assume the activity you want to mock has function signature as:
//   func MyActivity(ctx context.Context, msg string) (string, error)
// You can mock it by return a function with exact same signature:
//   t.OnActivity(MyActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, msg string) (string, error) {
//      // your mock function implementation
//      return "", nil
//   })
// OR return mock values with same types as activity function's return types:
//   t.OnActivity(MyActivity, mock.Anything, mock.Anything).Return("mock_result", nil)
func (t *TestWorkflowEnvironment) OnActivity(activity interface{}, args ...interface{}) *MockCallWrapper {
	fType := reflect.TypeOf(activity)
	var call *mock.Call
	switch fType.Kind() {
	case reflect.Func:
		fnType := reflect.TypeOf(activity)
		if err := validateFnFormat(fnType, false); err != nil {
			panic(err)
		}
		fnName := getFunctionName(activity)
		if alias, ok := getHostEnvironment().getActivityAlias(fnName); ok {
			fnName = alias
		}
		call = t.Mock.On(fnName, args...)

	case reflect.String:
		call = t.Mock.On(activity.(string), args...)
	default:
		panic("activity must be function or string")
	}

	return t.wrapCall(call)
}

// ErrMockStartChildWorkflowFailed is special error used to indicate the mocked child workflow should fail to start.
// This error is also exposed as public as testsuite.ErrMockStartChildWorkflowFailed
var ErrMockStartChildWorkflowFailed = fmt.Errorf("start child workflow failed: %v", shared.ChildWorkflowExecutionFailedCauseWorkflowAlreadyRunning)

// OnWorkflow setup a mock call for workflow. Parameter workflow must be workflow function (func) or workflow name (string).
// You must call Return() with appropriate parameters on the returned *MockCallWrapper instance. The supplied parameters to
// the Return() call should either be a function that has exact same signature as the mocked workflow, or it should be
// mock values with the same types as the mocked workflow function returns.
// Example: assume the workflow you want to mock has function signature as:
//   func MyChildWorkflow(ctx workflow.Context, msg string) (string, error)
// You can mock it by return a function with exact same signature:
//   t.OnWorkflow(MyChildWorkflow, mock.Anything, mock.Anything).Return(func(ctx workflow.Context, msg string) (string, error) {
//      // your mock function implementation
//      return "", nil
//   })
// OR return mock values with same types as workflow function's return types:
//   t.OnWorkflow(MyChildWorkflow, mock.Anything, mock.Anything).Return("mock_result", nil)
// You could also setup mock to simulate start child workflow failure case by returning ErrMockStartChildWorkflowFailed
// as error.
func (t *TestWorkflowEnvironment) OnWorkflow(workflow interface{}, args ...interface{}) *MockCallWrapper {
	fType := reflect.TypeOf(workflow)
	var call *mock.Call
	switch fType.Kind() {
	case reflect.Func:
		fnType := reflect.TypeOf(workflow)
		if err := validateFnFormat(fnType, true); err != nil {
			panic(err)
		}
		fnName := getFunctionName(workflow)
		if alias, ok := getHostEnvironment().getWorkflowAlias(fnName); ok {
			fnName = alias
		}
		call = t.Mock.On(fnName, args...)
	case reflect.String:
		call = t.Mock.On(workflow.(string), args...)
	default:
		panic("activity must be function or string")
	}

	return t.wrapCall(call)
}

const mockMethodForSignalExternalWorkflow = "workflow.SignalExternalWorkflow"
const mockMethodForRequestCancelExternalWorkflow = "workflow.RequestCancelExternalWorkflow"

// OnSignalExternalWorkflow setup a mock for sending signal to external workflow.
// This TestWorkflowEnvironment handles sending signals between the workflows that are started from the root workflow.
// For example, sending signals between parent and child workflows. Or sending signals between 2 child workflows.
// However, it does not know what to do if your tested workflow code is sending signal to external unknown workflows.
// In that case, you will need to setup mock for those signal calls.
// Some examples of how to setup mock:
//
// * mock for specific target workflow that matches specific signal name and signal data
// 	 env.OnSignalExternalWorkflow("test-domain", "test-workflow-id1", "test-runid1", "test-signal", "test-data").Return(nil).Once()
// * mock for anything and succeed the send
// 	 env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
// * mock for anything and fail the send
// 	 env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("unknown external workflow")).Once()
// * mock function for SignalExternalWorkflow
//   env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
//     func(domainName, workflowID, runID, signalName string, arg interface{}) error {
//       // you can do differently based on the parameters
//       return nil
//     })
func (t *TestWorkflowEnvironment) OnSignalExternalWorkflow(domainName, workflowID, runID, signalName, arg interface{}) *MockCallWrapper {
	call := t.Mock.On(mockMethodForSignalExternalWorkflow, domainName, workflowID, runID, signalName, arg)
	return t.wrapCall(call)
}

// OnRequestCancelExternalWorkflow setup a mock for cancellation of external workflow.
// This TestWorkflowEnvironment handles cancellation of workflows that are started from the root workflow.
// For example, cancellation sent from parent to child workflows. Or cancellation between 2 child workflows.
// However, it does not know what to do if your tested workflow code is sending cancellation to external unknown workflows.
// In that case, you will need to setup mock for those signal calls.
// Some examples of how to setup mock:
//
// * mock for specific target workflow that matches specific workflow ID and run ID
// 	 env.OnSignalExternalWorkflow("test-domain", "test-workflow-id1", "test-runid1").Return(nil).Once()
// * mock for anything and succeed the cancellation
// 	 env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
// * mock for anything and fail the cancellation
// 	 env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("unknown external workflow")).Once()
// * mock function for SignalExternalWorkflow
//   env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything).Return(
//     func(domainName, workflowID, runID) error {
//       // you can do differently based on the parameters
//       return nil
//     })
func (t *TestWorkflowEnvironment) OnRequestCancelExternalWorkflow(domainName, workflowID, runID string) *MockCallWrapper {
	call := t.Mock.On(mockMethodForRequestCancelExternalWorkflow, domainName, workflowID, runID)
	return t.wrapCall(call)
}

func (t *TestWorkflowEnvironment) wrapCall(call *mock.Call) *MockCallWrapper {
	callWrapper := &MockCallWrapper{call: call, env: t}
	call.Run(t.impl.getMockRunFn(callWrapper))
	return callWrapper
}

// Once indicates that that the mock should only return the value once.
func (c *MockCallWrapper) Once() *MockCallWrapper {
	return c.Times(1)
}

// Twice indicates that that the mock should only return the value twice.
func (c *MockCallWrapper) Twice() *MockCallWrapper {
	return c.Times(2)
}

// Times indicates that that the mock should only return the indicated number of times.
func (c *MockCallWrapper) Times(i int) *MockCallWrapper {
	c.call.Times(i)
	return c
}

// Run sets a handler to be called before returning. It can be used when mocking a method such as unmarshalers that
// takes a pointer to a struct and sets properties in such struct.
func (c *MockCallWrapper) Run(fn func(args mock.Arguments)) *MockCallWrapper {
	c.runFn = fn
	return c
}

// After sets how long to wait on workflow's clock before the mock call returns.
func (c *MockCallWrapper) After(d time.Duration) *MockCallWrapper {
	c.waitDuration = d
	return c
}

// Return specifies the return arguments for the expectation.
func (c *MockCallWrapper) Return(returnArguments ...interface{}) *MockCallWrapper {
	c.call.Return(returnArguments...)
	return c
}

// ExecuteWorkflow executes a workflow, wait until workflow complete. It will fail the test if workflow is blocked and
// cannot complete within TestTimeout (set by SetTestTimeout()).
func (t *TestWorkflowEnvironment) ExecuteWorkflow(workflowFn interface{}, args ...interface{}) {
	t.impl.mock = &t.Mock
	t.impl.executeWorkflow(workflowFn, args...)
}

// Now returns the current workflow time (a.k.a workflow.Now() time) of this TestWorkflowEnvironment.
func (t *TestWorkflowEnvironment) Now() time.Time {
	return t.impl.Now()
}

// SetWorkerOptions sets the WorkerOptions for TestWorkflowEnvironment. TestWorkflowEnvironment will use options set by
// use options of Identity, MetricsScope and BackgroundActivityContext on the WorkerOptions. Other options are ignored.
// Note: WorkerOptions is defined in internal package, use public type worker.Options instead.
func (t *TestWorkflowEnvironment) SetWorkerOptions(options WorkerOptions) *TestWorkflowEnvironment {
	t.impl.setWorkerOptions(options)
	return t
}

// SetTestTimeout sets the wall clock timeout for this workflow test run. When test timeout happen, it means workflow is
// blocked and cannot make progress. This could happen if workflow is waiting for activity result for too long.
// This is real wall clock time, not the workflow time (a.k.a workflow.Now() time).
func (t *TestWorkflowEnvironment) SetTestTimeout(idleTimeout time.Duration) *TestWorkflowEnvironment {
	t.impl.testTimeout = idleTimeout
	return t
}

// SetOnActivityStartedListener sets a listener that will be called before activity starts execution.
// Note: ActivityInfo is defined in internal package, use public type activity.Info instead.
func (t *TestWorkflowEnvironment) SetOnActivityStartedListener(
	listener func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values)) *TestWorkflowEnvironment {
	t.impl.onActivityStartedListener = listener
	return t
}

// SetOnActivityCompletedListener sets a listener that will be called after an activity is completed.
// Note: ActivityInfo is defined in internal package, use public type activity.Info instead.
func (t *TestWorkflowEnvironment) SetOnActivityCompletedListener(
	listener func(activityInfo *ActivityInfo, result encoded.Value, err error)) *TestWorkflowEnvironment {
	t.impl.onActivityCompletedListener = listener
	return t
}

// SetOnActivityCanceledListener sets a listener that will be called after an activity is canceled.
// Note: ActivityInfo is defined in internal package, use public type activity.Info instead.
func (t *TestWorkflowEnvironment) SetOnActivityCanceledListener(
	listener func(activityInfo *ActivityInfo)) *TestWorkflowEnvironment {
	t.impl.onActivityCanceledListener = listener
	return t
}

// SetOnActivityHeartbeatListener sets a listener that will be called when activity heartbeat.
// Note: ActivityInfo is defined in internal package, use public type activity.Info instead.
func (t *TestWorkflowEnvironment) SetOnActivityHeartbeatListener(
	listener func(activityInfo *ActivityInfo, details encoded.Values)) *TestWorkflowEnvironment {
	t.impl.onActivityHeartbeatListener = listener
	return t
}

// SetOnChildWorkflowStartedListener sets a listener that will be called before a child workflow starts execution.
// Note: WorkflowInfo is defined in internal package, use public type workflow.Info instead.
func (t *TestWorkflowEnvironment) SetOnChildWorkflowStartedListener(
	listener func(workflowInfo *WorkflowInfo, ctx Context, args encoded.Values)) *TestWorkflowEnvironment {
	t.impl.onChildWorkflowStartedListener = listener
	return t
}

// SetOnChildWorkflowCompletedListener sets a listener that will be called after a child workflow is completed.
// Note: WorkflowInfo is defined in internal package, use public type workflow.Info instead.
func (t *TestWorkflowEnvironment) SetOnChildWorkflowCompletedListener(
	listener func(workflowInfo *WorkflowInfo, result encoded.Value, err error)) *TestWorkflowEnvironment {
	t.impl.onChildWorkflowCompletedListener = listener
	return t
}

// SetOnChildWorkflowCanceledListener sets a listener that will be called when a child workflow is canceled.
// Note: WorkflowInfo is defined in internal package, use public type workflow.Info instead.
func (t *TestWorkflowEnvironment) SetOnChildWorkflowCanceledListener(
	listener func(workflowInfo *WorkflowInfo)) *TestWorkflowEnvironment {
	t.impl.onChildWorkflowCanceledListener = listener
	return t
}

// SetOnTimerScheduledListener sets a listener that will be called before a timer is scheduled.
func (t *TestWorkflowEnvironment) SetOnTimerScheduledListener(
	listener func(timerID string, duration time.Duration)) *TestWorkflowEnvironment {
	t.impl.onTimerScheduledListener = listener
	return t
}

// SetOnTimerFiredListener sets a listener that will be called after a timer is fired.
func (t *TestWorkflowEnvironment) SetOnTimerFiredListener(listener func(timerID string)) *TestWorkflowEnvironment {
	t.impl.onTimerFiredListener = listener
	return t
}

// SetOnTimerCancelledListener sets a listener that will be called after a timer is cancelled
func (t *TestWorkflowEnvironment) SetOnTimerCancelledListener(listener func(timerID string)) *TestWorkflowEnvironment {
	t.impl.onTimerCancelledListener = listener
	return t
}

// SetOnLocalActivityStartedListener sets a listener that will be called before local activity starts execution.
// Note: ActivityInfo is defined in internal package, use public type activity.Info instead.
func (t *TestWorkflowEnvironment) SetOnLocalActivityStartedListener(
	listener func(activityInfo *ActivityInfo, ctx context.Context, args []interface{})) *TestWorkflowEnvironment {
	t.impl.onLocalActivityStartedListener = listener
	return t
}

// SetOnLocalActivityCompletedListener sets a listener that will be called after local activity is completed.
// Note: ActivityInfo is defined in internal package, use public type activity.Info instead.
func (t *TestWorkflowEnvironment) SetOnLocalActivityCompletedListener(
	listener func(activityInfo *ActivityInfo, result encoded.Value, err error)) *TestWorkflowEnvironment {
	t.impl.onLocalActivityCompletedListener = listener
	return t
}

// SetOnLocalActivityCanceledListener sets a listener that will be called after local activity is canceled.
// Note: ActivityInfo is defined in internal package, use public type activity.Info instead.
func (t *TestWorkflowEnvironment) SetOnLocalActivityCanceledListener(
	listener func(activityInfo *ActivityInfo)) *TestWorkflowEnvironment {
	t.impl.onLocalActivityCanceledListener = listener
	return t
}

// IsWorkflowCompleted check if test is completed or not
func (t *TestWorkflowEnvironment) IsWorkflowCompleted() bool {
	return t.impl.isTestCompleted
}

// GetWorkflowResult extracts the encoded result from test workflow, it returns error if the extraction failed.
func (t *TestWorkflowEnvironment) GetWorkflowResult(valuePtr interface{}) error {
	if !t.impl.isTestCompleted {
		panic("workflow is not completed")
	}
	if t.impl.testResult == nil {
		return nil
	}
	return t.impl.testResult.Get(valuePtr)
}

// GetWorkflowError return the error from test workflow
func (t *TestWorkflowEnvironment) GetWorkflowError() error {
	return t.impl.testError
}

// CompleteActivity complete an activity that had returned activity.ErrResultPending error
func (t *TestWorkflowEnvironment) CompleteActivity(taskToken []byte, result interface{}, err error) error {
	return t.impl.CompleteActivity(taskToken, result, err)
}

// CancelWorkflow requests cancellation (through workflow Context) to the currently running test workflow.
func (t *TestWorkflowEnvironment) CancelWorkflow() {
	t.impl.cancelWorkflow(func(result []byte, err error) {})
}

// SignalWorkflow sends signal to the currently running test workflow.
func (t *TestWorkflowEnvironment) SignalWorkflow(name string, input interface{}) {
	t.impl.signalWorkflow(name, input)
}

// QueryWorkflow queries to the currently running test workflow and returns result synchronously.
func (t *TestWorkflowEnvironment) QueryWorkflow(queryType string, args ...interface{}) (encoded.Value, error) {
	return t.impl.queryWorkflow(queryType, args...)
}

// RegisterDelayedCallback creates a new timer with specified delayDuration using workflow clock (not wall clock). When
// the timer fires, the callback will be called. By default, this test suite uses mock clock which automatically move
// forward to fire next timer when workflow is blocked. Use this API to make some event (like activity completion,
// signal or workflow cancellation) at desired time.
func (t *TestWorkflowEnvironment) RegisterDelayedCallback(callback func(), delayDuration time.Duration) {
	t.impl.registerDelayedCallback(callback, delayDuration)
}

// SetActivityTaskList set the affinity between activity and tasklist. By default, activity can be invoked by any tasklist
// in this test environment. Use this SetActivityTaskList() to set affinity between activity and a tasklist. Once
// activity is set to a particular tasklist, that activity will only be available to that tasklist.
func (t *TestWorkflowEnvironment) SetActivityTaskList(tasklist string, activityFn ...interface{}) {
	t.impl.setActivityTaskList(tasklist, activityFn...)
}
