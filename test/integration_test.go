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
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/uber-go/tally/v4"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/test"
	"go.uber.org/goleak"

	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	contribtally "go.temporal.io/sdk/contrib/tally"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/common"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/interceptortest"
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
	config                    Config
	client                    client.Client
	activities                *Activities
	workflows                 *Workflows
	worker                    worker.Worker
	workerStopped             bool
	seq                       int64
	taskQueueName             string
	tracer                    *tracingInterceptor
	inboundSignalInterceptor  *signalInterceptor
	trafficController         *test.SimpleTrafficController
	metricsHandler            *metrics.CapturingHandler
	tallyScope                tally.TestScope
	interceptorCallRecorder   *interceptortest.CallRecordingInvoker
	openTelemetryTracer       trace.Tracer
	openTelemetrySpanRecorder *tracetest.SpanRecorder
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
	ts.metricsHandler = metrics.NewCapturingHandler()
	var metricsHandler client.MetricsHandler = ts.metricsHandler
	// Use Tally handler for Tally test
	if strings.HasPrefix(ts.T().Name(), "TestIntegrationSuite/TestTallyScopeAccess") {
		ts.tallyScope = tally.NewTestScope("", nil)
		metricsHandler = contribtally.NewMetricsHandler(ts.tallyScope)
	}

	var clientInterceptors []interceptor.ClientInterceptor
	// Record calls for interceptor test
	if strings.HasPrefix(ts.T().Name(), "TestIntegrationSuite/TestInterceptor") {
		ts.interceptorCallRecorder = &interceptortest.CallRecordingInvoker{}
		clientInterceptors = append(clientInterceptors, interceptortest.NewProxy(ts.interceptorCallRecorder))
	}

	// Record spans for tracing test
	if strings.HasPrefix(ts.T().Name(), "TestIntegrationSuite/TestOpenTelemetryTracing") {
		ts.openTelemetrySpanRecorder = tracetest.NewSpanRecorder()
		ts.openTelemetryTracer = sdktrace.NewTracerProvider(
			sdktrace.WithSpanProcessor(ts.openTelemetrySpanRecorder)).Tracer("")
		interceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{
			Tracer:               ts.openTelemetryTracer,
			DisableSignalTracing: strings.HasSuffix(ts.T().Name(), "WithoutSignalsAndQueries"),
			DisableQueryTracing:  strings.HasSuffix(ts.T().Name(), "WithoutSignalsAndQueries"),
		})
		ts.NoError(err)
		clientInterceptors = append(clientInterceptors, interceptor)
	}

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
		MetricsHandler:    metricsHandler,
		TrafficController: trafficController,
		Interceptors:      clientInterceptors,
	})
	ts.NoError(err)

	ts.trafficController = trafficController
	ts.seq++
	ts.activities.clearInvoked()
	ts.activities.client = ts.client
	ts.taskQueueName = fmt.Sprintf("tq-%v-%s", ts.seq, ts.T().Name())
	ts.tracer = newTracingInterceptor()
	ts.inboundSignalInterceptor = newSignalInterceptor()
	workerInterceptors := []interceptor.WorkerInterceptor{ts.tracer, ts.inboundSignalInterceptor}
	options := worker.Options{
		Interceptors:        workerInterceptors,
		WorkflowPanicPolicy: worker.FailWorkflow,
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

	if strings.Contains(ts.T().Name(), "GracefulActivityCompletion") {
		options.WorkerStopTimeout = 10 * time.Second
	}

	ts.worker = worker.New(ts.client, ts.taskQueueName, options)
	ts.workerStopped = false
	ts.registerWorkflowsAndActivities(ts.worker)
	ts.Nil(ts.worker.Start())
}

