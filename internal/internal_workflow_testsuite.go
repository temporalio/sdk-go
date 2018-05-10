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
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/facebookgo/clock"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/mock"
	"github.com/uber-go/tally"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/cadence/internal/common/metrics"
	"go.uber.org/yarpc"
	"go.uber.org/zap"
)

const (
	defaultTestDomain     = "default-test-domain"
	defaultTestTaskList   = "default-test-tasklist"
	defaultTestWorkflowID = "default-test-workflow-id"
	defaultTestRunID      = "default-test-run-id"
)

type (
	testTimerHandle struct {
		env            *testWorkflowEnvironmentImpl
		callback       resultHandler
		timer          *clock.Timer
		wallTimer      *clock.Timer
		duration       time.Duration
		mockTimeToFire time.Time
		wallTimeToFire time.Time
		timerID        int
	}

	testActivityHandle struct {
		callback     resultHandler
		activityType string
	}

	testWorkflowHandle struct {
		env      *testWorkflowEnvironmentImpl
		callback resultHandler
		handled  bool
		params   *executeWorkflowParams
		err      error
	}

	testCallbackHandle struct {
		callback          func()
		startDecisionTask bool // start a new decision task after callback() is handled.
		env               *testWorkflowEnvironmentImpl
	}

	activityExecutorWrapper struct {
		*activityExecutor
		env *testWorkflowEnvironmentImpl
	}

	workflowExecutorWrapper struct {
		*workflowExecutor
		env *testWorkflowEnvironmentImpl
	}

	mockWrapper struct {
		env        *testWorkflowEnvironmentImpl
		name       string
		fn         interface{}
		isWorkflow bool
	}

	taskListSpecificActivity struct {
		fn        interface{}
		taskLists map[string]struct{}
	}

	// testWorkflowEnvironmentShared is the shared data between parent workflow and child workflow test environments
	testWorkflowEnvironmentShared struct {
		locker    sync.Mutex
		testSuite *WorkflowTestSuite

		taskListSpecificActivities map[string]*taskListSpecificActivity

		mock          *mock.Mock
		service       workflowserviceclient.Interface
		workerOptions WorkerOptions
		logger        *zap.Logger
		metricsScope  *metrics.TaggedScope
		mockClock     *clock.Mock
		wallClock     clock.Clock
		startTime     time.Time

		callbackChannel chan testCallbackHandle
		testTimeout     time.Duration

		counterID        int
		activities       map[string]*testActivityHandle
		localActivities  map[string]*localActivityTask
		timers           map[string]*testTimerHandle
		runningWorkflows map[string]*testWorkflowHandle

		runningCount int

		expectedMockCalls map[string]struct{}

		onActivityStartedListener        func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values)
		onActivityCompletedListener      func(activityInfo *ActivityInfo, result encoded.Value, err error)
		onActivityCanceledListener       func(activityInfo *ActivityInfo)
		onLocalActivityStartedListener   func(activityInfo *ActivityInfo, ctx context.Context, args []interface{})
		onLocalActivityCompletedListener func(activityInfo *ActivityInfo, result encoded.Value, err error)
		onLocalActivityCanceledListener  func(activityInfo *ActivityInfo)
		onActivityHeartbeatListener      func(activityInfo *ActivityInfo, details encoded.Values)
		onChildWorkflowStartedListener   func(workflowInfo *WorkflowInfo, ctx Context, args encoded.Values)
		onChildWorkflowCompletedListener func(workflowInfo *WorkflowInfo, result encoded.Value, err error)
		onChildWorkflowCanceledListener  func(workflowInfo *WorkflowInfo)
		onTimerScheduledListener         func(timerID string, duration time.Duration)
		onTimerFiredListener             func(timerID string)
		onTimerCancelledListener         func(timerID string)
	}

	// testWorkflowEnvironmentImpl is the environment that runs the workflow/activity unit tests.
	testWorkflowEnvironmentImpl struct {
		*testWorkflowEnvironmentShared
		parentEnv *testWorkflowEnvironmentImpl

		workflowInfo   *WorkflowInfo
		workflowDef    workflowDefinition
		changeVersions map[string]Version

		workflowCancelHandler func()
		signalHandler         func(name string, input []byte)
		queryHandler          func(string, []byte) ([]byte, error)
		startedHandler        func(r WorkflowExecution, e error)

		isTestCompleted bool
		testResult      EncodedValue
		testError       error
		doneChannel     chan struct{}
	}
)

func newTestWorkflowEnvironmentImpl(s *WorkflowTestSuite) *testWorkflowEnvironmentImpl {
	env := &testWorkflowEnvironmentImpl{
		testWorkflowEnvironmentShared: &testWorkflowEnvironmentShared{
			testSuite:                  s,
			taskListSpecificActivities: make(map[string]*taskListSpecificActivity),

			logger:           s.logger,
			metricsScope:     metrics.NewTaggedScope(nil),
			mockClock:        clock.NewMock(),
			wallClock:        clock.New(),
			timers:           make(map[string]*testTimerHandle),
			activities:       make(map[string]*testActivityHandle),
			localActivities:  make(map[string]*localActivityTask),
			runningWorkflows: make(map[string]*testWorkflowHandle),
			callbackChannel:  make(chan testCallbackHandle, 1000),
			testTimeout:      time.Second * 3,

			expectedMockCalls: make(map[string]struct{}),
		},

		workflowInfo: &WorkflowInfo{
			Domain: defaultTestDomain,
			WorkflowExecution: WorkflowExecution{
				ID:    defaultTestWorkflowID,
				RunID: defaultTestRunID,
			},
			WorkflowType: WorkflowType{Name: "workflow-type-not-specified"},
			TaskListName: defaultTestTaskList,

			ExecutionStartToCloseTimeoutSeconds: 1,
			TaskStartToCloseTimeoutSeconds:      1,
		},

		changeVersions: make(map[string]Version),

		doneChannel: make(chan struct{}),
	}

	// move forward the mock clock to start time.
	startTime := env.startTime
	if startTime == time.Unix(0, 0) {
		// if start time not set, use current clock time
		startTime = env.wallClock.Now()
	}
	env.mockClock.Add(startTime.Sub(env.mockClock.Now()))

	// put current workflow as a running workflow so child can send signal to parent
	env.runningWorkflows[env.workflowInfo.WorkflowExecution.ID] = &testWorkflowHandle{env: env, callback: func(result []byte, err error) {}}

	if env.logger == nil {
		logger, _ := zap.NewDevelopment()
		env.logger = logger
	}
	if env.metricsScope == nil {
		env.metricsScope = metrics.NewTaggedScope(s.scope)
	}

	// setup mock service
	mockCtrl := gomock.NewController(&testReporter{logger: env.logger})
	mockService := workflowservicetest.NewMockClient(mockCtrl)

	mockHeartbeatFn := func(c context.Context, r *shared.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) error {
		activityID := string(r.TaskToken)
		env.locker.Lock() // need lock as this is running in activity worker's goroutinue
		activityHandle, ok := env.activities[activityID]
		env.locker.Unlock()
		if !ok {
			env.logger.Debug("RecordActivityTaskHeartbeat: ActivityID not found, could be already completed or cancelled.",
				zap.String(tagActivityID, activityID))
			return &shared.EntityNotExistsError{}
		}
		activityInfo := env.getActivityInfo(activityID, activityHandle.activityType)
		env.postCallback(func() {
			if env.onActivityHeartbeatListener != nil {
				env.onActivityHeartbeatListener(activityInfo, EncodedValues(r.Details))
			}
		}, false)

		env.logger.Debug("RecordActivityTaskHeartbeat", zap.String(tagActivityID, activityID))
		return nil
	}

	var callOptions []interface{}
	for range yarpcCallOptions {
		callOptions = append(callOptions, gomock.Any())
	}
	em := mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{CancelRequested: common.BoolPtr(false)}, nil)
	em.Do(func(ctx context.Context, r *shared.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
		// TODO: The following will hit a data race in the gomock code where the Do() action is executed outside
		// the lock and setting return value from inside the action is going to run into races.
		// err := mockHeartbeatFn(ctx, r, opts)
		// em.Return(&shared.RecordActivityTaskHeartbeatResponse{CancelRequested: common.BoolPtr(false)}, err)
		mockHeartbeatFn(ctx, r, opts...)
	}).AnyTimes()

	env.service = mockService

	if env.workerOptions.Logger == nil {
		env.workerOptions.Logger = env.logger
	}
	if env.workerOptions.MetricsScope == nil {
		env.workerOptions.MetricsScope = env.metricsScope
	}

	return env
}

