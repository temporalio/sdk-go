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

package cadence

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

type (
	// EncodedValues is a type alias used to encapsulate/extract encoded arguments from workflow/activity.
	EncodedValues []byte

	// WorkflowTestSuite is the test suite to run unit tests for workflow/activity.
	WorkflowTestSuite struct {
		logger  *zap.Logger
		hostEnv *hostEnvImpl
		locker  sync.Mutex
	}

	// TestWorkflowEnvironment is the environment that you use to test workflow
	TestWorkflowEnvironment struct {
		mock.Mock
		impl *testWorkflowEnvironmentImpl
	}

	// TestActivityEnviornment is the environment that you use to test activity
	TestActivityEnviornment struct {
		impl *testWorkflowEnvironmentImpl
	}
)

// Get extract data from encoded data to desired value type. valuePtr is pointer to the actual value type.
func (b EncodedValues) Get(valuePtr ...interface{}) error {
	return getHostEnvironment().decode(b, valuePtr)
}

func (s *WorkflowTestSuite) initIfNotDoneYet() {
	if s.hostEnv != nil {
		return
	}
	s.locker.Lock()
	if s.hostEnv == nil {
		s.hostEnv = &hostEnvImpl{
			workflowFuncMap: make(map[string]interface{}),
			activityFuncMap: make(map[string]interface{}),
			encoding:        gobEncoding{},
		}
	}
	s.locker.Unlock()
}

// NewTestWorkflowEnvironment creates a new instance of TestWorkflowEnvironment. You can use the returned TestWorkflowEnvironment
// to run your workflow in the test environment.
func (s *WorkflowTestSuite) NewTestWorkflowEnvironment() *TestWorkflowEnvironment {
	s.initIfNotDoneYet()
	return &TestWorkflowEnvironment{impl: newTestWorkflowEnvironmentImpl(s)}
}

// NewTestActivityEnvironment creates a new instance of TestActivityEnviornment. You can use the returned TestActivityEnviornment
// to run your activity in the test environment.
func (s *WorkflowTestSuite) NewTestActivityEnvironment() *TestActivityEnviornment {
	s.initIfNotDoneYet()
	return &TestActivityEnviornment{impl: newTestWorkflowEnvironmentImpl(s)}
}

// RegisterWorkflow registers a workflow that could be used by tests of this WorkflowTestSuite instance. All workflow registered
// via cadence.RegisterWorkflow() are still valid and will be available to all tests of all instance of WorkflowTestSuite.
// In the context of unit tests, workflow registration is only required if you are invoking workflow by name.
func (s *WorkflowTestSuite) RegisterWorkflow(workflowFn interface{}) {
	s.initIfNotDoneYet()
	err := s.hostEnv.RegisterWorkflow(workflowFn)
	if err != nil {
		panic(err)
	}
}

// RegisterActivity registers an activity to this WorkflowTestSuite instance. Activities registered here will be available
// and only available to all tests of this WorkflowTestSuite instance. Activities registered via cadence.RegisterActivity()
// are still valid and will be available to all tests of all instances of WorkflowTestSuite.
func (s *WorkflowTestSuite) RegisterActivity(activityFn interface{}) {
	s.initIfNotDoneYet()
	fnName := getFunctionName(activityFn)
	_, ok := s.hostEnv.activityFuncMap[fnName]
	if !ok {
		// activity not registered yet, register now
		err := s.hostEnv.RegisterActivity(activityFn)
		if err != nil {
			panic(err)
		}
	}
}

// SetLogger sets the logger for this WorkflowTestSuite. If you don't set logger, test suite will create a default logger
// with Debug level logging enabled.
func (s *WorkflowTestSuite) SetLogger(logger *zap.Logger) {
	s.logger = logger
}

// ExecuteActivity executes an activity. The tested activity will be executed synchronously in the calling goroutinue.
// Caller should use EncodedValue.Get() to extract strong typed result value.
func (t *TestActivityEnviornment) ExecuteActivity(activityFn interface{}, args ...interface{}) (EncodedValue, error) {
	return t.impl.executeActivity(activityFn, args...)
}