func (ts *IntegrationTestSuite) TearDownTest() {
	ts.client.Close()
	if !ts.workerStopped {
		ts.worker.Stop()
		ts.workerStopped = true
	}
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

	// Check metrics (some may be called a non-deterministic number of times
	// based on server speed)
	ts.assertMetricCount("temporal_request", 1, "operation", "StartWorkflowExecution")
	ts.assertMetricCountAtLeast("temporal_request", 1, "operation", "RespondWorkflowTaskCompleted")
	ts.assertMetricCountAtLeast("temporal_workflow_task_queue_poll_succeed", 1)
	ts.assertMetricCountAtLeast("temporal_long_request", 3, "operation", "PollActivityTaskQueue")
	ts.assertMetricCountAtLeast("temporal_long_request", 3, "operation", "PollWorkflowTaskQueue")
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

func (ts *IntegrationTestSuite) TestPanicActivityWorkflow() {
	var res []string
	// Retry once on each activity
	const maxAttempts int32 = 2
	err := ts.executeWorkflow("test-panic-activity", ts.workflows.PanickedActivity, &res, maxAttempts)
	ts.NoError(err)
	ts.Equal([]string{
		fmt.Sprintf("act err: simulated panic on attempt %v", maxAttempts),
		fmt.Sprintf("local act err: simulated panic on attempt %v", maxAttempts),
	}, res)
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

	// Check metrics (some may be called a non-deterministic number of times
	// based on server speed)
	ts.assertMetricCount("temporal_request", 1, "operation", "StartWorkflowExecution")
	ts.assertMetricCountAtLeast("temporal_request", 1, "operation", "RespondWorkflowTaskCompleted")
	ts.Equal(ts.metricCount("temporal_request"), ts.metricCount("temporal_request_attempt"))
	ts.assertMetricCountAtLeast("temporal_activity_execution_failed", 2)
	ts.assertMetricCountAtLeast("temporal_workflow_task_queue_poll_succeed", 1)
	ts.assertMetricCountAtLeast("temporal_long_request", 4, "operation", "PollActivityTaskQueue")
	ts.assertMetricCountAtLeast("temporal_long_request", 3, "operation", "PollWorkflowTaskQueue")
	ts.Equal(ts.metricCount("temporal_long_request"), ts.metricCount("temporal_long_request_attempt"))
}

func (ts *IntegrationTestSuite) TestActivityNotRegisteredRetry() {
	var expected string
	err := ts.executeWorkflow("test-activity-retry-on-error", ts.workflows.CallUnregisteredActivityRetry, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, "done")

	// Check metric (may be called a non-deterministic number of times based on
	// server speed)
	ts.assertMetricCountAtLeast("temporal_unregistered_activity_invocation", 2)
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

func (ts *IntegrationTestSuite) TestHeartbeatOnActivityFailure() {
	var heartbeatCounts int
	err := ts.executeWorkflow("test-heartbeat-on-activity-failure",
		ts.workflows.ActivityHeartbeatWithRetry, &heartbeatCounts)
	ts.NoError(err)
	// Final count should be 6 because the activity is called 3 times (first 2
	// fail) and each activity heartbeats twice. Before fixing a bug where the
	// gRPC call wasn't made on activity failure, this was 4 because the first 2
	// failing activities didn't have their second heartbeats recorded.
	ts.Equal(6, heartbeatCounts)
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
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "HandleSignal", "Go", "ExecuteWorkflow end", "HandleQuery begin", "HandleQuery end"},
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
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "HandleSignal", "HandleSignal", "ExecuteWorkflow end"},
		ts.tracer.GetTrace("SignalWorkflow"))
}

func (ts *IntegrationTestSuite) TestSignalWorkflowWithInterceptorError() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	// Return error 3 times from the interceptor
	ts.inboundSignalInterceptor.ReturnErrorTimes = 3
	wfOpts := ts.startWorkflowOptions("test-signal-workflow-interceptor-error")
	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.SignalWorkflow)
	ts.Nil(err)
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow-interceptor-error", run.GetRunID(), "string-signal", "string-value")
	ts.NoError(err)

	wt := &commonpb.WorkflowType{Name: "workflow-type"}
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow-interceptor-error", run.GetRunID(), "proto-signal", wt)
	ts.NoError(err)

	var protoValue *commonpb.WorkflowType
	err = run.Get(ctx, &protoValue)
	// Workflow should succeed after retries upon an error in the signal interceptor
	ts.NoError(err)
	// Expect that interceptors were called as many times as 2 signals plus the number of times error was induced into the chain.
	ts.Equal(2+ts.inboundSignalInterceptor.ReturnErrorTimes, ts.inboundSignalInterceptor.TimesInvoked)
}

func (ts *IntegrationTestSuite) TestSignalWorkflowWithStubbornGrpcError() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	ts.trafficController.AddError("SignalWorkflowExecution", serviceerror.NewInternal("server failure"), test.FailAllAttempts)
	wfOpts := ts.startWorkflowOptions("test-signal-workflow-grpc-error")
	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.SignalWorkflow)
	ts.Nil(err)
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow-grpc-error", run.GetRunID(), "string-signal", "string-value")
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
	ts.True(strings.HasPrefix(applicationErr.Error(), "child workflow execution already started"))
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
	ts.True(strings.HasPrefix(applicationErr.Error(), "child workflow execution already started"))
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