func (env *testWorkflowEnvironmentImpl) newTestWorkflowEnvironmentForChild(params *executeWorkflowParams, callback resultHandler, startedHandler func(r WorkflowExecution, e error)) (*testWorkflowEnvironmentImpl, error) {
	// create a new test env
	childEnv := newTestWorkflowEnvironmentImpl(env.testSuite)
	childEnv.parentEnv = env
	childEnv.startedHandler = startedHandler
	childEnv.testWorkflowEnvironmentShared = env.testWorkflowEnvironmentShared

	if params.workflowID == "" {
		params.workflowID = env.workflowInfo.WorkflowExecution.RunID + "_" + getStringID(env.nextID())
	}
	// set workflow info data for child workflow
	childEnv.workflowInfo.WorkflowExecution.ID = params.workflowID
	childEnv.workflowInfo.WorkflowExecution.RunID = params.workflowID + "_RunID"
	childEnv.workflowInfo.Domain = *params.domain
	childEnv.workflowInfo.TaskListName = *params.taskListName
	childEnv.workflowInfo.ExecutionStartToCloseTimeoutSeconds = *params.executionStartToCloseTimeoutSeconds
	childEnv.workflowInfo.TaskStartToCloseTimeoutSeconds = *params.taskStartToCloseTimeoutSeconds
	if workflowHandler, ok := env.runningWorkflows[params.workflowID]; ok {
		// duplicate workflow ID
		if !workflowHandler.handled {
			return nil, errors.New("child workflow already running")
		}
		if params.workflowIDReusePolicy == WorkflowIDReusePolicyRejectDuplicate {
			return nil, errors.New("duplicate workflow id not allowed")
		}
		if workflowHandler.err == nil && params.workflowIDReusePolicy == WorkflowIDReusePolicyAllowDuplicateFailedOnly {
			return nil, errors.New("child workflow with specified workflow id already completed")
		}
	}

	env.runningWorkflows[params.workflowID] = &testWorkflowHandle{env: childEnv, callback: callback, params: params}

	return childEnv, nil
}

func (env *testWorkflowEnvironmentImpl) setWorkerOptions(options WorkerOptions) {
	if len(options.Identity) > 0 {
		env.workerOptions.Identity = options.Identity
	}
	if options.BackgroundActivityContext != nil {
		env.workerOptions.BackgroundActivityContext = options.BackgroundActivityContext
	}
	if options.MetricsScope != nil {
		env.workerOptions.MetricsScope = options.MetricsScope
	}
}

func (env *testWorkflowEnvironmentImpl) setActivityTaskList(tasklist string, activityFns ...interface{}) {
	for _, activityFn := range activityFns {
		fnName := getFunctionName(activityFn)
		taskListActivity, ok := env.taskListSpecificActivities[fnName]
		if !ok {
			taskListActivity = &taskListSpecificActivity{fn: activityFn, taskLists: make(map[string]struct{})}
			env.taskListSpecificActivities[fnName] = taskListActivity
		}
		taskListActivity.taskLists[tasklist] = struct{}{}
	}
}

func (env *testWorkflowEnvironmentImpl) executeWorkflow(workflowFn interface{}, args ...interface{}) {
	workflowType, input, err := getValidatedWorkflowFunction(workflowFn, args)
	if err != nil {
		panic(err)
	}
	env.executeWorkflowInternal(workflowType.Name, input)
}

func (env *testWorkflowEnvironmentImpl) executeWorkflowInternal(workflowType string, input []byte) {
	env.workflowInfo.WorkflowType.Name = workflowType
	workflowDefinition, err := env.getWorkflowDefinition(env.workflowInfo.WorkflowType)
	if err != nil {
		panic(err)
	}
	env.workflowDef = workflowDefinition
	// env.workflowDef.Execute() method will execute dispatcher. We want the dispatcher to only run in main loop.
	// In case of child workflow, this executeWorkflowInternal() is run in separate goroutinue, so use postCallback
	// to make sure workflowDef.Execute() is run in main loop.
	env.postCallback(func() {
		env.workflowDef.Execute(env, input)
	}, false)
	env.startMainLoop()
}

func (env *testWorkflowEnvironmentImpl) getWorkflowDefinition(wt WorkflowType) (workflowDefinition, error) {
	hostEnv := getHostEnvironment()
	wf, ok := hostEnv.getWorkflowFn(wt.Name)
	if !ok {
		supported := strings.Join(hostEnv.getRegisteredWorkflowTypes(), ", ")
		return nil, fmt.Errorf("Unable to find workflow type: %v. Supported types: [%v]", wt.Name, supported)
	}
	wd := &workflowExecutorWrapper{
		workflowExecutor: &workflowExecutor{name: wt.Name, fn: wf},
		env:              env,
	}
	return newWorkflowDefinition(wd), nil
}