// SetWorkerOption sets the WorkerOptions that will be use by TestActivityEnviornment. TestActivityEnviornment will
// use options set by SetIdentity(), SetMetrics(), and WithActivityContext() on the WorkerOptions. Other options are ignored.
func (t *TestActivityEnviornment) SetWorkerOption(options WorkerOptions) *TestActivityEnviornment {
	t.impl.workerOptions = options
	return t
}

// OnActivity setup a mock call for activity. Parameter activity must be activity function (func) or activity name (string).
// You must call Return() with appropriate parameters on the returned *mock.Call instance. The supplied parameters to
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
func (t *TestWorkflowEnvironment) OnActivity(activity interface{}, args ...interface{}) *mock.Call {
	fType := reflect.TypeOf(activity)
	switch fType.Kind() {
	case reflect.Func:
		fnType := reflect.TypeOf(activity)
		if err := validateFnFormat(fnType, false); err != nil {
			panic(err)
		}
		return t.Mock.On(getFunctionName(activity), args...)

	case reflect.String:
		return t.Mock.On(activity.(string), args...)
	default:
		panic("activity must be function or string")
	}
}

// OnWorkflow setup a mock call for workflow. Parameter workflow must be workflow function (func) or workflow name (string).
// You must call Return() with appropriate parameters on the returned *mock.Call instance. The supplied parameters to
// the Return() call should either be a function that has exact same signature as the mocked workflow, or it should be
// mock values with the same types as the mocked workflow function returns.
// Example: assume the workflow you want to mock has function signature as:
//   func MyChildWorkflow(ctx cadence.Context, msg string) (string, error)
// You can mock it by return a function with exact same signature:
//   t.OnWorkflow(MyChildWorkflow, mock.Anything, mock.Anything).Return(func(ctx cadence.Context, msg string) (string, error) {
//      // your mock function implementation
//      return "", nil
//   })
// OR return mock values with same types as workflow function's return types:
//   t.OnWorkflow(MyChildWorkflow, mock.Anything, mock.Anything).Return("mock_result", nil)
func (t *TestWorkflowEnvironment) OnWorkflow(workflow interface{}, args ...interface{}) *mock.Call {
	fType := reflect.TypeOf(workflow)
	switch fType.Kind() {
	case reflect.Func:
		fnType := reflect.TypeOf(workflow)
		if err := validateFnFormat(fnType, true); err != nil {
			panic(err)
		}
		return t.Mock.On(getFunctionName(workflow), args...)

	case reflect.String:
		return t.Mock.On(workflow.(string), args...)
	default:
		panic("activity must be function or string")
	}
}

// ExecuteWorkflow executes a workflow, wait until workflow complete. It will fail the test if workflow is blocked and
// cannot complete within TestTimeout (set by SetTestTimeout()).
func (t *TestWorkflowEnvironment) ExecuteWorkflow(workflowFn interface{}, args ...interface{}) {
	t.impl.mock = &t.Mock
	t.impl.executeWorkflow(workflowFn, args...)
}

// OverrideActivity overrides an actual activity with a fake activity. The fake activity will be invoked in place where the
// actual activity should have been invoked.
func (t *TestWorkflowEnvironment) OverrideActivity(activityFn, fakeActivityFn interface{}) {
	t.impl.overrideActivity(activityFn, fakeActivityFn)
}

// OverrideWorkflow overrides an actual workflow with a fake workflow. The fake workflow will be invoked in place where the
// actual workflow should have been invoked.
func (t *TestWorkflowEnvironment) OverrideWorkflow(workflowFn, fakeWorkflowFn interface{}) {
	t.impl.overrideWorkflow(workflowFn, fakeWorkflowFn)
}

// Now returns the current workflow time (a.k.a cadence.Now() time) of this TestWorkflowEnvironment.
func (t *TestWorkflowEnvironment) Now() time.Time {
	return t.impl.Now()
}

// SetWorkerOption sets the WorkerOptions for TestWorkflowEnvironment. TestWorkflowEnvironment will use options set by
// SetIdentity(), SetMetrics(), and WithActivityContext() on the WorkerOptions. Other options are ignored.
func (t *TestWorkflowEnvironment) SetWorkerOption(options WorkerOptions) *TestWorkflowEnvironment {
	t.impl.workerOptions = options
	return t
}

