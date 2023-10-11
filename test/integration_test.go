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
	"go.opentelemetry.io/otel/baggage"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
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
	"go.uber.org/goleak"
	"google.golang.org/grpc"

	"go.temporal.io/sdk/contrib/opentelemetry"
	sdkopentracing "go.temporal.io/sdk/contrib/opentracing"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/test"

	historypb "go.temporal.io/api/history/v1"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	contribtally "go.temporal.io/sdk/contrib/tally"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal"
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
	namespaceCacheRefreshInterval = 20 * time.Second
	testContextKey1               = "test-context-key1"
	testContextKey2               = "test-context-key2"
	testContextKey3               = "test-context-key3"
)

type IntegrationTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	activities                *Activities
	workflows                 *Workflows
	worker                    worker.Worker
	workerStopped             bool
	tracer                    *tracingInterceptor
	inboundSignalInterceptor  *signalInterceptor
	trafficController         *test.SimpleTrafficController
	metricsHandler            *metrics.CapturingHandler
	tallyScope                tally.TestScope
	interceptorCallRecorder   *interceptortest.CallRecordingInvoker
	openTelemetryTracer       trace.Tracer
	openTelemetrySpanRecorder *tracetest.SpanRecorder
	openTracingTracer         opentracing.Tracer
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

func (ts *IntegrationTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.activities = newActivities()
	ts.workflows = &Workflows{}
	ts.NoError(ts.InitConfigAndNamespace())
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
	var workerInterceptors []interceptor.WorkerInterceptor
	// Record calls for interceptor test
	if strings.HasPrefix(ts.T().Name(), "TestIntegrationSuite/TestInterceptor") {
		ts.interceptorCallRecorder = &interceptortest.CallRecordingInvoker{}
		clientInterceptors = append(clientInterceptors, interceptortest.NewProxy(ts.interceptorCallRecorder))
	}

	// Record spans for tracing test
	if strings.HasPrefix(ts.T().Name(), "TestIntegrationSuite/TestOpenTelemetryTracing") ||
		strings.HasPrefix(ts.T().Name(), "TestIntegrationSuite/TestOpenTelemetryBaggageHandling") {
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
	} else if strings.HasPrefix(ts.T().Name(), "TestIntegrationSuite/TestOpenTracingNoopTracer") {
		ts.openTracingTracer = opentracing.NoopTracer{}
		interceptor, err := sdkopentracing.NewInterceptor(sdkopentracing.TracerOptions{Tracer: ts.openTracingTracer})
		ts.NoError(err)
		clientInterceptors = append(clientInterceptors, interceptor)
	}

	var err error
	trafficController := test.NewSimpleTrafficController()
	ts.client, err = client.Dial(client.Options{
		HostPort:  ts.config.ServiceAddr,
		Namespace: ts.config.Namespace,
		Logger:    ilog.NewDefaultLogger(),
		ContextPropagators: []workflow.ContextPropagator{
			NewKeysPropagator([]string{testContextKey1}),
			NewKeysPropagator([]string{testContextKey2}),
		},
		MetricsHandler:    metricsHandler,
		TrafficController: trafficController,
		Interceptors:      clientInterceptors,
		ConnectionOptions: client.ConnectionOptions{TLS: ts.config.TLS},
	})
	ts.NoError(err)

	ts.trafficController = trafficController
	ts.activities.clearInvoked()
	ts.activities.client = ts.client
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
	ts.tracer = newTracingInterceptor()
	ts.inboundSignalInterceptor = newSignalInterceptor()
	workerInterceptors = append(workerInterceptors, ts.tracer, ts.inboundSignalInterceptor)
	options := worker.Options{
		Interceptors:        workerInterceptors,
		WorkflowPanicPolicy: worker.FailWorkflow,
	}

	worker.SetStickyWorkflowCacheSize(ts.config.maxWorkflowCacheSize)

	if strings.Contains(ts.T().Name(), "Session") {
		options.EnableSessionWorker = true
		// Limit the session execution size
		if strings.Contains(ts.T().Name(), "TestMaxConcurrentSessionExecutionSize") {
			options.MaxConcurrentSessionExecutionSize = 3
		}
	}

	if strings.Contains(ts.T().Name(), "TestSessionOnWorkerFailure") ||
		strings.Contains(ts.T().Name(), "TestNonDeterminismFailureCause") {
		// We disable sticky execution here since we kill the worker and restart it
		// and sticky execution adds a 5s penalty
		worker.SetStickyWorkflowCacheSize(0)
	}

	if strings.Contains(ts.T().Name(), "LocalActivityWorkerOnly") {
		options.LocalActivityWorkerOnly = true
	}

	if strings.Contains(ts.T().Name(), "CancelTimerViaDeferAfterWFTFailure") ||
		strings.Contains(ts.T().Name(), "TestNonDeterminismFailureCause") {
		options.WorkflowPanicPolicy = worker.BlockWorkflow
	}

	if strings.Contains(ts.T().Name(), "GracefulActivityCompletion") {
		options.WorkerStopTimeout = 10 * time.Second
	}

	if strings.Contains(ts.T().Name(), "ReplayerWithInterceptor") {
		options.Interceptors = append(options.Interceptors, &localActivityInterceptor{})
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
	// We cannot check PollActivityTaskQueue metric because eager activities
	// affect poll count
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

func (ts *IntegrationTestSuite) TestSDKNameAndVersionWritten() {
	const wfID = "test-sdk-name-and-version"
	wfOpts := ts.startWorkflowOptions(wfID)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.sleep, time.Second)
	ts.NoError(err)

	var result int
	err = run.Get(ctx, &result)
	ts.NoError(err)

	iter := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var firstTaskFound bool
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		if event.EventType == enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
			sdkName := event.GetWorkflowTaskCompletedEventAttributes().GetSdkMetadata().GetSdkName()
			sdkVersion := event.GetWorkflowTaskCompletedEventAttributes().GetSdkMetadata().GetSdkVersion()
			if !firstTaskFound {
				firstTaskFound = true
				// The name and version should only be written once if they don't change
				ts.Equal(internal.SDKName, sdkName)
				ts.Equal(internal.SDKVersion, sdkVersion)
			} else {
				ts.Equal("", sdkName)
				ts.Equal("", sdkVersion)
			}
		}
	}
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
	startOptions.RetryPolicy = &temporal.RetryPolicy{
		MaximumAttempts: 123,
	}
	err := ts.executeWorkflowWithOption(startOptions, ts.workflows.ContinueAsNewWithOptions, &result, 4, ts.taskQueueName)
	ts.NoError(err)
	ts.Equal("memoVal,searchAttr,123", result)
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
	err := replayer.ReplayWorkflowHistoryFromJSONFile(ilog.NewDefaultLogger(), "fixtures/activity.cancel.sm.repro.json")
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

