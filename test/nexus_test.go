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
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/common/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

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

	taskQueue := "nexus-test" + uuid.NewString()

	w := worker.New(c, taskQueue, worker.Options{})

	service := nexus.NewService("test")
	require.NoError(t, service.Register(syncOp, workflowOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(waitForCancelWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	res, err := c.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: "sdk-go-test-" + uuid.NewString(),
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

	nc, err := nexus.NewClient(nexus.ClientOptions{
		BaseURL: "http://" + config.ServiceHTTPAddr + res.Endpoint.UrlPrefix,
		Service: service.Name,
	})
	require.NoError(t, err)

	t.Run("ok", func(t *testing.T) {
		metricsHandler.Clear()
		result, err := nexus.ExecuteOperation(ctx, nc, syncOp, "ok", nexus.ExecuteOperationOptions{
			RequestID:      "test-request-id",
			Header:         nexus.Header{"test": "ok"},
			CallbackURL:    "http://localhost/test",
			CallbackHeader: nexus.Header{"test": "ok"},
		})
		require.NoError(t, err)
		require.Equal(t, "ok", result)
		requireTimer(t, metricsHandler, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
		requireTimer(t, metricsHandler, metrics.NexusTaskScheduleToStartLatency, service.Name, syncOp.Name())
		requireTimer(t, metricsHandler, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
	})

	t.Run("fail", func(t *testing.T) {
		metricsHandler.Clear()
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "fail", nexus.ExecuteOperationOptions{})
		var unsuccessfulOperationErr *nexus.UnsuccessfulOperationError
		require.ErrorAs(t, err, &unsuccessfulOperationErr)
		require.Equal(t, nexus.OperationStateFailed, unsuccessfulOperationErr.State)
		require.Equal(t, "fail", unsuccessfulOperationErr.Failure.Message)

		requireTimer(t, metricsHandler, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
		requireTimer(t, metricsHandler, metrics.NexusTaskScheduleToStartLatency, service.Name, syncOp.Name())
		requireTimer(t, metricsHandler, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
		requireCounter(t, metricsHandler, metrics.NexusTaskExecutionFailedCounter, service.Name, syncOp.Name())
	})

	t.Run("handlererror", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "handlererror", nexus.ExecuteOperationOptions{})
		var unexpectedResponseErr *nexus.UnexpectedResponseError
		require.ErrorAs(t, err, &unexpectedResponseErr)
		require.Equal(t, http.StatusBadRequest, unexpectedResponseErr.Response.StatusCode)
		require.Contains(t, unexpectedResponseErr.Message, `"400 Bad Request": handlererror`)
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
	config := NewConfig()
	require.NoError(t, WaitForTCP(time.Minute, config.ServiceAddr))

	c, err := client.DialContext(ctx, client.Options{
		HostPort:          config.ServiceAddr,
		Namespace:         config.Namespace,
		Logger:            ilog.NewDefaultLogger(),
		ConnectionOptions: client.ConnectionOptions{TLS: config.TLS},
	})
	require.NoError(t, err)

	taskQueue := "nexus-test" + uuid.NewString()

	w := worker.New(c, taskQueue, worker.Options{})

	service := nexus.NewService("test")
	require.NoError(t, service.Register(syncOp, workflowOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(waitForCancelWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	res, err := c.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: "sdk-go-test-" + uuid.NewString(),
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

	nc, err := nexus.NewClient(nexus.ClientOptions{
		BaseURL: "http://" + config.ServiceHTTPAddr + res.Endpoint.UrlPrefix,
		Service: service.Name,
	})
	require.NoError(t, err)

	workflowID := "nexus-handler-workflow-" + uuid.NewString()
	result, err := nexus.StartOperation(ctx, nc, workflowOp, workflowID, nexus.StartOperationOptions{
		CallbackURL:    "http://localhost/test",
		CallbackHeader: nexus.Header{"test": "ok"},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Pending)
	handle := result.Pending
	require.Equal(t, workflowID, handle.ID)
	desc, err := c.DescribeWorkflowExecution(ctx, workflowID, "")
	require.NoError(t, err)

	require.Equal(t, 1, len(desc.Callbacks))
	callback, ok := desc.Callbacks[0].Callback.Variant.(*common.Callback_Nexus_)
	require.True(t, ok)
	require.Equal(t, "http://localhost/test", callback.Nexus.Url)
	require.Equal(t, map[string]string{"test": "ok"}, callback.Nexus.Header)

	run := c.GetWorkflow(ctx, workflowID, "")
	require.NoError(t, handle.Cancel(ctx, nexus.CancelOperationOptions{}))
	require.ErrorContains(t, run.Get(ctx, nil), "canceled")
}

func requireTimer(t *testing.T, metricsHandler *metrics.CapturingHandler, metric, service, operation string) {
	require.True(t, slices.ContainsFunc(metricsHandler.Timers(), func(ct *metrics.CapturedTimer) bool {
		return ct.Name == metric &&
			ct.Tags[metrics.NexusServiceTagName] == service &&
			ct.Tags[metrics.NexusOperationTagName] == operation
	}))
}

func requireCounter(t *testing.T, metricsHandler *metrics.CapturingHandler, metric, service, operation string) {
	require.True(t, slices.ContainsFunc(metricsHandler.Counters(), func(ct *metrics.CapturedCounter) bool {
		return ct.Name == metric &&
			ct.Tags[metrics.NexusServiceTagName] == service &&
			ct.Tags[metrics.NexusOperationTagName] == operation
	}))
}
