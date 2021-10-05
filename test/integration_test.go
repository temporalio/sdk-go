// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

package test_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/uber-go/tally"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/test"
	"go.uber.org/goleak"

	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptors"
	"go.temporal.io/sdk/internal/common"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	ctxTimeout                    = 15 * time.Second
	namespace                     = "integration-test-namespace"
	namespaceCacheRefreshInterval = 20 * time.Second
	testContextKey1               = "test-context-key1"
	testContextKey2               = "test-context-key2"
	testContextKey3               = "test-context-key3"
)

type IntegrationTestSuite struct {
	*require.Assertions
	suite.Suite
	config             Config
	client             client.Client
	activities         *Activities
	workflows          *Workflows
	worker             worker.Worker
	seq                int64
	taskQueueName      string
	tracer             *tracingInterceptor
	trafficController  *test.SimpleTrafficController
	metricsScopeCloser io.Closer
	metricsReporter    *metrics.CapturingStatsReporter
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

func (ts *IntegrationTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.config = NewConfig()
	ts.activities = newActivities()
	ts.workflows = &Workflows{}
	ts.NoError(WaitForTCP(time.Minute, ts.config.ServiceAddr))
	ts.registerNamespace()
}

func (ts *IntegrationTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())

	// allow the pollers to stop, and ensure there are no goroutine leaks.
	// this will wait for up to 1 minute for leaks to subside, but exit relatively quickly if possible.
	max := time.After(time.Minute)
	var last error
	for {
		select {
		case <-max:
			if last != nil {
				ts.NoError(last)
				return
			}
			ts.FailNow("leaks timed out but no error, should be impossible")
		case <-time.After(time.Second):
			// https://github.com/temporalio/go-sdk/issues/51
			last = goleak.Find(goleak.IgnoreTopFunction("go.temporal.io/sdk/internal.(*coroutineState).initialYield"))
			if last == nil {
				// no leak, done waiting
				return
			}
			// else wait for another check or the timeout (which will record the latest error)
		}
	}
}

func (ts *IntegrationTestSuite) SetupTest() {
	var metricsScope tally.Scope
	metricsScope, ts.metricsScopeCloser, ts.metricsReporter = metrics.NewTaggedMetricsScope()

	var err error
	trafficController := test.NewSimpleTrafficController()
	ts.client, err = client.NewClient(client.Options{
		HostPort:  ts.config.ServiceAddr,
		Namespace: namespace,
		Logger:    ilog.NewDefaultLogger(),
		ContextPropagators: []workflow.ContextPropagator{
			NewKeysPropagator([]string{testContextKey1}),
			NewKeysPropagator([]string{testContextKey2}),
		},
		MetricsScope:      metricsScope,
		TrafficController: trafficController,
	})
	ts.NoError(err)

	ts.trafficController = trafficController
	ts.seq++
	ts.activities.clearInvoked()
	ts.taskQueueName = fmt.Sprintf("tq-%v-%s", ts.seq, ts.T().Name())
	ts.tracer = newTracingInterceptor()
	options := worker.Options{
		WorkflowInterceptorChainFactories: []interceptors.WorkflowInterceptor{ts.tracer},
		ActivityInterceptorChainFactories: []interceptors.ActivityInterceptor{ts.tracer},
		WorkflowPanicPolicy:               worker.FailWorkflow,
	}

	worker.SetStickyWorkflowCacheSize(ts.config.maxWorkflowCacheSize)

	if strings.Contains(ts.T().Name(), "Session") {
		options.EnableSessionWorker = true
	}

	if strings.Contains(ts.T().Name(), "LocalActivityWorkerOnly") {
		options.LocalActivityWorkerOnly = true
	}

	if strings.Contains(ts.T().Name(), "CancelTimerViaDeferAfterWFTFailure") {
		options.WorkflowPanicPolicy = worker.BlockWorkflow
	}

	ts.worker = worker.New(ts.client, ts.taskQueueName, options)
	ts.registerWorkflowsAndActivities(ts.worker)
	ts.Nil(ts.worker.Start())
}

func (ts *IntegrationTestSuite) TearDownTest() {
	_ = ts.metricsScopeCloser.Close()
	ts.client.Close()
	ts.worker.Stop()
}

func (ts *IntegrationTestSuite) TestBasic() {
	var expected []string
	err := ts.executeWorkflow("test-basic", ts.workflows.Basic, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
	// See https://grokbase.com/p/gg/golang-nuts/153jjj8dgg/go-nuts-fm-suffix-in-function-name-what-does-it-mean
	// for explanation of -fm postfix.
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "ExecuteActivity", "ExecuteActivity", "ExecuteWorkflow end"},
		ts.tracer.GetTrace("Basic"))

	ts.assertMetricsCounters(
		"temporal_request", 5,
		"temporal_workflow_task_queue_poll_succeed", 1,
		"temporal_long_request", 7,
	)
}

// TestLocalActivityRetryBehavior verifies local activity retry behaviors:
// 1) local activity retry with local timer backoff when backoff duration is less than or equal to workflow task timeout
// 2) workflow task heartbeat is happening when local activity takes longer than workflow task timeout
// 3) server side timer is created when backoff is longer than workflow task timeout
func (ts *IntegrationTestSuite) TestLocalActivityRetryBehavior() {
	attempt := 0
	localActivityFn := func(ctx context.Context) error {
		attempt++
		info := activity.GetInfo(ctx)
		if info.Attempt <= 3 {
			return temporal.NewApplicationError("retry me", "MyApplicationError")
		}
		return nil
	}

	workflowFn := func(ctx workflow.Context) error {
		ao := workflow.LocalActivityOptions{
			ScheduleToCloseTimeout: 10 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				// 1st attempt executes immediately
				// 2nd attempt backoff 1s -- this will wait locally
				// 3rd attempt backoff 2s -- this will wait locally
				// 4th attempt backoff 4s -- this will wait on server timer
				InitialInterval:    time.Second,
				MaximumInterval:    4 * time.Second,
				BackoffCoefficient: 2,
			},
		}
		ctx1 := workflow.WithLocalActivityOptions(ctx, ao)
		f1 := workflow.ExecuteLocalActivity(ctx1, localActivityFn)
		err1 := f1.Get(ctx1, nil)
		return err1
	}

	ts.worker.RegisterWorkflowWithOptions(workflowFn, workflow.RegisterOptions{Name: "heartbeat-workflow"})
	id := "integration-test-workflow-heartbeat"
	startOptions := client.StartWorkflowOptions{
		ID:                  id,
		TaskQueue:           ts.taskQueueName,
		WorkflowRunTimeout:  20 * time.Second,
		WorkflowTaskTimeout: 3 * time.Second,
	}
	err := ts.executeWorkflowWithOption(startOptions, workflowFn, nil)
	ts.NoError(err)

	ts.Equal(4, attempt) // verify local activity executes 4 times

	history, err := ts.getHistory(id, "")
	ts.NoError(err)

	expectedEvents := []string{
		"WorkflowExecutionStarted",
		"WorkflowTaskScheduled",
		"WorkflowTaskStarted",
		"WorkflowTaskCompleted", // workflow task heartbeat at 80% of workflow task timeout (2.4s)
		"WorkflowTaskScheduled",
		"WorkflowTaskStarted",
		"WorkflowTaskCompleted", // completed and schedule timer for retry backoff
		"MarkerRecorded",        // record local activity error and used attempt count
		"TimerStarted",
		"TimerFired",
		"WorkflowTaskScheduled",
		"WorkflowTaskStarted",
		"WorkflowTaskCompleted",
		"MarkerRecorded", // record local activity success result
		"WorkflowExecutionCompleted",
	}
	var actualEvents []string
	for _, e := range history.Events {
		actualEvents = append(actualEvents, e.EventType.String())
	}
	ts.Equal(expectedEvents, actualEvents)
}