func (ts *IntegrationTestSuite) TestConcurrentMapWriteWorkflow() {
	testCases := []string{"activity", "child_workflow", "timer"}
	for _, t := range testCases {
		err := ts.executeWorkflow("test-concurrent-map-write-workflow", ts.workflows.RaceOnCacheEviction, nil, t)
		ts.NoError(err)
	}
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

	err = run.Get(context.Background(), nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestCancelChildWorkflowAndParentWorkflow() {
	wfid := "test-cancel-child-workflow-and-parent-workflow"
	run, err := ts.client.ExecuteWorkflow(context.Background(),
		ts.startWorkflowOptions(wfid),
		ts.workflows.ChildWorkflowAndParentCancel)
	ts.NoError(err)

	// Give it a sec to populate the query
	<-time.After(1 * time.Second)

	v, err := ts.client.QueryWorkflow(context.Background(), run.GetID(), "", "child-and-parent-cancel-child-workflow-id")
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

	err = run.Get(context.Background(), nil)
	ts.NoError(err)

	err = ts.client.GetWorkflow(context.Background(), childWorkflowID, "").Get(context.Background(), nil)
	var canceledError *temporal.CanceledError
	ts.ErrorAs(err, &canceledError)
}

func (ts *IntegrationTestSuite) TestChildWorkflowDuplicatePanic_Regression() {
	wfid := "test-child-workflow-duplicate-panic-regression"
	run, err := ts.client.ExecuteWorkflow(context.Background(),
		ts.startWorkflowOptions(wfid),
		ts.workflows.ChildWorkflowDuplicatePanicRepro)
	ts.NoError(err)
	err = run.Get(context.Background(), nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestChildWorkflowDuplicateGetExecutionStuck_Regression() {
	wfid := "test-child-workflow-duplicate-get-execution-stuck-regression"
	run, err := ts.client.ExecuteWorkflow(context.Background(),
		ts.startWorkflowOptions(wfid),
		ts.workflows.ChildWorkflowDuplicateGetExecutionStuckRepro)
	ts.NoError(err)
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

func (ts *IntegrationTestSuite) TestWorkflowWithLocalActivityStartToClose() {
	ts.NoError(ts.executeWorkflow("test-wf-la-start-to-close", ts.workflows.WorkflowWithLocalActivityStartToCloseTimeout, nil))
}

func (ts *IntegrationTestSuite) TestActivityTimeoutsWorkflow() {
	ts.NoError(ts.executeWorkflow("test-activity-timeout-workflow", ts.workflows.ActivityTimeoutsWorkflow, nil, workflow.ActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	}))

	ts.NoError(ts.executeWorkflow("test-activity-timeout-workflow", ts.workflows.ActivityTimeoutsWorkflow, nil, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
	}))

	ts.Error(ts.executeWorkflow("test-activity-timeout-workflow", ts.workflows.ActivityTimeoutsWorkflow, nil, workflow.ActivityOptions{}))
	ts.Error(ts.executeWorkflow("test-activity-timeout-workflow", ts.workflows.ActivityTimeoutsWorkflow, nil, workflow.ActivityOptions{
		ScheduleToStartTimeout: 5 * time.Second,
	}))

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

func (ts *IntegrationTestSuite) TestMutatingQuery() {
	ctx := context.Background()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-mutating-query"), ts.workflows.MutatingQueryWorkflow)
	ts.Nil(err)
	_, err = ts.client.QueryWorkflow(ctx, "test-mutating-query", run.GetRunID(), "mutating_query")
	ts.Error(err)
	ts.Nil(ts.client.CancelWorkflow(ctx, "test-mutating-query", ""))
}

func (ts *IntegrationTestSuite) TestMutatingUpdateValidator() {
	ctx := context.Background()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-mutating-update-validator"), ts.workflows.MutatingUpdateValidatorWorkflow)
	ts.Nil(err)
	handler, err := ts.client.UpdateWorkflow(ctx, "test-mutating-update-validator", run.GetRunID(), "mutating_update")
	ts.NoError(err)

	ts.Error(handler.Get(ctx, nil))
	ts.Nil(ts.client.CancelWorkflow(ctx, "test-mutating-update-validator", ""))
}

func (ts *IntegrationTestSuite) TestWaitForCancelWithDisconnectedContext() {
	ctx := context.Background()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-wait-for-cancel-with-disconnected-contex"), ts.workflows.WaitForCancelWithDisconnectedContextWorkflow)
	ts.Nil(err)

	ts.waitForQueryTrue(run, "timer-created", 1)

	ts.Nil(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))
	ts.Nil(run.Get(ctx, nil))
}

func (ts *IntegrationTestSuite) TestMutatingSideEffect() {
	ctx := context.Background()
	err := ts.executeWorkflowWithContextAndOption(ctx, ts.startWorkflowOptions("test-mutating-side-effect"), ts.workflows.MutatingSideEffectWorkflow, nil)
	ts.Error(err)
}

func (ts *IntegrationTestSuite) TestMutatingMutableSideEffect() {
	ctx := context.Background()
	err := ts.executeWorkflowWithContextAndOption(ctx, ts.startWorkflowOptions("test-mutating-mutable-side-effect"), ts.workflows.MutatingMutableSideEffectWorkflow, nil)
	ts.Error(err)
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

func (ts *IntegrationTestSuite) TestUpdateInfo() {
	ctx := context.Background()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-update-info"), ts.workflows.UpdateInfoWorkflow)
	ts.Nil(err)
	// Send an update request with a know update ID
	handler, err := ts.client.UpdateWorkflowWithOptions(ctx, &client.UpdateWorkflowWithOptionsRequest{
		UpdateID:   "testID",
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
		UpdateName: "update",
	})
	ts.NoError(err)
	// Verify the upate handler can access the update info and return the updateID
	var result string
	ts.NoError(handler.Get(ctx, &result))
	ts.Equal("testID", result)
	// Test the update validator can also use the update info
	handler, err = ts.client.UpdateWorkflowWithOptions(ctx, &client.UpdateWorkflowWithOptionsRequest{
		UpdateID:   "notTestID",
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
		UpdateName: "update",
	})
	ts.NoError(err)
	err = handler.Get(ctx, nil)
	ts.Error(err)
	// complete workflow
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", "finished"))
	ts.NoError(run.Get(ctx, nil))
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

func (ts *IntegrationTestSuite) TestEagerWorkflowDispatchRaceWithWorkerStop() {
	// Attempt to stop a worker while trying to schedule an eager workflow task
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, ctxTimeout)
		defer cancel()
		_, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-basic-session"), ts.workflows.SimplestWorkflow)
		ts.NoError(err)
		wg.Done()
	}()
	go func() {
		ts.worker.Stop()
		ts.workerStopped = true
		wg.Done()
	}()
	wg.Wait()
}

func (ts *IntegrationTestSuite) TestSessionStateFailedWorkerFailed() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts.activities.manualStopContext = ctx
	// We want to start a single long-running activity in a session
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-session-worker-failure"),
		ts.workflows.SessionFailedStateWorkflow,
		&AdvancedSessionParams{
			SessionCount:           1,
			SessionCreationTimeout: 10 * time.Second,
		})
	ts.NoError(err)

	// Wait until sessions started
	ts.waitForQueryTrue(run, "sessions-created-equals", 1)

	// Kill the worker, this should cause the session to timeout.
	ts.worker.Stop()
	ts.workerStopped = true

	// Now create a new worker on that same task queue to resume the work of the
	// workflow
	nextWorker := worker.New(ts.client, ts.taskQueueName, worker.Options{DisableStickyExecution: true})
	ts.registerWorkflowsAndActivities(nextWorker)
	ts.NoError(nextWorker.Start())
	defer nextWorker.Stop()

	// Get the result of the workflow run now
	err = run.Get(ctx, nil)
	ts.NoError(err)
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
			pendingActivities[1].State == enumspb.PENDING_ACTIVITY_STATE_STARTED &&
			len(ts.activities.invoked()) == 2 &&
			ts.activities.invoked()[0] == "asyncComplete" &&
			ts.activities.invoked()[1] == "asyncComplete" {
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

	// Complete first activity using ID
	err = ts.client.CompleteActivityByID(ctx, ts.config.Namespace,
		workflowRun.GetID(), workflowRun.GetRunID(), "A", "activityA completed", nil)
	ts.Nil(err)

	// Complete second activity using ID
	err = ts.client.CompleteActivityByID(ctx, ts.config.Namespace,
		workflowRun.GetID(), workflowRun.GetRunID(), "B", "activityB completed", nil)
	ts.Nil(err)

	// Now wait for workflow to complete
	var result []string
	err = workflowRun.Get(ctx, &result)
	ts.Nil(err)
	ts.EqualValues([]string{"activityA completed", "activityB completed"}, result)
}

func (ts *IntegrationTestSuite) TestVersionLoopWorkflow() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-version-loop-workflow"), ts.workflows.VersionLoopWorkflow, []string{"changeID_1", "changeID_2", "changeID_3"}, 126)
	ts.NoError(err)

	err = run.Get(ctx, nil)
	ts.NoError(err)

	resp, err := ts.client.DescribeWorkflowExecution(ctx, run.GetID(), run.GetRunID())
	ts.NoError(err)
	size := len(resp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[internal.TemporalChangeVersion].Data)
	ts.Less(size, 2048)
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

func (ts *IntegrationTestSuite) TestStartDelay() {
	const wfID = "test-start-delay"
	wfOpts := ts.startWorkflowOptions(wfID)
	wfOpts.StartDelay = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.sleep, time.Second)
	ts.NoError(err)

	var result int
	err = run.Get(ctx, &result)
	ts.NoError(err)

	iter := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	event, err := iter.Next()
	ts.NoError(err)
	ts.Equal(enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED, event.EventType)
	ts.Equal(5*time.Second, *event.GetWorkflowExecutionStartedEventAttributes().GetFirstWorkflowTaskBackoff())
}

func (ts *IntegrationTestSuite) TestStartDelaySignalWithStart() {
	const wfID = "test-start-delay-signal-with-start"
	wfOpts := ts.startWorkflowOptions(wfID)
	wfOpts.StartDelay = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := ts.client.SignalWithStartWorkflow(ctx, wfID, "done-signal", true, wfOpts, ts.workflows.WaitSignalReturnParam, 0)
	ts.NoError(err)

	var result int
	err = run.Get(ctx, &result)
	ts.NoError(err)

	iter := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	event, err := iter.Next()
	ts.NoError(err)
	ts.Equal(enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED, event.EventType)
	ts.Equal(5*time.Second, *event.GetWorkflowExecutionStartedEventAttributes().GetFirstWorkflowTaskBackoff())
}

func (ts *IntegrationTestSuite) TestResetWorkflowExecution() {
	var originalResult []string
	err := ts.executeWorkflow("basic-reset-workflow-execution", ts.workflows.Basic, &originalResult)
	ts.NoError(err)
	resp, err := ts.client.ResetWorkflowExecution(context.Background(), &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: ts.config.Namespace,
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

	// Query with options
	response, err := ts.client.QueryWorkflowWithOptions(ctx, &client.QueryWorkflowWithOptionsRequest{
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
		QueryType:  "query",
		Args:       []interface{}{"queryarg"},
	})
	ts.NoError(err)
	ts.NoError(response.QueryResult.Get(&queryRes))
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
		"WorkflowOutboundInterceptor.UpsertMemo":                    {},
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
	actual := interceptortest.Span("root-span")
	ts.addOpenTelemetryChildren(rootSpan.SpanContext().SpanID(), actual, spans)
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

func (ts *IntegrationTestSuite) addOpenTelemetryChildren(
	parentID trace.SpanID,
	parentSpan *interceptortest.SpanInfo,
	spans []sdktrace.ReadOnlySpan,
) {
	// Add any children that are not already present. We have to dedupe children
	// recursively like this because, in cases where we have disabled the cache,
	// the same interceptor may be called many times in duplicated ways but we
	// only want the unique set based on name.
	for _, s := range spans {
		// Must be same parent
		if s.Parent().SpanID() != parentID {
			continue
		}
		// Try to find child that already exists by name
		var child *interceptortest.SpanInfo
		for _, maybeChild := range parentSpan.Children {
			if maybeChild.Name == s.Name() {
				child = maybeChild
				break
			}
		}
		// Add child if not there
		if child == nil {
			child = interceptortest.Span(s.Name())
			parentSpan.Children = append(parentSpan.Children, child)
		}
		// Collect grandchildren
		ts.addOpenTelemetryChildren(s.SpanContext().SpanID(), child, spans)
	}
}

func (ts *IntegrationTestSuite) TestOpenTelemetryBaggageHandling() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Start a top-level span
	ctx, rootSpan := ts.openTelemetryTracer.Start(ctx, "root-span")
	defer rootSpan.End()

	// Add baggage to context
	expectedBaggage := "baggage-value"
	bag := baggage.FromContext(ctx)
	member, _ := baggage.NewMember("baggage-key", expectedBaggage)
	bag, _ = bag.SetMember(member)
	ctx = baggage.ContextWithBaggage(ctx, bag)

	// Start workflow
	var actualBaggage string
	opts := ts.startWorkflowOptions("test-interceptor-open-telemetry-baggage")
	err := ts.executeWorkflowWithContextAndOption(ctx, opts, ts.workflows.CheckOpenTelemetryBaggage, &actualBaggage, "baggage-key")
	ts.NoError(err)

	ts.Equal(expectedBaggage, actualBaggage)
}

func (ts *IntegrationTestSuite) TestOpenTracingNoopTracer() {
	// The setup of the test already puts a noop tracer on the client. In past
	// versions, this would break due to tracer.Extract returning an error every
	// time.
	ts.NotNil(ts.openTracingTracer)
	var ret string
	ts.NoError(ts.executeWorkflow("test-open-tracing-worker-no-client", ts.workflows.SimplestWorkflow, &ret))
	ts.Equal("hello", ret)
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

func (ts *IntegrationTestSuite) waitForQueryTrue(run client.WorkflowRun, query string, args ...interface{}) {
	var result bool
	for i := 0; !result && i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		val, err := ts.client.QueryWorkflow(context.Background(), run.GetID(), run.GetRunID(), query, args...)
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

func (ts *IntegrationTestSuite) TestNumPollersCounter() {
	_, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assertNumPollersEventually := func(expected float64, pollerType string, tags ...string) {
		// Try for two seconds
		var lastCount float64
		for start := time.Now(); time.Since(start) <= 10*time.Second; {
			lastCount = ts.metricGauge(
				metrics.NumPoller,
				"poller_type", pollerType,
				"task_queue", ts.taskQueueName,
			)
			if lastCount == expected {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		// Will fail
		ts.Equal(expected, lastCount)
	}
	if ts.config.maxWorkflowCacheSize == 0 {
		assertNumPollersEventually(2, "workflow_task")
		assertNumPollersEventually(0, "workflow_sticky_task")
	} else {
		assertNumPollersEventually(1, "workflow_task")
		assertNumPollersEventually(1, "workflow_sticky_task")
	}
	assertNumPollersEventually(2, "activity_task")
}

func (ts *IntegrationTestSuite) TestSlotsAvailableCounter() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	assertActivitySlotsAvailableEventually := func(expected float64, tags ...string) {
		// Try for two seconds
		var lastCount float64
		for start := time.Now(); time.Since(start) <= 2*time.Second; {
			lastCount = ts.metricGauge(
				metrics.WorkerTaskSlotsAvailable,
				"worker_type", "ActivityWorker",
				"task_queue", ts.taskQueueName,
			)
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

func (ts *IntegrationTestSuite) TestActivityOnlyWorker() {
	// Start worker
	taskQueue := "test-activity-only-queue-" + uuid.New()
	activityOnlyWorker := worker.New(ts.client, taskQueue, worker.Options{DisableWorkflowWorker: true})
	a := newActivities()
	activityOnlyWorker.RegisterActivity(a.activities2.ToUpper)
	ts.NoError(activityOnlyWorker.Start())
	defer activityOnlyWorker.Stop()

	// Exec workflow on primary worker, confirm activity executed
	var result string
	err := ts.executeWorkflow("test-activity-only-worker", ts.workflows.ExecuteRemoteActivityToUpper, &result,
		taskQueue, "fOobAr")
	ts.NoError(err)
	ts.Equal("FOOBAR", result)
	ts.Equal(1, a.invokedCount("toUpper"))
}

func (ts *IntegrationTestSuite) TestReturnCancelError() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wfIDPrefix := "test-return-cancel-error-"

	// For most tests we don't return the raw error since it loses context
	rawActivityError := false

	// Activity using temporal canceled error when not canceled should return
	// "unexpected activity cancel error"
	fromActivity, waitForCancel, goCancelError := true, false, false
	err := ts.executeWorkflow(wfIDPrefix+"1", ts.workflows.ReturnCancelError, nil,
		fromActivity, rawActivityError, waitForCancel, goCancelError)
	ts.Error(err)
	ts.Contains(err.Error(), "unexpected activity cancel error")

	// Activity using Go canceled error when not canceled should return a context
	// canceled error
	fromActivity, waitForCancel, goCancelError = true, false, true
	err = ts.executeWorkflow(wfIDPrefix+"2", ts.workflows.ReturnCancelError, nil,
		fromActivity, rawActivityError, waitForCancel, goCancelError)
	ts.Error(err)
	ts.Contains(err.Error(), "context canceled")

	// Activity using temporal canceled error after cancel should return normal
	// cancel error
	fromActivity, waitForCancel, goCancelError = true, true, false
	err = ts.executeWorkflow(wfIDPrefix+"3", ts.workflows.ReturnCancelError, nil,
		fromActivity, rawActivityError, waitForCancel, goCancelError)
	ts.Error(err)
	ts.NotContains(err.Error(), "unexpected")
	ts.Contains(err.Error(), "canceled")
	// We also check that, since rawActivityError is false, this is _not_ a
	// canceled workflow since just the error string is used. This assertion is
	// only made here to show it's the opposite of the raw one later.
	ts.False(temporal.IsCanceledError(err))
	resp, err := ts.client.DescribeWorkflowExecution(ctx, wfIDPrefix+"3", "")
	ts.NoError(err)
	ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_FAILED, resp.GetWorkflowExecutionInfo().GetStatus())

	// Activity using Go canceled error after cancel should return normal cancel
	// error
	fromActivity, waitForCancel, goCancelError = true, true, true
	err = ts.executeWorkflow(wfIDPrefix+"4", ts.workflows.ReturnCancelError, nil,
		fromActivity, rawActivityError, waitForCancel, goCancelError)
	ts.Error(err)
	ts.NotContains(err.Error(), "context canceled")
	ts.NotContains(err.Error(), "unexpected")
	ts.Contains(err.Error(), "canceled")

	// Workflow using temporal canceled error when not canceled will consider the
	// workflow canceled
	// TODO(cretz): Note, this is observed behavior, not necessarily desired
	// behavior
	fromActivity, waitForCancel, goCancelError = false, false, false
	err = ts.executeWorkflow(wfIDPrefix+"5", ts.workflows.ReturnCancelError, nil,
		fromActivity, rawActivityError, waitForCancel, goCancelError)
	ts.Error(err)
	ts.True(temporal.IsCanceledError(err))
	resp, err = ts.client.DescribeWorkflowExecution(ctx, wfIDPrefix+"5", "")
	ts.NoError(err)
	ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED, resp.GetWorkflowExecutionInfo().GetStatus())

	// Workflow just returning the raw activity cancel itself appears canceled
	// TODO(cretz): Note, this is observed behavior, not necessarily desired
	// behavior
	rawActivityError = true
	fromActivity, waitForCancel, goCancelError = true, true, false
	err = ts.executeWorkflow(wfIDPrefix+"6", ts.workflows.ReturnCancelError, nil,
		fromActivity, rawActivityError, waitForCancel, goCancelError)
	ts.Error(err)
	ts.True(temporal.IsCanceledError(err))
	resp, err = ts.client.DescribeWorkflowExecution(ctx, wfIDPrefix+"6", "")
	ts.NoError(err)
	ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED, resp.GetWorkflowExecutionInfo().GetStatus())
}

func (ts *IntegrationTestSuite) TestLocalActivityStringNameReplay() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the workflow
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-local-activity-string-name-replay"), ts.workflows.LocalActivityByStringName)
	ts.NotNil(run)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))

	// Obtain history
	var history historypb.History
	iter := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		history.Events = append(history.Events, event)
	}

	// Run in replayer
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ts.workflows.LocalActivityByStringName)
	ts.NoError(replayer.ReplayWorkflowHistory(nil, &history))
}