func (ts *IntegrationTestSuite) TestWorkflowIDReuseIgnoreDuplicateWhileRunning() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start two workflows with the same ID but different params
	opts := ts.startWorkflowOptions("test-workflow-id-reuse-ignore-dupes-" + uuid.New())
	run1, err := ts.client.ExecuteWorkflow(ctx, opts, ts.workflows.WaitSignalReturnParam, "run1")
	ts.NoError(err)
	run2, err := ts.client.ExecuteWorkflow(ctx, opts, ts.workflows.WaitSignalReturnParam, "run2")
	ts.NoError(err)

	// Confirm both runs have the same ID and run ID since the first one wasn't
	// done when we tried the second
	ts.Equal(run1.GetID(), run2.GetID())
	ts.Equal(run1.GetRunID(), run2.GetRunID())

	// Send signal to each (though in practice they both have the same ID and run
	// ID, so it's really just two signals)
	err = ts.client.SignalWorkflow(ctx, run1.GetID(), run1.GetRunID(), "done-signal", true)
	ts.NoError(err)
	err = ts.client.SignalWorkflow(ctx, run2.GetID(), run2.GetRunID(), "done-signal", true)
	ts.NoError(err)

	// Wait for responses and confirm they are the same "run1" which is the first
	// param
	var result string
	ts.NoError(run1.Get(ctx, &result))
	ts.Equal("run1", result)
	ts.NoError(run2.Get(ctx, &result))
	ts.Equal("run1", result)

	// Now start a third and confirm it is a new run ID because the other two are
	// done
	run3, err := ts.client.ExecuteWorkflow(ctx, opts, ts.workflows.WaitSignalReturnParam, "run1")
	ts.NoError(err)
	ts.Equal(run1.GetID(), run3.GetID())
	ts.NotEqual(run1.GetRunID(), run3.GetRunID())
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
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "ExecuteActivity", "HandleSignal", "Go", "ExecuteActivity", "ExecuteActivity", "ExecuteWorkflow end"},
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

func (ts *IntegrationTestSuite) TestEndToEndLatencyMetrics() {
	fetchMetrics := func() (localMetric, nonLocalMetric *metrics.CapturedTimer) {
		for _, timer := range ts.metricsHandler.Timers() {
			timer := timer
			if timer.Name == "temporal_activity_succeed_endtoend_latency" {
				nonLocalMetric = timer
			} else if timer.Name == "temporal_local_activity_succeed_endtoend_latency" {
				localMetric = timer
			}
		}
		return
	}

	// Confirm no metrics to start
	local, nonLocal := fetchMetrics()
	ts.Nil(local)
	ts.Nil(nonLocal)

	// Run regular activity and confirm non-local metric
	err := ts.executeWorkflow("test-end-to-end-metrics-1", ts.workflows.InspectActivityInfo, nil)
	ts.NoError(err)
	local, nonLocal = fetchMetrics()
	ts.Nil(local)
	ts.NotNil(nonLocal)
	ts.NotZero(nonLocal.Value())
	prevNonLocalValue := nonLocal.Value()

	// Run local activity and confirm local metric (and that non-local didn't
	// change)
	err = ts.executeWorkflow("test-end-to-end-metrics-2", ts.workflows.InspectLocalActivityInfo, nil)
	ts.NoError(err)
	local, nonLocal = fetchMetrics()
	ts.NotNil(local)
	ts.NotZero(nonLocal.Value())
	ts.NotNil(nonLocal)
	ts.Equal(prevNonLocalValue, nonLocal.Value())
}
func (ts *IntegrationTestSuite) TestGracefulActivityCompletion() {
	// FYI, setup of this test allows the worker to wait to stop for 10 seconds
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start workflow
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-graceful-activity-completion-"+uuid.New()),
		ts.workflows.ActivityWaitForWorkerStop, 10*time.Second)
	ts.NoError(err)

	// Wait for activity to report started
	for ts.activities.invokedCount("wait-for-worker-stop") == 0 && ctx.Err() == nil {
		time.Sleep(100 * time.Millisecond)
	}
	ts.NoError(ctx.Err())

	// Stop the worker
	ts.worker.Stop()
	ts.workerStopped = true

	// Look for activity completed from the history
	var completed *historypb.ActivityTaskCompletedEventAttributes
	iter := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(),
		false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for completed == nil && iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		if event.EventType == enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED {
			completed = event.GetActivityTaskCompletedEventAttributes()
		}
	}

	// Confirm it stored "stopped"
	ts.NotNil(completed)
	ts.Len(completed.GetResult().GetPayloads(), 1)
	var s string
	ts.NoError(converter.GetDefaultDataConverter().FromPayload(completed.Result.Payloads[0], &s))
	ts.Equal("stopped", s)
}