func (ts *IntegrationTestSuite) getHistory(workflowID string, runID string) (*historypb.History, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	var events []*historypb.HistoryEvent
	iter := ts.client.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err1 := iter.Next()
		if err1 != nil {
			return nil, err1
		}
		events = append(events, event)
	}

	return &historypb.History{Events: events}, nil
}

func (ts *IntegrationTestSuite) TestPanicFailWorkflow() {
	var expected []string
	wfOpts := ts.startWorkflowOptions("test-panic")
	wfOpts.WorkflowTaskTimeout = 5 * time.Second
	wfOpts.WorkflowRunTimeout = 5 * time.Minute
	err := ts.executeWorkflowWithOption(wfOpts, ts.workflows.Panicked, &expected)
	ts.Error(err)
	var applicationErr *temporal.ApplicationError
	ok := errors.As(err, &applicationErr)
	ts.True(ok)
	ts.True(strings.Contains(applicationErr.Error(), "simulated"))
}

func (ts *IntegrationTestSuite) TestDeadlockDetection() {
	var expected []string
	wfOpts := ts.startWorkflowOptions("test-deadlock")
	wfOpts.WorkflowTaskTimeout = 5 * time.Second
	wfOpts.WorkflowRunTimeout = 5 * time.Minute
	err := ts.executeWorkflowWithOption(wfOpts, ts.workflows.Deadlocked, &expected)
	if os.Getenv("TEMPORAL_DEBUG") != "" {
		ts.NoError(err)
	} else {
		ts.Error(err)
		var applicationErr *temporal.ApplicationError
		ok := errors.As(err, &applicationErr)
		ts.True(ok)
		ts.True(strings.Contains(applicationErr.Error(), "Potential deadlock detected"))
	}
}

func (ts *IntegrationTestSuite) TestDeadlockDetectionViaLocalActivity() {
	var expected []string
	wfOpts := ts.startWorkflowOptions("test-deadlock-local-activity")
	wfOpts.WorkflowTaskTimeout = 5 * time.Second
	wfOpts.WorkflowRunTimeout = 5 * time.Minute
	err := ts.executeWorkflowWithOption(wfOpts, ts.workflows.DeadlockedWithLocalActivity, &expected)
	ts.Error(err)
	var applicationErr *temporal.ApplicationError
	ok := errors.As(err, &applicationErr)
	ts.True(ok)
	ts.True(strings.Contains(applicationErr.Error(), "Potential deadlock detected"))
}

func (ts *IntegrationTestSuite) TestActivityRetryOnError() {
	var expected []string
	err := ts.executeWorkflow("test-activity-retry-on-error", ts.workflows.ActivityRetryOnError, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())

	ts.assertMetricsCounters(
		"temporal_request", 7,
		"temporal_request_attempt", 7,
		"temporal_activity_execution_failed", 2,
		"temporal_workflow_task_queue_poll_succeed", 1,
		"temporal_long_request", 8,
		"temporal_long_request_attempt", 8,
	)
}

func (ts *IntegrationTestSuite) TestActivityNotRegisteredRetry() {
	var expected string
	err := ts.executeWorkflow("test-activity-retry-on-error", ts.workflows.CallUnregisteredActivityRetry, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, "done")

	ts.assertMetricsCounters(
		"temporal_unregistered_activity_invocation", 2,
	)
}

func (ts *IntegrationTestSuite) TestActivityRetryOnTimeoutStableError() {
	var expected []string
	err := ts.executeWorkflow("test-activity-retry-on-timeout-stable-error", ts.workflows.RetryTimeoutStableErrorWorkflow, &expected)
	ts.Nil(err)
}