func (ts *IntegrationTestSuite) TestMaxConcurrentSessionExecutionSizeNoWait() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts.activities.manualStopContext = ctx
	// Since the test setup set the max execution size to 3, we want to try to
	// create 4 sessions with a creation timeout of 2s (which is basically
	// schedule-to-start of the session creation worker)
	err := ts.executeWorkflow("test-max-concurrent-session-execution-size", ts.workflows.AdvancedSession, nil,
		&AdvancedSessionParams{SessionCount: 4, SessionCreationTimeout: 2 * time.Second})
	// Confirm it failed on the 4th session because it took to long to create
	ts.Error(err)
	ts.Truef(strings.Contains(err.Error(), "failed creating session #4"), "wrong error, got: %v", err)
	ts.Truef(strings.Contains(err.Error(), "activity ScheduleToStart timeout"), "wrong error, got: %v", err)
}

func (ts *IntegrationTestSuite) TestMaxConcurrentSessionExecutionSizeWithRecreationAndWait() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var manualCancel context.CancelFunc
	ts.activities.manualStopContext, manualCancel = context.WithCancel(ctx)
	// Create 2 workflows each wanting to create 2 sessions (second session on
	// each is recreation to ensure counter works). This will hang with one
	// creating 2 and another creating 1 and waiting. Then when we send the signal
	// that was done creating sessions, they will complete theirs allowing the
	// other pending creation to complete.
	run1, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-max-concurrent-session-execution-size-recreate-1"),
		ts.workflows.AdvancedSession, &AdvancedSessionParams{
			SessionCount:           2,
			SessionCreationTimeout: 40 * time.Second,
			RecreateAtIndex:        1,
		})
	ts.NoError(err)
	// Wait until sessions created
	ts.waitForQueryTrue(run1, "sessions-created-equals", 2)

	// Now create second and wait until create pending after 1
	run2, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-max-concurrent-session-execution-size-recreate-2"),
		ts.workflows.AdvancedSession, &AdvancedSessionParams{
			SessionCount:           2,
			SessionCreationTimeout: 40 * time.Second,
			RecreateAtIndex:        1,
		})
	ts.NoError(err)
	// Wait until sessions created
	ts.waitForQueryTrue(run2, "sessions-created-equals-and-pending", 1)

	// Now let the activities complete which lets run1 complete and free up
	// sessions for run2
	manualCancel()
	ts.NoError(run1.Get(ctx, nil))
	ts.NoError(run2.Get(ctx, nil))
}

func (ts *IntegrationTestSuite) TestSessionOnWorkerFailure() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts.activities.manualStopContext = ctx
	// We want to start a single long-running activity in a session
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-session-worker-failure"),
		ts.workflows.AdvancedSession,
		&AdvancedSessionParams{
			SessionCount:           1,
			SessionCreationTimeout: 10 * time.Second,
		})
	ts.NoError(err)

	// Wait until sessions started
	ts.waitForQueryTrue(run, "sessions-created-equals", 1)

	// Kill the worker
	ts.worker.Stop()
	ts.workerStopped = true

	// Now create a new worker on that same task queue to resume the work of the
	// workflow
	nextWorker := worker.New(ts.client, ts.taskQueueName, worker.Options{DisableStickyExecution: true})
	ts.registerWorkflowsAndActivities(nextWorker)
	ts.NoError(nextWorker.Start())
	defer nextWorker.Stop()

	// Get the result of the workflow run now
	err = run.Get(ctx, nil)
	// We expect the activity to timeout (which shows as cancelled in Go) since
	// the original worker is no longer present that was running the activity.
	// Before the issue that was fixed when this test was written, this would hang
	// because sessions would inadvertently retry.
	ts.Error(err)
	ts.Truef(strings.HasSuffix(err.Error(), "activity on session #1 failed: canceled"), "wrong error, got: %v", err)
}