func (ts *IntegrationTestSuite) TestCancelChildAndExecuteActivityRace() {
	err := ts.executeWorkflow("cancel-child-and-execute-act-race", ts.workflows.CancelChildAndExecuteActivityRace, nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestInterceptorCalls() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start workflow
	run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-interceptor-calls"),
		ts.workflows.InterceptorCalls, "root")
	ts.NoError(err)

	// Query
	queryVal, err := ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "query", "queryarg")
	ts.NoError(err)
	var queryRes string
	ts.NoError(queryVal.Get(&queryRes))
	ts.Equal("queryresult(queryarg)", queryRes)

	// Send signal
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", "finished"))

	// Confirm response
	var resStr string
	ts.NoError(run.Get(ctx, &resStr))
	ts.Equal("finished(activity(workflow(root)))", resStr)

	// Make other client calls we know will fail
	_, _ = ts.client.SignalWithStartWorkflow(ctx, "badid", "badsignal", nil, client.StartWorkflowOptions{}, "badworkflow")
	_ = ts.client.CancelWorkflow(ctx, "badid", "badrunid")
	_ = ts.client.TerminateWorkflow(ctx, "badid", "badrunid", "")

	// Prepare call checks
	type check func(call *interceptortest.RecordedCall)
	arg := func(index int, cb func(interface{})) check {
		return func(call *interceptortest.RecordedCall) { cb(call.Args[index].Interface()) }
	}
	result := func(index int, cb func(interface{})) check {
		return func(call *interceptortest.RecordedCall) { cb(call.Results[index].Interface()) }
	}
	callChecks := map[string][]check{
		// ClientOutboundInterceptor
		"ClientOutboundInterceptor.ExecuteWorkflow": {
			arg(1, func(i interface{}) {
				ts.Equal("InterceptorCalls", i.(*interceptor.ClientExecuteWorkflowInput).WorkflowType)
			}),
		},
		// WorkflowInboundInterceptor
		"WorkflowInboundInterceptor.Init": {},
		"WorkflowInboundInterceptor.ExecuteWorkflow": {
			arg(1, func(i interface{}) {
				ts.Equal("root", i.(*interceptor.ExecuteWorkflowInput).Args[0])
			}),
		},
		"WorkflowInboundInterceptor.HandleSignal": {
			arg(1, func(i interface{}) {
				in := i.(*interceptor.HandleSignalInput)
				ts.Equal("finish", in.SignalName)
				// TODO(cretz): Argument is actually a payload
				// ts.Equal("finished", in.Arg)
			}),
		},
		"WorkflowInboundInterceptor.HandleQuery": {
			arg(1, func(i interface{}) {
				in := i.(*interceptor.HandleQueryInput)
				ts.Equal("query", in.QueryType)
				ts.Equal("queryarg", in.Args[0])
			}),
			result(0, func(i interface{}) {
				ts.Equal("queryresult(queryarg)", i)
			}),
		},
		// WorkflowOutboundInterceptor
		"WorkflowOutboundInterceptor.Go": {},
		"WorkflowOutboundInterceptor.ExecuteActivity": {
			arg(1, func(i interface{}) {
				ts.Equal("InterceptorCalls", i)
			}),
		},
		"WorkflowOutboundInterceptor.ExecuteLocalActivity": {
			arg(1, func(i interface{}) {
				ts.Equal("Echo", i)
			}),
		},
		"WorkflowOutboundInterceptor.ExecuteChildWorkflow": {},
		"WorkflowOutboundInterceptor.GetInfo": {
			result(0, func(i interface{}) {
				ts.Equal("InterceptorCalls", i.(*workflow.Info).WorkflowType.Name)
			}),
		},
		"WorkflowOutboundInterceptor.GetLogger":                     {},
		"WorkflowOutboundInterceptor.GetMetricsHandler":             {},
		"WorkflowOutboundInterceptor.Now":                           {},
		"WorkflowOutboundInterceptor.NewTimer":                      {},
		"WorkflowOutboundInterceptor.Sleep":                         {},
		"WorkflowOutboundInterceptor.RequestCancelExternalWorkflow": {},
		"WorkflowOutboundInterceptor.SignalExternalWorkflow":        {},
		"WorkflowOutboundInterceptor.UpsertSearchAttributes":        {},
		"WorkflowOutboundInterceptor.GetSignalChannel":              {},
		"WorkflowOutboundInterceptor.SideEffect":                    {},
		"WorkflowOutboundInterceptor.MutableSideEffect":             {},
		"WorkflowOutboundInterceptor.GetVersion":                    {},
		"WorkflowOutboundInterceptor.SetQueryHandler":               {},
		"WorkflowOutboundInterceptor.IsReplaying":                   {},
		"WorkflowOutboundInterceptor.HasLastCompletionResult":       {},
		"WorkflowOutboundInterceptor.GetLastCompletionResult":       {},
		"WorkflowOutboundInterceptor.GetLastError":                  {},
		"WorkflowOutboundInterceptor.NewContinueAsNewError":         {},
		// ActivityInboundInterceptor
		"ActivityInboundInterceptor.Init": {},
		"ActivityInboundInterceptor.ExecuteActivity": {
			arg(1, func(i interface{}) {
				ts.Equal("workflow(root)", i.(*interceptor.ExecuteActivityInput).Args[0])
			}),
			result(0, func(i interface{}) {
				ts.Equal("activity(workflow(root))", i)
			}),
		},
		// ActivityOutboundInterceptor
		"ActivityOutboundInterceptor.GetInfo": {
			result(0, func(i interface{}) {
				ts.Equal("InterceptorCalls", i.(activity.Info).ActivityType.Name)
			}),
		},
		"ActivityOutboundInterceptor.GetLogger":            {},
		"ActivityOutboundInterceptor.GetMetricsHandler":    {},
		"ActivityOutboundInterceptor.RecordHeartbeat":      {},
		"ActivityOutboundInterceptor.HasHeartbeatDetails":  {},
		"ActivityOutboundInterceptor.GetHeartbeatDetails":  {},
		"ActivityOutboundInterceptor.GetWorkerStopChannel": {},
	}

	// Do call checks
	for qualifiedName, checks := range callChecks {
		// Find call
		var call *interceptortest.RecordedCall
		for _, maybeCall := range ts.interceptorCallRecorder.Calls() {
			if maybeCall.Interface.Name()+"."+maybeCall.Method.Name == qualifiedName {
				call = maybeCall
				break
			}
		}
		ts.NotNilf(call, "Can't find call for %v", qualifiedName)
		// Do checks
		for _, check := range checks {
			check(call)
		}
	}
}