func (env *testWorkflowEnvironmentImpl) executeActivity(
	activityFn interface{},
	args ...interface{},
) (encoded.Value, error) {
	activityType, input, err := getValidatedActivityFunction(activityFn, args)
	if err != nil {
		panic(err)
	}

	params := executeActivityParams{
		activityOptions: activityOptions{
			ScheduleToCloseTimeoutSeconds: 600,
			StartToCloseTimeoutSeconds:    600,
		},
		ActivityType: *activityType,
		Input:        input,
	}

	task := newTestActivityTask(
		defaultTestWorkflowID,
		defaultTestRunID,
		"0",
		params,
	)

	// ensure activityFn is registered to defaultTestTaskList
	taskHandler := env.newTestActivityTaskHandler(defaultTestTaskList)
	result, err := taskHandler.Execute(defaultTestTaskList, task)
	if err != nil {
		topLine := fmt.Sprintf("activity for %s [panic]:", defaultTestTaskList)
		st := getStackTraceRaw(topLine, 7, 0)
		return nil, newPanicError(err.Error(), st)
	}

	if result == ErrActivityResultPending {
		return nil, ErrActivityResultPending
	}

	switch request := result.(type) {
	case *shared.RespondActivityTaskCanceledRequest:
		return nil, NewCanceledError(request.Details)
	case *shared.RespondActivityTaskFailedRequest:
		return nil, constructError(request.GetReason(), request.Details)
	case *shared.RespondActivityTaskCompletedRequest:
		return EncodedValue(request.Result), nil
	default:
		// will never happen
		return nil, fmt.Errorf("unsupported respond type %T", result)
	}
}

func (env *testWorkflowEnvironmentImpl) executeLocalActivity(
	activityFn interface{},
	args ...interface{},
) (encoded.Value, error) {
	params := executeLocalActivityParams{
		localActivityOptions: localActivityOptions{
			ScheduleToCloseTimeoutSeconds: common.Int32Ceil(env.testTimeout.Seconds()),
		},
		ActivityFn:   activityFn,
		InputArgs:    args,
		WorkflowInfo: env.workflowInfo,
	}
	task := &localActivityTask{
		activityID: "test-local-activity",
		params:     &params,
		callback: func(result []byte, err error) {
		},
	}
	taskHandler := localActivityTaskHandler{
		userContext:  env.workerOptions.BackgroundActivityContext,
		metricsScope: env.metricsScope,
		logger:       env.logger,
	}

	result := taskHandler.executeLocalActivityTask(task)
	return EncodedValue(result.result), result.err
}

func (env *testWorkflowEnvironmentImpl) startDecisionTask() {
	if !env.isTestCompleted {
		env.workflowDef.OnDecisionTaskStarted()
	}
}

func (env *testWorkflowEnvironmentImpl) isChildWorkflow() bool {
	return env.parentEnv != nil
}

func (env *testWorkflowEnvironmentImpl) startMainLoop() {
	if env.isChildWorkflow() {
		// child workflow rely on parent workflow's main loop to process events
		<-env.doneChannel // wait until workflow is complete
		return
	}

	for {
		// use non-blocking-select to check if there is anything pending in the main thread.
		select {
		case c := <-env.callbackChannel:
			// this will drain the callbackChannel
			c.processCallback()
		default:
			// nothing to process, main thread is blocked at this moment, now check if we should auto fire next timer
			if !env.autoFireNextTimer() {
				if env.isTestCompleted {
					return
				}

				// no timer to fire, wait for things to do or timeout.
				select {
				case c := <-env.callbackChannel:
					c.processCallback()
				case <-time.After(env.testTimeout):
					// not able to complete workflow within test timeout, workflow likely stuck somewhere,
					// check workflow stack for more details.
					panicMsg := fmt.Sprintf("test timeout: %v, workflow stack: %v",
						env.testTimeout, env.workflowDef.StackTrace())
					panic(panicMsg)
				}
			}
		}
	}
}

func (env *testWorkflowEnvironmentImpl) registerDelayedCallback(f func(), delayDuration time.Duration) {
	timerCallback := func(result []byte, err error) {
		f()
	}
	mainLoopCallback := func() {
		env.newTimer(delayDuration, timerCallback, false)
	}
	env.postCallback(mainLoopCallback, false)
}

func (c *testCallbackHandle) processCallback() {
	c.env.locker.Lock()
	defer c.env.locker.Unlock()
	c.callback()
	if c.startDecisionTask {
		c.env.startDecisionTask()
	}
}

func (env *testWorkflowEnvironmentImpl) autoFireNextTimer() bool {
	if len(env.timers) == 0 {
		return false
	}

	// find next timer
	var nextTimer *testTimerHandle
	for _, t := range env.timers {
		if nextTimer == nil {
			nextTimer = t
		} else if t.mockTimeToFire.Before(nextTimer.mockTimeToFire) ||
			(t.mockTimeToFire.Equal(nextTimer.mockTimeToFire) && t.timerID < nextTimer.timerID) {
			nextTimer = t
		}
	}

	// function to fire timer
	fireTimer := func(th *testTimerHandle) {
		skipDuration := th.mockTimeToFire.Sub(env.mockClock.Now())
		env.logger.Debug("Auto fire timer",
			zap.Int(tagTimerID, th.timerID),
			zap.Duration("TimerDuration", th.duration),
			zap.Duration("TimeSkipped", skipDuration))

		// Move mockClock forward, this will fire the timer, and the timer callback will remove timer from timers.
		env.mockClock.Add(skipDuration)
	}

	// fire timer if there is no running activity
	if env.runningCount == 0 {
		if nextTimer.wallTimer != nil {
			nextTimer.wallTimer.Stop()
			nextTimer.wallTimer = nil
		}
		fireTimer(nextTimer)
		return true
	}

	durationToFire := nextTimer.mockTimeToFire.Sub(env.mockClock.Now())
	wallTimeToFire := env.wallClock.Now().Add(durationToFire)

	if nextTimer.wallTimer != nil && nextTimer.wallTimeToFire.Before(wallTimeToFire) {
		// nextTimer already set, meaning we already have a wall clock timer for the nextTimer setup earlier. And the
		// previously scheduled wall time to fire is before the wallTimeToFire calculated this time. This could happen
		// if workflow was blocked while there was activity running, and when that activity completed, there are some
		// other activities still running while the nextTimer is still that same nextTimer. In that case, we should not
		// reset the wall time to fire for the nextTimer.
		return false
	}
	if nextTimer.wallTimer != nil {
		// wallTimer was scheduled, but the wall time to fire should be earlier based on current calculation.
		nextTimer.wallTimer.Stop()
	}

	// there is running activities, we would fire next timer only if wall time passed by nextTimer duration.
	nextTimer.wallTimeToFire, nextTimer.wallTimer = wallTimeToFire, env.wallClock.AfterFunc(durationToFire, func() {
		// make sure it is running in the main loop
		nextTimer.env.postCallback(func() {
			if timerHandle, ok := env.timers[getStringID(nextTimer.timerID)]; ok {
				fireTimer(timerHandle)
			}
		}, true)
	})

	return false
}

func (env *testWorkflowEnvironmentImpl) postCallback(cb func(), startDecisionTask bool) {
	env.callbackChannel <- testCallbackHandle{callback: cb, startDecisionTask: startDecisionTask, env: env}
}