func (ts *IntegrationTestSuite) TestQueryOnlyCoroutineUsage() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start the workflow that should run forever, send 5 signals, and wait until
	// all received
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-query-only-coroutine-"+uuid.New()),
		ts.workflows.SignalCounter,
	)
	ts.NoError(err)
	for i := 0; i < 5; i++ {
		ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "signal", nil))
	}
	ts.waitForQueryTrue(run, "has-signal-count", 5)

	// Now stop the worker and reset sticky on the workflow so it'll quickly
	// failover to our new worker
	ts.worker.Stop()
	ts.workerStopped = true
	_, err = ts.client.WorkflowService().ResetStickyTaskQueue(ctx, &workflowservice.ResetStickyTaskQueueRequest{
		Namespace: ts.config.Namespace,
		Execution: &commonpb.WorkflowExecution{WorkflowId: run.GetID(), RunId: run.GetRunID()},
	})
	ts.NoError(err)

	// Start a new worker with a counting interceptor
	counter := &coroutineCountingInterceptor{}
	nextWorker := worker.New(ts.client, ts.taskQueueName, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{counter},
	})
	ts.registerWorkflowsAndActivities(nextWorker)
	ts.NoError(nextWorker.Start())
	defer nextWorker.Stop()

	// Now issue 20 queries
	for i := 0; i < 20; i++ {
		_, err := ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "has-signal-count", 5)
		ts.NoError(err)
	}

	// Check coroutines are cleaned up. Before the fix accompanying this test, the
	// count was the same as the number of queries issued.
	ts.EventuallyWithT(func(c *assert.CollectT) {
		assert.Zero(c, counter.count())
	}, time.Second, 100*time.Millisecond)
}

func (ts *IntegrationTestSuite) TestLargeHistoryReplay() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start workflow
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions("test-large-history-replay"),
		ts.workflows.PanicOnSignal,
	)
	ts.NoError(err)

	// Send 300 signals to go over page limit
	for i := 0; i < 300; i++ {
		ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "unhandled-signal", "some-arg"))
	}

	// Now cause panic and confirm error
	var ret string
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "panic-signal", nil))
	err = run.Get(ctx, &ret)
	ts.Error(err)
	ts.Contains(err.Error(), "intentional panic")

	// Try to replay from just service and confirm panic which means it reached
	// last signal
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ts.workflows.PanicOnSignal)
	err = replayer.ReplayWorkflowExecution(ctx, ts.client.WorkflowService(), nil,
		ts.config.Namespace, workflow.Execution{ID: run.GetID(), RunID: run.GetRunID()})
	ts.Error(err)
	ts.Contains(err.Error(), "intentional panic")
}

func (ts *IntegrationTestSuite) TestWorkerFatalErrorOnRun() {
	ts.testWorkerFatalError(true)
}

func (ts *IntegrationTestSuite) TestWorkerFatalErrorOnStart() {
	ts.testWorkerFatalError(false)
}

func (ts *IntegrationTestSuite) testWorkerFatalError(useWorkerRun bool) {
	// Allow the worker to fail faster so the test does not take 2 minutes.
	internal.SetRetryLongPollGracePeriod(5 * time.Second)
	// Make a new client that will fail a poll with a namespace not found
	c, err := client.Dial(client.Options{
		HostPort:  ts.config.ServiceAddr,
		Namespace: ts.config.Namespace,
		ConnectionOptions: client.ConnectionOptions{
			TLS: ts.config.TLS,
			DialOptions: []grpc.DialOption{
				grpc.WithUnaryInterceptor(func(
					ctx context.Context,
					method string,
					req interface{},
					reply interface{},
					cc *grpc.ClientConn,
					invoker grpc.UnaryInvoker,
					opts ...grpc.CallOption,
				) error {
					if method == "/temporal.api.workflowservice.v1.WorkflowService/PollWorkflowTaskQueue" {
						// We sleep a bit to let all internal workers start
						time.Sleep(1 * time.Second)
						return serviceerror.NewNamespaceNotFound(ts.config.Namespace)
					}
					return invoker(ctx, method, req, reply, cc, opts...)
				}),
			},
		},
	})
	ts.NoError(err)
	defer c.Close()

	// Create a worker that uses that client
	callbackErrCh := make(chan error, 1)
	w := worker.New(c, "ignored-task-queue", worker.Options{OnFatalError: func(err error) { callbackErrCh <- err }})

	// Do run-based or start-based worker
	runErrCh := make(chan error, 1)
	if useWorkerRun {
		go func() { runErrCh <- w.Run(nil) }()
	} else {
		ts.NoError(w.Start())
	}

	// Wait for done
	var callbackErr, runErr error
	for callbackErr == nil || (useWorkerRun && runErr == nil) {
		select {
		case <-time.After(10 * time.Second):
			ts.Fail("timeout")
		case callbackErr = <-callbackErrCh:
		case runErr = <-runErrCh:
		}
	}

	// Check error
	ts.IsType(&serviceerror.NamespaceNotFound{}, callbackErr)
	if runErr != nil {
		ts.Equal(callbackErr, runErr)
	}
}

func (ts *IntegrationTestSuite) TestNonDeterminismFailureCauseBadStateMachine() {
	ts.testNonDeterminismFailureCause(false)
}

func (ts *IntegrationTestSuite) TestNonDeterminismFailureCauseHistoryMismatch() {
	ts.testNonDeterminismFailureCause(true)
}

func (ts *IntegrationTestSuite) testNonDeterminismFailureCause(historyMismatch bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start workflow
	forcedNonDeterminismCounter = 0
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions("test-non-determinism-failure-cause-"+uuid.New()),
		ts.workflows.ForcedNonDeterminism,
		historyMismatch,
	)
	ts.NoError(err)
	defer func() { _ = ts.client.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "", nil) }()

	// Wait for tick count as 1, send tick to do an action, then wait for 2
	ts.waitForQueryTrue(run, "is-wait-tick-count", 1)
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "tick", nil))
	ts.waitForQueryTrue(run, "is-wait-tick-count", 2)

	// Now, stop the worker and start a new one
	ts.worker.Stop()
	ts.workerStopped = true
	nextWorker := worker.New(ts.client, ts.taskQueueName, worker.Options{DisableStickyExecution: true})
	ts.registerWorkflowsAndActivities(nextWorker)
	ts.NoError(nextWorker.Start())
	defer nextWorker.Stop()

	// Increase the determinism counter and send a tick to trigger replay
	// non-determinism
	forcedNonDeterminismCounter++
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "tick", nil))

	// Now let's try to get history until we see a task failure
	var histErr error
	var taskFailed *historypb.WorkflowTaskFailedEventAttributes
	ts.Eventually(func() bool {
		iter := ts.client.GetWorkflowHistory(
			ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for iter.HasNext() {
			event, err := iter.Next()
			taskFailed, histErr = event.GetWorkflowTaskFailedEventAttributes(), err
			if taskFailed != nil || histErr != nil {
				return true
			}
		}
		return false
	}, 10*time.Second, 300*time.Millisecond)

	// Check the task has the expected cause
	ts.NoError(histErr)
	ts.Equal(enumspb.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR, taskFailed.Cause)
}

func (ts *IntegrationTestSuite) TestDeterminismUpsertSearchAttributesConditional() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	maxTicks := 3
	options := ts.startWorkflowOptions("test-determinism-upsert-search-attributes-conidtional-" + uuid.New())
	options.SearchAttributes = map[string]interface{}{
		"CustomKeywordField": "unset",
	}
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		options,
		ts.workflows.UpsertSearchAttributesConditional,
		maxTicks,
	)
	ts.NoError(err)

	ts.testStaleCacheReplayDeterminism(ctx, run, maxTicks)
}

func (ts *IntegrationTestSuite) TestLocalActivityWorkerRestart() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	maxTicks := 3
	options := ts.startWorkflowOptions("test-local-activity-worker-restart-" + uuid.New())

	run, err := ts.client.ExecuteWorkflow(
		ctx,
		options,
		ts.workflows.LocalActivityStaleCache,
		maxTicks,
	)
	ts.NoError(err)

	// clean up if test fails
	defer func() { _ = ts.client.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "", nil) }()
	ts.waitForQueryTrue(run, "is-wait-tick-count", 1)

	// Restart worker
	ts.workerStopped = true
	currentWorker := ts.worker
	currentWorker.Stop()
	currentWorker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.registerWorkflowsAndActivities(currentWorker)
	ts.NoError(currentWorker.Start())
	defer currentWorker.Stop()

	for i := 0; i < maxTicks-1; i++ {
		ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "tick", nil))
		ts.waitForQueryTrue(run, "is-wait-tick-count", 2+i)
	}
	err = run.Get(ctx, nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestLocalActivityStaleCache() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	maxTicks := 3
	options := ts.startWorkflowOptions("test-local-activity-stale-cache-" + uuid.New())

	run, err := ts.client.ExecuteWorkflow(
		ctx,
		options,
		ts.workflows.LocalActivityStaleCache,
		maxTicks,
	)
	ts.NoError(err)

	// clean up if test fails
	defer func() { _ = ts.client.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "", nil) }()
	ts.waitForQueryTrue(run, "is-wait-tick-count", 1)

	ts.workerStopped = true
	currentWorker := ts.worker
	currentWorker.Stop()
	for i := 0; i < maxTicks-1; i++ {
		func() {
			ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "tick", nil))
			currentWorker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
			defer currentWorker.Stop()
			ts.registerWorkflowsAndActivities(currentWorker)
			ts.NoError(currentWorker.Start())
			ts.waitForQueryTrue(run, "is-wait-tick-count", 2+i)
		}()
	}
	err = run.Get(ctx, nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestDeterminismUpsertMemoConditional() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	maxTicks := 3
	options := ts.startWorkflowOptions("test-determinism-upsert-search-attributes-conidtional-" + uuid.New())
	options.Memo = map[string]interface{}{
		"TestMemo": "unset",
	}
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		options,
		ts.workflows.UpsertMemoConditional,
		maxTicks,
	)
	ts.NoError(err)

	ts.testStaleCacheReplayDeterminism(ctx, run, maxTicks)
}