func (ts *IntegrationTestSuite) TestActivityRetryOptionsChange() {
	var expected []string
	err := ts.executeWorkflow("test-activity-retry-options-change", ts.workflows.ActivityRetryOptionsChange, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestActivityRetryOnStartToCloseTimeout() {
	var expected []string
	err := ts.executeWorkflow(
		"test-activity-retry-on-start2close-timeout",
		ts.workflows.ActivityRetryOnTimeout,
		&expected,
		enumspb.TIMEOUT_TYPE_START_TO_CLOSE)

	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestActivityRetryOnHBTimeout() {
	var expected []string
	err := ts.executeWorkflow("test-activity-retry-on-hbtimeout", ts.workflows.ActivityRetryOnHBTimeout, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestLongRunningActivityWithHB() {
	var expected []string
	err := ts.executeWorkflow("test-long-running-activity-with-hb", ts.workflows.LongRunningActivityWithHB, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestLongRunningActivityWithHBAndGrpcRetries() {
	var expected []string
	// Fail every other HB attempt, otherwise it's too easy to exceed the HB timeout
	ts.trafficController.AddError("RecordActivityTaskHeartbeat", errors.New("call not allowed"), 1, 3, 5)
	err := ts.executeWorkflow("test-long-running-activity-with-hb", ts.workflows.LongRunningActivityWithHB, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
	// we induce 3 failures, but they all should be retried
	ts.assertReportedOperationCount("temporal_request_failure", "RecordActivityTaskHeartbeat", 0)
	// expect 3 retry attempts
	ts.assertReportedOperationCount("temporal_request_failure_attempt", "RecordActivityTaskHeartbeat", 3)
	// save number of heartbeats sent to the server
	totalHeartbeats := ts.getReportedOperationCount("temporal_request", "RecordActivityTaskHeartbeat")
	// and make sure that number of reported attempts is 3 more, because of retries.
	ts.assertReportedOperationCount("temporal_request_attempt", "RecordActivityTaskHeartbeat", int(totalHeartbeats+3))
}

func (ts *IntegrationTestSuite) TestContinueAsNew() {
	var result int
	err := ts.executeWorkflow("test-continueasnew", ts.workflows.ContinueAsNew, &result, 4, ts.taskQueueName)
	ts.NoError(err)
	ts.Equal(999, result)
}

func (ts *IntegrationTestSuite) TestContinueAsNewCarryOver() {
	var result string
	startOptions := ts.startWorkflowOptions("test-continueasnew-carryover")
	startOptions.Memo = map[string]interface{}{
		"memoKey": "memoVal",
	}
	startOptions.SearchAttributes = map[string]interface{}{
		"CustomKeywordField": "searchAttr",
	}
	err := ts.executeWorkflowWithOption(startOptions, ts.workflows.ContinueAsNewWithOptions, &result, 4, ts.taskQueueName)
	ts.NoError(err)
	ts.Equal("memoVal,searchAttr", result)
}

func (ts *IntegrationTestSuite) TestCancellation() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-cancellation"), ts.workflows.Basic)
	ts.NoError(err)
	ts.NotNil(run)
	ts.Nil(ts.client.CancelWorkflow(ctx, "test-cancellation", run.GetRunID()))
	err = run.Get(ctx, nil)
	ts.Error(err)
	var canceledErr *temporal.CanceledError
	ts.True(errors.As(err, &canceledErr))
}

func (ts *IntegrationTestSuite) TestCascadingCancellation() {
	workflowID := "test-cascading-cancellation-" + uuid.New()
	childWorkflowID := workflowID + "-child"
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions(workflowID), ts.workflows.CascadingCancellation)
	ts.NotNil(run)
	ts.NoError(err)

	// Need to give workflow time to start its child
	started := make(chan bool, 1)
	go func() {
		for {
			_, err := ts.client.DescribeWorkflowExecution(ctx, childWorkflowID, "")
			if err == nil {
				break
			}
		}
		started <- true
	}()
	select {
	case <-started:
		// Nothing to do
	case <-time.After(5 * time.Second):
		ts.Fail("Timed out waiting for child workflow to start")
	}

	ts.Nil(ts.client.CancelWorkflow(ctx, workflowID, ""))
	err = run.Get(ctx, nil)
	ts.Error(err)
	var canceledErr *temporal.CanceledError
	ts.True(errors.As(err, &canceledErr))

	resp, err := ts.client.DescribeWorkflowExecution(ctx, childWorkflowID, "")
	ts.NoError(err)
	ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED, resp.GetWorkflowExecutionInfo().GetStatus())
}

func (ts *IntegrationTestSuite) TestStackTraceQuery() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-stack-trace-query"), ts.workflows.Basic)
	ts.NoError(err)
	value, err := ts.client.QueryWorkflow(ctx, "test-stack-trace-query", run.GetRunID(), "__stack_trace")
	ts.NoError(err)
	ts.NotNil(value)
	var trace string
	ts.Nil(value.Get(&trace))
	ts.True(strings.Contains(trace, "go.temporal.io/sdk/test_test.(*Workflows).Basic"), trace)
}

func (ts *IntegrationTestSuite) TestConsistentQuery() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	// this workflow will start a local activity which blocks for long enough
	// to ensure that consistent query must wait in order to satisfy consistency
	wfOpts := ts.startWorkflowOptions("test-consistent-query")
	wfOpts.WorkflowTaskTimeout = 5 * time.Second
	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.ConsistentQueryWorkflow, 3*time.Second)
	ts.Nil(err)
	// Wait for a second to ensure that first workflow task gets started and completed before we send signal.
	// Query cannot be run until first workflow task has been completed.
	// If signal occurs right after workflow start then WorkflowStarted and Signal events will both be part of the same
	// workflow task. So query will be blocked waiting for signal to complete, this is not what we want because it
	// will not exercise the consistent query code path.
	<-time.After(time.Second)
	err = ts.client.SignalWorkflow(ctx, "test-consistent-query", run.GetRunID(), consistentQuerySignalCh, "signal-input")
	ts.NoError(err)

	value, err := ts.client.QueryWorkflowWithOptions(ctx, &client.QueryWorkflowWithOptionsRequest{
		WorkflowID: "test-consistent-query",
		RunID:      run.GetRunID(),
		QueryType:  "consistent_query",
	})
	ts.Nil(err)
	ts.NotNil(value)
	ts.NotNil(value.QueryResult)
	ts.Nil(value.QueryRejected)
	var queryResult string
	ts.Nil(value.QueryResult.Get(&queryResult))
	ts.Equal("signal-input", queryResult)
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "ProcessSignal", "Go", "ExecuteWorkflow end", "HandleQuery begin", "HandleQuery end"},
		ts.tracer.GetTrace("ConsistentQueryWorkflow"))
}

func (ts *IntegrationTestSuite) TestSignalWorkflow() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfOpts := ts.startWorkflowOptions("test-signal-workflow")
	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.SignalWorkflow)
	ts.Nil(err)
	// Let workflow task run and send signal after to ensure correct order.
	<-time.After(time.Second)
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow", run.GetRunID(), "string-signal", "string-value")
	ts.NoError(err)

	wt := &commonpb.WorkflowType{Name: "workflow-type"}
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow", run.GetRunID(), "proto-signal", wt)
	ts.NoError(err)

	var protoValue *commonpb.WorkflowType
	err = run.Get(ctx, &protoValue)
	ts.NoError(err)
	ts.Equal(commonpb.WorkflowType{Name: "string-value"}, *protoValue)
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "ProcessSignal", "ProcessSignal", "ExecuteWorkflow end"},
		ts.tracer.GetTrace("SignalWorkflow"))
}

