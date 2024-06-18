// The MIT License
//
// Copyright (c) 2024 Temporal Technologies Inc.  All rights reserved.
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
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type testContext struct {
	client                               client.Client
	metricsHandler                       *metrics.CapturingHandler
	testConfig                           Config
	taskQueue, endpoint, endpointBaseURL string
}

func newTestContext(t *testing.T, ctx context.Context) *testContext {
	config := NewConfig()
	require.NoError(t, WaitForTCP(time.Minute, config.ServiceAddr))

	metricsHandler := metrics.NewCapturingHandler()
	c, err := client.DialContext(ctx, client.Options{
		HostPort:          config.ServiceAddr,
		Namespace:         config.Namespace,
		Logger:            ilog.NewDefaultLogger(),
		ConnectionOptions: client.ConnectionOptions{TLS: config.TLS},
		MetricsHandler:    metricsHandler,
	})
	require.NoError(t, err)

	taskQueue := "sdk-go-nexus-test-tq-" + uuid.NewString()
	endpoint := "sdk-go-nexus-test-ep-" + uuid.NewString()
	res, err := c.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: endpoint,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: config.Namespace,
						TaskQueue: taskQueue,
					},
				},
			},
		},
	})
	require.NoError(t, err)

	scheme := "http"
	if config.TLS != nil {
		scheme = "https"
	}
	endpointBaseURL := scheme + "://" + config.ServiceHTTPAddr + res.Endpoint.UrlPrefix

	tc := &testContext{
		client:          c,
		testConfig:      config,
		metricsHandler:  metricsHandler,
		taskQueue:       taskQueue,
		endpoint:        endpoint,
		endpointBaseURL: endpointBaseURL,
	}

	return tc
}

func (tc *testContext) newNexusClient(t *testing.T, service string) *nexus.Client {
	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tc.testConfig.TLS,
		},
	}
	nc, err := nexus.NewClient(nexus.ClientOptions{
		BaseURL: tc.endpointBaseURL,
		Service: service,
		HTTPCaller: func(r *http.Request) (*http.Response, error) {
			attempt := 0
			for {
				attempt++
				res, err := httpClient.Do(r)
				// Give the endpoint configuration some time to propagate in the frontend.
				// This should not take more than a few milliseconds.
				// TODO(bergundy): Remove this once the server supports cache read through for unknown endpoints.
				if attempt < 10 && err == nil && res.StatusCode == http.StatusNotFound {
					time.Sleep(time.Millisecond * 100)
					continue
				}
				return res, err
			}
		},
	})
	require.NoError(t, err)
	return nc
}

func (tc *testContext) requireTimer(t *assert.CollectT, metric, service, operation string) {
	assert.True(t, slices.ContainsFunc(tc.metricsHandler.Timers(), func(ct *metrics.CapturedTimer) bool {
		return ct.Name == metric &&
			ct.Tags[metrics.NexusServiceTagName] == service &&
			ct.Tags[metrics.NexusOperationTagName] == operation
	}))
}

func (tc *testContext) requireCounter(t *assert.CollectT, metric, service, operation string) {
	assert.True(t, slices.ContainsFunc(tc.metricsHandler.Counters(), func(ct *metrics.CapturedCounter) bool {
		return ct.Name == metric &&
			ct.Tags[metrics.NexusServiceTagName] == service &&
			ct.Tags[metrics.NexusOperationTagName] == operation
	}))
}

var syncOp = temporalnexus.NewSyncOperation("sync-op", func(ctx context.Context, c client.Client, s string, o nexus.StartOperationOptions) (string, error) {
	switch s {
	case "ok":
		// Verify options are properly propagated.
		if _, ok := ctx.Deadline(); !ok {
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "expected context deadline to be set")
		}
		if o.RequestID != "test-request-id" {
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "invalid request ID, got: %v", o.RequestID)
		}
		if o.Header.Get("test") != "ok" {
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "invalid test header, got: %v", o.Header.Get("test"))
		}
		if o.CallbackURL != "http://localhost/test" {
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "invalid test callback URL, got: %v", o.CallbackURL)
		}
		if o.CallbackHeader.Get("test") != "ok" {
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "invalid test callback header, got: %v", o.CallbackHeader.Get("test"))
		}
		return s, nil
	case "fail":
		return "", &nexus.UnsuccessfulOperationError{
			State: nexus.OperationStateFailed,
			Failure: nexus.Failure{
				Message: "fail",
			},
		}
	case "handlererror":
		return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, s)
	case "panic":
		panic("panic")
	}
	return "", nil
})