func (env *testWorkflowEnvironmentImpl) RequestCancelActivity(activityID string) {
	handle, ok := env.activities[activityID]
	if !ok {
		env.logger.Debug("RequestCancelActivity failed, Activity not exists or already completed.", zap.String(tagActivityID, activityID))
		return
	}
	activityInfo := env.getActivityInfo(activityID, handle.activityType)
	env.logger.Debug("RequestCancelActivity", zap.String(tagActivityID, activityID))
	delete(env.activities, activityID)
	env.postCallback(func() {
		handle.callback(nil, NewCanceledError())
		if env.onActivityCanceledListener != nil {
			env.onActivityCanceledListener(activityInfo)
		}
	}, true)
}

// RequestCancelTimer request to cancel timer on this testWorkflowEnvironmentImpl.
func (env *testWorkflowEnvironmentImpl) RequestCancelTimer(timerID string) {
	env.logger.Debug("RequestCancelTimer", zap.String(tagTimerID, timerID))
	timerHandle, ok := env.timers[timerID]
	if !ok {
		env.logger.Debug("RequestCancelTimer failed, TimerID not exists.", zap.String(tagTimerID, timerID))
		return
	}

	delete(env.timers, timerID)
	timerHandle.timer.Stop()
	timerHandle.env.postCallback(func() {
		timerHandle.callback(nil, NewCanceledError())
		if timerHandle.env.onTimerCancelledListener != nil {
			timerHandle.env.onTimerCancelledListener(timerID)
		}
	}, true)
}

func (env *testWorkflowEnvironmentImpl) Complete(result []byte, err error) {
	if env.isTestCompleted {
		env.logger.Debug("Workflow already completed.")
		return
	}
	if _, ok := err.(*CanceledError); ok && env.workflowCancelHandler != nil {
		env.workflowCancelHandler()
	}

	env.isTestCompleted = true
	env.testResult = EncodedValue(result)

	if err != nil {
		switch err := err.(type) {
		case *CanceledError, *ContinueAsNewError, *TimeoutError:
			env.testError = err
		default:
			env.testError = constructError(getErrorDetails(err))
		}
	}

	close(env.doneChannel)

	if env.isChildWorkflow() {
		// this is completion of child workflow
		childWorkflowID := env.workflowInfo.WorkflowExecution.ID
		if childWorkflowHandle, ok := env.runningWorkflows[childWorkflowID]; ok && !childWorkflowHandle.handled {
			// It is possible that child workflow could complete after cancellation. In that case, childWorkflowHandle
			// would have already been removed from the runningWorkflows map by RequestCancelWorkflow().
			childWorkflowHandle.handled = true
			env.parentEnv.postCallback(func() {
				// deliver result
				childWorkflowHandle.err = env.testError
				childWorkflowHandle.callback(env.testResult, env.testError)
				if env.onChildWorkflowCompletedListener != nil {
					env.onChildWorkflowCompletedListener(env.workflowInfo, env.testResult, env.testError)
				}
			}, true /* true to trigger parent workflow to resume to handle child workflow's result */)
		}
	}
}

func (env *testWorkflowEnvironmentImpl) CompleteActivity(taskToken []byte, result interface{}, err error) error {
	if taskToken == nil {
		return errors.New("nil task token provided")
	}
	var data []byte
	if result != nil {
		var encodeErr error
		data, encodeErr = getHostEnvironment().encodeArg(result)
		if encodeErr != nil {
			return encodeErr
		}
	}

	activityID := string(taskToken)
	env.postCallback(func() {
		activityHandle, ok := env.activities[activityID]
		if !ok {
			env.logger.Debug("CompleteActivity: ActivityID not found, could be already completed or cancelled.",
				zap.String(tagActivityID, activityID))
			return
		}
		request := convertActivityResultToRespondRequest("test-identity", taskToken, data, err)
		env.handleActivityResult(activityID, request, activityHandle.activityType)
	}, false /* do not auto schedule decision task, because activity might be still pending */)

	return nil
}

func (env *testWorkflowEnvironmentImpl) GetLogger() *zap.Logger {
	return env.logger
}

func (env *testWorkflowEnvironmentImpl) GetMetricsScope() tally.Scope {
	return env.workerOptions.MetricsScope
}

func (env *testWorkflowEnvironmentImpl) ExecuteActivity(parameters executeActivityParams, callback resultHandler) *activityInfo {
	var activityID string
	if parameters.ActivityID == nil || *parameters.ActivityID == "" {
		activityID = getStringID(env.nextID())
	} else {
		activityID = *parameters.ActivityID
	}
	activityInfo := &activityInfo{activityID: activityID}
	task := newTestActivityTask(
		defaultTestWorkflowID,
		defaultTestRunID,
		activityInfo.activityID,
		parameters,
	)

	taskHandler := env.newTestActivityTaskHandler(parameters.TaskListName)
	activityHandle := &testActivityHandle{callback: callback, activityType: parameters.ActivityType.Name}

	env.activities[activityInfo.activityID] = activityHandle
	env.runningCount++
	// activity runs in separate goroutinue outside of workflow dispatcher
	go func() {
		result, err := taskHandler.Execute(parameters.TaskListName, task)
		if err != nil {
			panic(err)
		}
		// post activity result to workflow dispatcher
		env.postCallback(func() {
			env.handleActivityResult(activityInfo.activityID, result, parameters.ActivityType.Name)
			env.runningCount--
		}, false /* do not auto schedule decision task, because activity might be still pending */)
	}()

	return activityInfo
}

func (env *testWorkflowEnvironmentImpl) ExecuteLocalActivity(params executeLocalActivityParams, callback resultHandler) *localActivityInfo {
	activityID := getStringID(env.nextID())
	wOptions := fillWorkerOptionsDefaults(env.workerOptions)
	ae := &activityExecutor{name: getFunctionName(params.ActivityFn), fn: params.ActivityFn}
	if at, _, _ := getValidatedActivityFunction(params.ActivityFn, params.InputArgs); at != nil {
		// local activity could be registered, if so use the registered name. This name is only used to find a mock.
		ae.name = at.Name
	}
	aew := &activityExecutorWrapper{activityExecutor: ae, env: env}

	// substitute the local activity function so we could replace with mock if it is supplied.
	params.ActivityFn = func(ctx context.Context, inputArgs ...interface{}) ([]byte, error) {
		return aew.ExecuteWithActualArgs(ctx, params.InputArgs)
	}

	task := &localActivityTask{
		activityID: activityID,
		params:     &params,
		callback:   callback,
	}
	taskHandler := localActivityTaskHandler{
		userContext:  wOptions.BackgroundActivityContext,
		metricsScope: wOptions.MetricsScope,
		logger:       wOptions.Logger,
	}

	env.localActivities[activityID] = task
	env.runningCount++

	go func() {
		result := taskHandler.executeLocalActivityTask(task)
		env.postCallback(func() {
			env.handleLocalActivityResult(result)
			env.runningCount--
		}, false)
	}()

	return &localActivityInfo{activityID: activityID}
}