// SetTestTimeout sets the wall clock timeout for this workflow test run. When test timeout happen, it means workflow is
// blocked and cannot make progress. This could happen if workflow is waiting for activity result for too long.
// This is real wall clock time, not the workflow time (a.k.a cadence.Now() time).
func (t *TestWorkflowEnvironment) SetTestTimeout(idleTimeout time.Duration) *TestWorkflowEnvironment {
	t.impl.testTimeout = idleTimeout
	return t
}

// SetOnActivityStartedListener sets a listener that will be called before activity starts execution.
func (t *TestWorkflowEnvironment) SetOnActivityStartedListener(
	listener func(activityInfo *ActivityInfo, ctx context.Context, args EncodedValues)) *TestWorkflowEnvironment {
	t.impl.onActivityStartedListener = listener
	return t
}

// SetOnActivityCompletedListener sets a listener that will be called after an activity is completed.
func (t *TestWorkflowEnvironment) SetOnActivityCompletedListener(
	listener func(activityInfo *ActivityInfo, result EncodedValue, err error)) *TestWorkflowEnvironment {
	t.impl.onActivityCompletedListener = listener
	return t
}

// SetOnActivityCanceledListener sets a listener that will be called after an activity is canceled.
func (t *TestWorkflowEnvironment) SetOnActivityCanceledListener(
	listener func(activityInfo *ActivityInfo)) *TestWorkflowEnvironment {
	t.impl.onActivityCanceledListener = listener
	return t
}

// SetOnActivityHeartbeatListener sets a listener that will be called when activity heartbeat.
func (t *TestWorkflowEnvironment) SetOnActivityHeartbeatListener(
	listener func(activityInfo *ActivityInfo, details EncodedValues)) *TestWorkflowEnvironment {
	t.impl.onActivityHeartbeatListener = listener
	return t
}

// SetOnChildWorkflowStartedListener sets a listener that will be called before a child workflow starts execution.
func (t *TestWorkflowEnvironment) SetOnChildWorkflowStartedListener(
	listener func(workflowInfo *WorkflowInfo, ctx Context, args EncodedValues)) *TestWorkflowEnvironment {
	t.impl.onChildWorkflowStartedListener = listener
	return t
}

// SetOnChildWorkflowCompletedListener sets a listener that will be called after a child workflow is completed.
func (t *TestWorkflowEnvironment) SetOnChildWorkflowCompletedListener(
	listener func(workflowInfo *WorkflowInfo, result EncodedValue, err error)) *TestWorkflowEnvironment {
	t.impl.onChildWorkflowCompletedListener = listener
	return t
}

// SetOnChildWorkflowCanceledListener sets a listener that will be called when a child workflow is canceled.
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

// CompleteActivity complete an activity that had returned ErrActivityResultPending error
func (t *TestWorkflowEnvironment) CompleteActivity(taskToken []byte, result interface{}, err error) error {
	return t.impl.CompleteActivity(taskToken, result, err)
}

// CancelWorkflow requests cancellation (through workflow Context) to the currently running test workflow.
func (t *TestWorkflowEnvironment) CancelWorkflow() {
	t.impl.cancelWorkflow()
}

// SignalWorkflow requests signal (through workflow Context) to the currently running test workflow.
func (t *TestWorkflowEnvironment) SignalWorkflow(name string, input interface{}) {
	t.impl.signalWorkflow(name, input)
}

// RegisterDelayedCallback creates a new timer with specified delayDuration using workflow clock (not wall clock). When
// the timer fires, the callback will be called. By default, this test suite uses mock clock which automatically move
// forward to fire next timer when workflow is blocked. You can use this API to make some event (like activity completion,
// signal or workflow cancellation) at desired time.
func (t *TestWorkflowEnvironment) RegisterDelayedCallback(callback func(), delayDuration time.Duration) {
	t.impl.registerDelayedCallback(callback, delayDuration)
}

// SetActivityTaskList set the affinity between activity and tasklist. By default, activity can be invoked by any tasklist
// in this test environment. You can use this SetActivityTaskList() to set affinity between activity and a tasklist. Once
// activity is set to a particular tasklist, that activity will only be available to that tasklist.
func (t *TestWorkflowEnvironment) SetActivityTaskList(tasklist string, activityFn ...interface{}) {
	t.impl.setActivityTaskList(tasklist, activityFn...)
}
