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

package test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/goleak"
	"go.uber.org/zap"

	commonproto "github.com/temporalio/temporal-proto/common"
	"github.com/temporalio/temporal-proto/enums"
	"github.com/temporalio/temporal-proto/workflowservice"
	"go.temporal.io/temporal"
	"go.temporal.io/temporal/client"
	"go.temporal.io/temporal/worker"
	"go.temporal.io/temporal/workflow"
)

type IntegrationTestSuite struct {
	*require.Assertions
	suite.Suite
	config       Config
	rpcClient    *rpcClient
	libClient    client.Client
	activities   *Activities
	workflows    *Workflows
	worker       worker.Worker
	seq          int64
	taskListName string
}

const (
	ctxTimeout                 = 15 * time.Second
	domainName                 = "integration-test-domain"
	domainCacheRefreshInterval = 20 * time.Second
)

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

// waitForTCP waits until target tcp address is available.
func waitForTCP(timeout time.Duration, addr string) error {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("failed to wait until %s: %v", addr, ctx.Err())
		default:
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				continue
			}
			_ = conn.Close()
			return nil
		}
	}
}

func (ts *IntegrationTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.config = newConfig()
	ts.activities = newActivities()
	ts.workflows = &Workflows{}
	ts.Nil(waitForTCP(time.Minute, ts.config.ServiceAddr))
	rpcClient, err := newRPCClient(ts.config.ServiceName, ts.config.ServiceAddr)
	ts.NoError(err)
	ts.rpcClient = rpcClient
	ts.libClient = client.NewClient(ts.rpcClient.WorkflowServiceYARPCClient, domainName, &client.Options{})
	ts.registerDomain()
}

func (ts *IntegrationTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.rpcClient.Close()
	// allow the pollers to shut down, and ensure there are no goroutine leaks.
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
			// https://github.com/uber-go/cadence-client/issues/739
			last = goleak.FindLeaks(goleak.IgnoreTopFunction("go.temporal.io/temporal/internal.(*coroutineState).initialYield"))
			if last == nil {
				// no leak, done waiting
				return
			}
			// else wait for another check or the timeout (which will record the latest error)
		}
	}
}

func (ts *IntegrationTestSuite) SetupTest() {
	ts.seq++
	ts.activities.clearInvoked()
	ts.taskListName = fmt.Sprintf("tl-%v", ts.seq)
	logger, err := zap.NewDevelopment()
	ts.NoError(err)
	ts.worker = worker.New(ts.rpcClient.WorkflowServiceYARPCClient, domainName, ts.taskListName, worker.Options{
		DisableStickyExecution: ts.config.IsStickyOff,
		Logger:                 logger,
	})
	ts.registerWorkflowsAndActivities(ts.worker)
	ts.Nil(ts.worker.Start())
}

func (ts *IntegrationTestSuite) TearDownTest() {
	ts.worker.Stop()
}