func (env *testWorkflowEnvironmentImpl) RequestCancelLocalActivity(activityID string) {
	task, ok := env.localActivities[activityID]
	if !ok {
		env.logger.Debug("RequestCancelLocalActivity failed, LocalActivity not exists or already completed.", zap.String(tagActivityID, activityID))
		return
	}
	activityInfo := env.getActivityInfo(activityID, getFunctionName(task.params.ActivityFn))
	env.logger.Debug("RequestCancelLocalActivity", zap.String(tagActivityID, activityID))
	delete(env.localActivities, activityID)
	env.postCallback(func() {
		task.callback(nil, NewCanceledError())
		if env.onLocalActivityCanceledListener != nil {
			env.onLocalActivityCanceledListener(activityInfo)
		}
	}, true)
}

func (env *testWorkflowEnvironmentImpl) handleActivityResult(activityID string, result interface{}, activityType string) {
	env.logger.Debug(fmt.Sprintf("handleActivityResult: %T.", result),
		zap.String(tagActivityID, activityID), zap.String(tagActivityType, activityType))
	activityInfo := env.getActivityInfo(activityID, activityType)
	if result == ErrActivityResultPending {
		// In case activity returns ErrActivityResultPending, the respond will be nil, and we don't need to do anything.
		// Activity will need to complete asynchronously using CompleteActivity().
		if env.onActivityCompletedListener != nil {
			env.onActivityCompletedListener(activityInfo, nil, ErrActivityResultPending)
		}
		return
	}

	// this is running in dispatcher
	activityHandle, ok := env.activities[activityID]
	if !ok {
		env.logger.Debug("handleActivityResult: ActivityID not exists, could be already completed or cancelled.",
			zap.String(tagActivityID, activityID))
		return
	}

	delete(env.activities, activityID)

	var blob []byte
	var err error

	switch request := result.(type) {
	case *shared.RespondActivityTaskCanceledRequest:
		err = NewCanceledError(request.Details)
		activityHandle.callback(nil, err)
	case *shared.RespondActivityTaskFailedRequest:
		err = constructError(*request.Reason, request.Details)
		activityHandle.callback(nil, err)
	case *shared.RespondActivityTaskCompletedRequest:
		blob = request.Result
		activityHandle.callback(blob, nil)
	default:
		panic(fmt.Sprintf("unsupported respond type %T", result))
	}

	if env.onActivityCompletedListener != nil {
		env.onActivityCompletedListener(activityInfo, EncodedValue(blob), err)
	}

	env.startDecisionTask()
}

func (env *testWorkflowEnvironmentImpl) handleLocalActivityResult(result *localActivityResult) {
	activityID := result.task.activityID
	activityType := getFunctionName(result.task.params.ActivityFn)
	env.logger.Debug(fmt.Sprintf("handleLocalActivityResult: Err: %v, Result: %v.", result.err, string(result.result)),
		zap.String(tagActivityID, activityID), zap.String(tagActivityType, activityType))

	activityInfo := env.getActivityInfo(activityID, activityType)
	task, ok := env.localActivities[activityID]
	if !ok {
		env.logger.Debug("handleLocalActivityResult: ActivityID not exists, could be already completed or cancelled.",
			zap.String(tagActivityID, activityID))
		return
	}

	delete(env.localActivities, activityID)
	task.callback(result.result, result.err)
	if env.onLocalActivityCompletedListener != nil {
		env.onLocalActivityCompletedListener(activityInfo, EncodedValue(result.result), result.err)
	}

	env.startDecisionTask()
}

// runBeforeMockCallReturns is registered as mock call's RunFn by *mock.Call.Run(fn). It will be called by testify's
// mock.MethodCalled() before it returns.
func (env *testWorkflowEnvironmentImpl) runBeforeMockCallReturns(call *MockCallWrapper, args mock.Arguments) {
	if call.waitDuration > 0 {
		// we want this mock call to block until the wait duration is elapsed (on workflow clock).
		waitCh := make(chan time.Time)
		env.registerDelayedCallback(func() {
			env.runningCount++  // increase runningCount as the mock call is ready to resume.
			waitCh <- env.Now() // this will unblock mock call
		}, call.waitDuration)

		// make sure decrease runningCount after delayed callback is posted
		env.postCallback(func() {
			env.runningCount-- // reduce runningCount, since this mock call is about to be blocked.
		}, false)
		<-waitCh // this will block until mock clock move forward by waitDuration
	}

	// run the actual runFn if it was setup
	if call.runFn != nil {
		call.runFn(args)
	}
}

// Execute executes the activity code.
func (a *activityExecutorWrapper) Execute(ctx context.Context, input []byte) ([]byte, error) {
	activityInfo := GetActivityInfo(ctx)
	if a.env.onActivityStartedListener != nil {
		a.env.postCallback(func() {
			a.env.onActivityStartedListener(&activityInfo, ctx, EncodedValues(input))
		}, false)
	}

	m := &mockWrapper{env: a.env, name: a.name, fn: a.fn, isWorkflow: false}
	if mockRet := m.getMockReturn(ctx, input); mockRet != nil {
		return m.executeMock(ctx, input, mockRet)
	}

	return a.activityExecutor.Execute(ctx, input)
}

// Execute executes the activity code.
func (a *activityExecutorWrapper) ExecuteWithActualArgs(ctx context.Context, inputArgs []interface{}) ([]byte, error) {
	activityInfo := GetActivityInfo(ctx)
	if a.env.onLocalActivityStartedListener != nil {
		a.env.postCallback(func() {
			a.env.onLocalActivityStartedListener(&activityInfo, ctx, inputArgs)
		}, false)
	}

	m := &mockWrapper{env: a.env, name: a.name, fn: a.fn, isWorkflow: false}
	if mockRet := m.getMockReturnWithActualArgs(ctx, inputArgs); mockRet != nil {
		return m.executeMockWithActualArgs(ctx, inputArgs, mockRet)
	}

	return a.activityExecutor.ExecuteWithActualArgs(ctx, inputArgs)
}