func (ts *IntegrationTestSuite) TestInterceptorStartWithSignal() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal with start
	run, err := ts.client.SignalWithStartWorkflow(ctx, "test-interceptor-start-with-signal", "start-signal",
		"signal-value", ts.startWorkflowOptions("test-interceptor-start-with-signal"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	var result string
	ts.NoError(run.Get(ctx, &result))
	ts.Equal("signal-value", result)

	// Check that handle signal was called
	foundHandleSignal := false
	for _, call := range ts.interceptorCallRecorder.Calls() {
		foundHandleSignal = call.Interface.Name()+"."+call.Method.Name == "WorkflowInboundInterceptor.HandleSignal"
		if foundHandleSignal {
			break
		}
	}
	ts.True(foundHandleSignal)
}

func (ts *IntegrationTestSuite) TestOpenTelemetryTracing() {
	ts.testOpenTelemetryTracing(true)
}

func (ts *IntegrationTestSuite) TestOpenTelemetryTracingWithoutSignalsAndQueries() {
	ts.testOpenTelemetryTracing(false)
}

func (ts *IntegrationTestSuite) testOpenTelemetryTracing(withSignalAndQueryHeaders bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Start a top-level span
	ctx, rootSpan := ts.openTelemetryTracer.Start(ctx, "root-span")

	// Signal with start
	run, err := ts.client.SignalWithStartWorkflow(ctx, "test-interceptor-open-telemetry", "start-signal",
		nil, ts.startWorkflowOptions("test-interceptor-open-telemetry"), ts.workflows.SignalsAndQueries, true, true)
	ts.NoError(err)

	// Query
	val, err := ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "workflow-query", nil)
	ts.NoError(err)
	var queryResp string
	ts.NoError(val.Get(&queryResp))
	ts.Equal("query-response", queryResp)

	// Finish signal
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish-signal", nil))
	ts.NoError(run.Get(ctx, nil))

	// Finish span and collect
	rootSpan.End()
	spans := ts.openTelemetrySpanRecorder.Ended()

	// Span builder
	span := func(name string, children ...*interceptortest.SpanInfo) *interceptortest.SpanInfo {
		// If without signal-and-query headers, filter out those children in place
		if !withSignalAndQueryHeaders {
			n := 0
			for _, child := range children {
				isSignalOrQuery := strings.HasPrefix(child.Name, "SignalWorkflow:") ||
					strings.HasPrefix(child.Name, "SignalChildWorkflow:") ||
					strings.HasPrefix(child.Name, "HandleSignal:") ||
					strings.HasPrefix(child.Name, "QueryWorkflow:") ||
					strings.HasPrefix(child.Name, "HandleQuery:")
				if !isSignalOrQuery {
					children[n] = child
					n++
				}
			}
			children = children[:n]
		}
		return interceptortest.Span(name, children...)
	}

	// Confirm expected
	actual := interceptortest.Span("root-span", ts.openTelemetrySpanChildren(spans, rootSpan.SpanContext().SpanID())...)
	expected := span("root-span",
		span("SignalWithStartWorkflow:SignalsAndQueries",
			span("HandleSignal:start-signal"),
			span("RunWorkflow:SignalsAndQueries",
				// Child workflow exec
				span("StartChildWorkflow:SignalsAndQueries",
					span("RunWorkflow:SignalsAndQueries",
						// Activity inside child workflow
						span("StartActivity:ExternalSignalsAndQueries",
							span("RunActivity:ExternalSignalsAndQueries",
								// Signal and query inside activity
								span("SignalWithStartWorkflow:SignalsAndQueries",
									span("HandleSignal:start-signal"),
									span("RunWorkflow:SignalsAndQueries"),
								),
								span("QueryWorkflow:workflow-query",
									span("HandleQuery:workflow-query"),
								),
								span("SignalWorkflow:finish-signal",
									span("HandleSignal:finish-signal"),
								),
							),
						),
					),
				),
				span("SignalChildWorkflow:start-signal",
					span("HandleSignal:start-signal"),
				),
				span("SignalChildWorkflow:finish-signal",
					span("HandleSignal:finish-signal"),
				),
				// Activity in top-level
				span("StartActivity:ExternalSignalsAndQueries",
					span("RunActivity:ExternalSignalsAndQueries",
						span("SignalWithStartWorkflow:SignalsAndQueries",
							span("HandleSignal:start-signal"),
							span("RunWorkflow:SignalsAndQueries"),
						),
						span("QueryWorkflow:workflow-query",
							span("HandleQuery:workflow-query"),
						),
						span("SignalWorkflow:finish-signal",
							span("HandleSignal:finish-signal"),
						),
					),
				),
			),
		),
		// Top-level query and signal
		span("QueryWorkflow:workflow-query",
			span("HandleQuery:workflow-query"),
		),
		span("SignalWorkflow:finish-signal",
			span("HandleSignal:finish-signal"),
		),
	)

	ts.Equal(expected, actual)
}