func (ts *IntegrationTestSuite) testStaleCacheReplayDeterminism(ctx context.Context, run client.WorkflowRun, maxTicks int) {
	// clean up if test fails
	defer func() { _ = ts.client.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "", nil) }()
	ts.waitForQueryTrue(run, "is-wait-tick-count", 1)

	ts.workerStopped = true
	currentWorker := ts.worker
	currentWorker.Stop()
	for i := 0; i < maxTicks-1; i++ {
		func() {
			ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "tick", nil))
			currentWorker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
			defer currentWorker.Stop()
			ts.registerWorkflowsAndActivities(currentWorker)
			ts.NoError(currentWorker.Start())
			ts.waitForQueryTrue(run, "is-wait-tick-count", 2+i)
		}()
	}
	err := run.Get(ctx, nil)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestClientGetNotFollowingRuns() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start workflow that does a continue as new
	run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-client-get-not-following-runs"),
		ts.workflows.ContinueAsNew, 1, ts.taskQueueName)
	ts.NoError(err)

	// Do the regular get which returns the final value and a different run ID
	origRunID := run.GetRunID()
	var val int
	ts.NoError(run.Get(ctx, &val))
	ts.Equal(999, val)
	ts.NotEqual(origRunID, run.GetRunID())

	// Get the run with the original ID and fetch without following runs
	run = ts.client.GetWorkflow(ctx, run.GetID(), origRunID)
	err = run.GetWithOptions(ctx, nil, client.WorkflowRunGetOptions{DisableFollowingRuns: true})
	ts.Error(err)
	contErr := err.(*workflow.ContinueAsNewError)
	ts.Equal("ContinueAsNew", contErr.WorkflowType.Name)
	ts.Equal("0", string(contErr.Input.Payloads[0].Data))
	ts.Equal("\""+ts.taskQueueName+"\"", string(contErr.Input.Payloads[1].Data))
	ts.Equal(ts.taskQueueName, contErr.TaskQueueName)
}

func (ts *IntegrationTestSuite) TestMutableSideEffects() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run workflow that does side effects to add 1 to our number
	run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-mutable-side-effects"),
		ts.workflows.MutableSideEffect, 42)
	ts.NoError(err)
	var val int
	ts.NoError(run.Get(ctx, &val))
	ts.Equal(45, val)

	// Now replay it
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ts.workflows.MutableSideEffect)
	ts.NoError(replayer.ReplayWorkflowExecution(ctx, ts.client.WorkflowService(), nil, ts.config.Namespace,
		workflow.Execution{ID: run.GetID(), RunID: run.GetRunID()}))
}

type localActivityInterceptor struct{ interceptor.InterceptorBase }

type localActivityWorkflowInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (l *localActivityInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	var ret localActivityWorkflowInterceptor
	ret.Next = next
	return &ret
}

func (l *localActivityWorkflowInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (interface{}, error) {
	// Execute local activity before running workflow
	var res int
	var a Activities
	actCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{ScheduleToCloseTimeout: time.Second})
	if err := workflow.ExecuteLocalActivity(actCtx, a.Echo, 0, 123).Get(ctx, &res); err != nil {
		return nil, err
	} else if res != 123 {
		return nil, fmt.Errorf("expected 123, got %v", res)
	}
	return l.Next.ExecuteWorkflow(ctx, in)
}

func (ts *IntegrationTestSuite) TestReplayerWithInterceptor() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Do basic test
	var expected []string
	run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-replayer-interceptor-"+uuid.New()),
		ts.workflows.Basic)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, &expected))
	ts.EqualValues(expected, ts.activities.invoked())

	// Now replay it with the interceptor
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		Interceptors: []interceptor.WorkerInterceptor{&localActivityInterceptor{}},
	})
	ts.NoError(err)
	replayer.RegisterWorkflow(ts.workflows.Basic)
	ts.NoError(replayer.ReplayWorkflowExecution(ctx, ts.client.WorkflowService(), nil, ts.config.Namespace,
		workflow.Execution{ID: run.GetID(), RunID: run.GetRunID()}))
}

// We count on the no-cache test to test replay conditions here
func (ts *IntegrationTestSuite) TestHistoryLength() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run workflow with 3 activities and check history lengths given
	run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("test-history-length"),
		ts.workflows.HistoryLengths, 6)
	ts.NoError(err)
	var actual, expected []int
	ts.NoError(run.Get(ctx, &actual))

	// Get history and record expected length each activity schedule
	iter := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(),
		false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var lastStartID int
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		if event.GetWorkflowTaskStartedEventAttributes() != nil {
			lastStartID = int(event.EventId)
		} else if event.GetActivityTaskScheduledEventAttributes() != nil ||
			event.GetMarkerRecordedEventAttributes().GetMarkerName() == "LocalActivity" {
			expected = append(expected, lastStartID)
		}
	}

	// Compare
	ts.Equal(expected, actual)
}

func (ts *IntegrationTestSuite) TestMultiNamespaceClient() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Make simple call to describe an execution
	_, _ = ts.client.DescribeWorkflowExecution(ctx, "id-that-does-not-exist", "")

	// Confirm count on our namespace but not on the other
	ts.assertMetricCount(metrics.TemporalRequest, 1,
		metrics.OperationTagName, "DescribeWorkflowExecution",
		metrics.NamespaceTagName, ts.config.Namespace)
	ts.assertMetricCount(metrics.TemporalRequest, 0,
		metrics.OperationTagName, "DescribeWorkflowExecution",
		metrics.NamespaceTagName, "some-other-namespace")

	// Make a new client with a different namespace and run again
	newClient, err := client.NewClientFromExisting(ts.client, client.Options{Namespace: "some-other-namespace"})
	ts.NoError(err)
	defer newClient.Close()
	_, _ = newClient.DescribeWorkflowExecution(ctx, "id-that-does-not-exist", "")

	// Confirm there was no count change to other namespace but there is now a
	// request for this one
	ts.assertMetricCount(metrics.TemporalRequest, 1,
		metrics.OperationTagName, "DescribeWorkflowExecution",
		metrics.NamespaceTagName, ts.config.Namespace)
	ts.assertMetricCount(metrics.TemporalRequest, 1,
		metrics.OperationTagName, "DescribeWorkflowExecution",
		metrics.NamespaceTagName, "some-other-namespace")
}

func (ts *IntegrationTestSuite) TestHeartbeatThrottleDisabled() {
	// Heartbeat 4 times, 100ms apart
	ts.NoError(ts.executeWorkflow("test-heartbeat-throttle-disabled-1", ts.workflows.HeartbeatSpecificCount, nil,
		100*time.Millisecond, 4))

	// That short of time by default on non-failure would only record the first
	// one
	ts.assertReportedOperationCount("temporal_request_attempt", "RecordActivityTaskHeartbeat", 1)

	// Restart worker with heartbeat throttling effectively disabled
	ts.worker.Stop()
	ts.workerStopped = true
	newWorker := worker.New(ts.client, ts.taskQueueName, worker.Options{
		MaxHeartbeatThrottleInterval: 1 * time.Nanosecond,
	})
	ts.registerWorkflowsAndActivities(newWorker)
	ts.NoError(newWorker.Start())
	defer newWorker.Stop()

	// Try that again
	ts.metricsHandler.Clear()
	ts.NoError(ts.executeWorkflow("test-heartbeat-throttle-disabled-2", ts.workflows.HeartbeatSpecificCount, nil,
		100*time.Millisecond, 4))

	// Now that heartbeat throttling was disabled, it should have sent all 4 times
	ts.assertReportedOperationCount("temporal_request", "RecordActivityTaskHeartbeat", 4)
	ts.assertReportedOperationCount("temporal_request_failure", "RecordActivityTaskHeartbeat", 0)
}

func (ts *IntegrationTestSuite) TestUpsertMemoFromNil() {
	ts.T().Skip("temporal server 1.18.0 has a bug")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	systemInfo, err := ts.client.WorkflowService().GetSystemInfo(
		ctx,
		&workflowservice.GetSystemInfoRequest{},
	)
	ts.NoError(err)
	if !systemInfo.GetCapabilities().GetUpsertMemo() {
		ts.T().Skip("UpsertMemo not implemented in server yet")
	}

	upsertMemo := map[string]interface{}{
		"key_1": "new_value_1",
		"key_2": nil,
		"key_3": 123,
	}

	expectedKey1Value, _ := converter.GetDefaultDataConverter().ToPayload("new_value_1")
	expectedKey3Value, _ := converter.GetDefaultDataConverter().ToPayload(123)
	expectedMemo := &commonpb.Memo{
		Fields: map[string]*commonpb.Payload{
			"key_1": expectedKey1Value,
			"key_3": expectedKey3Value,
		},
	}

	// Start workflow
	wfid := "test-upsert-memo-from-nil"
	wfOptions := ts.startWorkflowOptions(wfid)
	run, err := ts.client.ExecuteWorkflow(ctx, wfOptions, ts.workflows.UpsertMemo, upsertMemo)
	ts.NoError(err)
	ts.NotNil(run)

	var memo *commonpb.Memo
	err = run.Get(ctx, &memo)
	ts.NoError(err)

	// Wait a little bit for ES to update
	time.Sleep(2 * time.Second)

	// Query ES for memo
	resp, err := ts.client.DescribeWorkflowExecution(ctx, wfid, "")
	ts.NoError(err)
	ts.NotNil(resp)

	// workflow execution info matches memo in ES and correct
	ts.Equal(resp.WorkflowExecutionInfo.Memo, memo)
	ts.Equal(expectedMemo, memo)
}

func (ts *IntegrationTestSuite) TestUpsertMemoFromEmptyMap() {
	ts.T().Skip("temporal server 1.18.0 has a bug")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	systemInfo, err := ts.client.WorkflowService().GetSystemInfo(
		ctx,
		&workflowservice.GetSystemInfoRequest{},
	)
	ts.NoError(err)
	if !systemInfo.GetCapabilities().GetUpsertMemo() {
		ts.T().Skip("UpsertMemo not implemented in server yet")
	}

	upsertMemo := map[string]interface{}{
		"key_1": "new_value_1",
		"key_2": nil,
		"key_3": 123,
	}

	expectedKey1Value, _ := converter.GetDefaultDataConverter().ToPayload("new_value_1")
	expectedKey3Value, _ := converter.GetDefaultDataConverter().ToPayload(123)
	expectedMemo := &commonpb.Memo{
		Fields: map[string]*commonpb.Payload{
			"key_1": expectedKey1Value,
			"key_3": expectedKey3Value,
		},
	}

	// Start workflow
	wfid := "test-upsert-memo-from-empty-map"
	wfOptions := ts.startWorkflowOptions(wfid)
	wfOptions.Memo = map[string]interface{}{}
	run, err := ts.client.ExecuteWorkflow(ctx, wfOptions, ts.workflows.UpsertMemo, upsertMemo)
	ts.NoError(err)
	ts.NotNil(run)

	var memo *commonpb.Memo
	err = run.Get(ctx, &memo)
	ts.NoError(err)

	// Wait a little bit for ES to update
	time.Sleep(2 * time.Second)

	// Query ES for memo
	resp, err := ts.client.DescribeWorkflowExecution(ctx, wfid, "")
	ts.NoError(err)
	ts.NotNil(resp)

	// workflow execution info matches memo in ES and correct
	ts.Equal(resp.WorkflowExecutionInfo.Memo, memo)
	ts.Equal(expectedMemo, memo)
}

