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
	"go.uber.org/goleak"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptors"
	"go.temporal.io/sdk/internal/common"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
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
	metricsScopeCloser io.Closer
	metricsReporter    *metrics.CapturingStatsReporter
}

const (
	ctxTimeout                    = 15 * time.Second
	namespace                     = "integration-test-namespace"
	namespaceCacheRefreshInterval = 20 * time.Second
	testContextKey1               = "test-context-key1"
	testContextKey2               = "test-context-key2"
	testContextKey3               = "test-context-key3"
)

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
	ts.client, err = client.NewClient(client.Options{
		HostPort:  ts.config.ServiceAddr,
		Namespace: namespace,
		Logger:    ilog.NewDefaultLogger(),
		ContextPropagators: []workflow.ContextPropagator{
			NewKeysPropagator([]string{testContextKey1}),
			NewKeysPropagator([]string{testContextKey2}),
		},
		MetricsScope: metricsScope,
	})
	ts.NoError(err)

	ts.seq++
	ts.activities.clearInvoked()
	ts.taskQueueName = fmt.Sprintf("tq-%v-%s", ts.seq, ts.T().Name())
	ts.tracer = newTracingInterceptor()
	options := worker.Options{
		DisableStickyExecution:            ts.config.IsStickyOff,
		WorkflowInterceptorChainFactories: []interceptors.WorkflowInterceptor{ts.tracer},
		WorkflowPanicPolicy:               worker.FailWorkflow,
	}

	if strings.Contains(ts.T().Name(), "Session") {
		options.EnableSessionWorker = true
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
		"temporal_workflow_task_queue_poll_succeed", 1,
		"temporal_long_request", 8,
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
}

func (ts *IntegrationTestSuite) TestSignalWorkflow() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfOpts := ts.startWorkflowOptions("test-signal-workflow")
	run, err := ts.client.ExecuteWorkflow(ctx, wfOpts, ts.workflows.SignalWorkflow)
	ts.Nil(err)
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow", run.GetRunID(), "string-signal", "string-value")
	ts.NoError(err)

	wt := &commonpb.WorkflowType{Name: "workflow-type"}
	err = ts.client.SignalWorkflow(ctx, "test-signal-workflow", run.GetRunID(), "proto-signal", wt)
	ts.NoError(err)

	var protoValue *commonpb.WorkflowType
	err = run.Get(ctx, &protoValue)
	ts.NoError(err)
	ts.Equal(commonpb.WorkflowType{Name: "string-value"}, *protoValue)
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

func (ts *IntegrationTestSuite) TestCancelChildWorkflow() {
	var expected []string
	err := ts.executeWorkflow("test-cancel-child-workflow", ts.workflows.CancelChildWorkflow, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestCancelActivityImmediately() {
	ts.T().Skip(`Currently fails with "PanicError": "unknown command internal.commandID{commandType:0, id:"5"}, possible causes are nondeterministic workflow definition code or incompatible change in the workflow definition`)
	var expected []string
	err := ts.executeWorkflow("test-cancel-activity-immediately", ts.workflows.CancelActivityImmediately, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
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

func (ts *IntegrationTestSuite) TestWorkflowWithParallelLongLocalActivityAndHeartbeat() {
	if !ts.config.IsStickyOff {
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

func (ts *IntegrationTestSuite) TestInspectLocalActivityInfo() {
	err := ts.executeWorkflow("test-local-activity-info", ts.workflows.InspectLocalActivityInfo, nil)
	ts.Nil(err)
}

func (ts *IntegrationTestSuite) TestBasicSession() {
	var expected []string
	err := ts.executeWorkflow("test-basic-session", ts.workflows.BasicSession, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
	// createSession activity, actual activity, completeSession activity.
	ts.Equal([]string{"Go", "ExecuteWorkflow begin", "ExecuteActivity", "Go", "ExecuteActivity", "ExecuteActivity", "ExecuteWorkflow end"},
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

func (ts *IntegrationTestSuite) TestCron() {
	var expected int
	err := ts.executeWorkflow(CronWorkflowID, ts.workflows.CronWorkflow, &expected)
	// Workflow finishes itself by cancelling
	ts.Error(err)
	var canceledErr *temporal.CanceledError
	ts.True(errors.As(err, &canceledErr))
	var errDeets *string
	ts.NoError(canceledErr.Details(&errDeets))
	ts.EqualValues("finished OK", *errDeets)
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
	// REVIEW: Putting this here feels a bit lame, but plumbing arguments down through `executeWorkflow`
	//  feels like a lot of change that almost all the tests don't need. Thoughts?
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
	instances map[string]*tracingInboundCallsInterceptor
}

type tracingInboundCallsInterceptor struct {
	Next  interceptors.WorkflowInboundCallsInterceptor
	trace []string
}

type tracingOutboundCallsInterceptor struct {
	interceptors.WorkflowOutboundCallsInterceptorBase
	inbound *tracingInboundCallsInterceptor
}

func (t *tracingOutboundCallsInterceptor) Go(ctx workflow.Context, name string, f func(ctx workflow.Context)) workflow.Context {
	t.inbound.trace = append(t.inbound.trace, "Go")
	return t.Next.Go(ctx, name, f)
}

func newTracingInterceptor() *tracingInterceptor {
	return &tracingInterceptor{instances: make(map[string]*tracingInboundCallsInterceptor)}
}

func (t *tracingInterceptor) InterceptWorkflow(info *workflow.Info, next interceptors.WorkflowInboundCallsInterceptor) interceptors.WorkflowInboundCallsInterceptor {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	result := &tracingInboundCallsInterceptor{next, nil}
	t.instances[info.WorkflowType.Name] = result
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
	result := t.Next.ExecuteWorkflow(ctx, workflowType, args...)
	t.trace = append(t.trace, "ExecuteWorkflow end")
	return result
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