func (ts *IntegrationTestSuite) openTelemetrySpanChildren(
	spans []sdktrace.ReadOnlySpan,
	parentID trace.SpanID,
) (ret []*interceptortest.SpanInfo) {
	var lastSpan *interceptortest.SpanInfo
	for _, s := range spans {
		if s.Parent().SpanID() == parentID {
			// In cases where we have disabled the cache, the same interceptors get
			// called many times with replayed values which create replayed spans. We
			// can't disable  spans during replay because they are sometimes the ones
			// that code paths continue on so they need spans. So in this case we will
			// reuse adjacent spans of the same name.
			children := ts.openTelemetrySpanChildren(spans, s.SpanContext().SpanID())
			if ts.config.maxWorkflowCacheSize == 0 && lastSpan != nil && lastSpan.Name == s.Name() {
				lastSpan.Children = append(lastSpan.Children, children...)
			} else {
				lastSpan = interceptortest.Span(s.Name(), children...)
				ret = append(ret, lastSpan)
			}
		}
	}
	return
}

func (ts *IntegrationTestSuite) TestAdvancedPostCancellation() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	assertPostCancellation := func(in *AdvancedPostCancellationInput) {
		// Start workflow
		run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-advanced-post-cancellation-"+uuid.New()),
			ts.workflows.AdvancedPostCancellation, in)
		ts.NoError(err)

		// Wait for cancel
		ts.waitForQueryTrue(run, "waiting-for-cancel")

		// Now cancel it
		ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

		// Confirm no error
		ts.NoError(run.Get(ctx, nil))
	}

	// Check just activity and timer
	assertPostCancellation(&AdvancedPostCancellationInput{
		PreCancelActivity:  true,
		PostCancelActivity: true,
	})
	assertPostCancellation(&AdvancedPostCancellationInput{
		PreCancelTimer:  true,
		PostCancelTimer: true,
	})
	// Check mixed
	assertPostCancellation(&AdvancedPostCancellationInput{
		PreCancelActivity: true,
		PostCancelTimer:   true,
	})
	assertPostCancellation(&AdvancedPostCancellationInput{
		PreCancelTimer:     true,
		PostCancelActivity: true,
	})
	// Check all
	assertPostCancellation(&AdvancedPostCancellationInput{
		PreCancelActivity:  true,
		PreCancelTimer:     true,
		PostCancelActivity: true,
		PostCancelTimer:    true,
	})
}

func (ts *IntegrationTestSuite) TestAdvancedPostCancellationChildWithDone() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start workflow
	startOpts := ts.startWorkflowOptions("test-advanced-post-cancellation-child-with-done-" + uuid.New())
	run, err := ts.client.ExecuteWorkflow(ctx, startOpts, ts.workflows.AdvancedPostCancellationChildWithDone)
	ts.NoError(err)

	// Wait for cancel
	ts.waitForQueryTrue(run, "waiting-for-cancel")

	// Now cancel it
	ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

	// Confirm no error
	ts.NoError(run.Get(ctx, nil))
}

func (ts *IntegrationTestSuite) waitForQueryTrue(run client.WorkflowRun, query string) {
	var result bool
	for i := 0; !result && i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		val, err := ts.client.QueryWorkflow(context.Background(), run.GetID(), run.GetRunID(), query)
		// Ignore query failed because it means query may not be registered yet
		var queryFailed *serviceerror.QueryFailed
		if errors.As(err, &queryFailed) {
			continue
		}
		ts.NoError(err)
		ts.NoError(val.Get(&result))
	}
	ts.True(result, "query didn't return true in reasonable amount of time")
}

