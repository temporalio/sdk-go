package cadence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type (
	// EncodedValue is type alias used to encapsulate/extract encoded result from workflow/activity.
	EncodedValue []byte

	// EncodedValues is a type alias used to encapsulate/extract encoded arguments from workflow/activity.
	EncodedValues []byte

	// WorkflowTestSuite is the test suite to run unit tests for workflow/activity.
	WorkflowTestSuite struct {
		suite.Suite
		logger  *zap.Logger
		hostEnv *hostEnvImpl
	}

	// TestWorkflowEnvironment is the environment that you use to test workflow
	TestWorkflowEnvironment struct {
		impl *testWorkflowEnvironmentImpl
	}

	// TestActivityEnviornment is the environment that you use to test activity
	TestActivityEnviornment struct {
		impl *testWorkflowEnvironmentImpl
	}
)

// Get extract data from encoded data to desired value type. valuePtr is pointer to the actual value type.
func (b EncodedValue) Get(valuePtr interface{}) error {
	return getHostEnvironment().decodeArg(b, valuePtr)
}

// Get extract values from encoded data to desired value type. valuePtrs are pointers to the actual value types.
func (b EncodedValues) Get(valuePtrs ...interface{}) error {
	return getHostEnvironment().decode(b, valuePtrs)
}

// SetT sets the testing.T instance. This method is called by testify to setup the testing.T for test suite.
func (s *WorkflowTestSuite) SetT(t *testing.T) {
	s.Suite.SetT(t)
	if s.hostEnv == nil {
		s.hostEnv = &hostEnvImpl{
			workflowFuncMap: make(map[string]interface{}),
			activityFuncMap: make(map[string]interface{}),
			encoding:        gobEncoding{},
		}
	}
}

// NewWorkflowTestSuite creates a WorkflowTestSuite
func NewWorkflowTestSuite() *WorkflowTestSuite {
	return &WorkflowTestSuite{
		hostEnv: &hostEnvImpl{
			workflowFuncMap: make(map[string]interface{}),
			activityFuncMap: make(map[string]interface{}),
			encoding:        gobEncoding{},
		},
	}
}

// NewTestWorkflowEnvironment creates a new instance of TestWorkflowEnvironment. You can use the returned TestWorkflowEnvironment
// to run your workflow in the test environment.
func (s *WorkflowTestSuite) NewTestWorkflowEnvironment() *TestWorkflowEnvironment {
	return &TestWorkflowEnvironment{impl: newTestWorkflowEnvironmentImpl(s)}
}

// NewTestActivityEnvironment creates a new instance of TestActivityEnviornment. You can use the returned TestActivityEnviornment
// to run your activity in the test environment.
func (s *WorkflowTestSuite) NewTestActivityEnvironment() *TestActivityEnviornment {
	return &TestActivityEnviornment{impl: newTestWorkflowEnvironmentImpl(s)}
}

// RegisterWorkflow registers a workflow that could be used by tests of this WorkflowTestSuite instance. All workflow registered
// via cadence.RegisterWorkflow() are still valid and will be available to all tests of all instance of WorkflowTestSuite.
// In the context of unit tests, workflow registration is only required if you are invoking workflow by name.
func (s *WorkflowTestSuite) RegisterWorkflow(workflowFn interface{}) {
	err := s.hostEnv.RegisterWorkflow(workflowFn)
	if err != nil {
		panic(err)
	}
}

// RegisterActivity registers an activity to this WorkflowTestSuite instance. Activities registered here will be available
// and only available to all tests of this WorkflowTestSuite instance. Activities registered via cadence.RegisterActivity()
// are still valid and will be available to all tests of all instances of WorkflowTestSuite.
func (s *WorkflowTestSuite) RegisterActivity(activityFn interface{}) {
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
func (t *WorkflowTestSuite) SetLogger(logger *zap.Logger) {
	t.logger = logger
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

// ExecuteWorkflow executes a workflow, wait until workflow complete. It will fail the test if workflow is blocked and
// cannot complete within TestTimeout (set by SetTestTimeout()).
func (t *TestWorkflowEnvironment) ExecuteWorkflow(workflowFn interface{}, args ...interface{}) {
	t.impl.executeWorkflow(workflowFn, args...)
}

// OverrideActivity overrides an actual activity with a fake activity. The fake activity will be invoked in place where the
// actual activity should have been invoked.
func (t *TestWorkflowEnvironment) OverrideActivity(activityFn, fakeActivityFn interface{}) {
	t.impl.overrideActivity(activityFn, fakeActivityFn)
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

// TODO: create an interface for all the test intercept callbacks.
// SetOnActivityStartedListener sets a listener that will be called before an activity task started.
func (t *TestWorkflowEnvironment) SetOnActivityStartedListener(
	listener func(ctx context.Context, args EncodedValues)) *TestWorkflowEnvironment {
	t.impl.onActivityStartedListener = listener
	return t
}

// SetOnActivityEndedListener sets a listener that will be called after an activity task ended.
func (t *TestWorkflowEnvironment) SetOnActivityEndedListener(
	listener func(result EncodedValue, err error, activityType string)) *TestWorkflowEnvironment {
	t.impl.onActivityEndedListener = listener
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

// GetWorkflowResult return the encoded result from test workflow
func (t *TestWorkflowEnvironment) GetWorkflowResult(valuePtr interface{}) {
	s, r := t.impl.testSuite, t.impl.testResult
	s.NotNil(r)
	err := r.Get(valuePtr)
	s.NoError(err)
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
	t.impl.RequestCancelWorkflow("test-domain",
		t.impl.workflowInfo.WorkflowExecution.ID,
		t.impl.workflowInfo.WorkflowExecution.RunID)
}

//
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
func (env *TestWorkflowEnvironment) SetActivityTaskList(tasklist string, activityFn ...interface{}) {
	env.impl.setActivityTaskList(tasklist, activityFn...)
}