// Execute executes the workflow code.
func (w *workflowExecutorWrapper) Execute(ctx Context, input []byte) (result []byte, err error) {
	env := w.env
	if env.isChildWorkflow() && env.onChildWorkflowStartedListener != nil {
		env.postCallback(func() {
			env.onChildWorkflowStartedListener(GetWorkflowInfo(ctx), ctx, EncodedValues(input))
		}, false)
	}

	if !env.isChildWorkflow() {
		// This is to prevent auto-forwarding mock clock before main workflow starts. For child workflow, we increase
		// the counter in env.ExecuteChildWorkflow(). We cannot do it here for child workflow, because we need to make
		// sure the counter is increased before returning from ExecuteChildWorkflow().
		env.runningCount++
	}

	m := &mockWrapper{env: env, name: w.name, fn: w.fn, isWorkflow: true}
	// This method is called by workflow's dispatcher. In this test suite, it is run in the main loop. We cannot block
	// the main loop, but the mock could block if it is configured to wait. So we need to use a separate goroutinue to
	// run the mock, and resume after mock call returns.
	mockReadyChannel := NewChannel(ctx)
	// make a copy of the context for getMockReturn() call to avoid race condition
	ctxCopy := newWorkflowContext(w.env)
	go func() {
		// getMockReturn could block if mock is configured to wait. The returned mockRet is what has been configured
		// for the mock by using MockCallWrapper.Return(). The mockRet could be mock values or mock function. We process
		// the returned mockRet by calling executeMock() later in the main thread after it is send over via mockReadyChannel.
		mockRet := m.getMockReturn(ctxCopy, input)
		env.postCallback(func() {
			mockReadyChannel.SendAsync(mockRet)
		}, true /* true to trigger the dispatcher for this workflow so it resume from mockReadyChannel block*/)
	}()

	var mockRet mock.Arguments
	// This will block workflow dispatcher (on cadence channel), which the dispatcher understand and will return from
	// ExecuteUntilAllBlocked() so the main loop is not blocked. The dispatcher will unblock when getMockReturn() returns.
	mockReadyChannel.Receive(ctx, &mockRet)

	// reduce runningCount to allow auto-forwarding mock clock after current workflow dispatcher run is blocked (aka
	// ExecuteUntilAllBlocked() returns).
	env.runningCount--

	childWE := env.workflowInfo.WorkflowExecution
	var startedErr error
	if mockRet != nil {
		// workflow was mocked.
		result, err = m.executeMock(ctx, input, mockRet)
		if env.isChildWorkflow() && err == ErrMockStartChildWorkflowFailed {
			childWE, startedErr = WorkflowExecution{}, err
		}
	}

	if env.isChildWorkflow() {
		// notify parent that child workflow is started
		env.parentEnv.postCallback(func() {
			env.startedHandler(childWE, startedErr)
		}, true)
	}

	if mockRet != nil {
		return result, err
	}

	// no mock, so call the actual workflow
	return w.workflowExecutor.Execute(ctx, input)
}

func (m *mockWrapper) getCtxArg(ctx interface{}) []interface{} {
	fnType := reflect.TypeOf(m.fn)
	if fnType.NumIn() > 0 {
		if (!m.isWorkflow && isActivityContext(fnType.In(0))) ||
			(m.isWorkflow && isWorkflowContext(fnType.In(0))) {
			return []interface{}{ctx}
		}
	}
	return nil
}

func (m *mockWrapper) getMockReturn(ctx interface{}, input []byte) (retArgs mock.Arguments) {
	if _, ok := m.env.expectedMockCalls[m.name]; !ok {
		// no mock
		return nil
	}

	fnType := reflect.TypeOf(m.fn)
	reflectArgs, err := getHostEnvironment().decodeArgs(fnType, input)
	if err != nil {
		panic(err)
	}
	realArgs := m.getCtxArg(ctx)
	for _, arg := range reflectArgs {
		realArgs = append(realArgs, arg.Interface())
	}

	return m.env.mock.MethodCalled(m.name, realArgs...)
}

func (m *mockWrapper) getMockReturnWithActualArgs(ctx interface{}, inputArgs []interface{}) (retArgs mock.Arguments) {
	if _, ok := m.env.expectedMockCalls[m.name]; !ok {
		// no mock
		return nil
	}

	realArgs := m.getCtxArg(ctx)
	realArgs = append(realArgs, inputArgs...)
	return m.env.mock.MethodCalled(m.name, realArgs...)
}

func (m *mockWrapper) getMockFn(mockRet mock.Arguments) interface{} {
	fnName := m.name
	mockRetLen := len(mockRet)
	if mockRetLen == 0 {
		panic(fmt.Sprintf("mock of %v has no returns", fnName))
	}

	fnType := reflect.TypeOf(m.fn)
	// check if mock returns function which must match to the actual function.
	mockFn := mockRet.Get(0)
	mockFnType := reflect.TypeOf(mockFn)
	if mockFnType != nil && mockFnType.Kind() == reflect.Func {
		if mockFnType != fnType {
			panic(fmt.Sprintf("mock of %v has incorrect return function, expected %v, but actual is %v",
				fnName, fnType, mockFnType))
		}
		return mockFn
	}
	return nil
}

func (m *mockWrapper) getMockValue(mockRet mock.Arguments) ([]byte, error) {
	fnName := m.name
	mockRetLen := len(mockRet)
	fnType := reflect.TypeOf(m.fn)
	// check if mockRet have same types as function's return types
	if mockRetLen != fnType.NumOut() {
		panic(fmt.Sprintf("mock of %v has incorrect number of returns, expected %d, but actual is %d",
			fnName, fnType.NumOut(), mockRetLen))
	}
	// we already verified function either has 1 return value (error) or 2 return values (result, error)
	var retErr error
	mockErr := mockRet[mockRetLen-1] // last mock return must be error
	if mockErr == nil {
		retErr = nil
	} else if err, ok := mockErr.(error); ok {
		retErr = err
	} else {
		panic(fmt.Sprintf("mock of %v has incorrect return type, expected error, but actual is %T (%v)",
			fnName, mockErr, mockErr))
	}

	switch mockRetLen {
	case 1:
		return nil, retErr
	case 2:
		expectedType := fnType.Out(0)
		mockResult := mockRet[0]
		if mockResult == nil {
			switch expectedType.Kind() {
			case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Array:
				// these are supported nil-able types. (reflect.Chan, reflect.Func are nil-able, but not supported)
				return nil, retErr
			default:
				panic(fmt.Sprintf("mock of %v has incorrect return type, expected %v, but actual is %T (%v)",
					fnName, expectedType, mockResult, mockResult))
			}
		} else {
			if !reflect.TypeOf(mockResult).AssignableTo(expectedType) {
				panic(fmt.Sprintf("mock of %v has incorrect return type, expected %v, but actual is %T (%v)",
					fnName, expectedType, mockResult, mockResult))
			}
			result, encodeErr := getHostEnvironment().encodeArg(mockResult)
			if encodeErr != nil {
				panic(fmt.Sprintf("encode result from mock of %v failed: %v", fnName, encodeErr))
			}
			return result, retErr
		}
	default:
		// this will never happen, panic just in case
		panic("mock should either have 1 return value (error) or 2 return values (result, error)")
	}
}

func (m *mockWrapper) executeMock(ctx interface{}, input []byte, mockRet mock.Arguments) ([]byte, error) {
	fnName := m.name
	// check if mock returns function which must match to the actual function.
	if mockFn := m.getMockFn(mockRet); mockFn != nil {
		// we found a mock function that matches to actual function, so call that mockFn
		if m.isWorkflow {
			executor := &workflowExecutor{name: fnName, fn: mockFn}
			return executor.Execute(ctx.(Context), input)
		}
		executor := &activityExecutor{name: fnName, fn: mockFn}
		return executor.Execute(ctx.(context.Context), input)
	}

	return m.getMockValue(mockRet)
}