func (ts *IntegrationTestSuite) TestSlotsAvailableCounter() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	assertActivitySlotsAvailableEventually := func(expected float64, tags ...string) {
		// Try for two seconds
		var lastCount float64
		for start := time.Now(); time.Since(start) <= 2*time.Second; {
			lastCount = ts.metricGauge(metrics.WorkerTaskSlotsAvailable, "worker_type", "ActivityWorker")
			if lastCount == expected {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		// Will fail
		ts.Equal(expected, lastCount)
	}

	// Confirm all available to start
	assertActivitySlotsAvailableEventually(1000)

	// Start workflow and confirm reduced by one
	run1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-slots-available-counter-1"),
		ts.workflows.ActivityHeartbeatUntilSignal)
	ts.NoError(err)
	assertActivitySlotsAvailableEventually(999)

	// Start two more and confirm reduced by two
	run2, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-slots-available-counter-2"),
		ts.workflows.ActivityHeartbeatUntilSignal)
	ts.NoError(err)
	run3, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-slots-available-counter-3"),
		ts.workflows.ActivityHeartbeatUntilSignal)
	ts.NoError(err)
	assertActivitySlotsAvailableEventually(997)

	// Signal the first and last to close and confirm increased by two
	time.Sleep(2 * time.Second)
	ts.NoError(ts.client.SignalWorkflow(ctx, run1.GetID(), run1.GetRunID(), "cancel", nil))
	ts.NoError(ts.client.SignalWorkflow(ctx, run3.GetID(), run3.GetRunID(), "cancel", nil))
	ts.NoError(run1.Get(ctx, nil))
	ts.NoError(run3.Get(ctx, nil))
	assertActivitySlotsAvailableEventually(999)

	// Signal the middle to close and confirm increased by one
	ts.NoError(ts.client.SignalWorkflow(ctx, run2.GetID(), run2.GetRunID(), "cancel", nil))
	ts.NoError(run2.Get(ctx, nil))
	assertActivitySlotsAvailableEventually(1000)
}

func (ts *IntegrationTestSuite) TestTooFewParams() {
	var res ParamsValue
	// Only give first param
	ts.NoError(ts.executeWorkflow("test-too-few-params", "TooFewParams", &res, "first param"))
	// Confirm workflow and activity were called with zero values
	ts.Equal(ParamsValue{Param1: "first param", Child: &ParamsValue{Param1: "first param"}}, res)
}

func (ts *IntegrationTestSuite) TestTallyScopeAccess() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tallyScopeAccessWorkflow := func(ctx workflow.Context) error {
		hist := contribtally.ScopeFromHandler(workflow.GetMetricsHandler(ctx)).Histogram("some_histogram", nil)
		// This records even during replay
		hist.RecordDuration(5 * time.Second)
		return workflow.SetQueryHandler(ctx, "some-query", func() (string, error) { return "ok", nil })
	}

	ts.worker.RegisterWorkflow(tallyScopeAccessWorkflow)
	run, err := ts.client.ExecuteWorkflow(context.TODO(),
		ts.startWorkflowOptions("tally-scope-access-"+uuid.New()), tallyScopeAccessWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(context.TODO(), nil))

	assertHistDuration := func(name string, d time.Duration, expected int64) {
		for _, hist := range ts.tallyScope.Snapshot().Histograms() {
			if hist.Name() == name {
				ts.Equal(expected, hist.Durations()[d])
				return
			}
		}
		ts.Fail("no histogram")
	}
	// Confirm hit once
	assertHistDuration("some_histogram", 5*time.Second, 1)

	// Query the workflow and confirm hit during replay
	_, err = ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "some-query")
	ts.NoError(err)
	assertHistDuration("some_histogram", 5*time.Second, 2)
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

var _ interceptor.WorkerInterceptor = (*tracingInterceptor)(nil)
var _ interceptor.WorkflowInboundInterceptor = (*tracingWorkflowInboundInterceptor)(nil)
var _ interceptor.WorkflowOutboundInterceptor = (*tracingWorkflowOutboundInterceptor)(nil)

type tracingInterceptor struct {
	interceptor.WorkerInterceptorBase
	sync.Mutex
	// key is workflow id
	instances map[string]*tracingWorkflowInboundInterceptor
}

type tracingWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	trace []string
}

type tracingWorkflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	inbound *tracingWorkflowInboundInterceptor
}

func (t *tracingWorkflowOutboundInterceptor) Go(ctx workflow.Context, name string, f func(ctx workflow.Context)) workflow.Context {
	t.inbound.trace = append(t.inbound.trace, "Go")
	return t.Next.Go(ctx, name, f)
}

func newTracingInterceptor() *tracingInterceptor {
	return &tracingInterceptor{instances: make(map[string]*tracingWorkflowInboundInterceptor)}
}

func (t *tracingInterceptor) InterceptWorkflow(ctx workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	var result tracingWorkflowInboundInterceptor
	result.Next = next
	t.instances[workflow.GetInfo(ctx).WorkflowType.Name] = &result
	return &result
}

func (t *tracingInterceptor) GetTrace(workflowType string) []string {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	if i, ok := t.instances[workflowType]; ok {
		return i.trace
	}
	panic(fmt.Sprintf("Unknown workflowType %v, known types: %v", workflowType, t.instances))
}