func (ts *IntegrationTestSuite) TestSignalWorkflowWithStubbornGrpcError() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	ts.trafficController.AddError("SignalWorkflowExecution", serviceerror.NewInternal("server failure"), test.FailAllAttempts)
	wfOpts := ts.startWorkflowOptions("test-signal-workflow")
	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.SignalWorkflow)
	ts.Nil(err)
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow", run.GetRunID(), "string-signal", "string-value")
	ts.Error(err)
	ts.Equal("context deadline exceeded", err.Error())
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseRejectDuplicateNoChildWorkflow() {
	specialstr := uuid.New()
	wfOpts := ts.startWorkflowOptions("test-workflow-id-reuse-reject-dupes-no-children-" + specialstr)
	wfOpts.WorkflowIDReusePolicy = enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	wfOpts.WorkflowExecutionErrorWhenAlreadyStarted = true

	var result []string
	err := ts.executeWorkflowWithOption(
		wfOpts,
		ts.workflows.Basic,
		&result,
	)
	ts.NoError(err)

	var result2 []string
	err = ts.executeWorkflowWithOption(
		wfOpts,
		ts.workflows.Basic,
		&result2,
	)
	ts.Error(err)
	var returnedErr *serviceerror.WorkflowExecutionAlreadyStarted
	ok := errors.As(err, &returnedErr)
	ts.True(ok)
	ts.True(strings.HasPrefix(returnedErr.Error(), "Workflow execution already finished"))
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseRejectDuplicate() {
	var result string
	err := ts.executeWorkflow(
		"test-workflowidreuse-reject-duplicate",
		ts.workflows.IDReusePolicy,
		&result,
		uuid.New(),
		enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		false,
		false,
	)
	ts.Error(err)
	var applicationErr *temporal.ApplicationError
	ok := errors.As(err, &applicationErr)
	ts.True(ok)
	ts.Equal("workflow execution already started", applicationErr.Error())
	ts.False(applicationErr.NonRetryable())
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseAllowDuplicateFailedOnly1() {
	var result string
	err := ts.executeWorkflow(
		"test-workflowidreuse-reject-duplicate-failed-only1",
		ts.workflows.IDReusePolicy,
		&result,
		uuid.New(),
		enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		false,
		false,
	)
	ts.Error(err)
	var applicationErr *temporal.ApplicationError
	ok := errors.As(err, &applicationErr)
	ts.True(ok)
	ts.Equal("workflow execution already started", applicationErr.Error())
	ts.False(applicationErr.NonRetryable())
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseAllowDuplicateFailedOnly2() {
	var result string
	err := ts.executeWorkflow(
		"test-workflowidreuse-reject-duplicate-failed-only2",
		ts.workflows.IDReusePolicy,
		&result,
		uuid.New(),
		enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		false,
		true,
	)
	ts.NoError(err)
	ts.Equal("WORLD", result)
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseAllowDuplicate() {
	var result string
	err := ts.executeWorkflow(
		"test-workflowidreuse-allow-duplicate",
		ts.workflows.IDReusePolicy,
		&result,
		uuid.New(),
		enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		false,
		false,
	)
	ts.NoError(err)
	ts.Equal("HELLOWORLD", result)
}

func (ts *IntegrationTestSuite) TestChildWFRetryOnError() {
	err := ts.executeWorkflow("test-childwf-retry-on-error", ts.workflows.ChildWorkflowRetryOnError, nil)
	ts.Error(err)
	ts.EqualValues([]string{"toUpper", "toUpper", "toUpper"}, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestChildWFRetryOnTimeout() {
	err := ts.executeWorkflow("test-childwf-retry-on-timeout", ts.workflows.ChildWorkflowRetryOnTimeout, nil)
	ts.Error(err)
	ts.EqualValues([]string{"sleep", "sleep", "sleep"}, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestChildWFWithMemoAndSearchAttributes() {
	var result string
	err := ts.executeWorkflow("test-childwf-success-memo-searchAttr", ts.workflows.ChildWorkflowSuccess, &result)
	ts.NoError(err)
	ts.EqualValues([]string{"getMemoAndSearchAttr"}, ts.activities.invoked())
	ts.Equal("memoVal, searchAttrVal", result)
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "ExecuteChildWorkflow", "ExecuteWorkflow end"}, ts.tracer.GetTrace("ChildWorkflowSuccess"))
}

func (ts *IntegrationTestSuite) TestChildWFWithParentClosePolicyTerminate() {
	var childWorkflowID string
	err := ts.executeWorkflow("test-childwf-parent-close-policy", ts.workflows.ChildWorkflowSuccessWithParentClosePolicyTerminate, &childWorkflowID)
	ts.NoError(err)
	for {
		resp, err := ts.client.DescribeWorkflowExecution(context.Background(), childWorkflowID, "")
		ts.NoError(err)
		info := resp.WorkflowExecutionInfo
		if !common.TimeValue(info.GetCloseTime()).IsZero() {
			ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED, info.GetStatus(), info)
			break
		}
		time.Sleep(time.Millisecond * 500)
	}
}

func (ts *IntegrationTestSuite) TestChildWFWithParentClosePolicyAbandon() {
	var childWorkflowID string
	err := ts.executeWorkflow("test-childwf-parent-close-policy", ts.workflows.ChildWorkflowSuccessWithParentClosePolicyAbandon, &childWorkflowID)
	ts.NoError(err)

	for {
		resp, err := ts.client.DescribeWorkflowExecution(context.Background(), childWorkflowID, "")
		ts.NoError(err)
		info := resp.WorkflowExecutionInfo
		if !common.TimeValue(info.GetCloseTime()).IsZero() {
			ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, info.GetStatus(), info)
			break
		}
		time.Sleep(time.Millisecond * 500)
	}
}

func (ts *IntegrationTestSuite) TestActivityCancelUsingReplay() {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(ts.workflows.ActivityCancelRepro, workflow.RegisterOptions{DisableAlreadyRegisteredCheck: true})
	err := replayer.ReplayPartialWorkflowHistoryFromJSONFile(ilog.NewDefaultLogger(), "fixtures/activity.cancel.sm.repro.json", 12)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestActivityCancelRepro() {
	var expected []string
	err := ts.executeWorkflow("test-activity-cancel-sm", ts.workflows.ActivityCancelRepro, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestCancelActivity() {
	var expected []string
	err := ts.executeWorkflow("test-cancel-activity", ts.workflows.CancelActivity, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestCancelTimer() {
	var expected []string
	err := ts.executeWorkflow("test-cancel-timer", ts.workflows.CancelTimer, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestCancelTimerAfterActivity() {
	var wfResult string
	err := ts.executeWorkflow("test-cancel-timer-after-activity", ts.workflows.CancelTimerAfterActivity, &wfResult)
	ts.NoError(err)
	ts.EqualValues("HELLO", wfResult)
}

func (ts *IntegrationTestSuite) TestCancelTimerViaDeferAfterWFTFailure() {
	// NOTE: Uses test name to adjust worker options to make panic fail WFT
	err := ts.executeWorkflow("test-cancel-timer-via-defer", ts.workflows.CancelTimerViaDeferAfterWFTFailure, nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestCancelTimerAfterActivity_Replay() {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(ts.workflows.CancelTimerAfterActivity, workflow.RegisterOptions{DisableAlreadyRegisteredCheck: true})
	err := replayer.ReplayWorkflowHistoryFromJSONFile(ilog.NewDefaultLogger(), "replaytests/cancel-timer-after-activity.json")
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestCancelChildWorkflow() {
	var expected []string
	err := ts.executeWorkflow("test-cancel-child-workflow", ts.workflows.CancelChildWorkflow, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestCantStartChildAfterBeingCancelled() {
	const wfID = "test-cant-start-child-after-cancel"
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions(wfID), ts.workflows.StartingChildAfterBeingCanceled)
	ts.NotNil(run)
	ts.NoError(err)

	ts.Nil(ts.client.CancelWorkflow(ctx, wfID, ""))

	err = run.Get(ctx, nil)
	ts.Error(err)
	var canceledErr *temporal.CanceledError
	ts.True(errors.As(err, &canceledErr))
}

func (ts *IntegrationTestSuite) TestCancelChildWorkflowUnusualTransitions() {
	wfid := "test-cancel-child-workflow-unusual-transitions"
	run, err := ts.client.ExecuteWorkflow(context.Background(),
		ts.startWorkflowOptions(wfid),
		ts.workflows.ChildWorkflowCancelUnusualTransitionsRepro)
	ts.NoError(err)

	// Give it a sec to populate the query
	<-time.After(1 * time.Second)

	v, err := ts.client.QueryWorkflow(context.Background(), run.GetID(), "", "child-workflow-id")
	ts.NoError(err)

	var childWorkflowID string
	err = v.Get(&childWorkflowID)
	ts.NoError(err)
	ts.NotNil(childWorkflowID)
	ts.NotEmpty(childWorkflowID)

	err = ts.client.CancelWorkflow(context.Background(), childWorkflowID, "")
	ts.NoError(err)

	err = ts.client.CancelWorkflow(context.Background(), run.GetID(), "")
	ts.NoError(err)

	err = ts.client.SignalWorkflow(
		context.Background(),
		childWorkflowID,
		"",
		"unblock",
		nil,
	)
	ts.NoError(err)

	// Synchronously wait for the workflow completion. Behind the scenes the SDK performs a long poll operation.
	// If you need to wait for the workflow completion from another process use
	// Client.GetWorkflow API to get an instance of a WorkflowRun.
	err = run.Get(context.Background(), nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestCancelActivityImmediately() {
	var expected []string
	err := ts.executeWorkflow("test-cancel-activity-immediately", ts.workflows.CancelActivityImmediately, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestCancelMultipleCommandsOverMultipleTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-cancel-multiple-commands-over-multiple-tasks"),
		ts.workflows.CancelMultipleCommandsOverMultipleTasks)
	ts.NoError(err)
	ts.NotNil(run)
	// We need to wait a beat before firing the cancellation
	time.Sleep(time.Second)
	ts.Nil(ts.client.CancelWorkflow(ctx, "test-cancel-multiple-commands-over-multiple-tasks",
		run.GetRunID()))
	err = run.Get(ctx, nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestWorkflowWithLocalActivityCtxPropagation() {
	var expected string
	err := ts.executeWorkflow("test-wf-local-activity-ctx-prop", ts.workflows.WorkflowWithLocalActivityCtxPropagation, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, "test-data-in-contexttest-data-in-context")
}

func (ts *IntegrationTestSuite) TestWorkflowWithParallelLocalActivities() {
	ts.NoError(ts.executeWorkflow("test-wf-parallel-local-activities", ts.workflows.WorkflowWithParallelLocalActivities, nil))
}

func (ts *IntegrationTestSuite) TestWorkflowWithParallelLocalActivitiesUsingReplay() {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(ts.workflows.WorkflowWithParallelLocalActivities, workflow.RegisterOptions{DisableAlreadyRegisteredCheck: true})
	err := replayer.ReplayWorkflowHistoryFromJSONFile(ilog.NewDefaultLogger(), "replaytests/parallel-local-activities.json")
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestActivityStartedAtSameTimeAsTimerCancel() {
	wfID := "test-activity-start-with-timer-cancel"
	wfOpts := ts.startWorkflowOptions(wfID)
	wfOpts.WorkflowExecutionTimeout = 5 * time.Second
	wfOpts.WorkflowTaskTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts,
		ts.workflows.WorkflowWithLocalActivityStartWhenTimerCancel)
	ts.Nil(err)

	<-time.After(1 * time.Second)
	err = ts.client.SignalWorkflow(ctx, wfID, run.GetRunID(), "signal", "")
	ts.NoError(err)
	var res *bool
	err = run.Get(ctx, &res)
	ts.NoError(err)
	ts.True(*res)
}

func (ts *IntegrationTestSuite) TestActivityStartedAtSameTimeAsTimerCancel_Replay() {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(ts.workflows.WorkflowWithLocalActivityStartWhenTimerCancel, workflow.RegisterOptions{DisableAlreadyRegisteredCheck: true})
	err := replayer.ReplayWorkflowHistoryFromJSONFile(ilog.NewDefaultLogger(), "replaytests/activity-same-time-as-cancel.json")
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestWorkflowWithLocalActivityRetries() {
	ts.NoError(ts.executeWorkflow("test-wf-local-activity-retries", ts.workflows.WorkflowWithLocalActivityRetries, nil))
}

func (ts *IntegrationTestSuite) TestWorkflowWithLocalActivityRetriesDefaultRetryPolicy() {
	ts.NoError(ts.executeWorkflow("test-wf-local-activity-retries-default-policy", ts.workflows.WorkflowWithLocalActivityRetriesAndDefaultRetryPolicy, nil))
}

func (ts *IntegrationTestSuite) TestWorkflowWithLocalActivityRetriesPartialPolicy() {
	ts.NoError(ts.executeWorkflow("test-wf-local-activity-retries-partial-policy", ts.workflows.WorkflowWithLocalActivityRetriesAndPartialRetryPolicy, nil))
}

func (ts *IntegrationTestSuite) TestWorkflowWithParallelLongLocalActivityAndHeartbeat() {
	if ts.config.maxWorkflowCacheSize > 0 {
		ts.NoError(ts.executeWorkflow("test-wf-parallel-long-local-activities-and-heartbeat", ts.workflows.WorkflowWithParallelLongLocalActivityAndHeartbeat, nil))
	}
}

func (ts *IntegrationTestSuite) TestWorkflowWithParallelSideEffects() {
	ts.NoError(ts.executeWorkflow("test-wf-parallel-side-effects", ts.workflows.WorkflowWithParallelSideEffects, nil))
}

func (ts *IntegrationTestSuite) TestWorkflowWithParallelSideEffectsUsingReplay() {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(ts.workflows.WorkflowWithParallelSideEffects, workflow.RegisterOptions{DisableAlreadyRegisteredCheck: true})
	err := replayer.ReplayWorkflowHistoryFromJSONFile(ilog.NewDefaultLogger(), "replaytests/parallel-side-effect.json")
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestWorkflowWithParallelMutableSideEffects() {
	ts.NoError(ts.executeWorkflow("test-wf-parallel-mutable-side-effects", ts.workflows.WorkflowWithParallelMutableSideEffects, nil))
}

func (ts *IntegrationTestSuite) TestLargeQueryResultError() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-large-query-error"), ts.workflows.LargeQueryResultWorkflow)
	ts.Nil(err)
	value, err := ts.client.QueryWorkflow(ctx, "test-large-query-error", run.GetRunID(), "large_query")
	ts.Error(err)

	ts.IsType(&serviceerror.QueryFailed{}, err)
	ts.Equal("query result size (3000036) exceeds limit (2000000)", err.Error())
	ts.Nil(value)
}

func (ts *IntegrationTestSuite) TestInspectActivityInfo() {
	err := ts.executeWorkflow("test-activity-info", ts.workflows.InspectActivityInfo, nil)
	ts.Nil(err)
}

func (ts *IntegrationTestSuite) TestInspectActivityInfoLocalActivityWorkerOnly() {
	err := ts.executeWorkflow("test-activity-info-local-worker-only", ts.workflows.InspectActivityInfo, nil)
	ts.Error(err)
}

func (ts *IntegrationTestSuite) TestInspectLocalActivityInfo() {
	err := ts.executeWorkflow("test-local-activity-info", ts.workflows.InspectLocalActivityInfo, nil)
	ts.Nil(err)
}

func (ts *IntegrationTestSuite) TestInspectLocalActivityInfoLocalActivityWorkerOnly() {
	err := ts.executeWorkflow("test-local-activity-info-local-activity-worker-only", ts.workflows.InspectLocalActivityInfo, nil)
	ts.Nil(err)
}

func (ts *IntegrationTestSuite) TestBasicSession() {
	var expected []string
	err := ts.executeWorkflow("test-basic-session", ts.workflows.BasicSession, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
	// createSession activity, actual activity, completeSession activity.
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "ExecuteActivity", "ProcessSignal", "Go", "ExecuteActivity", "ExecuteActivity", "ExecuteWorkflow end"},
		ts.tracer.GetTrace("BasicSession"))
}

func (ts *IntegrationTestSuite) TestAsyncActivityCompletion() {
	workflowID := "test-async-activity-completion"
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	workflowRun, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions(workflowID), ts.workflows.ActivityCompletionUsingID)
	ts.Nil(err)
	ts.Equal(workflowID, workflowRun.GetID())
	ts.NotEqual("", workflowRun.GetRunID())

	// wait for both the activities to be started
	describeResp, err := ts.client.DescribeWorkflowExecution(ctx, workflowID, workflowRun.GetRunID())
	ts.Nil(err)
	status := describeResp.WorkflowExecutionInfo.Status
	ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, status)
	var pendingActivities []*workflowpb.PendingActivityInfo
	for {
		pendingActivities = describeResp.PendingActivities
		if len(pendingActivities) == 2 && pendingActivities[0].State == enumspb.PENDING_ACTIVITY_STATE_STARTED &&
			pendingActivities[1].State == enumspb.PENDING_ACTIVITY_STATE_STARTED {
			// condition met
			break
		}

		time.Sleep(100 * time.Millisecond)

		// check to see if workflow is still running
		describeResp, err = ts.client.DescribeWorkflowExecution(ctx, workflowID, workflowRun.GetRunID())
		ts.Nil(err)
		status := describeResp.WorkflowExecutionInfo.Status
		ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, status)
	}

	// Both the activities are started
	ts.EqualValues([]string{"asyncComplete", "asyncComplete"}, ts.activities.invoked())

	// Complete first activity using ID
	err = ts.client.CompleteActivityByID(ctx, namespace, workflowRun.GetID(), workflowRun.GetRunID(), "A", "activityA completed", nil)
	ts.Nil(err)

	// Complete second activity using ID
	err = ts.client.CompleteActivityByID(ctx, namespace, workflowRun.GetID(), workflowRun.GetRunID(), "B", "activityB completed", nil)
	ts.Nil(err)

	// Now wait for workflow to complete
	var result []string
	err = workflowRun.Get(ctx, &result)
	ts.Nil(err)
	ts.EqualValues([]string{"activityA completed", "activityB completed"}, result)
}

func (ts *IntegrationTestSuite) TestContextPropagator() {
	var propagatedValues []string
	ctx := context.Background()
	// Propagate values using different context propagators.
	ctx = context.WithValue(ctx, contextKey(testContextKey1), "propagatedValue1")
	ctx = context.WithValue(ctx, contextKey(testContextKey2), "propagatedValue2")
	ctx = context.WithValue(ctx, contextKey(testContextKey3), "non-propagatedValue")
	err := ts.executeWorkflowWithContextAndOption(ctx, ts.startWorkflowOptions("test-context-propagator"), ts.workflows.ContextPropagator, &propagatedValues, true)
	ts.NoError(err)
	// One copy from workflow and one copy from activity * 2 for child workflow
	ts.EqualValues([]string{
		"propagatedValue1", "propagatedValue2", "activity_propagatedValue1", "activity_propagatedValue2",
		"child_propagatedValue1", "child_propagatedValue2", "child_activity_propagatedValue1", "child_activity_propagatedValue2",
	}, propagatedValues)
}

const CronWorkflowID = "test-cron"

func (ts *IntegrationTestSuite) TestFailurePropagation() {
	var expected int
	err := ts.executeWorkflow(CronWorkflowID, ts.workflows.CronWorkflow, &expected)
	// Workflow asks to be cancelled
	ts.Error(err)
	var canceledErr *temporal.CanceledError
	ts.True(errors.As(err, &canceledErr))
	var errDeets *string
	ts.NoError(canceledErr.Details(&errDeets))
	ts.EqualValues("finished OK", *errDeets)
}

func (ts *IntegrationTestSuite) TestTimerCancellationConcurrentWithOtherCommandDoesNotCausePanic() {
	const wfID = "test-timer-cancel-concurrent-with-other-cmd"
	wfOpts := ts.startWorkflowOptions(wfID)
	wfOpts.WorkflowTaskTimeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := ts.client.SignalWithStartWorkflow(ctx, wfID, "signal", "", wfOpts, ts.workflows.CancelTimerConcurrentWithOtherCommandWorkflow)
	ts.Nil(err)
	if err != nil {
		ilog.NewDefaultLogger().Error("Unable to execute workflow {}", err)
	}

	var result int
	err = run.Get(ctx, &result)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestResetWorkflowExecution() {
	var originalResult []string
	err := ts.executeWorkflow("basic-reset-workflow-execution", ts.workflows.Basic, &originalResult)
	ts.NoError(err)
	resp, err := ts.client.ResetWorkflowExecution(context.Background(), &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "basic-reset-workflow-execution",
		},
		Reason:                    "integration test",
		WorkflowTaskFinishEventId: 4,
	})

	ts.NoError(err)
	ts.NotEmpty(resp.GetRunId())
	newWf := ts.client.GetWorkflow(context.Background(), "basic-reset-workflow-execution", resp.GetRunId())
	var newResult []string
	err = newWf.Get(context.Background(), &newResult)
	ts.NoError(err)
	ts.Equal(originalResult, newResult)
}

func (ts *IntegrationTestSuite) TestExecuteInterpreters() {
	// Add callbacks to collect info
	ifaceTypes := func(ifaces []interface{}) (types []string) {
		for _, iface := range ifaces {
			types = append(types, fmt.Sprintf("%T", iface))
		}
		return
	}
	var actualWorkflowType string
	var actualWorkflowArgTypes, actualWorkflowResultTypes []string
	ts.tracer.executeWorkflowCallback = func(
		next interceptors.WorkflowInboundCallsInterceptor,
		ctx workflow.Context,
		workflowType string,
		args ...interface{},
	) []interface{} {
		actualWorkflowType = workflowType
		actualWorkflowArgTypes = ifaceTypes(args)
		results := next.ExecuteWorkflow(ctx, workflowType, args...)
		actualWorkflowResultTypes = ifaceTypes(results)
		return results
	}
	// Using the presence of a task token to check if local
	var actualActivityIsLocal []bool
	var actualActivityTypes []string
	var actualActivityArgTypes, actualActivityResultTypes [][]string
	ts.tracer.executeActivityCallback = func(
		next interceptors.ActivityInboundCallsInterceptor,
		ctx context.Context,
		activityType string,
		args ...interface{},
	) []interface{} {
		actualActivityIsLocal = append(actualActivityIsLocal, len(activity.GetInfo(ctx).TaskToken) == 0)
		actualActivityTypes = append(actualActivityTypes, activityType)
		actualActivityArgTypes = append(actualActivityArgTypes, ifaceTypes(args))
		results := next.ExecuteActivity(ctx, activityType, args...)
		actualActivityResultTypes = append(actualActivityResultTypes, ifaceTypes(results))
		return results
	}

	// Run
	input := workflowAdvancedArg{
		StringVal:  "strval",
		BytesVal:   []byte("bytesval"),
		IntVal:     123,
		ArgValPtr1: &workflowAdvancedArg{StringVal: "strval"},
		ArgValPtr2: &workflowAdvancedArg{BytesVal: []byte("bytesval")},
	}
	var actualOutput workflowAdvancedArg
	err := ts.executeWorkflow("test-execution-interpreters", ts.workflows.BasicWithArguments, &actualOutput,
		input.StringVal, input.BytesVal, input.IntVal, *input.ArgValPtr1, input.ArgValPtr2)
	ts.NoError(err)

	// Confirm workflow interceptor expectations
	expectedType := "BasicWithArguments"
	expectedArgTypes := []string{
		"*string", "*[]uint8", "*int", "*test_test.workflowAdvancedArg", "**test_test.workflowAdvancedArg"}
	expectedResultTypes := []string{"test_test.workflowAdvancedArg", "<nil>"}
	ts.Equal(expectedType, actualWorkflowType)
	ts.Equal(expectedArgTypes, actualWorkflowArgTypes)
	ts.Equal(expectedResultTypes, actualWorkflowResultTypes)

	// Confirm activity interceptor expectations of calling the non-local one
	// first then the local one
	ts.False(actualActivityIsLocal[0])
	ts.Equal(expectedType, actualActivityTypes[0])
	ts.Equal(expectedArgTypes, actualActivityArgTypes[0])
	ts.Equal(expectedResultTypes, actualActivityResultTypes[0])
	ts.True(actualActivityIsLocal[1])
	ts.Equal(expectedType, actualActivityTypes[1])
	ts.Equal(expectedArgTypes, actualActivityArgTypes[1])
	ts.Equal(expectedResultTypes, actualActivityResultTypes[1])

	// Confirm result which has the two pointers set as regular and local activity
	// responses
	expectedOutput := &workflowAdvancedArg{
		StringVal:  input.StringVal,
		BytesVal:   input.BytesVal,
		IntVal:     input.IntVal,
		ArgValPtr1: input.ArgValPtr1,
		ArgValPtr2: input.ArgValPtr2,
	}
	expectedOutput = &workflowAdvancedArg{
		ArgValPtr1: expectedOutput,
		ArgValPtr2: expectedOutput,
	}
	ts.Equal(expectedOutput, &actualOutput)

	// Also try with empty/nil params
	actualOutput = workflowAdvancedArg{}
	actualActivityIsLocal = nil
	actualActivityTypes = nil
	actualActivityArgTypes = nil
	actualActivityResultTypes = nil
	err = ts.executeWorkflow("test-execution-interpreters", ts.workflows.BasicWithArguments, &actualOutput,
		"", nil, 123, workflowAdvancedArg{}, nil)
	ts.NoError(err)
	ts.False(actualActivityIsLocal[0])
	ts.Equal(expectedType, actualActivityTypes[0])
	ts.Equal(expectedArgTypes, actualActivityArgTypes[0])
	ts.Equal(expectedResultTypes, actualActivityResultTypes[0])
	ts.True(actualActivityIsLocal[1])
	ts.Equal(expectedType, actualActivityTypes[1])
	ts.Equal(expectedArgTypes, actualActivityArgTypes[1])
	ts.Equal(expectedResultTypes, actualActivityResultTypes[1])
}

func (ts *IntegrationTestSuite) TestReturn() {
	// Test all permutations
	for _, retVal := range []*workflowAdvancedArg{nil, {StringVal: "some string"}} {
		for _, errStr := range []string{"", "some error"} {
			for _, useLocal := range []bool{true, false} {
				var ret *workflowAdvancedArg
				err := ts.executeWorkflow("test-execution-return", ts.workflows.BasicAffectReturn,
					&ret, retVal, errStr, useLocal)
				// Check error or return value
				if errStr != "" {
					ts.Error(err)
					ts.True(strings.HasSuffix(err.Error(), errStr))
				} else {
					ts.NoError(err)
					ts.Equal(retVal, ret)
				}
			}
		}
	}
}

func (ts *IntegrationTestSuite) TestActivityOnlyErrorNoContext() {
	err := ts.executeWorkflow("test-activity-only-error-no-context",
		ts.workflows.DynamicActivity, nil, "OnlyErrorNoContext", []interface{}{""})
	ts.NoError(err)
	err = ts.executeWorkflow("test-activity-only-error-no-context",
		ts.workflows.DynamicActivity, nil, "OnlyErrorNoContext", []interface{}{"some error"})
	ts.Error(err)
	ts.True(strings.HasSuffix(err.Error(), "some error"))
}

func (ts *IntegrationTestSuite) TestDynamicParams() {
	input := []interface{}{"foo", 123, []byte("test"), nil, &workflowAdvancedArg{StringVal: "some string"}}
	var actualOutput []interface{}
	// TODO(cretz): Ok that we don't spread variadic arguments when calling ExecuteActivity?
	err := ts.executeWorkflow("test-dynamic-params",
		ts.workflows.DynamicActivity, &actualOutput, "DynamicResponse", []interface{}{input})
	ts.NoError(err)
	expectedOutput := []interface{}{
		"foo",
		// Since JSON is used for serialization, this is a float
		123.0,
		// JSON encodes this as base64 and doesn't know it was a byte slice
		"dGVzdA==",
		nil,
		// JSON encodes the structure but doesn't know the struct type
		map[string]interface{}{"StringVal": "some string"},
	}
	ts.Equal(expectedOutput, actualOutput)
}

func (ts *IntegrationTestSuite) registerNamespace() {
	client, err := client.NewNamespaceClient(client.Options{HostPort: ts.config.ServiceAddr})
	ts.NoError(err)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	retention := 1 * time.Hour * 24
	err = client.Register(ctx, &workflowservice.RegisterNamespaceRequest{
		Namespace:                        namespace,
		WorkflowExecutionRetentionPeriod: &retention,
	})
	client.Close()
	if _, ok := err.(*serviceerror.NamespaceAlreadyExists); ok {
		return
	}
	ts.NoError(err)
	time.Sleep(namespaceCacheRefreshInterval) // wait for namespace cache refresh on temporal-server
	// bellow is used to guarantee namespace is ready
	var dummyReturn string
	err = ts.executeWorkflow("test-namespace-exist", ts.workflows.SimplestWorkflow, &dummyReturn)
	numOfRetry := 20
	for err != nil && numOfRetry >= 0 {
		if _, ok := err.(*serviceerror.NotFound); ok {
			time.Sleep(namespaceCacheRefreshInterval)
			err = ts.executeWorkflow("test-namespace-exist", ts.workflows.SimplestWorkflow, &dummyReturn)
		} else {
			break
		}
		numOfRetry--
	}
}

// executeWorkflow executes a given workflow and waits for the result
func (ts *IntegrationTestSuite) executeWorkflow(
	wfID string, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	return ts.executeWorkflowWithOption(ts.startWorkflowOptions(wfID), wfFunc, retValPtr, args...)
}
func (ts *IntegrationTestSuite) executeWorkflowWithOption(
	options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	return ts.executeWorkflowWithContextAndOption(context.Background(), options, wfFunc, retValPtr, args...)
}

func (ts *IntegrationTestSuite) executeWorkflowWithContextAndOption(
	ctx context.Context, options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx, options, wfFunc, args...)
	if err != nil {
		return err
	}
	err = run.Get(ctx, retValPtr)
	if ts.config.Debug {
		iter := ts.client.GetWorkflowHistory(ctx, options.ID, run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for iter.HasNext() {
			event, err1 := iter.Next()
			if err1 != nil {
				break
			}
			fmt.Println(event.String())
		}
	}
	return err
}

func (ts *IntegrationTestSuite) startWorkflowOptions(wfID string) client.StartWorkflowOptions {
	var wfOptions = client.StartWorkflowOptions{
		ID:                       wfID,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: 15 * time.Second,
		WorkflowTaskTimeout:      time.Second,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}
	if wfID == CronWorkflowID {
		wfOptions.CronSchedule = "@every 1s"
	}
	return wfOptions
}

func (ts *IntegrationTestSuite) registerWorkflowsAndActivities(w worker.Worker) {
	ts.workflows.register(w)
	ts.activities.register(w)
}

var _ interceptors.WorkflowInterceptor = (*tracingInterceptor)(nil)
var _ interceptors.WorkflowInboundCallsInterceptor = (*tracingInboundCallsInterceptor)(nil)
var _ interceptors.WorkflowOutboundCallsInterceptor = (*tracingOutboundCallsInterceptor)(nil)

type tracingInterceptor struct {
	sync.Mutex
	// key is workflow id
	instances               map[string]*tracingInboundCallsInterceptor
	executeWorkflowCallback func(interceptors.WorkflowInboundCallsInterceptor, workflow.Context, string, ...interface{}) []interface{}
	activityInstances       map[string]*tracingActivityInboundCallsInterceptor
	executeActivityCallback func(interceptors.ActivityInboundCallsInterceptor, context.Context, string, ...interface{}) []interface{}
}

type tracingInboundCallsInterceptor struct {
	Next                    interceptors.WorkflowInboundCallsInterceptor
	trace                   []string
	executeWorkflowCallback func(interceptors.WorkflowInboundCallsInterceptor, workflow.Context, string, ...interface{}) []interface{}
}

type tracingOutboundCallsInterceptor struct {
	interceptors.WorkflowOutboundCallsInterceptorBase
	inbound *tracingInboundCallsInterceptor
}

type tracingActivityInboundCallsInterceptor struct {
	Next                    interceptors.ActivityInboundCallsInterceptor
	executeActivityCallback func(interceptors.ActivityInboundCallsInterceptor, context.Context, string, ...interface{}) []interface{}
}

func (t *tracingOutboundCallsInterceptor) Go(ctx workflow.Context, name string, f func(ctx workflow.Context)) workflow.Context {
	t.inbound.trace = append(t.inbound.trace, "Go")
	return t.Next.Go(ctx, name, f)
}

func newTracingInterceptor() *tracingInterceptor {
	return &tracingInterceptor{
		instances:         make(map[string]*tracingInboundCallsInterceptor),
		activityInstances: make(map[string]*tracingActivityInboundCallsInterceptor),
	}
}

func (t *tracingInterceptor) InterceptWorkflow(info *workflow.Info, next interceptors.WorkflowInboundCallsInterceptor) interceptors.WorkflowInboundCallsInterceptor {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	result := &tracingInboundCallsInterceptor{
		Next:                    next,
		executeWorkflowCallback: t.executeWorkflowCallback,
	}
	t.instances[info.WorkflowType.Name] = result
	return result
}

func (t *tracingInterceptor) InterceptActivity(
	ctx context.Context,
	next interceptors.ActivityInboundCallsInterceptor,
) interceptors.ActivityInboundCallsInterceptor {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	result := &tracingActivityInboundCallsInterceptor{
		Next:                    next,
		executeActivityCallback: t.executeActivityCallback,
	}
	t.activityInstances[activity.GetInfo(ctx).ActivityType.Name] = result
	return result
}

func (t *tracingInterceptor) GetTrace(workflowType string) []string {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	if i, ok := t.instances[workflowType]; ok {
		return i.trace
	}
	panic(fmt.Sprintf("Unknown workflowType %v, known types: %v", workflowType, t.instances))
}

func (t *tracingInboundCallsInterceptor) Init(outbound interceptors.WorkflowOutboundCallsInterceptor) error {
	return t.Next.Init(&tracingOutboundCallsInterceptor{
		interceptors.WorkflowOutboundCallsInterceptorBase{Next: outbound}, t})
}

func (t *tracingOutboundCallsInterceptor) ExecuteActivity(ctx workflow.Context, activityType string, args ...interface{}) workflow.Future {
	t.inbound.trace = append(t.inbound.trace, "ExecuteActivity")
	return t.Next.ExecuteActivity(ctx, activityType, args...)
}

func (t *tracingOutboundCallsInterceptor) ExecuteChildWorkflow(ctx workflow.Context, childWorkflowType string, args ...interface{}) workflow.ChildWorkflowFuture {
	t.inbound.trace = append(t.inbound.trace, "ExecuteChildWorkflow")
	return t.Next.ExecuteChildWorkflow(ctx, childWorkflowType, args...)
}

func (t *tracingInboundCallsInterceptor) ExecuteWorkflow(ctx workflow.Context, workflowType string, args ...interface{}) []interface{} {
	t.trace = append(t.trace, "ExecuteWorkflow begin")
	if t.executeWorkflowCallback != nil {
		return t.executeWorkflowCallback(t.Next, ctx, workflowType, args...)
	}
	result := t.Next.ExecuteWorkflow(ctx, workflowType, args...)
	t.trace = append(t.trace, "ExecuteWorkflow end")
	return result
}

func (t *tracingInboundCallsInterceptor) ProcessSignal(ctx workflow.Context, signalName string, arg interface{}) {
	t.trace = append(t.trace, "ProcessSignal")
	t.Next.ProcessSignal(ctx, signalName, arg)
}

func (t *tracingInboundCallsInterceptor) HandleQuery(ctx workflow.Context, queryType string, args *commonpb.Payloads,
	handler func(*commonpb.Payloads) (*commonpb.Payloads, error)) (*commonpb.Payloads, error) {
	t.trace = append(t.trace, "HandleQuery begin")
	result, err := t.Next.HandleQuery(ctx, queryType, args, handler)
	t.trace = append(t.trace, "HandleQuery end")
	return result, err
}

func (ts *IntegrationTestSuite) assertMetricsCounters(keyValuePairs ...interface{}) {
	counters := make(map[string]int64, len(ts.metricsReporter.Counts()))
	for _, counter := range ts.metricsReporter.Counts() {
		counters[counter.Name()] += counter.Value()
	}

	for i := 0; i < len(keyValuePairs); i += 2 {
		expectedCounterName := keyValuePairs[i]
		expectedCounterValue := keyValuePairs[i+1]

		actualCounterValue, counterExists := counters[expectedCounterName.(string)]
		ts.True(counterExists, fmt.Sprintf("Counter %v was expected but doesn't exist", expectedCounterName))
		ts.EqualValues(expectedCounterValue, actualCounterValue, fmt.Sprintf("Expected value doesn't match actual value for counter %v", expectedCounterName))
	}
}

func (ts *IntegrationTestSuite) assertReportedOperationCount(metricName string, operation string, expectedCount int) {
	count := ts.getReportedOperationCount(metricName, operation)
	ts.EqualValues(expectedCount, count, fmt.Sprintf("Metric %v for operation %v has been reported unexpected number of times", metricName, operation))
}

func (ts *IntegrationTestSuite) getReportedOperationCount(metricName string, operation string) int64 {
	count := int64(0)
	for _, counter := range ts.metricsReporter.Counts() {
		if counter.Name() != metricName {
			continue
		}
		if op, ok := counter.Tags()[metrics.OperationTagName]; ok && op == operation {
			count += counter.Value()
		}
	}
	return count
}

func (t *tracingActivityInboundCallsInterceptor) Init(ctx context.Context) error {
	return t.Next.Init(ctx)
}

func (t *tracingActivityInboundCallsInterceptor) ExecuteActivity(
	ctx context.Context,
	activityType string,
	args ...interface{},
) []interface{} {
	if t.executeActivityCallback != nil {
		return t.executeActivityCallback(t.Next, ctx, activityType, args...)
	}
	return t.Next.ExecuteActivity(ctx, activityType, args...)
}