func waitForCancelWorkflow(ctx workflow.Context, ownID string) (string, error) {
	return "", workflow.Await(ctx, func() bool { return false })
}

var workflowOp = temporalnexus.NewWorkflowRunOperation(
	"workflow-op",
	waitForCancelWorkflow,
	func(ctx context.Context, id string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		return client.StartWorkflowOptions{ID: id}, nil
	},
)

func TestNexusSyncOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	tc := newTestContext(t, ctx)

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(syncOp, workflowOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(waitForCancelWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	nc := tc.newNexusClient(t, service.Name)

	t.Run("ok", func(t *testing.T) {
		tc.metricsHandler.Clear()
		result, err := nexus.ExecuteOperation(ctx, nc, syncOp, "ok", nexus.ExecuteOperationOptions{
			RequestID:      "test-request-id",
			Header:         nexus.Header{"test": "ok"},
			CallbackURL:    "http://localhost/test",
			CallbackHeader: nexus.Header{"test": "ok"},
		})
		require.NoError(t, err)
		require.Equal(t, "ok", result)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskScheduleToStartLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("fail", func(t *testing.T) {
		tc.metricsHandler.Clear()
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "fail", nexus.ExecuteOperationOptions{})
		var unsuccessfulOperationErr *nexus.UnsuccessfulOperationError
		require.ErrorAs(t, err, &unsuccessfulOperationErr)
		require.Equal(t, nexus.OperationStateFailed, unsuccessfulOperationErr.State)
		require.Equal(t, "fail", unsuccessfulOperationErr.Failure.Message)
	})

	t.Run("handlererror", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "handlererror", nexus.ExecuteOperationOptions{})
		var unexpectedResponseErr *nexus.UnexpectedResponseError
		require.ErrorAs(t, err, &unexpectedResponseErr)
		require.Equal(t, http.StatusBadRequest, unexpectedResponseErr.Response.StatusCode)
		require.Contains(t, unexpectedResponseErr.Message, `"400 Bad Request": handlererror`)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskScheduleToStartLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireCounter(t, metrics.NexusTaskExecutionFailedCounter, service.Name, syncOp.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("panic", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "panic", nexus.ExecuteOperationOptions{})
		var unexpectedResponseErr *nexus.UnexpectedResponseError
		require.ErrorAs(t, err, &unexpectedResponseErr)
		// TODO(bergundy): Eventually we'll get rid of this special status and propagate error from worker.
		// At that point this test will need to be modified.
		require.Equal(t, 520, unexpectedResponseErr.Response.StatusCode)
		require.Contains(t, unexpectedResponseErr.Message, "internal error")
	})
}

func TestNexusWorkflowRunOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	tc := newTestContext(t, ctx)

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(syncOp, workflowOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(waitForCancelWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	nc := tc.newNexusClient(t, service.Name)

	workflowID := "nexus-handler-workflow-" + uuid.NewString()
	result, err := nexus.StartOperation(ctx, nc, workflowOp, workflowID, nexus.StartOperationOptions{
		CallbackURL:    "http://localhost/test",
		CallbackHeader: nexus.Header{"test": "ok"},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Pending)
	handle := result.Pending
	require.Equal(t, workflowID, handle.ID)
	desc, err := tc.client.DescribeWorkflowExecution(ctx, workflowID, "")
	require.NoError(t, err)

	require.Equal(t, 1, len(desc.Callbacks))
	callback, ok := desc.Callbacks[0].Callback.Variant.(*common.Callback_Nexus_)
	require.True(t, ok)
	require.Equal(t, "http://localhost/test", callback.Nexus.Url)
	require.Equal(t, map[string]string{"test": "ok"}, callback.Nexus.Header)

	run := tc.client.GetWorkflow(ctx, workflowID, "")
	require.NoError(t, handle.Cancel(ctx, nexus.CancelOperationOptions{}))
	require.ErrorContains(t, run.Get(ctx, nil), "canceled")
}

func TestSyncOperationFromWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	tc := newTestContext(t, ctx)

	op := temporalnexus.NewSyncOperation("op", func(ctx context.Context, c client.Client, outcome string, o nexus.StartOperationOptions) (string, error) {
		switch outcome {
		case "successful":
			return outcome, nil
		case "failed":
			return "", &nexus.UnsuccessfulOperationError{
				State:   nexus.OperationStateFailed,
				Failure: nexus.Failure{Message: "failed for test"},
			}
		case "canceled":
			return "", &nexus.UnsuccessfulOperationError{
				State:   nexus.OperationStateCanceled,
				Failure: nexus.Failure{Message: "canceled for test"},
			}
		default:
			panic(fmt.Errorf("unexpected outcome: %s", outcome))
		}
	})

	wf := func(ctx workflow.Context, outcome string) error {
		c := workflow.NewNexusClient(tc.endpoint, "test")
		fut := c.ExecuteOperation(ctx, op, outcome, workflow.NexusOperationOptions{})
		var res string

		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil && outcome == "successful" {
			return fmt.Errorf("expected start to succeed: %w", err)
		}
		if exec.OperationID != "" {
			return fmt.Errorf("expected empty operation ID")
		}
		if err := fut.Get(ctx, &res); err != nil {
			return err
		}
		// If the operation didn't fail the only expected result is "successful".
		if res != "successful" {
			return fmt.Errorf("unexpected result: %v", res)
		}
		return nil
	}

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(op))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(wf)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	t.Run("OpSuccessful", func(t *testing.T) {
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "successful")
		require.NoError(t, err)
		require.NoError(t, run.Get(ctx, nil))
	})

	t.Run("OpFailed", func(t *testing.T) {
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "failed")
		require.NoError(t, err)
		var execErr *temporal.WorkflowExecutionError
		err = run.Get(ctx, nil)
		require.ErrorAs(t, err, &execErr)
		var opErr *temporal.NexusOperationError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &opErr)
		require.Equal(t, tc.endpoint, opErr.Endpoint)
		require.Equal(t, "test", opErr.Service)
		require.Equal(t, op.Name(), opErr.Operation)
		require.Equal(t, "", opErr.OperationID)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "failed for test", appErr.Message())
	})

	t.Run("OpCanceled", func(t *testing.T) {
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "canceled")
		require.NoError(t, err)
		var execErr *temporal.WorkflowExecutionError
		err = run.Get(ctx, nil)
		require.ErrorAs(t, err, &execErr)
		// The Go SDK unwraps workflow errors to check for cancelation even if the workflow was never canceled, losing
		// the error chain, Nexus operation errors are treated the same as other workflow errors for consistency.
		var canceledErr *temporal.CanceledError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &canceledErr)
	})
}

func TestAsyncOperationFromWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	tc := newTestContext(t, ctx)

	handlerWorkflow := func(ctx workflow.Context, action string) (string, error) {
		switch action {
		case "succeed":
			return action, nil
		case "fail":
			return "", fmt.Errorf("handler workflow failed in test")
		case "wait-for-cancel":
			return "", workflow.Await(ctx, func() bool { return false })
		default:
			panic(fmt.Errorf("unexpected outcome: %s", action))
		}
	}
	op := temporalnexus.NewWorkflowRunOperation("op", handlerWorkflow, func(ctx context.Context, action string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		if action == "fail-to-start" {
			return client.StartWorkflowOptions{}, nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "fake internal error")
		}
		return client.StartWorkflowOptions{
			ID: soo.RequestID,
		}, nil
	})
	callerWorkflow := func(ctx workflow.Context, action string) error {
		c := workflow.NewNexusClient(tc.endpoint, "test")
		ctx, cancel := workflow.WithCancel(ctx)
		defer cancel()
		fut := c.ExecuteOperation(ctx, op, action, workflow.NexusOperationOptions{})
		var res string
		ch := workflow.GetSignalChannel(ctx, "cancel-op")
		workflow.Go(ctx, func(ctx workflow.Context) {
			var action string
			ch.Receive(ctx, &action)
			switch action {
			case "wait-for-started":
				fut.GetNexusOperationExecution().Get(ctx, nil)
			case "sleep":
				workflow.Sleep(ctx, time.Millisecond)
			}
			cancel()
		})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil && action != "fail-to-start" {
			return fmt.Errorf("expected start to succeed: %w", err)
		}
		if exec.OperationID == "" && action != "fail-to-start" {
			return fmt.Errorf("expected non empty operation ID")
		}
		if err := fut.Get(ctx, &res); err != nil {
			return err
		}
		// If the operation didn't fail the only expected result is "successful".
		if res != "succeed" {
			return fmt.Errorf("unexpected result: %v", res)
		}
		return nil
	}

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(op))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(handlerWorkflow)
	w.RegisterWorkflow(callerWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	t.Run("OpSuccessful", func(t *testing.T) {
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, "succeed")
		require.NoError(t, err)
		require.NoError(t, run.Get(ctx, nil))
	})

	t.Run("OpFailed", func(t *testing.T) {
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, "fail")
		require.NoError(t, err)
		var execErr *temporal.WorkflowExecutionError
		err = run.Get(ctx, nil)
		require.ErrorAs(t, err, &execErr)
		var opErr *temporal.NexusOperationError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &opErr)
		require.Equal(t, tc.endpoint, opErr.Endpoint)
		require.Equal(t, "test", opErr.Service)
		require.Equal(t, op.Name(), opErr.Operation)
		require.NotEmpty(t, opErr.OperationID)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "handler workflow failed in test", appErr.Message())
	})

	t.Run("OpCanceledBeforeSent", func(t *testing.T) {
		run, err := tc.client.SignalWithStartWorkflow(ctx, uuid.NewString(), "cancel-op", "no-wait", client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
		}, callerWorkflow, "wait-for-cancel")
		require.NoError(t, err)
		var execErr *temporal.WorkflowExecutionError
		err = run.Get(ctx, nil)
		require.ErrorAs(t, err, &execErr)
		// The Go SDK unwraps workflow errors to check for cancelation even if the workflow was never canceled, losing
		// the error chain, Nexus operation errors are treated the same as other workflow errors for consistency.
		var canceledErr *temporal.CanceledError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &canceledErr)

		// Verify that the operation was never scheduled.
		history := tc.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for history.HasNext() {
			event, err := history.Next()
			require.NoError(t, err)
			require.NotEqual(t, enums.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED, event.EventType)
		}
	})

	t.Run("OpCanceledBeforeStarted", func(t *testing.T) {
		run, err := tc.client.SignalWithStartWorkflow(ctx, uuid.NewString(), "cancel-op", "sleep", client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
		}, callerWorkflow, "fail-to-start")
		require.NoError(t, err)
		var execErr *temporal.WorkflowExecutionError
		err = run.Get(ctx, nil)
		require.ErrorAs(t, err, &execErr)
		// The Go SDK unwraps workflow errors to check for cancelation even if the workflow was never canceled, losing
		// the error chain, Nexus operation errors are treated the same as other workflow errors for consistency.
		var canceledErr *temporal.CanceledError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &canceledErr)
	})

	t.Run("OpCanceledAfterStarted", func(t *testing.T) {
		run, err := tc.client.SignalWithStartWorkflow(ctx, uuid.NewString(), "cancel-op", "wait-for-started", client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
		}, callerWorkflow, "wait-for-cancel")
		require.NoError(t, err)
		var execErr *temporal.WorkflowExecutionError
		err = run.Get(ctx, nil)
		require.ErrorAs(t, err, &execErr)
		// The Go SDK unwraps workflow errors to check for cancelation even if the workflow was never canceled, losing
		// the error chain, Nexus operation errors are treated the same as other workflow errors for consistency.
		var canceledErr *temporal.CanceledError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &canceledErr)
	})
}

func TestReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	tc := newTestContext(t, ctx)

	op := temporalnexus.NewSyncOperation("op", func(ctx context.Context, c client.Client, nv nexus.NoValue, soo nexus.StartOperationOptions) (nexus.NoValue, error) {
		return nil, nil
	})

	endpointForTest := tc.endpoint
	serviceForTest := "test"
	opForTest := op.Name()

	callerWorkflow := func(ctx workflow.Context) error {
		c := workflow.NewNexusClient(endpointForTest, serviceForTest)
		ctx, cancel := workflow.WithCancel(ctx)
		defer cancel()
		fut := c.ExecuteOperation(ctx, opForTest, nil, workflow.NexusOperationOptions{})
		if err := fut.Get(ctx, nil); err != nil {
			return err
		}
		return nil
	}

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(op))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(callerWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue: tc.taskQueue,
		// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
		// timeout to speed up the attempts.
		WorkflowTaskTimeout: time.Second,
	}, callerWorkflow)
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))

	events := make([]*historypb.HistoryEvent, 0)
	hist := tc.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for hist.HasNext() {
		e, err := hist.Next()
		require.NoError(t, err)
		events = append(events, e)
	}

	t.Run("OK", func(t *testing.T) {
		// endpointForTest, serviceForTest = tc.endpoint, "test"
		rw := worker.NewWorkflowReplayer()
		rw.RegisterWorkflow(callerWorkflow)
		err = rw.ReplayWorkflowHistory(ilog.NewDefaultLogger(), &historypb.History{Events: events})
		require.NoError(t, err)
	})

	t.Run("EndpointMismatchOK", func(t *testing.T) {
		endpointForTest = "endpoint-changed" // It's okay to change the endpoint as it is environment specific.
		// endpointForTest, serviceForTest = tc.endpoint, "test"
		rw := worker.NewWorkflowReplayer()
		rw.RegisterWorkflow(callerWorkflow)
		err = rw.ReplayWorkflowHistory(ilog.NewDefaultLogger(), &historypb.History{Events: events})
		require.NoError(t, err)
	})

	t.Run("ServiceMismatchNDE", func(t *testing.T) {
		serviceForTest = "service-changed"
		// endpointForTest, serviceForTest = tc.endpoint, "test"
		rw := worker.NewWorkflowReplayer()
		rw.RegisterWorkflow(callerWorkflow)
		err = rw.ReplayWorkflowHistory(ilog.NewDefaultLogger(), &historypb.History{Events: events})
		require.ErrorContains(t, err, "[TMPRL1100]")
	})

	t.Run("OperationMismatchNDE", func(t *testing.T) {
		serviceForTest = "test" // Restore
		opForTest = "op-changed"
		rw := worker.NewWorkflowReplayer()
		rw.RegisterWorkflow(callerWorkflow)
		err = rw.ReplayWorkflowHistory(ilog.NewDefaultLogger(), &historypb.History{Events: events})
		require.ErrorContains(t, err, "[TMPRL1100]")
	})
}