func (m *mockWrapper) executeMockWithActualArgs(ctx interface{}, inputArgs []interface{}, mockRet mock.Arguments) ([]byte, error) {
	fnName := m.name
	// check if mock returns function which must match to the actual function.
	if mockFn := m.getMockFn(mockRet); mockFn != nil {
		executor := &activityExecutor{name: fnName, fn: mockFn}
		return executor.ExecuteWithActualArgs(ctx.(context.Context), inputArgs)
	}

	return m.getMockValue(mockRet)
}

func (env *testWorkflowEnvironmentImpl) newTestActivityTaskHandler(taskList string) ActivityTaskHandler {
	wOptions := fillWorkerOptionsDefaults(env.workerOptions)
	params := workerExecutionParameters{
		TaskList:     taskList,
		Identity:     wOptions.Identity,
		MetricsScope: wOptions.MetricsScope,
		Logger:       wOptions.Logger,
		UserContext:  wOptions.BackgroundActivityContext,
	}
	ensureRequiredParams(&params)

	if len(getHostEnvironment().getRegisteredActivities()) == 0 {
		panic(fmt.Sprintf("no activity is registered for tasklist '%v'", taskList))
	}

	getActivity := func(name string) activity {
		tlsa, ok := env.taskListSpecificActivities[name]
		if ok {
			_, ok := tlsa.taskLists[taskList]
			if !ok {
				// activity are bind to specific task list but not to current task list
				return nil
			}
		}

		activity, ok := getHostEnvironment().getActivity(name)
		if !ok {
			return nil
		}
		ae := &activityExecutor{name: activity.ActivityType().Name, fn: activity.GetFunction()}
		return &activityExecutorWrapper{activityExecutor: ae, env: env}
	}

	taskHandler := newActivityTaskHandlerWithCustomProvider(env.service, params, getHostEnvironment(), getActivity)
	return taskHandler
}

func newTestActivityTask(workflowID, runID, activityID string, params executeActivityParams) *shared.PollForActivityTaskResponse {
	task := &shared.PollForActivityTaskResponse{
		WorkflowExecution: &shared.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      common.StringPtr(runID),
		},
		ActivityId:                    common.StringPtr(activityID),
		TaskToken:                     []byte(activityID), // use activityID as TaskToken so we can map TaskToken in heartbeat calls.
		ActivityType:                  &shared.ActivityType{Name: common.StringPtr(params.ActivityType.Name)},
		Input:                         params.Input,
		ScheduledTimestamp:            common.Int64Ptr(time.Now().UnixNano()),
		ScheduleToCloseTimeoutSeconds: common.Int32Ptr(params.ScheduleToCloseTimeoutSeconds),
		StartedTimestamp:              common.Int64Ptr(time.Now().UnixNano()),
		StartToCloseTimeoutSeconds:    common.Int32Ptr(params.StartToCloseTimeoutSeconds),
		HeartbeatTimeoutSeconds:       common.Int32Ptr(params.HeartbeatTimeoutSeconds),
	}
	return task
}

func (env *testWorkflowEnvironmentImpl) newTimer(d time.Duration, callback resultHandler, notifyListener bool) *timerInfo {
	nextID := env.nextID()
	timerInfo := &timerInfo{timerID: getStringID(nextID)}
	timer := env.mockClock.AfterFunc(d, func() {
		delete(env.timers, timerInfo.timerID)
		env.postCallback(func() {
			callback(nil, nil)
			if notifyListener && env.onTimerFiredListener != nil {
				env.onTimerFiredListener(timerInfo.timerID)
			}
		}, true)
	})
	env.timers[timerInfo.timerID] = &testTimerHandle{
		env:            env,
		callback:       callback,
		timer:          timer,
		mockTimeToFire: env.mockClock.Now().Add(d),
		wallTimeToFire: env.wallClock.Now().Add(d),
		duration:       d,
		timerID:        nextID,
	}
	if notifyListener && env.onTimerScheduledListener != nil {
		env.onTimerScheduledListener(timerInfo.timerID, d)
	}
	return timerInfo
}

func (env *testWorkflowEnvironmentImpl) NewTimer(d time.Duration, callback resultHandler) *timerInfo {
	return env.newTimer(d, callback, true)
}

func (env *testWorkflowEnvironmentImpl) Now() time.Time {
	return env.mockClock.Now()
}

func (env *testWorkflowEnvironmentImpl) WorkflowInfo() *WorkflowInfo {
	return env.workflowInfo
}

func (env *testWorkflowEnvironmentImpl) RegisterCancelHandler(handler func()) {
	env.workflowCancelHandler = handler
}

func (env *testWorkflowEnvironmentImpl) RegisterSignalHandler(handler func(name string, input []byte)) {
	env.signalHandler = handler
}

func (env *testWorkflowEnvironmentImpl) RegisterQueryHandler(handler func(string, []byte) ([]byte, error)) {
	env.queryHandler = handler
}

func (env *testWorkflowEnvironmentImpl) RequestCancelChildWorkflow(domainName, workflowID string) {
	if childHandle, ok := env.runningWorkflows[workflowID]; ok && !childHandle.handled {
		// current workflow is a parent workflow, and we are canceling a child workflow
		childEnv := childHandle.env
		childEnv.cancelWorkflow(func(result []byte, err error) {})
		return
	}
}

func (env *testWorkflowEnvironmentImpl) RequestCancelExternalWorkflow(domainName, workflowID, runID string, callback resultHandler) {
	if env.workflowInfo.WorkflowExecution.ID == workflowID {
		// cancel current workflow
		env.workflowCancelHandler()
		// check if current workflow is a child workflow
		if env.isChildWorkflow() && env.onChildWorkflowCanceledListener != nil {
			env.postCallback(func() {
				env.onChildWorkflowCanceledListener(env.workflowInfo)
			}, false)
		}
		return
	} else if childHandle, ok := env.runningWorkflows[workflowID]; ok && !childHandle.handled {
		// current workflow is a parent workflow, and we are canceling a child workflow
		if !childHandle.params.waitForCancellation {
			childHandle.env.Complete(nil, ErrCanceled)
		}
		childEnv := childHandle.env
		env.postCallback(func() {
			callback(nil, nil)
		}, true)
		childEnv.cancelWorkflow(callback)
		return
	}

	// target workflow is not child workflow, we need the mock. The mock needs to be called in a separate goroutinue
	// so it can block and wait on the requested delay time (if configured). If we run it in main thread, and the mock
	// configured to delay, it will block the main loop which stops the world.
	env.runningCount++
	go func() {
		args := []interface{}{domainName, workflowID, runID}
		// below call will panic if mock is not properly setup.
		mockRet := env.mock.MethodCalled(mockMethodForRequestCancelExternalWorkflow, args...)
		m := &mockWrapper{name: mockMethodForRequestCancelExternalWorkflow, fn: mockFnRequestCancelExternalWorkflow}
		var err error
		if mockFn := m.getMockFn(mockRet); mockFn != nil {
			executor := &activityExecutor{name: mockMethodForRequestCancelExternalWorkflow, fn: mockFn}
			_, err = executor.ExecuteWithActualArgs(nil, args)
		} else {
			_, err = m.getMockValue(mockRet)
		}
		env.postCallback(func() {
			callback(nil, err)
			env.runningCount--
		}, true)
	}()
}