func (ts *IntegrationTestSuite) TestUpsertMemoWithExistingMemo() {
	ts.T().Skip("temporal server 1.18.0 has a bug")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	systemInfo, err := ts.client.WorkflowService().GetSystemInfo(
		ctx,
		&workflowservice.GetSystemInfoRequest{},
	)
	ts.NoError(err)
	if !systemInfo.GetCapabilities().GetUpsertMemo() {
		ts.T().Skip("UpsertMemo not implemented in server yet")
	}

	upsertMemo := map[string]interface{}{
		"key_1": "new_value_1",
		"key_2": nil,
		"key_3": 123,
	}

	expectedKey1Value, _ := converter.GetDefaultDataConverter().ToPayload("new_value_1")
	expectedKey3Value, _ := converter.GetDefaultDataConverter().ToPayload(123)
	expectedMemo := &commonpb.Memo{
		Fields: map[string]*commonpb.Payload{
			"key_1": expectedKey1Value,
			"key_3": expectedKey3Value,
		},
	}

	// Start workflow
	wfid := "test-upsert-memo-with-existing-memo"
	wfOptions := ts.startWorkflowOptions(wfid)
	wfOptions.Memo = map[string]interface{}{
		"key_1": "value_1",
		"key_2": "value_2",
	}
	run, err := ts.client.ExecuteWorkflow(ctx, wfOptions, ts.workflows.UpsertMemo, upsertMemo)
	ts.NoError(err)
	ts.NotNil(run)

	var memo *commonpb.Memo
	err = run.Get(ctx, &memo)
	ts.NoError(err)

	// Wait a little bit for ES to update
	time.Sleep(2 * time.Second)

	// Query ES for memo
	resp, err := ts.client.DescribeWorkflowExecution(ctx, wfid, "")
	ts.NoError(err)
	ts.NotNil(resp)

	// workflow execution info matches memo in ES and correct
	ts.Equal(resp.WorkflowExecutionInfo.Memo, memo)
	ts.Equal(expectedMemo, memo)
}

func (ts *IntegrationTestSuite) createBasicScheduleWorkflowAction(ID string) client.ScheduleAction {
	return &client.ScheduleWorkflowAction{
		Workflow:                 ts.workflows.SimplestWorkflow,
		ID:                       ID,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: 15 * time.Second,
		WorkflowTaskTimeout:      time.Second,
	}
}

func (ts *IntegrationTestSuite) TestScheduleCreate() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     "test-schedule-create-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-create-workflow"),
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-create-schedule", handle.GetID())

	err = handle.Delete(ctx)
	ts.NoError(err)

	description, err := handle.Describe(ctx)
	ts.IsType(&serviceerror.NotFound{}, err)
	ts.Nil(description)
}

func (ts *IntegrationTestSuite) TestScheduleCalendarDefault() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: "test-schedule-calendar-default-schedule",
		Spec: client.ScheduleSpec{
			Calendars: []client.ScheduleCalendarSpec{
				{
					Second: []client.ScheduleRange{{Start: 30, End: 30}},
				},
			},
		},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-calendar-default-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-calendar-default-schedule", handle.GetID())
	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	// test default calendar spec
	ts.Equal([]client.ScheduleCalendarSpec{
		{
			Second: []client.ScheduleRange{{Start: 30, End: 30, Step: 1}},
			Minute: []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			Hour:   []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			DayOfMonth: []client.ScheduleRange{
				{
					Start: 1,
					End:   31,
					Step:  1,
				},
			},
			Month: []client.ScheduleRange{
				{
					Start: 1,
					End:   12,
					Step:  1,
				},
			},
			Year: []client.ScheduleRange{},
			DayOfWeek: []client.ScheduleRange{
				{
					Start: 0,
					End:   6,
					Step:  1,
				},
			},
		},
	}, description.Schedule.Spec.Calendars)
}

func (ts *IntegrationTestSuite) TestScheduleCreateDuplicate() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scheduleOptions := client.ScheduleOptions{
		ID:     "test-schedule-create-duplicate-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-create-duplicate-workflow"),
	}

	handle, err := ts.client.ScheduleClient().Create(ctx, scheduleOptions)
	ts.NoError(err)
	ts.EqualValues("test-schedule-create-duplicate-schedule", handle.GetID())
	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()

	handle2, err := ts.client.ScheduleClient().Create(ctx, scheduleOptions)
	ts.ErrorIs(temporal.ErrScheduleAlreadyRunning, err)
	ts.Nil(handle2)
}

func (ts *IntegrationTestSuite) TestScheduleDescribeSpec() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: "test-schedule-describe-spec-schedule",
		Spec: client.ScheduleSpec{
			Calendars: []client.ScheduleCalendarSpec{
				{
					Second: []client.ScheduleRange{{}},
					Minute: []client.ScheduleRange{{}},
					Hour: []client.ScheduleRange{{
						Start: 12,
					}},
					DayOfMonth: []client.ScheduleRange{
						{
							Start: 1,
							End:   31,
						},
					},
					Month: []client.ScheduleRange{
						{
							Start: 1,
							End:   12,
						},
					},
					DayOfWeek: []client.ScheduleRange{
						{
							Start: 1,
						},
					},
				},
			},
			Intervals: []client.ScheduleIntervalSpec{
				{
					Every:  time.Hour,
					Offset: time.Minute,
				},
				{
					Every: 30 * time.Minute,
				},
			},
			Skip: []client.ScheduleCalendarSpec{
				{
					Second: []client.ScheduleRange{{}},
					Minute: []client.ScheduleRange{{}},
					Hour: []client.ScheduleRange{{
						Start: 12,
					}},
					DayOfMonth: []client.ScheduleRange{
						{
							Start: 1,
							End:   31,
						},
					},
					Month: []client.ScheduleRange{
						{
							Start: 1,
							End:   12,
						},
					},
					DayOfWeek: []client.ScheduleRange{
						{
							Start: 1,
						},
					},
				},
			},
		},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-describe-spec-workflow"),
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-describe-spec-schedule", handle.GetID())

	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()

	description, err := handle.Describe(ctx)
	ts.NoError(err)
	// test spec
	ts.Equal([]client.ScheduleCalendarSpec{
		{
			Second: []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			Minute: []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			Hour: []client.ScheduleRange{{
				Start: 12,
				End:   12,
				Step:  1,
			}},
			DayOfMonth: []client.ScheduleRange{
				{
					Start: 1,
					End:   31,
					Step:  1,
				},
			},
			Month: []client.ScheduleRange{
				{
					Start: 1,
					End:   12,
					Step:  1,
				},
			},
			Year: []client.ScheduleRange{},
			DayOfWeek: []client.ScheduleRange{
				{
					Start: 1,
					End:   1,
					Step:  1,
				},
			},
		},
	}, description.Schedule.Spec.Calendars)

	ts.Equal([]client.ScheduleIntervalSpec{
		{
			Every:  time.Hour,
			Offset: time.Minute,
		},
		{
			Every: 30 * time.Minute,
		},
	}, description.Schedule.Spec.Intervals)

	ts.Equal([]client.ScheduleCalendarSpec{
		{
			Second: []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			Minute: []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			Hour: []client.ScheduleRange{{
				Start: 12,
				End:   12,
				Step:  1,
			}},
			DayOfMonth: []client.ScheduleRange{
				{
					Start: 1,
					End:   31,
					Step:  1,
				},
			},
			Month: []client.ScheduleRange{
				{
					Start: 1,
					End:   12,
					Step:  1,
				},
			},
			Year: []client.ScheduleRange{},
			DayOfWeek: []client.ScheduleRange{
				{
					Start: 1,
					Step:  1,
					End:   1,
				},
			},
		},
	}, description.Schedule.Spec.Skip)
}

func (ts *IntegrationTestSuite) TestScheduleDescribeSpecCron() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: "test-schedule-describe-spec-cron-schedule",
		Spec: client.ScheduleSpec{
			CronExpressions: []string{
				"0 12 * * MON",
			},
		},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-describe-spec-cron-workflow"),
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-describe-spec-cron-schedule", handle.GetID())

	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()

	description, err := handle.Describe(ctx)
	ts.NoError(err)
	// test spec
	ts.Equal([]client.ScheduleCalendarSpec{
		{
			Second: []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			Minute: []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
			Hour: []client.ScheduleRange{{
				Start: 12,
				End:   12,
				Step:  1,
			}},
			DayOfMonth: []client.ScheduleRange{
				{
					Start: 1,
					End:   31,
					Step:  1,
				},
			},
			Month: []client.ScheduleRange{
				{
					Start: 1,
					End:   12,
					Step:  1,
				},
			},
			Year: []client.ScheduleRange{},
			DayOfWeek: []client.ScheduleRange{
				{
					Start: 1,
					End:   1,
					Step:  1,
				},
			},
		},
	}, description.Schedule.Spec.Calendars)
}

func (ts *IntegrationTestSuite) TestScheduleDescribeState() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testNote := "test note"
	scheduleMemo := map[string]interface{}{
		"key_1": "new_value_1",
		"key_2": 123,
	}

	expectedKey1Value, _ := converter.GetDefaultDataConverter().ToPayload("new_value_1")
	expectedKey2Value, _ := converter.GetDefaultDataConverter().ToPayload(123)
	expectedMemo := &commonpb.Memo{
		Fields: map[string]*commonpb.Payload{
			"key_1": expectedKey1Value,
			"key_2": expectedKey2Value,
		},
	}

	expectedArg1Value, _ := converter.GetDefaultDataConverter().ToPayload("Test Arg 1")
	expectedArg2Value, _ := converter.GetDefaultDataConverter().ToPayload("Test Arg 2")

	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   "test-schedule-describe-state-schedule",
		Spec: client.ScheduleSpec{},
		Action: &client.ScheduleWorkflowAction{
			Workflow:                 ts.workflows.TwoParameterWorkflow,
			Args:                     []interface{}{"Test Arg 1", "Test Arg 2"},
			ID:                       "test-schedule-describe-state-workflow",
			TaskQueue:                ts.taskQueueName,
			WorkflowExecutionTimeout: 15 * time.Second,
			WorkflowTaskTimeout:      time.Second,
		},
		Overlap:          enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		CatchupWindow:    time.Minute,
		PauseOnFailure:   true,
		Note:             testNote,
		Paused:           true,
		RemainingActions: 10,
		Memo:             scheduleMemo,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-describe-state-schedule", handle.GetID())

	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()

	description, err := handle.Describe(ctx)
	ts.NoError(err)
	ts.Equal(expectedMemo, description.Memo)
	// test policy
	ts.Equal(time.Minute, description.Schedule.Policy.CatchupWindow)
	ts.Equal(true, description.Schedule.Policy.PauseOnFailure)
	ts.Equal(enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, description.Schedule.Policy.Overlap)
	// test state
	ts.EqualValues(10, description.Schedule.State.RemainingActions)
	ts.Equal(true, description.Schedule.State.LimitedActions)
	ts.Equal(true, description.Schedule.State.Paused)
	ts.Equal(testNote, description.Schedule.State.Note)
	// test action
	switch action := description.Schedule.Action.(type) {
	case *client.ScheduleWorkflowAction:
		ts.Equal("TwoParameterWorkflow", action.Workflow)
		ts.Equal(expectedArg1Value, action.Args[0])
		ts.Equal(expectedArg2Value, action.Args[1])
	default:
		ts.Fail("schedule action wrong type")
	}
}