func TestWorkflowTestSuite_NexusSyncOperation(t *testing.T) {
	op := nexus.NewSyncOperation("op", func(ctx context.Context, outcome string, opts nexus.StartOperationOptions) (string, error) {
		switch outcome {
		case "ok":
			return outcome, nil
		case "failure":
			return "", &nexus.UnsuccessfulOperationError{
				State: nexus.OperationStateFailed,
				Failure: nexus.Failure{
					Message: "test operation failed",
				},
			}
		case "handler-error":
			return "", &nexus.HandlerError{
				Type: nexus.HandlerErrorTypeBadRequest,
				Failure: &nexus.Failure{
					Message: "test operation failed",
				},
			}
		}
		panic(fmt.Errorf("invalid outcome: %q", outcome))
	})
	wf := func(ctx workflow.Context, outcome string) error {
		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(ctx, op, outcome, workflow.NexusOperationOptions{})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		var res string
		if err := fut.Get(ctx, &res); err != nil {
			return err
		}
		if res != "ok" {
			return fmt.Errorf("unexpected result: %v", res)
		}
		return nil
	}

	service := nexus.NewService("test")
	service.Register(op)

	t.Run("ok", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.ExecuteWorkflow(wf, "ok")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
	})

	for _, outcome := range []string{"failure", "handler-error"} {
		outcome := outcome // capture just in case.
		t.Run(outcome, func(t *testing.T) {
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterNexusService(service)
			env.ExecuteWorkflow(wf, "failure")
			require.True(t, env.IsWorkflowCompleted())
			var execErr *temporal.WorkflowExecutionError
			err := env.GetWorkflowError()
			require.ErrorAs(t, err, &execErr)
			var opErr *temporal.NexusOperationError
			err = execErr.Unwrap()
			require.ErrorAs(t, err, &opErr)
			require.Equal(t, "endpoint", opErr.Endpoint)
			require.Equal(t, "test", opErr.Service)
			require.Equal(t, op.Name(), opErr.Operation)
			require.Empty(t, opErr.OperationID)
			require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
			err = opErr.Unwrap()
			var appErr *temporal.ApplicationError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, "test operation failed", appErr.Message())
		})
	}
}