func (t *tracingWorkflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return t.Next.Init(&tracingWorkflowOutboundInterceptor{
		interceptor.WorkflowOutboundInterceptorBase{Next: outbound}, t})
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteActivity(ctx workflow.Context, activityType string, args ...interface{}) workflow.Future {
	t.inbound.trace = append(t.inbound.trace, "ExecuteActivity")
	return t.Next.ExecuteActivity(ctx, activityType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteChildWorkflow(ctx workflow.Context, childWorkflowType string, args ...interface{}) workflow.ChildWorkflowFuture {
	t.inbound.trace = append(t.inbound.trace, "ExecuteChildWorkflow")
	return t.Next.ExecuteChildWorkflow(ctx, childWorkflowType, args...)
}

func (t *tracingWorkflowInboundInterceptor) ExecuteWorkflow(ctx workflow.Context, in *interceptor.ExecuteWorkflowInput) (interface{}, error) {
	t.trace = append(t.trace, "ExecuteWorkflow begin")
	result, err := t.Next.ExecuteWorkflow(ctx, in)
	t.trace = append(t.trace, "ExecuteWorkflow end")
	return result, err
}

func (t *tracingWorkflowInboundInterceptor) HandleSignal(ctx workflow.Context, in *interceptor.HandleSignalInput) error {
	t.trace = append(t.trace, "HandleSignal")
	return t.Next.HandleSignal(ctx, in)
}

func (t *tracingWorkflowInboundInterceptor) HandleQuery(ctx workflow.Context, in *interceptor.HandleQueryInput) (interface{}, error) {
	t.trace = append(t.trace, "HandleQuery begin")
	result, err := t.Next.HandleQuery(ctx, in)
	t.trace = append(t.trace, "HandleQuery end")
	return result, err
}

var _ interceptor.WorkerInterceptor = (*signalInterceptor)(nil)
var _ interceptor.WorkflowInboundInterceptor = (*signalWorkflowInboundInterceptor)(nil)

type signalInterceptor struct {
	interceptor.WorkerInterceptorBase
	ReturnErrorTimes int
	TimesInvoked     int
}

func newSignalInterceptor() *signalInterceptor {
	return &signalInterceptor{}
}

type signalWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	control *signalInterceptor
}

func (t *signalWorkflowInboundInterceptor) HandleSignal(ctx workflow.Context, in *interceptor.HandleSignalInput) error {
	t.control.TimesInvoked++
	if t.control.TimesInvoked <= t.control.ReturnErrorTimes {
		return fmt.Errorf("interceptor induced failure while processing signal %v", in.SignalName)
	}
	return t.Next.HandleSignal(ctx, in)
}

func (t *signalInterceptor) InterceptWorkflow(ctx workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	result := &signalWorkflowInboundInterceptor{}
	result.Next = next
	result.control = t
	return result
}

func (ts *IntegrationTestSuite) metricCount(name string, tagFilterKeyValue ...string) (total int64) {
	for _, counter := range ts.metricsHandler.Counters() {
		if counter.Name != name {
			continue
		}
		// Check that it matches tag filter
		validCounter := true
		for i := 0; i < len(tagFilterKeyValue); i += 2 {
			if counter.Tags[tagFilterKeyValue[i]] != tagFilterKeyValue[i+1] {
				validCounter = false
				break
			}
		}
		if validCounter {
			total += counter.Value()
		}
	}
	return
}

func (ts *IntegrationTestSuite) metricGauge(name string, tagFilterKeyValue ...string) (final float64) {
	for _, gauge := range ts.metricsHandler.Gauges() {
		if gauge.Name != name {
			continue
		}
		// Check that it matches tag filter
		validCounter := true
		for i := 0; i < len(tagFilterKeyValue); i += 2 {
			if gauge.Tags[tagFilterKeyValue[i]] != tagFilterKeyValue[i+1] {
				validCounter = false
				break
			}
		}
		if validCounter {
			final = gauge.Value()
		}
	}
	return
}

func (ts *IntegrationTestSuite) assertMetricCount(name string, value int64, tagFilterKeyValue ...string) {
	ts.Equal(value, ts.metricCount(name, tagFilterKeyValue...))
}

func (ts *IntegrationTestSuite) assertMetricCountAtLeast(name string, value int64, tagFilterKeyValue ...string) {
	ts.GreaterOrEqual(ts.metricCount(name, tagFilterKeyValue...), value)
}

func (ts *IntegrationTestSuite) assertReportedOperationCount(metricName string, operation string, expectedCount int) {
	count := ts.getReportedOperationCount(metricName, operation)
	ts.EqualValues(expectedCount, count, fmt.Sprintf("Metric %v for operation %v has been reported unexpected number of times", metricName, operation))
}

func (ts *IntegrationTestSuite) getReportedOperationCount(metricName string, operation string) int64 {
	count := int64(0)
	for _, counter := range ts.metricsHandler.Counters() {
		if counter.Name != metricName {
			continue
		}
		if op, ok := counter.Tags[metrics.OperationTagName]; ok && op == operation {
			count += counter.Value()
		}
	}
	return count
}