func (ts *IntegrationTestSuite) TestSchedulePause() {
	verifyState := func(ctx context.Context, handle client.ScheduleHandle, paused bool, note string) {
		description, err := handle.Describe(ctx)
		ts.NoError(err)
		ts.Equal(paused, description.Schedule.State.Paused)
		if paused {
			ts.Equal(note, description.Schedule.State.Note)
		} else {
			ts.Equal(note, description.Schedule.State.Note)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     "test-schedule-pause-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-pause-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-pause-schedule", handle.GetID())
	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()
	// Workflow should start paused
	verifyState(ctx, handle, true, "")
	// Pausing a paused workflow should be a no-op
	ts.NoError(handle.Pause(ctx, client.SchedulePauseOptions{}))
	verifyState(ctx, handle, true, "Paused via Go SDK")
	// Unpause workflow
	ts.NoError(handle.Unpause(ctx, client.ScheduleUnpauseOptions{}))
	verifyState(ctx, handle, false, "Unpaused via Go SDK")
	// Unpausing a paused workflow should be a no-op
	ts.NoError(handle.Unpause(ctx, client.ScheduleUnpauseOptions{}))
	verifyState(ctx, handle, false, "Unpaused via Go SDK")
	// Pause workflow again
	ts.NoError(handle.Pause(ctx, client.SchedulePauseOptions{}))
	verifyState(ctx, handle, true, "Paused via Go SDK")
	// Verify pausing sets the note
	ts.NoError(handle.Pause(ctx, client.SchedulePauseOptions{
		Note: "test pause note",
	}))
	verifyState(ctx, handle, true, "test pause note")
	// Pausing again overrides the note
	ts.NoError(handle.Pause(ctx, client.SchedulePauseOptions{
		Note: "test another pause note",
	}))
	verifyState(ctx, handle, true, "test another pause note")
	// Verify unpausing sets the note
	ts.NoError(handle.Unpause(ctx, client.ScheduleUnpauseOptions{
		Note: "test unpause note",
	}))
	verifyState(ctx, handle, false, "test unpause note")
}

func (ts *IntegrationTestSuite) TestScheduleTrigger() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:      "test-schedule-trigger-schedule",
		Spec:    client.ScheduleSpec{},
		Action:  ts.createBasicScheduleWorkflowAction("test-schedule-trigger-workflow"),
		Paused:  true,
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-trigger-schedule", handle.GetID())
	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()
	for i := 0; i < 5; i++ {
		ts.NoError(handle.Trigger(ctx, client.ScheduleTriggerOptions{
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
		}))
		// schedule actions can only trigger once per second
		time.Sleep(2 * time.Second)
	}
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	ts.EqualValues(5, description.Info.NumActions)
	ts.EqualValues(5, len(description.Info.RecentActions))
	for _, wf := range description.Info.RecentActions {
		wfRun := ts.client.GetWorkflow(ctx, wf.StartWorkflowResult.WorkflowID, wf.StartWorkflowResult.FirstExecutionRunID)
		var result string
		ts.NoError(wfRun.Get(ctx, &result))
		ts.Equal("hello", result)
	}
}

func (ts *IntegrationTestSuite) TestScheduleBackfillCreate() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	now := time.Now()
	endTime := now
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: "test-schedule-backfill-create-schedule",
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{
					Every: time.Minute,
				},
			},
			EndAt: endTime,
		},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-backfill-create-workflow"),
		ScheduleBackfill: []client.ScheduleBackfill{
			{
				Start:   now.Add(-time.Hour),
				End:     now.Add(-30 * time.Minute),
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
			},
			{
				Start:   now.Add(-30 * time.Minute),
				End:     now,
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
			},
		},
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-backfill-create-schedule", handle.GetID())
	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()
	time.Sleep(5 * time.Second)
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	ts.EqualValues(60, description.Info.NumActions)
}

func (ts *IntegrationTestSuite) TestScheduleBackfill() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	now := time.Now()
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: "test-schedule-backfill-schedule",
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{
					Every: time.Minute,
				},
			},
		},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-backfill-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-backfill-schedule", handle.GetID())
	defer func() {
		ts.NoError(handle.Delete(ctx))
	}()
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	ts.EqualValues(0, description.Info.NumActions)
	err = handle.Backfill(ctx, client.ScheduleBackfillOptions{
		Backfill: []client.ScheduleBackfill{
			{
				Start:   now.Add(-4 * time.Minute),
				End:     now.Add(-2 * time.Minute),
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
			},
			{
				Start:   now.Add(-2 * time.Minute),
				End:     now,
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
			},
		},
	})
	ts.NoError(err)
	time.Sleep(5 * time.Second)
	description, err = handle.Describe(ctx)
	ts.NoError(err)
	ts.EqualValues(4, description.Info.NumActions)
}

func (ts *IntegrationTestSuite) TestScheduleList() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 5; i++ {
		scheduleID := fmt.Sprintf("test-schedule-list-schedule-%d", i)
		workflowID := fmt.Sprintf("test-schedule-list-workflow-%d", i)
		// Create a paused workflow
		handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
			ID:   scheduleID,
			Spec: client.ScheduleSpec{},
			Action: &client.ScheduleWorkflowAction{
				Workflow:                 ts.workflows.SimplestWorkflow,
				ID:                       workflowID,
				TaskQueue:                ts.taskQueueName,
				WorkflowExecutionTimeout: 15 * time.Second,
				WorkflowTaskTimeout:      time.Second,
			},
		})
		ts.NoError(err)
		ts.EqualValues(scheduleID, handle.GetID())
		defer func() {
			ts.NoError(handle.Delete(ctx))
		}()
	}
	iter, err := ts.client.ScheduleClient().List(ctx, client.ScheduleListOptions{
		PageSize: 1,
	})
	var events []*client.ScheduleListEntry
	for iter.HasNext() {
		event, err := iter.Next()
		ts.Nil(err)
		events = append(events, event)
	}
	ts.GreaterOrEqual(5, len(events))
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestScheduleUpdate() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     "test-schedule-update-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-update-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-update-schedule", handle.GetID())
	defer func() {
		err = handle.Delete(ctx)
		ts.NoError(err)
	}()
	updateFunc := func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		return &client.ScheduleUpdate{
			Schedule: &input.Description.Schedule,
		}, nil
	}
	description, err := handle.Describe(ctx)
	ts.NoError(err)

	err = handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: updateFunc,
	})
	ts.NoError(err)

	description2, err := handle.Describe(ctx)
	ts.NoError(err)
	ts.Equal(description.Schedule, description2.Schedule)
}

func (ts *IntegrationTestSuite) TestScheduleUpdateCancelUpdate() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     "test-schedule-update-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-update-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-update-schedule", handle.GetID())
	defer func() {
		err = handle.Delete(ctx)
		ts.NoError(err)
	}()
	updateFunc := func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		switch action := input.Description.Schedule.Action.(type) {
		case *client.ScheduleWorkflowAction:
			action.ID = "new-workflow-id"
			input.Description.Schedule.Action = action
			return &client.ScheduleUpdate{
				Schedule: &input.Description.Schedule,
			}, temporal.ErrSkipScheduleUpdate
		default:
			ts.Fail("schedule action wrong type")
			return nil, nil
		}
	}
	err = handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: updateFunc,
	})
	ts.NoError(err)
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	switch action := description.Schedule.Action.(type) {
	case *client.ScheduleWorkflowAction:
		ts.Equal("test-schedule-update-workflow", action.ID)
	default:
		ts.Fail("schedule action wrong type")
	}
}

func (ts *IntegrationTestSuite) TestScheduleUpdateError() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     "test-schedule-update-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-update-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-update-schedule", handle.GetID())
	defer func() {
		err = handle.Delete(ctx)
		ts.NoError(err)
	}()
	updateFunc := func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		return nil, errors.New("test failure")
	}
	err = handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: updateFunc,
	})
	ts.EqualError(err, "test failure")
}

func (ts *IntegrationTestSuite) TestScheduleUpdateNewAction() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     "test-schedule-update-new-action-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-update-new-action-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-update-new-action-schedule", handle.GetID())
	defer func() {
		err = handle.Delete(ctx)
		ts.NoError(err)
	}()
	// change workflow type
	updateFunc := func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		input.Description.Schedule.Action = &client.ScheduleWorkflowAction{
			Workflow:                 ts.workflows.Basic,
			ID:                       "test-schedule-update-new-action-workflow",
			TaskQueue:                ts.taskQueueName,
			WorkflowExecutionTimeout: 15 * time.Second,
			WorkflowTaskTimeout:      time.Second,
		}
		return &client.ScheduleUpdate{
			Schedule: &input.Description.Schedule,
		}, nil
	}
	err = handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: updateFunc,
	})
	ts.NoError(err)
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	switch action := description.Schedule.Action.(type) {
	case *client.ScheduleWorkflowAction:
		ts.Equal("Basic", action.Workflow)
	default:
		ts.Fail("schedule action wrong type")
	}
}