func TestWorkflowTestSuite_WorkflowRunOperation(t *testing.T) {
	handlerWF := func(ctx workflow.Context, outcome string) (string, error) {
		if outcome == "ok" {
			return "ok", nil
		}
		return "", fmt.Errorf("expected failure")
	}

	op := temporalnexus.NewWorkflowRunOperation(
		"op",
		handlerWF,
		func(ctx context.Context, id string, opts nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{ID: opts.RequestID}, nil
		})

	callerWF := func(ctx workflow.Context, outcome string) error {
		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(ctx, op, outcome, workflow.NexusOperationOptions{})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		if exec.OperationID == "" {
			return errors.New("got empty operation ID")
		}

		var result string
		if err := fut.Get(ctx, &result); err != nil {
			return err
		}
		if result != "ok" {
			return fmt.Errorf("expected result to be 'ok', got: %s", result)
		}
		return nil
	}

	service := nexus.NewService("test")
	service.Register(op)

	t.Run("ok", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(handlerWF)
		env.RegisterNexusService(service)

		env.ExecuteWorkflow(callerWF, "ok")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
	})

	t.Run("fail", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(handlerWF)
		env.RegisterNexusService(service)

		env.ExecuteWorkflow(callerWF, "fail")
		require.True(t, env.IsWorkflowCompleted())

		var execErr *temporal.WorkflowExecutionError
		err := env.GetWorkflowError()
		require.ErrorAs(t, err, &execErr)
		var opErr *temporal.NexusOperationError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &opErr)
		require.Equal(t, "endpoint", opErr.Endpoint)
		require.Equal(t, "test", opErr.Service)
		require.Equal(t, op.Name(), opErr.Operation)
		require.Empty(t, opErr.OperationID)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "expected failure", appErr.Message())
	})
}

func TestWorkflowTestSuite_WorkflowRunOperation_WithCancel(t *testing.T) {
	wf := func(ctx workflow.Context, cancelBeforeStarted bool) error {
		childCtx, cancel := workflow.WithCancel(ctx)
		defer cancel()

		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(childCtx, workflowOp, "op-id", workflow.NexusOperationOptions{})
		if cancelBeforeStarted {
			cancel()
		}
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		if exec.OperationID != "op-id" {
			return fmt.Errorf("unexpected operation ID: %q", exec.OperationID)
		}

		if !cancelBeforeStarted {
			cancel()
		}
		err := fut.Get(ctx, nil)
		return err
	}

	service := nexus.NewService("test")
	service.Register(workflowOp)

	cases := []struct {
		cancelBeforeStarted bool
		name                string
	}{
		{false, "AfterStarted"},
		{true, "BeforeStarted"},
	}
	for _, tc := range cases {
		tc := tc // capture just in case.
		t.Run(tc.name, func(t *testing.T) {
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(waitForCancelWorkflow)
			env.RegisterNexusService(service)
			env.ExecuteWorkflow(wf, tc.cancelBeforeStarted)
			require.True(t, env.IsWorkflowCompleted())
			// Error wrapping is different in the test environment than the server (same as for child workflows).
			var execErr *temporal.WorkflowExecutionError
			err := env.GetWorkflowError()
			require.ErrorAs(t, err, &execErr)
			var opErr *temporal.NexusOperationError
			err = execErr.Unwrap()
			require.ErrorAs(t, err, &opErr)
			require.Equal(t, "endpoint", opErr.Endpoint)
			require.Equal(t, "test", opErr.Service)
			require.Equal(t, workflowOp.Name(), opErr.Operation)
			require.Equal(t, "op-id", opErr.OperationID)
			require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
			err = opErr.Unwrap()
			var canceledError *temporal.CanceledError
			require.ErrorAs(t, err, &canceledError)
		})
	}
}

func TestWorkflowTestSuite_NexusSyncOperation_ClientMethods_Panic(t *testing.T) {
	var panicReason any
	op := temporalnexus.NewSyncOperation("signal-op", func(ctx context.Context, c client.Client, _ nexus.NoValue, opts nexus.StartOperationOptions) (nexus.NoValue, error) {
		func() {
			defer func() {
				panicReason = recover()
			}()
			c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{}, "test", "", "get-secret")
		}()
		return nil, nil
	})
	wf := func(ctx workflow.Context) error {
		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(ctx, op, nil, workflow.NexusOperationOptions{})
		return fut.Get(ctx, nil)
	}

	service := nexus.NewService("test")
	service.Register(op)

	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForCancelWorkflow)
	env.RegisterNexusService(service)
	env.ExecuteWorkflow(wf)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "not implemented in the test environment", panicReason)
}