func (env *testWorkflowEnvironmentImpl) IsReplaying() bool {
	// this test environment never replay
	return false
}

func (env *testWorkflowEnvironmentImpl) SignalExternalWorkflow(domainName, workflowID, runID, signalName string, input []byte, arg interface{}, childWorkflowOnly bool, callback resultHandler) {
	// check if target workflow is a known workflow
	if childHandle, ok := env.runningWorkflows[workflowID]; ok {
		// target workflow is a child
		childEnv := childHandle.env
		if childEnv.isTestCompleted {
			// child already completed (NOTE: we have only one failed cause now)
			err := fmt.Errorf("signal external workflow failed, %v", shared.SignalExternalWorkflowExecutionFailedCauseUnknownExternalWorkflowExecution)
			callback(nil, err)
		} else {
			childEnv.signalHandler(signalName, input)
			callback(nil, nil)
		}
		childEnv.postCallback(func() {}, true) // resume child workflow since a signal is sent.
		return
	}

	// here we signal a child workflow but we cannot find it
	if childWorkflowOnly {
		err := fmt.Errorf("signal external workflow failed, %v", shared.SignalExternalWorkflowExecutionFailedCauseUnknownExternalWorkflowExecution)
		callback(nil, err)
		return
	}

	// target workflow is not child workflow, we need the mock. The mock needs to be called in a separate goroutinue
	// so it can block and wait on the requested delay time (if configured). If we run it in main thread, and the mock
	// configured to delay, it will block the main loop which stops the world.
	env.runningCount++
	go func() {
		args := []interface{}{domainName, workflowID, runID, signalName, arg}
		// below call will panic if mock is not properly setup.
		mockRet := env.mock.MethodCalled(mockMethodForSignalExternalWorkflow, args...)
		m := &mockWrapper{name: mockMethodForSignalExternalWorkflow, fn: mockFnSignalExternalWorkflow}
		var err error
		if mockFn := m.getMockFn(mockRet); mockFn != nil {
			executor := &activityExecutor{name: mockMethodForSignalExternalWorkflow, fn: mockFn}
			_, err = executor.ExecuteWithActualArgs(nil, args)
		} else {
			_, err = m.getMockValue(mockRet)
		}
		env.postCallback(func() {
			callback(nil, err)
			env.runningCount--
		}, true)
	}()
}

func (env *testWorkflowEnvironmentImpl) ExecuteChildWorkflow(params executeWorkflowParams, callback resultHandler, startedHandler func(r WorkflowExecution, e error)) error {
	childEnv, err := env.newTestWorkflowEnvironmentForChild(&params, callback, startedHandler)
	if err != nil {
		env.logger.Sugar().Infof("ExecuteChildWorkflow failed: %v", err)
		return err
	}

	env.logger.Sugar().Infof("ExecuteChildWorkflow: %v", params.workflowType.Name)
	env.runningCount++

	// run child workflow in separate goroutinue
	go childEnv.executeWorkflowInternal(params.workflowType.Name, params.input)

	return nil
}

func (env *testWorkflowEnvironmentImpl) SideEffect(f func() ([]byte, error), callback resultHandler) {
	callback(f())
}

func (env *testWorkflowEnvironmentImpl) GetVersion(changeID string, minSupported, maxSupported Version) Version {
	if version, ok := env.changeVersions[changeID]; ok {
		validateVersion(changeID, version, minSupported, maxSupported)
		return version
	}
	env.changeVersions[changeID] = maxSupported
	return maxSupported
}

func (env *testWorkflowEnvironmentImpl) MutableSideEffect(id string, f func() interface{}, equals func(a, b interface{}) bool) encoded.Value {
	return EncodedValue(encodeValue(f()))
}

func (env *testWorkflowEnvironmentImpl) nextID() int {
	activityID := env.counterID
	env.counterID++
	return activityID
}

func getStringID(intID int) string {
	return fmt.Sprintf("%d", intID)
}

func (env *testWorkflowEnvironmentImpl) getActivityInfo(activityID, activityType string) *ActivityInfo {
	return &ActivityInfo{
		ActivityID:        activityID,
		ActivityType:      ActivityType{Name: activityType},
		TaskToken:         []byte(activityID),
		WorkflowExecution: env.workflowInfo.WorkflowExecution,
	}
}

func (env *testWorkflowEnvironmentImpl) cancelWorkflow(callback resultHandler) {
	env.postCallback(func() {
		// RequestCancelWorkflow needs to be run in main thread
		env.RequestCancelExternalWorkflow(
			env.workflowInfo.Domain,
			env.workflowInfo.WorkflowExecution.ID,
			env.workflowInfo.WorkflowExecution.RunID,
			callback,
		)
	}, true)
}

func (env *testWorkflowEnvironmentImpl) signalWorkflow(name string, input interface{}) {
	data, err := getHostEnvironment().encodeArg(input)
	if err != nil {
		panic(err)
	}
	env.postCallback(func() {
		env.signalHandler(name, data)
	}, true)
}

func (env *testWorkflowEnvironmentImpl) queryWorkflow(queryType string, args ...interface{}) (encoded.Value, error) {
	data, err := getHostEnvironment().encodeArg(args)
	if err != nil {
		return nil, err
	}
	blob, err := env.queryHandler(queryType, data)
	if err != nil {
		return nil, err
	}
	return EncodedValue(blob), nil
}

func (env *testWorkflowEnvironmentImpl) getMockRunFn(callWrapper *MockCallWrapper) func(args mock.Arguments) {
	env.locker.Lock()
	defer env.locker.Unlock()

	env.expectedMockCalls[callWrapper.call.Method] = struct{}{}
	return func(args mock.Arguments) {
		env.runBeforeMockCallReturns(callWrapper, args)
	}
}

// function signature for mock SignalExternalWorkflow
func mockFnSignalExternalWorkflow(domainName, workflowID, runID, signalName string, arg interface{}) error {
	return nil
}

// function signature for mock RequestCancelExternalWorkflow
func mockFnRequestCancelExternalWorkflow(domainName, workflowID, runID string) error {
	return nil
}

// make sure interface is implemented
var _ workflowEnvironment = (*testWorkflowEnvironmentImpl)(nil)

type testReporter struct {
	logger *zap.Logger
}

func (t *testReporter) Errorf(format string, args ...interface{}) {
	t.logger.Error(fmt.Sprintf(format, args...))
}

func (t *testReporter) Fatalf(format string, args ...interface{}) {
	t.logger.Fatal(fmt.Sprintf(format, args...))
}