func (ts *IntegrationTestSuite) TestScheduleUpdateAction() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     "test-schedule-update-action-schedule",
		Spec:   client.ScheduleSpec{},
		Action: ts.createBasicScheduleWorkflowAction("test-schedule-update-action-workflow"),
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-update-action-schedule", handle.GetID())
	defer func() {
		err = handle.Delete(ctx)
		ts.NoError(err)
	}()
	updateFunc := func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		switch action := input.Description.Schedule.Action.(type) {
		case *client.ScheduleWorkflowAction:
			action.ID = "new-workflow-id"
			input.Description.Schedule.Action = action
			return &client.ScheduleUpdate{
				Schedule: &input.Description.Schedule,
			}, nil
		default:
			ts.Fail("schedule action wrong type")
			return nil, nil
		}
	}
	err = handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: updateFunc,
	})
	ts.NoError(err)
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	switch action := description.Schedule.Action.(type) {
	case *client.ScheduleWorkflowAction:
		ts.Equal("new-workflow-id", action.ID)
	default:
		ts.Fail("schedule action wrong type")
	}
}

func (ts *IntegrationTestSuite) TestScheduleUpdateActionParameter() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	expectedArg1Value, _ := converter.GetDefaultDataConverter().ToPayload("Test Arg 1")
	expectedArg2Value, _ := converter.GetDefaultDataConverter().ToPayload("Test Arg 2")
	expectedArg3Value, _ := converter.GetDefaultDataConverter().ToPayload("Test Arg 3")
	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   "test-schedule-update-action-parameter-schedule",
		Spec: client.ScheduleSpec{},
		Action: &client.ScheduleWorkflowAction{
			Workflow:                 ts.workflows.TwoParameterWorkflow,
			Args:                     []interface{}{"arg 1", "arg 2"},
			ID:                       "test-schedule-update-action-parameter-workflow",
			TaskQueue:                ts.taskQueueName,
			WorkflowExecutionTimeout: 15 * time.Second,
			WorkflowTaskTimeout:      time.Second,
		},
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-update-action-parameter-schedule", handle.GetID())
	defer func() {
		err = handle.Delete(ctx)
		ts.NoError(err)
	}()
	updateFunc := func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		switch action := input.Description.Schedule.Action.(type) {
		case *client.ScheduleWorkflowAction:
			action.Workflow = ts.workflows.ThreeParameterWorkflow
			action.Args = []interface{}{"Test Arg 1", "Test Arg 2", "Test Arg 3"}
			input.Description.Schedule.Action = action
			return &client.ScheduleUpdate{
				Schedule: &input.Description.Schedule,
			}, nil
		default:
			ts.Fail("schedule action wrong type")
			return nil, nil
		}
	}
	err = handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: updateFunc,
	})
	ts.NoError(err)
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	switch action := description.Schedule.Action.(type) {
	case *client.ScheduleWorkflowAction:
		ts.Equal("ThreeParameterWorkflow", action.Workflow)
		ts.Equal(expectedArg1Value, action.Args[0])
		ts.Equal(expectedArg2Value, action.Args[1])
		ts.Equal(expectedArg3Value, action.Args[2])
	default:
		ts.Fail("schedule action wrong type")
	}
}

func (ts *IntegrationTestSuite) TestScheduleUpdateWorkflowActionMemo() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	expectedKey1Value, _ := converter.GetDefaultDataConverter().ToPayload("value")
	expectedKey2Value, _ := converter.GetDefaultDataConverter().ToPayload(123)
	expectedKey3Value, _ := converter.GetDefaultDataConverter().ToPayload("other value")
	expectedMemo := map[string]interface{}{
		"key_1": expectedKey1Value,
		"key_2": expectedKey2Value,
		"key_3": expectedKey3Value,
	}

	// Create a paused workflow
	handle, err := ts.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   "test-schedule-update-action-memo-schedule",
		Spec: client.ScheduleSpec{},
		Action: &client.ScheduleWorkflowAction{
			Workflow:                 ts.workflows.SimplestWorkflow,
			ID:                       "test-schedule-update-action-memo-workflow",
			TaskQueue:                ts.taskQueueName,
			WorkflowExecutionTimeout: 15 * time.Second,
			WorkflowTaskTimeout:      time.Second,
			Memo: map[string]interface{}{
				"key_1": "value",
			},
		},
		Paused: true,
	})
	ts.NoError(err)
	ts.EqualValues("test-schedule-update-action-memo-schedule", handle.GetID())
	defer func() {
		err = handle.Delete(ctx)
		ts.NoError(err)
	}()
	updateFunc := func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		switch action := input.Description.Schedule.Action.(type) {
		case *client.ScheduleWorkflowAction:
			key2Value, _ := converter.GetDefaultDataConverter().ToPayload(123)
			action.Memo["key_2"] = key2Value
			action.Memo["key_3"] = "other value"
			return &client.ScheduleUpdate{
				Schedule: &input.Description.Schedule,
			}, nil
		default:
			ts.Fail("schedule action wrong type")
			return nil, nil
		}
	}
	err = handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: updateFunc,
	})
	ts.NoError(err)
	description, err := handle.Describe(ctx)
	ts.NoError(err)
	switch action := description.Schedule.Action.(type) {
	case *client.ScheduleWorkflowAction:
		ts.EqualValues(expectedMemo, action.Memo)
	default:
		ts.Fail("schedule action wrong type")
	}
}

func (ts *IntegrationTestSuite) TestSendsCorrectMeteringData() {
	nonfirstLAAttemptCounts := make([]uint32, 0)
	c, err := client.Dial(client.Options{
		HostPort:  ts.config.ServiceAddr,
		Namespace: ts.config.Namespace,
		ConnectionOptions: client.ConnectionOptions{
			TLS: ts.config.TLS,
			DialOptions: []grpc.DialOption{
				grpc.WithUnaryInterceptor(func(
					ctx context.Context,
					method string,
					req interface{},
					reply interface{},
					cc *grpc.ClientConn,
					invoker grpc.UnaryInvoker,
					opts ...grpc.CallOption,
				) error {
					if method == "/temporal.api.workflowservice.v1.WorkflowService/RespondWorkflowTaskCompleted" {
						asReq := req.(*workflowservice.RespondWorkflowTaskCompletedRequest)
						nonfirstLAAttemptCounts = append(nonfirstLAAttemptCounts, asReq.MeteringMetadata.NonfirstLocalActivityExecutionAttempts)
					}
					return invoker(ctx, method, req, reply, cc, opts...)
				}),
			},
		},
	})
	ts.NoError(err)
	defer c.Close()

	ts.worker.Stop()
	ts.workerStopped = true
	w := worker.New(c, ts.taskQueueName, worker.Options{})
	ts.registerWorkflowsAndActivities(w)
	ts.Nil(w.Start())

	wfOpts := ts.startWorkflowOptions("test-sends-correct-metering-data")
	wfOpts.WorkflowTaskTimeout = 2 * time.Second
	ts.NoError(ts.executeWorkflowWithOption(wfOpts,
		ts.workflows.WorkflowWithLocalActivityRetriesAndHeartbeat, nil))

	ts.Equal(uint32(0), nonfirstLAAttemptCounts[0])
	for i := 1; i < len(nonfirstLAAttemptCounts); i++ {
		ts.True(nonfirstLAAttemptCounts[i] > 0)
	}
	w.Stop()
}

func (ts *IntegrationTestSuite) TestNondeterministicUpdateRegistertion() {
	var expected []string
	err := ts.executeWorkflow("test-activity-retry-options-change", ts.workflows.ActivityRetryOptionsChange, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

// executeWorkflow executes a given workflow and waits for the result
func (ts *IntegrationTestSuite) executeWorkflow(
	wfID string, wfFunc interface{}, retValPtr interface{}, args ...interface{},
) error {
	return ts.executeWorkflowWithOption(ts.startWorkflowOptions(wfID), wfFunc, retValPtr, args...)
}

func (ts *IntegrationTestSuite) executeWorkflowWithOption(
	options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{},
) error {
	return ts.executeWorkflowWithContextAndOption(context.Background(), options, wfFunc, retValPtr, args...)
}

func (ts *IntegrationTestSuite) executeWorkflowWithContextAndOption(
	ctx context.Context, options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{},
) error {
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
	wfOptions := client.StartWorkflowOptions{
		ID:                       wfID,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: 15 * time.Second,
		WorkflowTaskTimeout:      time.Second,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		EnableEagerStart:         true,
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

var (
	_ interceptor.WorkerInterceptor           = (*tracingInterceptor)(nil)
	_ interceptor.WorkflowInboundInterceptor  = (*tracingWorkflowInboundInterceptor)(nil)
	_ interceptor.WorkflowOutboundInterceptor = (*tracingWorkflowOutboundInterceptor)(nil)
)

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
		interceptor.WorkflowOutboundInterceptorBase{Next: outbound}, t,
	})
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

var (
	_ interceptor.WorkerInterceptor          = (*signalInterceptor)(nil)
	_ interceptor.WorkflowInboundInterceptor = (*signalWorkflowInboundInterceptor)(nil)
)

type signalInterceptor struct {
	interceptor.WorkerInterceptorBase
	ReturnErrorTimes uint32
	TimesInvoked     uint32
}

func newSignalInterceptor() *signalInterceptor {
	return &signalInterceptor{}
}

type signalWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	control *signalInterceptor
}

func (t *signalWorkflowInboundInterceptor) HandleSignal(ctx workflow.Context, in *interceptor.HandleSignalInput) error {
	timesInvoked := atomic.AddUint32(&t.control.TimesInvoked, 1)
	if timesInvoked <= t.control.ReturnErrorTimes {
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

type coroutineCountingInterceptor struct {
	interceptor.WorkerInterceptorBase
	// Access via count()
	_count int32
}

type coroutineCountingWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	root *coroutineCountingInterceptor
}

type coroutineCountingWorkflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	root *coroutineCountingInterceptor
}

func (c *coroutineCountingInterceptor) count() int {
	return int(atomic.LoadInt32(&c._count))
}

func (c *coroutineCountingInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	var ret coroutineCountingWorkflowInboundInterceptor
	ret.Next = next
	ret.root = c
	return &ret
}

func (c *coroutineCountingWorkflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return c.Next.Init(&coroutineCountingWorkflowOutboundInterceptor{
		interceptor.WorkflowOutboundInterceptorBase{Next: outbound}, c.root,
	})
}

func (c *coroutineCountingWorkflowOutboundInterceptor) Go(
	ctx workflow.Context,
	name string,
	f func(ctx workflow.Context),
) workflow.Context {
	atomic.AddInt32(&c.root._count, 1)
	return c.Next.Go(ctx, name, func(ctx workflow.Context) {
		defer atomic.AddInt32(&c.root._count, -1)
		f(ctx)
	})
}