func (ts *IntegrationTestSuite) TestBasic() {
	var expected []string
	err := ts.executeWorkflow("test-basic", ts.workflows.Basic, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestActivityRetryOnError() {
	var expected []string
	err := ts.executeWorkflow("test-activity-retry-on-error", ts.workflows.ActivityRetryOnError, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
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
		enums.TimeoutTypeStartToClose)

	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestActivityRetryOnHBTimeout() {
	var expected []string
	err := ts.executeWorkflow("test-activity-retry-on-hbtimeout", ts.workflows.ActivityRetryOnHBTimeout, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestContinueAsNew() {
	var result int
	err := ts.executeWorkflow("test-continueasnew", ts.workflows.ContinueAsNew, &result, 4, ts.taskListName)
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
	err := ts.executeWorkflowWithOption(startOptions, ts.workflows.ContinueAsNewWithOptions, &result, 4, ts.taskListName)
	ts.NoError(err)
	ts.Equal("memoVal,searchAttr", result)
}

func (ts *IntegrationTestSuite) TestCancellation() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.libClient.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-cancellation"), ts.workflows.Basic)
	ts.NoError(err)
	ts.NotNil(run)
	ts.Nil(ts.libClient.CancelWorkflow(ctx, "test-cancellation", run.GetRunID()))
	err = run.Get(ctx, nil)
	ts.Error(err)
	_, ok := err.(*temporal.CanceledError)
	ts.True(ok)
}

func (ts *IntegrationTestSuite) TestStackTraceQuery() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.libClient.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-stack-trace-query"), ts.workflows.Basic)
	ts.NoError(err)
	value, err := ts.libClient.QueryWorkflow(ctx, "test-stack-trace-query", run.GetRunID(), "__stack_trace")
	ts.NoError(err)
	ts.NotNil(value)
	var trace string
	ts.Nil(value.Get(&trace))
	ts.True(strings.Contains(trace, "go.temporal.io/temporal/test.(*Workflows).Basic"))
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseRejectDuplicate() {
	var result string
	err := ts.executeWorkflow(
		"test-workflowidreuse-reject-duplicate",
		ts.workflows.IDReusePolicy,
		&result,
		uuid.New(),
		client.WorkflowIDReusePolicyRejectDuplicate,
		false,
		false,
	)
	ts.Error(err)
	gerr, ok := err.(*workflow.GenericError)
	ts.True(ok)
	ts.True(strings.Contains(gerr.Error(), "WorkflowExecutionAlreadyStartedError"))
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseAllowDuplicateFailedOnly1() {
	var result string
	err := ts.executeWorkflow(
		"test-workflowidreuse-reject-duplicate-failed-only1",
		ts.workflows.IDReusePolicy,
		&result,
		uuid.New(),
		client.WorkflowIDReusePolicyAllowDuplicateFailedOnly,
		false,
		false,
	)
	ts.Error(err)
	gerr, ok := err.(*workflow.GenericError)
	ts.True(ok)
	ts.True(strings.Contains(gerr.Error(), "WorkflowExecutionAlreadyStartedError"))
}

func (ts *IntegrationTestSuite) TestWorkflowIDReuseAllowDuplicateFailedOnly2() {
	var result string
	err := ts.executeWorkflow(
		"test-workflowidreuse-reject-duplicate-failed-only2",
		ts.workflows.IDReusePolicy,
		&result,
		uuid.New(),
		client.WorkflowIDReusePolicyAllowDuplicateFailedOnly,
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
		client.WorkflowIDReusePolicyAllowDuplicate,
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
}

func (ts *IntegrationTestSuite) TestChildWFWithParentClosePolicyTerminate() {
	var childWorkflowID string
	err := ts.executeWorkflow("test-childwf-parent-close-policy", ts.workflows.ChildWorkflowSuccessWithParentClosePolicyTerminate, &childWorkflowID)
	ts.NoError(err)
	resp, err := ts.libClient.DescribeWorkflowExecution(context.Background(), childWorkflowID, "")
	ts.NoError(err)
	ts.True(resp.WorkflowExecutionInfo.GetCloseTime() > 0)
}

func (ts *IntegrationTestSuite) TestChildWFWithParentClosePolicyAbandon() {
	var childWorkflowID string
	err := ts.executeWorkflow("test-childwf-parent-close-policy", ts.workflows.ChildWorkflowSuccessWithParentClosePolicyAbandon, &childWorkflowID)
	ts.NoError(err)
	resp, err := ts.libClient.DescribeWorkflowExecution(context.Background(), childWorkflowID, "")
	ts.NoError(err)
	ts.True(resp.WorkflowExecutionInfo.GetCloseTime() == 0)
}

func (ts *IntegrationTestSuite) TestActivityCancelUsingReplay() {
	logger, err := zap.NewDevelopment()
	workflow.RegisterWithOptions(ts.workflows.ActivityCancelRepro, workflow.RegisterOptions{DisableAlreadyRegisteredCheck: true})
	err = worker.ReplayPartialWorkflowHistoryFromJSONFile(logger, "fixtures/activity.cancel.sm.repro.json", 12)
	ts.NoError(err)
}

func (ts *IntegrationTestSuite) TestActivityCancelRepro() {
	var expected []string
	err := ts.executeWorkflow("test-activity-cancel-sm", ts.workflows.ActivityCancelRepro, &expected)
	ts.NoError(err)
	ts.EqualValues(expected, ts.activities.invoked())
}

func (ts *IntegrationTestSuite) TestLargeQueryResultError() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.libClient.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("test-large-query-error"), ts.workflows.LargeQueryResultWorkflow)
	ts.Nil(err)
	value, err := ts.libClient.QueryWorkflow(ctx, "test-large-query-error", run.GetRunID(), "large_query")
	ts.Error(err)

	queryErr, ok := err.(*commonproto.QueryFailedError)
	ts.True(ok)
	ts.Equal("query result size (3000000) exceeds limit (2000000)", queryErr.Message)
	ts.Nil(value)
}

func (ts *IntegrationTestSuite) registerDomain() {
	client := client.NewDomainClient(ts.rpcClient.WorkflowServiceYARPCClient, &client.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	name := domainName
	retention := int32(1)
	err := client.Register(ctx, &workflowservice.RegisterDomainRequest{
		Name:                                   name,
		WorkflowExecutionRetentionPeriodInDays: retention,
	})
	if err != nil {
		if _, ok := err.(*commonproto.DomainAlreadyExistsError); ok {
			return
		}
	}
	ts.NoError(err)
	time.Sleep(domainCacheRefreshInterval) // wait for domain cache refresh on temporal-server
	// bellow is used to guarantee domain is ready
	var dummyReturn string
	err = ts.executeWorkflow("test-domain-exist", ts.workflows.SimplestWorkflow, &dummyReturn)
	numOfRetry := 20
	for err != nil && numOfRetry >= 0 {
		if _, ok := err.(*commonproto.EntityNotExistsError); ok {
			time.Sleep(domainCacheRefreshInterval)
			err = ts.executeWorkflow("test-domain-exist", ts.workflows.SimplestWorkflow, &dummyReturn)
		} else {
			break
		}
		numOfRetry--
	}
}

// executeWorkflow executes a given workflow and waits for the result
func (ts *IntegrationTestSuite) executeWorkflow(
	wfID string, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	options := ts.startWorkflowOptions(wfID)
	return ts.executeWorkflowWithOption(options, wfFunc, retValPtr, args...)
}

func (ts *IntegrationTestSuite) executeWorkflowWithOption(
	options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.libClient.ExecuteWorkflow(ctx, options, wfFunc, args...)
	if err != nil {
		return err
	}
	err = run.Get(ctx, retValPtr)
	if ts.config.Debug {
		iter := ts.libClient.GetWorkflowHistory(ctx, options.ID, run.GetRunID(), false, enums.HistoryEventFilterTypeAllEvent)
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
	return client.StartWorkflowOptions{
		ID:                              wfID,
		TaskList:                        ts.taskListName,
		ExecutionStartToCloseTimeout:    15 * time.Second,
		DecisionTaskStartToCloseTimeout: time.Second,
		WorkflowIDReusePolicy:           client.WorkflowIDReusePolicyAllowDuplicate,
	}
}

func (ts *IntegrationTestSuite) registerWorkflowsAndActivities(w worker.Worker) {
	ts.workflows.register(w)
	ts.activities.register(w)
}
