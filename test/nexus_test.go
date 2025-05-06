package test_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/opentracing/opentracing-go/mocktracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/contrib/opentracing"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/interceptortest"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

const defaultNexusTestTimeout = 10 * time.Second

type testContext struct {
	client                               client.Client
	metricsHandler                       *metrics.CapturingHandler
	logger                               *ilog.MemoryLogger
	testConfig                           Config
	taskQueue, endpoint, endpointBaseURL string
}

type testContextOptions struct {
	clientInterceptors []interceptor.ClientInterceptor
}

type testContextOption func(opts *testContextOptions)

func withClientInterceptors(interceptors ...interceptor.ClientInterceptor) testContextOption {
	return func(opts *testContextOptions) {
		opts.clientInterceptors = append(opts.clientInterceptors, interceptors...)
	}
}

func newTestContext(t *testing.T, ctx context.Context, optionFuncs ...testContextOption) *testContext {
	options := &testContextOptions{}
	for _, opt := range optionFuncs {
		opt(options)
	}

	config := NewConfig()
	require.NoError(t, WaitForTCP(time.Minute, config.ServiceAddr))

	metricsHandler := metrics.NewCapturingHandler()
	logger := ilog.NewMemoryLogger()
	c, err := client.DialContext(ctx, client.Options{
		HostPort:          config.ServiceAddr,
		Namespace:         config.Namespace,
		Logger:            logger,
		ConnectionOptions: client.ConnectionOptions{TLS: config.TLS},
		MetricsHandler:    metricsHandler,
		Interceptors:      options.clientInterceptors,
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
		logger:          logger,
		taskQueue:       taskQueue,
		endpoint:        endpoint,
		endpointBaseURL: endpointBaseURL,
	}

	return tc
}

func (tc *testContext) newNexusClient(t *testing.T, service string) *nexus.HTTPClient {
	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tc.testConfig.TLS,
		},
	}
	nc, err := nexus.NewHTTPClient(nexus.HTTPClientOptions{
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

func (tc *testContext) requireTaskQueueTimer(t *assert.CollectT, metric string) {
	assert.True(t, slices.ContainsFunc(tc.metricsHandler.Timers(), func(ct *metrics.CapturedTimer) bool {
		return ct.Name == metric && ct.Tags[metrics.TaskQueueTagName] == tc.taskQueue
	}))
}

func (tc *testContext) requireTimer(t *assert.CollectT, metric, service, operation string) {
	assert.True(t, slices.ContainsFunc(tc.metricsHandler.Timers(), func(ct *metrics.CapturedTimer) bool {
		return ct.Name == metric &&
			ct.Tags[metrics.TaskQueueTagName] == tc.taskQueue &&
			ct.Tags[metrics.NexusServiceTagName] == service &&
			ct.Tags[metrics.NexusOperationTagName] == operation
	}))
}

func (tc *testContext) requireFailureCounter(t *assert.CollectT, service, operation, failureType string) {
	assert.True(t, slices.ContainsFunc(tc.metricsHandler.Counters(), func(ct *metrics.CapturedCounter) bool {
		return ct.Name == metrics.NexusTaskExecutionFailedCounter &&
			ct.Tags[metrics.NexusServiceTagName] == service &&
			ct.Tags[metrics.NexusOperationTagName] == operation &&
			ct.Tags[metrics.FailureReasonTagName] == failureType
	}))
}

func (tc *testContext) requireLogTags(t *assert.CollectT, message, service, operation string) {
	assert.True(t, slices.ContainsFunc(tc.logger.Lines(), func(line string) bool {
		return strings.Contains(line, message) &&
			strings.Contains(line, "NexusService") &&
			strings.Contains(line, service) &&
			strings.Contains(line, "NexusOperation") &&
			strings.Contains(line, operation)
	}))
}

var syncOp = nexus.NewSyncOperation("sync-op", func(ctx context.Context, s string, o nexus.StartOperationOptions) (string, error) {
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
		return "", nexus.NewOperationFailedError("fail")
	case "fmt-errorf":
		return "", fmt.Errorf("arbitrary error message")
	case "handlererror":
		return "", &nexus.HandlerError{
			Type:  nexus.HandlerErrorTypeBadRequest,
			Cause: errors.New(s),
		}
	case "already-started":
		return "", serviceerror.NewWorkflowExecutionAlreadyStarted("faking workflow already started", "dont-care", "dont-care")
	case "retryable-application-error":
		return "", temporal.NewApplicationError("fake app error for test", "FakeTestError")
	case "non-retryable-application-error":
		return "", temporal.NewApplicationErrorWithOptions("fake app error for test", "FakeTestError", temporal.ApplicationErrorOptions{
			NonRetryable: true,
		})
	case "timeout":
		<-ctx.Done()
		return "", ctx.Err()
	case "panic":
		panic("panic requested")
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
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()

	tc := newTestContext(t, ctx)

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(syncOp))
	w.RegisterNexusService(service)
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
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("fail", func(t *testing.T) {
		tc.metricsHandler.Clear()
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "fail", nexus.ExecuteOperationOptions{})
		var opErr *nexus.OperationError
		require.ErrorAs(t, err, &opErr)
		require.Equal(t, nexus.OperationStateFailed, opErr.State)
		require.Equal(t, "fail", opErr.Cause.Error())

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "operation_failed")
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("fmt-errorf", func(t *testing.T) {
		tc.metricsHandler.Clear()
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "fmt-errorf", nexus.ExecuteOperationOptions{})
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
		require.Contains(t, handlerErr.Cause.Error(), "arbitrary error message")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "handler_error_INTERNAL")
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("handlererror", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "handlererror", nexus.ExecuteOperationOptions{})
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
		require.Contains(t, handlerErr.Cause.Error(), "handlererror")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "handler_error_BAD_REQUEST")
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("already-started", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "already-started", nexus.ExecuteOperationOptions{})
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
		if os.Getenv("DISABLE_SERVER_1_27_TESTS") == "" {
			require.Equal(t, nexus.HandlerErrorRetryBehaviorNonRetryable, handlerErr.RetryBehavior)
		}
		require.Contains(t, handlerErr.Cause.Error(), "faking workflow already started")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "handler_error_BAD_REQUEST")
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("retryable-application-error", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "retryable-application-error", nexus.ExecuteOperationOptions{})
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
		require.Contains(t, handlerErr.Cause.Error(), "fake app error for test")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "handler_error_INTERNAL")
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("non-retryable-application-error", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "non-retryable-application-error", nexus.ExecuteOperationOptions{})
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
		if os.Getenv("DISABLE_SERVER_1_27_TESTS") == "" {
			require.Equal(t, nexus.HandlerErrorRetryBehaviorNonRetryable, handlerErr.RetryBehavior)
		}
		require.Contains(t, handlerErr.Cause.Error(), "fake app error for test")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "handler_error_BAD_REQUEST")
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("panic", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "panic", nexus.ExecuteOperationOptions{})
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
		require.Contains(t, handlerErr.Cause.Error(), "panic: panic requested")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			tc.requireTimer(t, metrics.NexusTaskEndToEndLatency, service.Name, syncOp.Name())
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "handler_error_INTERNAL")
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("timeout", func(t *testing.T) {
		_, err := nexus.ExecuteOperation(ctx, nc, syncOp, "timeout", nexus.ExecuteOperationOptions{
			// Force shorter timeout to speed up the test and get a response back.
			Header: nexus.Header{nexus.HeaderRequestTimeout: "300ms"},
		})
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeUpstreamTimeout, handlerErr.Type)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTaskQueueTimer(t, metrics.NexusTaskScheduleToStartLatency)
			// NOTE metrics.NexusTaskEndToEndLatency isn't recorded on timeouts.
			tc.requireTimer(t, metrics.NexusTaskExecutionLatency, service.Name, syncOp.Name())
			tc.requireFailureCounter(t, service.Name, syncOp.Name(), "timeout")
		}, time.Second*3, time.Millisecond*100)
	})
}

func TestNexusWorkflowRunOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
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

	link := &common.Link_WorkflowEvent{
		Namespace:  tc.testConfig.Namespace,
		WorkflowId: "caller-wf-id",
		RunId:      "caller-run-id",
		Reference: &common.Link_WorkflowEvent_EventRef{
			EventRef: &common.Link_WorkflowEvent_EventReference{
				EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED,
			},
		},
	}

	workflowID := "nexus-handler-workflow-" + uuid.NewString()
	result, err := nexus.StartOperation(ctx, nc, workflowOp, workflowID, nexus.StartOperationOptions{
		CallbackURL:    "http://localhost/test",
		CallbackHeader: nexus.Header{"test": "ok"},
		Links:          []nexus.Link{temporalnexus.ConvertLinkWorkflowEventToNexusLink(link)},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Pending)
	handle := result.Pending
	require.NotEmpty(t, handle.Token)
	desc, err := tc.client.DescribeWorkflowExecution(ctx, workflowID, "")
	require.NoError(t, err)

	require.Equal(t, 1, len(desc.Callbacks))
	callback, ok := desc.Callbacks[0].Callback.Variant.(*common.Callback_Nexus_)
	require.True(t, ok)
	require.Equal(t, "http://localhost/test", callback.Nexus.Url)
	require.Subset(t, callback.Nexus.Header, map[string]string{"test": "ok"})

	iter := tc.client.GetWorkflowHistory(ctx, workflowID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		if event.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
			require.Len(t, event.GetLinks(), 1)
			require.True(t, proto.Equal(link, event.GetLinks()[0].GetWorkflowEvent()))
			break
		}
	}

	run := tc.client.GetWorkflow(ctx, workflowID, "")
	require.NoError(t, handle.Cancel(ctx, nexus.CancelOperationOptions{}))
	require.ErrorContains(t, run.Get(ctx, nil), "canceled")
}

func TestOperationSummary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	op := nexus.NewSyncOperation("op", func(ctx context.Context, outcome string, o nexus.StartOperationOptions) (string, error) {
		return outcome, nil
	})

	wf := func(ctx workflow.Context, outcome string) error {
		c := workflow.NewNexusClient(tc.endpoint, "test")
		fut := c.ExecuteOperation(ctx, op, outcome, workflow.NexusOperationOptions{
			Summary: "nexus operation summary",
		})
		var res string

		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil && outcome == "successful" {
			return fmt.Errorf("expected start to succeed: %w", err)
		}
		if exec.OperationToken != "" {
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

	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue: tc.taskQueue,
		// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
		// timeout to speed up the attempts.
		WorkflowTaskTimeout: time.Second,
	}, wf, "successful")
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))

	iter := tc.client.GetWorkflowHistory(ctx, run.GetID(), "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var nexusScheduledWorkflowEvent *historypb.HistoryEvent
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		if event.GetNexusOperationScheduledEventAttributes() != nil {
			require.Nil(t, nexusScheduledWorkflowEvent)
			nexusScheduledWorkflowEvent = event
		}
	}

	require.NotNil(t, nexusScheduledWorkflowEvent)
	var str string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(
		nexusScheduledWorkflowEvent.UserMetadata.Summary, &str))
	require.Equal(t, "nexus operation summary", str)
}

func TestSyncOperationFromWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	testTimerName := "user-timer"
	op := nexus.NewSyncOperation("op", func(ctx context.Context, outcome string, o nexus.StartOperationOptions) (string, error) {
		require.NotPanicsf(t, func() {
			temporalnexus.GetClient(ctx)
		}, "Failed to get client from operation context.")

		mh := temporalnexus.GetMetricsHandler(ctx)
		mh.Timer(testTimerName).Record(time.Second)

		l := temporalnexus.GetLogger(ctx)
		l.Info(outcome)

		switch outcome {
		case "successful":
			return outcome, nil
		case "operation-plain-error":
			return "", nexus.NewOperationFailedError("failed for test")
		case "operation-app-error":
			return "", &nexus.OperationError{
				State: nexus.OperationStateFailed,
				Cause: temporal.NewApplicationError("failed with app error", "TestType", "foo"),
			}
		case "handler-plain-error":
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "bad request")
		case "handler-app-error":
			return "", &nexus.HandlerError{
				Type:  nexus.HandlerErrorTypeBadRequest,
				Cause: temporal.NewApplicationError("failed with app error", "TestType", "foo"),
			}
		case "canceled":
			return "", nexus.NewOperationCanceledError("canceled for test")
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
		if exec.OperationToken != "" {
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
		tc.metricsHandler.Clear()
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "successful")
		require.NoError(t, err)
		require.NoError(t, run.Get(ctx, nil))
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, testTimerName, service.Name, op.Name())
			tc.requireLogTags(t, "successful", service.Name, op.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("OpFailedPlainError", func(t *testing.T) {
		tc.metricsHandler.Clear()
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "operation-plain-error")
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
		require.Equal(t, "", opErr.OperationToken)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "failed for test", appErr.Message())
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, testTimerName, service.Name, op.Name())
			tc.requireLogTags(t, "operation-plain-error", service.Name, op.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("OpFailedAppError", func(t *testing.T) {
		tc.metricsHandler.Clear()
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "operation-app-error")
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
		require.Equal(t, "", opErr.OperationToken)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "failed with app error", appErr.Message())
		require.Equal(t, "TestType", appErr.Type())
		var detail string
		require.NoError(t, appErr.Details(&detail))
		require.Equal(t, "foo", detail)
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, testTimerName, service.Name, op.Name())
			tc.requireLogTags(t, "operation-app-error", service.Name, op.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("OpHandlerPlainError", func(t *testing.T) {
		tc.metricsHandler.Clear()
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "handler-plain-error")
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
		require.Equal(t, "", opErr.OperationToken)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, handlerErr.Cause, &appErr)
		require.Equal(t, "bad request", appErr.Message())
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, testTimerName, service.Name, op.Name())
			tc.requireLogTags(t, "handler-plain-error", service.Name, op.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("OpHandlerAppError", func(t *testing.T) {
		tc.metricsHandler.Clear()
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf, "handler-app-error")
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
		require.Equal(t, "", opErr.OperationToken)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var handlerErr *nexus.HandlerError
		require.ErrorAs(t, err, &handlerErr)
		require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, handlerErr.Cause, &appErr)
		require.Equal(t, "failed with app error", appErr.Message())
		require.Equal(t, "TestType", appErr.Type())
		var detail string
		require.NoError(t, appErr.Details(&detail))
		require.Equal(t, "foo", detail)
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, testTimerName, service.Name, op.Name())
			tc.requireLogTags(t, "handler-app-error", service.Name, op.Name())
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("OpCanceled", func(t *testing.T) {
		tc.metricsHandler.Clear()
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
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			tc.requireTimer(t, testTimerName, service.Name, op.Name())
			tc.requireLogTags(t, "canceled", service.Name, op.Name())
		}, time.Second*3, time.Millisecond*100)
	})
}

func TestInvalidOperationInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	wf := func(ctx workflow.Context) error {
		c := workflow.NewNexusClient(tc.endpoint, "test")
		fut := c.ExecuteOperation(ctx, workflowOp, 3456, workflow.NexusOperationOptions{})
		return fut.Get(ctx, nil)
	}
	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	w.RegisterWorkflow(wf)
	w.Start()
	t.Cleanup(w.Stop)
	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: tc.taskQueue}, wf)
	require.NoError(t, err)
	require.ErrorContains(t, run.Get(ctx, nil), `cannot assign argument of type "int" to type "string" for operation "workflow-op"`)
}

func TestAsyncOperationFromWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	handlerWorkflow := func(ctx workflow.Context, action string) (string, error) {
		switch action {
		case "succeed":
			return action, nil
		case "fail":
			return "", fmt.Errorf("handler workflow failed in test")
		case "fail-app-error":
			return "", temporal.NewApplicationError("failed with app error", "TestType", "foo")
		case "wait-for-cancel":
			return "", workflow.Await(ctx, func() bool { return false })
		default:
			panic(fmt.Errorf("unexpected outcome: %s", action))
		}
	}
	handlerWfID := ""
	op := temporalnexus.NewWorkflowRunOperation(
		"op",
		handlerWorkflow,
		func(ctx context.Context, action string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			require.NotPanicsf(t, func() {
				temporalnexus.GetMetricsHandler(ctx)
				temporalnexus.GetLogger(ctx)
			}, "Failed to get metrics handler or logger from operation context.")

			handlerWfID = ""
			if action == "fail-to-start" {
				return client.StartWorkflowOptions{}, nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "fake internal error")
			}
			handlerWfID = soo.RequestID
			return client.StartWorkflowOptions{
				ID: soo.RequestID,
			}, nil
		},
	)
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
			}
			cancel()
		})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil && action != "fail-to-start" {
			return fmt.Errorf("expected start to succeed: %w", err)
		}
		if exec.OperationToken == "" && action != "fail-to-start" {
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
	w.RegisterWorkflowWithOptions(handlerWorkflow, workflow.RegisterOptions{Name: "foo"})
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

		// Check the link is added in the caller workflow.
		iter := tc.client.GetWorkflowHistory(
			ctx,
			run.GetID(),
			run.GetRunID(),
			false,
			enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
		)
		var nexusOperationScheduleEventID int64
		var targetEvent *historypb.HistoryEvent
		for iter.HasNext() {
			event, err := iter.Next()
			require.NoError(t, err)
			if event.GetEventType() == enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED {
				nexusOperationScheduleEventID = event.GetEventId()
			} else if event.GetEventType() == enumspb.EVENT_TYPE_NEXUS_OPERATION_STARTED {
				targetEvent = event
				break
			}
		}
		require.NotNil(t, targetEvent)
		require.Len(t, targetEvent.GetLinks(), 1)
		link := targetEvent.GetLinks()[0]
		require.Equal(t, tc.testConfig.Namespace, link.GetWorkflowEvent().GetNamespace())
		require.Equal(t, handlerWfID, link.GetWorkflowEvent().GetWorkflowId())
		require.NotEmpty(t, link.GetWorkflowEvent().GetRunId())
		require.True(t, proto.Equal(
			&common.Link_WorkflowEvent_EventReference{
				EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			},
			link.GetWorkflowEvent().GetEventRef(),
		))
		handlerRunID := link.GetWorkflowEvent().GetRunId()

		// Check the link is added in the handler workflow.
		iter = tc.client.GetWorkflowHistory(
			ctx,
			handlerWfID,
			handlerRunID,
			false,
			enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
		)
		targetEvent = nil
		for iter.HasNext() {
			event, err := iter.Next()
			require.NoError(t, err)
			if event.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
				targetEvent = event
				break
			}
		}
		require.NotNil(t, targetEvent)
		// Verify that calling by name works.
		require.Equal(t, "foo", targetEvent.GetWorkflowExecutionStartedEventAttributes().WorkflowType.Name)
		// Verify that links are properly attached.
		require.Len(t, targetEvent.GetLinks(), 1)
		require.True(t, proto.Equal(
			&common.Link_WorkflowEvent{
				Namespace:  tc.testConfig.Namespace,
				WorkflowId: run.GetID(),
				RunId:      run.GetRunID(),
				Reference: &common.Link_WorkflowEvent_EventRef{
					EventRef: &common.Link_WorkflowEvent_EventReference{
						EventId:   nexusOperationScheduleEventID,
						EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED,
					},
				},
			},
			targetEvent.GetLinks()[0].GetWorkflowEvent(),
		))
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
		require.NotEmpty(t, opErr.OperationToken)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "handler workflow failed in test", appErr.Message())
	})

	t.Run("OpFailedAppError", func(t *testing.T) {
		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, "fail-app-error")
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
		require.NotEmpty(t, opErr.OperationToken)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		require.Greater(t, opErr.ScheduledEventID, int64(0))
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "failed with app error", appErr.Message())
		require.Equal(t, "TestType", appErr.Type())
		var details string
		require.NoError(t, appErr.Details(&details))
		require.Equal(t, "foo", details)
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
		history := tc.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for history.HasNext() {
			event, err := history.Next()
			require.NoError(t, err)
			require.NotEqual(t, enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED, event.EventType)
		}
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

func runCancellationTypeTest(ctx context.Context, tc *testContext, cancellationType workflow.NexusOperationCancellationType, t *testing.T) (client.WorkflowRun, string, time.Time) {
	handlerWf := func(ctx workflow.Context, ownID string) (string, error) {
		err := workflow.Await(ctx, func() bool { return false })
		// Delay completion after receiving cancellation so that assertions on end time aren't flakey.
		disconCtx, _ := workflow.NewDisconnectedContext(ctx)
		_ = workflow.Sleep(disconCtx, time.Second)
		return "", err
	}

	handlerID := ""
	op := temporalnexus.NewWorkflowRunOperation(
		"workflow-op",
		handlerWf,
		func(ctx context.Context, _ string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			handlerID = soo.RequestID
			return client.StartWorkflowOptions{ID: soo.RequestID}, nil
		},
	)

	var unblockedTime time.Time
	callerWf := func(ctx workflow.Context, cancellation workflow.NexusOperationCancellationType) error {
		c := workflow.NewNexusClient(tc.endpoint, "test")
		fut := c.ExecuteOperation(ctx, op, "", workflow.NexusOperationOptions{
			CancellationType: cancellation,
		})

		if err := fut.GetNexusOperationExecution().Get(ctx, nil); err != nil {
			return err
		}

		if cancellation == workflow.NexusOperationCancellationTypeTryCancel || cancellation == workflow.NexusOperationCancellationTypeWaitRequested {
			disconCtx, _ := workflow.NewDisconnectedContext(ctx) // Use disconnected ctx so it is not auto canceled.
			workflow.Go(disconCtx, func(ctx workflow.Context) {
				// Wake up the caller so it is not waiting for the operation to complete to get the next WFT.
				_ = workflow.Sleep(ctx, time.Millisecond)
			})
		}

		_ = fut.Get(ctx, nil)
		unblockedTime = workflow.Now(ctx).UTC()
		return workflow.Await(ctx, func() bool { return false })
	}

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(op))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(handlerWf)
	w.RegisterWorkflow(callerWf)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue:           tc.taskQueue,
		WorkflowTaskTimeout: time.Second,
	}, callerWf, cancellationType)
	require.NoError(t, err)
	require.Eventuallyf(t, func() bool {
		if handlerID == "" {
			return false
		}
		_, descErr := tc.client.DescribeWorkflow(ctx, handlerID, "")
		return descErr == nil
	}, 2*time.Second, 20*time.Millisecond, "timed out waiting for handler wf to start")
	require.NoError(t, tc.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

	err = run.Get(ctx, nil)
	var execErr *temporal.WorkflowExecutionError
	require.ErrorAs(t, err, &execErr)
	err = execErr.Unwrap()
	var canceledErr *temporal.CanceledError
	require.ErrorAs(t, err, &canceledErr)

	return run, handlerID, unblockedTime
}

func TestAsyncOperationFromWorkflow_CancellationTypes(t *testing.T) {
	t.Run("Abandon", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
		defer cancel()
		tc := newTestContext(t, ctx)

		callerRun, handlerID, unblockedTime := runCancellationTypeTest(ctx, tc, workflow.NexusOperationCancellationTypeAbandon, t)
		require.NotZero(t, unblockedTime)

		// Verify that caller never sent a cancellation request.
		history := tc.client.GetWorkflowHistory(ctx, callerRun.GetID(), callerRun.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for history.HasNext() {
			event, err := history.Next()
			require.NoError(t, err)
			require.NotEqual(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED, event.EventType)
			require.NotEqual(t, enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCEL_REQUEST_COMPLETED, event.EventType)
			require.NotEqual(t, enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCEL_REQUEST_FAILED, event.EventType)
		}

		handlerDesc, err := tc.client.DescribeWorkflowExecution(ctx, handlerID, "")
		require.NoError(t, err)
		require.Equal(t, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, handlerDesc.WorkflowExecutionInfo.Status)

		require.NoError(t, tc.client.TerminateWorkflow(ctx, handlerID, "", "test"))
	})

	t.Run("TryCancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
		defer cancel()
		tc := newTestContext(t, ctx)
		callerRun, handlerID, unblockedTime := runCancellationTypeTest(ctx, tc, workflow.NexusOperationCancellationTypeTryCancel, t)

		// Verify operation future was unblocked after cancel command was recorded.
		callerHist := tc.client.GetWorkflowHistory(ctx, callerRun.GetID(), callerRun.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		var callerCloseEvent *historypb.HistoryEvent
		foundRequestedEvent := false
		for callerHist.HasNext() {
			event, err := callerHist.Next()
			require.NoError(t, err)
			if event.EventType == enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCEL_REQUESTED {
				foundRequestedEvent = true
				require.Greater(t, unblockedTime, event.EventTime.AsTime().UTC())
			}
			callerCloseEvent = event
		}
		require.True(t, foundRequestedEvent)

		// Verify that caller completed before the handler.
		var err error
		var handlerCloseEvent *historypb.HistoryEvent
		require.Eventuallyf(t, func() bool {
			handlerHist := tc.client.GetWorkflowHistory(ctx, handlerID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
			handlerCloseEvent, err = handlerHist.Next()
			return handlerCloseEvent != nil && err == nil
		}, 5*time.Second, 200*time.Millisecond, "timed out waiting for handler wf close event")
		require.Greater(t, handlerCloseEvent.EventTime.AsTime().UTC(), callerCloseEvent.EventTime.AsTime().UTC())
	})

	t.Run("WaitRequested", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
		defer cancel()
		tc := newTestContext(t, ctx)
		callerRun, handlerID, unblockedTime := runCancellationTypeTest(ctx, tc, workflow.NexusOperationCancellationTypeWaitRequested, t)

		// Verify operation future was unblocked after cancel request was delivered.
		callerHist := tc.client.GetWorkflowHistory(ctx, callerRun.GetID(), callerRun.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		var callerCloseEvent *historypb.HistoryEvent
		foundRequestCompleted := false
		for callerHist.HasNext() {
			event, err := callerHist.Next()
			require.NoError(t, err)
			if event.EventType == enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCEL_REQUEST_COMPLETED {
				foundRequestCompleted = true
				require.Greater(t, unblockedTime, event.EventTime.AsTime().UTC())
			}
			callerCloseEvent = event
		}
		require.True(t, foundRequestCompleted)

		// Verify that caller completed before the handler.
		var err error
		var handlerCloseEvent *historypb.HistoryEvent
		require.Eventuallyf(t, func() bool {
			handlerHist := tc.client.GetWorkflowHistory(ctx, handlerID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
			handlerCloseEvent, err = handlerHist.Next()
			return handlerCloseEvent != nil && err == nil
		}, 5*time.Second, 200*time.Millisecond, "timed out waiting for handler wf close event")
		require.Greater(t, handlerCloseEvent.EventTime.AsTime().UTC(), callerCloseEvent.EventTime.AsTime().UTC())
	})

	t.Run("WaitCompleted", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
		defer cancel()
		tc := newTestContext(t, ctx)
		callerRun, handlerID, unblockedTime := runCancellationTypeTest(ctx, tc, workflow.NexusOperationCancellationTypeWaitCompleted, t)

		// Verify operation future was unblocked after operation was cancelled.
		callerHist := tc.client.GetWorkflowHistory(ctx, callerRun.GetID(), callerRun.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		var callerCloseEvent *historypb.HistoryEvent
		foundCancelledEvent := false
		for callerHist.HasNext() {
			event, err := callerHist.Next()
			require.NoError(t, err)
			if event.EventType == enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCELED {
				foundCancelledEvent = true
				require.Greater(t, unblockedTime, event.EventTime.AsTime().UTC())
			}
			callerCloseEvent = event
		}
		require.True(t, foundCancelledEvent)

		// Verify that caller completed after the handler.
		var err error
		var handlerCloseEvent *historypb.HistoryEvent
		require.Eventuallyf(t, func() bool {
			handlerHist := tc.client.GetWorkflowHistory(ctx, handlerID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
			handlerCloseEvent, err = handlerHist.Next()
			return handlerCloseEvent != nil && err == nil
		}, 500*time.Millisecond, 50*time.Millisecond, "timed out waiting for handler wf close event")
		require.Greater(t, callerCloseEvent.EventTime.AsTime(), handlerCloseEvent.EventTime.AsTime())
	})
}

func TestAsyncOperationFromWorkflow_MultipleCallers(t *testing.T) {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") == "1" {
		t.Skip()
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tctx := newTestContext(t, ctx)

	handlerWorkflowID := uuid.NewString()
	handlerWf := func(ctx workflow.Context, input string) (string, error) {
		workflow.GetSignalChannel(ctx, "terminate").Receive(ctx, nil)
		return "hello " + input, nil
	}

	op := temporalnexus.NewWorkflowRunOperation(
		"op",
		handlerWf,
		func(ctx context.Context, input string, opts nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			var conflictPolicy enumspb.WorkflowIdConflictPolicy
			if input == "conflict-policy-use-existing" {
				conflictPolicy = enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING
			}
			return client.StartWorkflowOptions{
				ID:                       handlerWorkflowID,
				WorkflowIDConflictPolicy: conflictPolicy,
			}, nil
		},
	)

	type CallerWfOutput struct {
		CntOk  int
		CntErr int
	}

	callerWf := func(ctx workflow.Context, input string, numCalls int) (CallerWfOutput, error) {
		output := CallerWfOutput{}
		var retError error

		wg := workflow.NewWaitGroup(ctx)
		execOpCh := workflow.NewChannel(ctx)
		client := workflow.NewNexusClient(tctx.endpoint, "test")

		for i := 0; i < numCalls; i++ {
			wg.Add(1)
			workflow.Go(ctx, func(ctx workflow.Context) {
				defer wg.Done()
				fut := client.ExecuteOperation(ctx, op, input, workflow.NexusOperationOptions{})
				var exec workflow.NexusOperationExecution
				err := fut.GetNexusOperationExecution().Get(ctx, &exec)
				if err != nil {
					output.CntErr++
					var handlerErr *nexus.HandlerError
					var appErr *temporal.ApplicationError
					if !errors.As(err, &handlerErr) {
						retError = err
					} else if !errors.As(handlerErr, &appErr) {
						retError = err
					} else if appErr.Type() != "WorkflowExecutionAlreadyStarted" {
						retError = err
					}
				} else {
					output.CntOk++
				}
				execOpCh.Send(ctx, nil)
				if err != nil {
					return
				}
				var res string
				err = fut.Get(ctx, &res)
				if err != nil {
					retError = err
				} else if res != "hello "+input {
					retError = fmt.Errorf("unexpected result from handler workflow: %q", res)
				}
			})
		}

		for i := 0; i < numCalls; i++ {
			execOpCh.Receive(ctx, nil)
		}

		if output.CntOk > 0 {
			// signal handler workflow so it will complete
			workflow.SignalExternalWorkflow(ctx, handlerWorkflowID, "", "terminate", nil).Get(ctx, nil)
		}
		wg.Wait(ctx)
		return output, retError
	}

	w := worker.New(tctx.client, tctx.taskQueue, worker.Options{})
	service := nexus.NewService("test")
	require.NoError(t, service.Register(op))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(handlerWf)
	w.RegisterWorkflow(callerWf)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	testCases := []struct {
		input       string
		checkOutput func(t *testing.T, numCalls int, res CallerWfOutput, err error)
	}{
		{
			input: "conflict-policy-fail",
			checkOutput: func(t *testing.T, numCalls int, res CallerWfOutput, err error) {
				require.NoError(t, err)
				require.EqualValues(t, 1, res.CntOk)
				require.EqualValues(t, numCalls-1, res.CntErr)
			},
		},
		{
			input: "conflict-policy-use-existing",
			checkOutput: func(t *testing.T, numCalls int, res CallerWfOutput, err error) {
				require.NoError(t, err)
				require.EqualValues(t, numCalls, res.CntOk)
				require.EqualValues(t, 0, res.CntErr)
			},
		},
	}

	// number of concurrent Nexus operation calls
	numCalls := 5
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			run, err := tctx.client.ExecuteWorkflow(
				ctx,
				client.StartWorkflowOptions{
					TaskQueue: tctx.taskQueue,
					// The endpoint registry may take a bit to propagate to the history service, use a shorter
					// workflow task timeout to speed up the attempts.
					WorkflowTaskTimeout: time.Second,
				},
				callerWf,
				tc.input,
				numCalls,
			)
			require.NoError(t, err)
			var res CallerWfOutput
			err = run.Get(ctx, &res)
			tc.checkOutput(t, numCalls, res, err)
		})
	}
}

type manualAsyncOp struct {
	nexus.UnimplementedOperation[nexus.NoValue, nexus.NoValue]
}

func (*manualAsyncOp) Name() string {
	return "op"
}

// Not relevant for this test.
func (o *manualAsyncOp) FailureToError(nexus.Failure) error {
	panic("not implemented")
}

func (o *manualAsyncOp) ErrorToFailure(err error) nexus.Failure {
	return nexus.Failure{
		Message: err.Error(),
		Metadata: map[string]string{
			"type": "custom",
		},
		Details: []byte(`"details"`),
	}
}

func (o *manualAsyncOp) Start(ctx context.Context, input nexus.NoValue, options nexus.StartOperationOptions) (nexus.HandlerStartOperationResult[nexus.NoValue], error) {
	// Complete before start.
	completion, err := nexus.NewOperationCompletionUnsuccessful(nexus.NewFailedOperationError(errors.New("async failure")), nexus.OperationCompletionUnsuccessfulOptions{
		FailureConverter: o,
	})
	if err != nil {
		return nil, err
	}
	req, err := nexus.NewCompletionHTTPRequest(ctx, options.CallbackURL, completion)
	if err != nil {
		return nil, err
	}
	for k, v := range options.CallbackHeader {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nexus.NewFailedOperationError(fmt.Errorf("failed to post completion, got status: %v", resp.Status))
	}
	// This result will be ignored.
	return &nexus.HandlerStartOperationResultAsync{OperationToken: "dont-care"}, nil
}

// TestAsyncOperationCompletionCustomFailureConverter tests the completion path when a failure is generated with a
// custom failure converter.
func TestAsyncOperationCompletionCustomFailureConverter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	op := &manualAsyncOp{}

	callerWorkflow := func(ctx workflow.Context) error {
		c := workflow.NewNexusClient(tc.endpoint, "test")
		ctx, cancel := workflow.WithCancel(ctx)
		defer cancel()
		fut := c.ExecuteOperation(ctx, op, nil, workflow.NexusOperationOptions{})
		return fut.Get(ctx, nil)
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
	var execErr *temporal.WorkflowExecutionError
	err = run.Get(ctx, nil)
	require.ErrorAs(t, err, &execErr)
	var opErr *temporal.NexusOperationError
	err = execErr.Unwrap()
	require.ErrorAs(t, err, &opErr)
	require.Equal(t, tc.endpoint, opErr.Endpoint)
	require.Equal(t, "test", opErr.Service)
	require.Equal(t, op.Name(), opErr.Operation)
	require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
	require.Greater(t, opErr.ScheduledEventID, int64(0))
	err = opErr.Unwrap()
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, "async failure", appErr.Message())
	require.Equal(t, "NexusFailure", appErr.Type())
	var details nexus.Failure
	require.NoError(t, appErr.Details(&details))
	require.Equal(t, "custom", details.Metadata["type"])
	var nexusDetails string
	require.NoError(t, json.Unmarshal(details.Details, &nexusDetails))
	require.Equal(t, "details", nexusDetails)
}

func TestNewNexusClientValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	callerWorkflow := func(ctx workflow.Context, endpoint, service string) (err error) {
		defer func() {
			panicMessage := recover()
			err = fmt.Errorf("recovered: %s", panicMessage)
		}()
		_ = workflow.NewNexusClient(endpoint, service)
		return
	}

	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	w.RegisterWorkflow(callerWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	opts := client.StartWorkflowOptions{
		TaskQueue: tc.taskQueue,
	}

	run, err := tc.client.ExecuteWorkflow(ctx, opts, callerWorkflow, "", "service")
	require.NoError(t, err)
	require.ErrorContains(t, run.Get(ctx, nil), "recovered: endpoint must not be empty")

	run, err = tc.client.ExecuteWorkflow(ctx, opts, callerWorkflow, "endpoint", "")
	require.NoError(t, err)
	require.ErrorContains(t, run.Get(ctx, nil), "recovered: service must not be empty")
}

func TestReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()
	tc := newTestContext(t, ctx)

	op := nexus.NewSyncOperation("op", func(ctx context.Context, nv nexus.NoValue, soo nexus.StartOperationOptions) (nexus.NoValue, error) {
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
	hist := tc.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
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
		dealine, ok := ctx.Deadline()
		require.True(t, ok)
		timeout := time.Until(dealine)
		require.GreaterOrEqual(t, 10*time.Second, timeout)
		require.NotPanicsf(t, func() {
			temporalnexus.GetMetricsHandler(ctx)
			temporalnexus.GetLogger(ctx)
		}, "Failed to get metrics handler or logger from operation context.")

		switch outcome {
		case "ok":
			return outcome, nil
		case "operation-error":
			return "", nexus.NewOperationFailedError("test operation failed")
		case "handler-error":
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "test operation failed")
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

	cases := []struct {
		outcome    string
		checkError func(t *testing.T, err error)
	}{
		{
			outcome: "ok",
			checkError: func(t *testing.T, err error) {
				require.NoError(t, err)

			},
		},
		{
			outcome: "operation-error",
			checkError: func(t *testing.T, err error) {
				var execErr *temporal.WorkflowExecutionError
				require.ErrorAs(t, err, &execErr)
				var opErr *temporal.NexusOperationError
				err = execErr.Unwrap()
				require.ErrorAs(t, err, &opErr)
				require.Equal(t, "endpoint", opErr.Endpoint)
				require.Equal(t, "test", opErr.Service)
				require.Equal(t, op.Name(), opErr.Operation)
				require.Empty(t, opErr.OperationToken)
				require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
				err = opErr.Unwrap()
				var appErr *temporal.ApplicationError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, "test operation failed", appErr.Message())
			},
		},
		{
			outcome: "handler-error",
			checkError: func(t *testing.T, err error) {
				var execErr *temporal.WorkflowExecutionError
				require.ErrorAs(t, err, &execErr)
				var opErr *temporal.NexusOperationError
				err = execErr.Unwrap()
				require.ErrorAs(t, err, &opErr)
				require.Equal(t, "endpoint", opErr.Endpoint)
				require.Equal(t, "test", opErr.Service)
				require.Equal(t, op.Name(), opErr.Operation)
				require.Empty(t, opErr.OperationToken)
				require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
				err = opErr.Unwrap()
				var handlerErr *nexus.HandlerError
				require.ErrorAs(t, err, &handlerErr)
				require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
				err = handlerErr.Unwrap()
				var appErr *temporal.ApplicationError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, "test operation failed", appErr.Message())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.outcome, func(t *testing.T) {
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterNexusService(service)
			env.ExecuteWorkflow(wf, tc.outcome)
			require.True(t, env.IsWorkflowCompleted())
			tc.checkError(t, env.GetWorkflowError())
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
			require.NotPanicsf(t, func() {
				temporalnexus.GetMetricsHandler(ctx)
				temporalnexus.GetLogger(ctx)
			}, "Failed to get metrics handler or logger from operation context.")

			return client.StartWorkflowOptions{ID: opts.RequestID}, nil
		})

	callerWF := func(ctx workflow.Context, outcome string) error {
		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(ctx, op, outcome, workflow.NexusOperationOptions{})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		if exec.OperationToken == "" {
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
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		err = opErr.Unwrap()
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "expected failure", appErr.Message())
	})
}

func TestWorkflowTestSuite_WorkflowRunOperation_ScheduleToCloseTimeout(t *testing.T) {
	handlerSleepDuration := 500 * time.Millisecond
	handlerWF := func(ctx workflow.Context, _ nexus.NoValue) (nexus.NoValue, error) {
		return nil, workflow.Sleep(ctx, handlerSleepDuration)
	}

	opSleepDuration := 250 * time.Millisecond
	op := temporalnexus.NewWorkflowRunOperation(
		"op",
		handlerWF,
		func(ctx context.Context, _ nexus.NoValue, opts nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			if opts.Header.Get(nexus.HeaderOperationTimeout) == "" {
				return client.StartWorkflowOptions{}, nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "expected non empty operation timeout header")
			}
			time.Sleep(opSleepDuration)
			return client.StartWorkflowOptions{ID: opts.RequestID}, nil
		})

	callerWF := func(ctx workflow.Context, scheduleToCloseTimeout time.Duration) error {
		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(ctx, op, nil, workflow.NexusOperationOptions{
			ScheduleToCloseTimeout: scheduleToCloseTimeout,
		})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		if exec.OperationToken == "" {
			return errors.New("got empty operation ID")
		}
		return fut.Get(ctx, nil)
	}

	service := nexus.NewService("test")
	service.Register(op)

	testCases := []struct {
		name                   string
		scheduleToCloseTimeout time.Duration
	}{
		{
			name:                   "success",
			scheduleToCloseTimeout: opSleepDuration + handlerSleepDuration + 100*time.Millisecond,
		},
		{
			name:                   "timeout before operation start",
			scheduleToCloseTimeout: opSleepDuration - 100*time.Millisecond,
		},
		{
			name:                   "timeout after operation start",
			scheduleToCloseTimeout: opSleepDuration + 100*time.Millisecond,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(handlerWF)
			env.RegisterNexusService(service)
			env.ExecuteWorkflow(callerWF, tc.scheduleToCloseTimeout)
			require.True(t, env.IsWorkflowCompleted())
			if tc.scheduleToCloseTimeout >= opSleepDuration+handlerSleepDuration {
				require.NoError(t, env.GetWorkflowError())
			} else {
				var execErr *temporal.WorkflowExecutionError
				err := env.GetWorkflowError()
				require.ErrorAs(t, err, &execErr)
				var opErr *temporal.NexusOperationError
				err = execErr.Unwrap()
				require.ErrorAs(t, err, &opErr)
				require.Equal(t, "endpoint", opErr.Endpoint)
				require.Equal(t, "test", opErr.Service)
				require.Equal(t, op.Name(), opErr.Operation)
				if tc.scheduleToCloseTimeout < opSleepDuration {
					require.Empty(t, opErr.OperationToken)
				} else {
					require.NotEmpty(t, opErr.OperationToken)
				}
				require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
				err = opErr.Unwrap()
				var timeoutErr *temporal.TimeoutError
				require.ErrorAs(t, err, &timeoutErr)
				require.Equal(t, "operation timed out", timeoutErr.Message())
			}
		})
	}
}

func TestWorkflowTestSuite_WorkflowRunOperation_WithCancel(t *testing.T) {
	wf := func(ctx workflow.Context, cancelBeforeStarted bool) error {
		childCtx, cancel := workflow.WithCancel(ctx)
		defer cancel()

		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(childCtx, workflowOp, "workflow-id", workflow.NexusOperationOptions{})
		if cancelBeforeStarted {
			cancel()
		}
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		if exec.OperationToken == "" {
			return errors.New("unexpected non empty operation token")
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
			require.NotEmpty(t, opErr.OperationToken)
			require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
			err = opErr.Unwrap()
			var canceledError *temporal.CanceledError
			require.ErrorAs(t, err, &canceledError)
		})
	}
}

func TestWorkflowTestSuite_WorkflowRunOperation_MultipleCallers(t *testing.T) {
	handlerWorkflowID := uuid.NewString()
	handlerWf := func(ctx workflow.Context, input string) (string, error) {
		workflow.GetSignalChannel(ctx, "terminate").Receive(ctx, nil)
		return "hello " + input, nil
	}

	op := temporalnexus.NewWorkflowRunOperation(
		"op",
		handlerWf,
		func(ctx context.Context, input string, opts nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			var conflictPolicy enumspb.WorkflowIdConflictPolicy
			if input == "conflict-policy-use-existing" {
				conflictPolicy = enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING
			}
			return client.StartWorkflowOptions{
				ID:                       handlerWorkflowID,
				WorkflowIDConflictPolicy: conflictPolicy,
			}, nil
		},
	)

	type CallerWfOutput struct {
		CntOk  int
		CntErr int
	}

	callerWf := func(ctx workflow.Context, input string, numCalls int) (CallerWfOutput, error) {
		output := CallerWfOutput{}
		var retError error

		wg := workflow.NewWaitGroup(ctx)
		execOpCh := workflow.NewChannel(ctx)
		client := workflow.NewNexusClient("endpoint", "test")

		for i := 0; i < numCalls; i++ {
			wg.Add(1)
			workflow.Go(ctx, func(ctx workflow.Context) {
				defer wg.Done()
				fut := client.ExecuteOperation(ctx, op, input, workflow.NexusOperationOptions{})
				var exec workflow.NexusOperationExecution
				err := fut.GetNexusOperationExecution().Get(ctx, &exec)
				if err != nil {
					output.CntErr++
					var handlerErr *nexus.HandlerError
					var appErr *temporal.ApplicationError
					if !errors.As(err, &handlerErr) {
						retError = err
					} else if !errors.As(handlerErr, &appErr) {
						retError = err
					} else if appErr.Type() != "WorkflowExecutionAlreadyStarted" {
						retError = err
					}
				} else {
					output.CntOk++
				}
				execOpCh.Send(ctx, nil)
				if err != nil {
					return
				}
				var res string
				err = fut.Get(ctx, &res)
				if err != nil {
					retError = err
				} else if res != "hello "+input {
					retError = fmt.Errorf("unexpected result from handler workflow: %q", res)
				}
			})
		}

		for i := 0; i < numCalls; i++ {
			execOpCh.Receive(ctx, nil)
		}

		if output.CntOk > 0 {
			// signal handler workflow so it will complete
			workflow.SignalExternalWorkflow(ctx, handlerWorkflowID, "", "terminate", nil).Get(ctx, nil)
		}
		wg.Wait(ctx)
		return output, retError
	}

	service := nexus.NewService("test")
	service.MustRegister(op)

	testCases := []struct {
		input       string
		checkOutput func(t *testing.T, numCalls int, res CallerWfOutput, err error)
	}{
		{
			input: "conflict-policy-fail",
			checkOutput: func(t *testing.T, numCalls int, res CallerWfOutput, err error) {
				require.NoError(t, err)
				require.EqualValues(t, 1, res.CntOk)
				require.EqualValues(t, numCalls-1, res.CntErr)
			},
		},
		{
			input: "conflict-policy-use-existing",
			checkOutput: func(t *testing.T, numCalls int, res CallerWfOutput, err error) {
				require.NoError(t, err)
				require.EqualValues(t, numCalls, res.CntOk)
				require.EqualValues(t, 0, res.CntErr)
			},
		},
	}

	// number of concurrent Nexus operation calls
	numCalls := 5
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(handlerWf)
			env.RegisterNexusService(service)

			env.ExecuteWorkflow(callerWf, tc.input, numCalls)
			require.True(t, env.IsWorkflowCompleted())
			var res CallerWfOutput
			err := env.GetWorkflowResult(&res)
			tc.checkOutput(t, numCalls, res, err)
		})
	}
}

func TestWorkflowTestSuite_NexusSyncOperation_ScheduleToCloseTimeout(t *testing.T) {
	sleepDuration := 500 * time.Millisecond
	op := nexus.NewSyncOperation(
		"sync-op",
		func(
			ctx context.Context,
			_ nexus.NoValue,
			opts nexus.StartOperationOptions,
		) (nexus.NoValue, error) {
			time.Sleep(sleepDuration)
			return nil, nil
		},
	)
	wf := func(ctx workflow.Context, scheduleToCloseTimeout time.Duration) error {
		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(ctx, op, nil, workflow.NexusOperationOptions{
			ScheduleToCloseTimeout: scheduleToCloseTimeout,
		})
		return fut.Get(ctx, nil)
	}

	service := nexus.NewService("test")
	service.Register(op)

	testCases := []struct {
		name                   string
		scheduleToCloseTimeout time.Duration
	}{
		{
			name:                   "success",
			scheduleToCloseTimeout: sleepDuration * 2,
		},
		{
			name:                   "timeout",
			scheduleToCloseTimeout: sleepDuration / 2,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(waitForCancelWorkflow)
			env.RegisterNexusService(service)
			env.ExecuteWorkflow(wf, tc.scheduleToCloseTimeout)
			require.True(t, env.IsWorkflowCompleted())
			if tc.scheduleToCloseTimeout >= sleepDuration {
				require.NoError(t, env.GetWorkflowError())
			} else {
				var execErr *temporal.WorkflowExecutionError
				err := env.GetWorkflowError()
				require.ErrorAs(t, err, &execErr)
				var opErr *temporal.NexusOperationError
				err = execErr.Unwrap()
				require.ErrorAs(t, err, &opErr)
				require.Equal(t, "endpoint", opErr.Endpoint)
				require.Equal(t, "test", opErr.Service)
				require.Equal(t, op.Name(), opErr.Operation)
				require.Empty(t, opErr.OperationToken)
				require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
				err = opErr.Unwrap()
				var timeoutErr *temporal.TimeoutError
				require.ErrorAs(t, err, &timeoutErr)
				require.Equal(t, "operation timed out", timeoutErr.Message())
			}
		})
	}
}

func TestWorkflowTestSuite_NexusSyncOperation_ClientMethods_Panic(t *testing.T) {
	var panicReason any
	op := nexus.NewSyncOperation("signal-op", func(ctx context.Context, _ nexus.NoValue, opts nexus.StartOperationOptions) (nexus.NoValue, error) {
		func() {
			defer func() {
				panicReason = recover()
			}()
			temporalnexus.GetClient(ctx).ExecuteWorkflow(ctx, client.StartWorkflowOptions{}, "test", "", "get-secret")
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

func TestWorkflowTestSuite_MockNexusOperation(t *testing.T) {
	serviceName := "test"
	dummyOpName := "dummy-operation"
	dummyOp := nexus.NewSyncOperation(
		dummyOpName,
		func(ctx context.Context, name string, opts nexus.StartOperationOptions) (string, error) {
			return "Hello " + name, nil
		},
	)

	wf := func(ctx workflow.Context, name string) (string, error) {
		client := workflow.NewNexusClient("endpoint", serviceName)
		fut := client.ExecuteOperation(
			ctx,
			dummyOp,
			name,
			workflow.NexusOperationOptions{
				ScheduleToCloseTimeout: 2 * time.Second,
			},
		)
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return "", err
		}
		var res string
		if err := fut.Get(ctx, &res); err != nil {
			return "", err
		}
		return res, nil
	}

	service := nexus.NewService(serviceName)
	service.Register(dummyOp)

	t.Run("mock result sync", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.OnNexusOperation(
			service,
			dummyOp,
			"Temporal",
			workflow.NexusOperationOptions{
				ScheduleToCloseTimeout: 2 * time.Second,
				CancellationType:       workflow.NexusOperationCancellationTypeWaitCompleted,
			},
		).Return(
			&nexus.HandlerStartOperationResultSync[string]{
				Value: "fake result",
			},
			nil,
		)

		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var res string
		require.NoError(t, env.GetWorkflowResult(&res))
		require.Equal(t, "fake result", res)

		env.AssertExpectations(t)
		env.AssertNexusOperationNumberOfCalls(t, service.Name, 1)
		env.AssertNexusOperationCalled(t, service.Name, dummyOp.Name(), "Temporal", mock.Anything)
		env.AssertNexusOperationNotCalled(t, service.Name, dummyOp.Name(), "random", mock.Anything)
	})

	t.Run("mock result async", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.OnNexusOperation(service, dummyOp, "Temporal", mock.Anything).Return(
			&nexus.HandlerStartOperationResultAsync{
				OperationToken: "operation-token",
			},
			nil,
		)
		require.NoError(t, env.RegisterNexusAsyncOperationCompletion(
			service.Name,
			dummyOp.Name(),
			"operation-token",
			"fake result",
			nil,
			0,
		))

		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var res string
		require.NoError(t, env.GetWorkflowResult(&res))
		require.Equal(t, "fake result", res)
	})

	t.Run("mock operation reference", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.OnNexusOperation(
			serviceName,
			nexus.NewOperationReference[string, string](dummyOpName),
			"Temporal",
			mock.Anything,
		).Return(
			&nexus.HandlerStartOperationResultSync[string]{
				Value: "fake result",
			},
			nil,
		)
		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var res string
		require.NoError(t, env.GetWorkflowResult(&res))
		require.Equal(t, "fake result", res)
	})

	t.Run("mock operation reference existing service", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.OnNexusOperation(
			serviceName,
			nexus.NewOperationReference[string, string](dummyOpName),
			"Temporal",
			mock.Anything,
		).Return(
			&nexus.HandlerStartOperationResultSync[string]{
				Value: "fake result",
			},
			nil,
		)
		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var res string
		require.NoError(t, env.GetWorkflowResult(&res))
		require.Equal(t, "fake result", res)
	})

	t.Run("mock error operation", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.OnNexusOperation(service, dummyOp, "Temporal", mock.Anything).Return(
			nil,
			errors.New("workflow operation failed"),
		)

		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		require.ErrorContains(t, env.GetWorkflowError(), "workflow operation failed")
	})

	t.Run("mock error handler", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.OnNexusOperation(service, dummyOp, "Temporal", mock.Anything).Return(
			&nexus.HandlerStartOperationResultAsync{
				OperationToken: "operation-token",
			},
			nil,
		)
		require.NoError(t, env.RegisterNexusAsyncOperationCompletion(
			serviceName,
			dummyOpName,
			"operation-token",
			"",
			errors.New("workflow handler failed"),
			1*time.Second,
		))

		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		var execErr *temporal.WorkflowExecutionError
		err := env.GetWorkflowError()
		require.ErrorAs(t, err, &execErr)
		var opErr *temporal.NexusOperationError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &opErr)
		require.ErrorContains(t, opErr, "workflow handler failed")
	})

	t.Run("mock after ok", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.OnNexusOperation(
			service,
			dummyOp,
			"Temporal",
			workflow.NexusOperationOptions{
				ScheduleToCloseTimeout: 2 * time.Second,
				CancellationType:       workflow.NexusOperationCancellationTypeWaitCompleted,
			},
		).After(1*time.Second).Return(
			&nexus.HandlerStartOperationResultSync[string]{
				Value: "fake result",
			},
			nil,
		)

		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var res string
		require.NoError(t, env.GetWorkflowResult(&res))
		require.Equal(t, "fake result", res)
	})

	t.Run("mock after timeout", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterNexusService(service)
		env.OnNexusOperation(
			service,
			dummyOp,
			"Temporal",
			workflow.NexusOperationOptions{
				ScheduleToCloseTimeout: 2 * time.Second,
				CancellationType:       workflow.NexusOperationCancellationTypeWaitCompleted,
			},
		).After(3*time.Second).Return(
			&nexus.HandlerStartOperationResultSync[string]{
				Value: "fake result",
			},
			nil,
		)

		env.ExecuteWorkflow(wf, "Temporal")
		require.True(t, env.IsWorkflowCompleted())
		var execErr *temporal.WorkflowExecutionError
		err := env.GetWorkflowError()
		require.ErrorAs(t, err, &execErr)
		var opErr *temporal.NexusOperationError
		err = execErr.Unwrap()
		require.ErrorAs(t, err, &opErr)
		require.Equal(t, "nexus operation completed unsuccessfully", opErr.Message)
		err = opErr.Unwrap()
		var timeoutErr *temporal.TimeoutError
		require.ErrorAs(t, err, &timeoutErr)
		require.Equal(t, "operation timed out", timeoutErr.Message())
	})
}

func TestWorkflowTestSuite_NexusListeners(t *testing.T) {
	startedListenerCalled := false
	completedListenerCalled := false
	handlerWf := func(ctx workflow.Context, _ nexus.NoValue) (nexus.NoValue, error) {
		require.True(t, startedListenerCalled)
		require.False(t, completedListenerCalled)
		return nil, nil
	}
	op := temporalnexus.NewWorkflowRunOperation(
		"op",
		handlerWf,
		func(
			ctx context.Context,
			_ nexus.NoValue,
			opts nexus.StartOperationOptions,
		) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{ID: opts.RequestID}, nil
		},
	)

	callerWf := func(ctx workflow.Context) error {
		client := workflow.NewNexusClient("endpoint", "test")
		fut := client.ExecuteOperation(ctx, op, nil, workflow.NexusOperationOptions{})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		err := fut.Get(ctx, nil)
		require.True(t, completedListenerCalled)
		return err
	}

	service := nexus.NewService("test")
	service.Register(op)

	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(handlerWf)
	env.RegisterWorkflow(callerWf)
	env.RegisterNexusService(service)

	env.SetOnNexusOperationStartedListener(
		func(service, operation string, input converter.EncodedValue) {
			startedListenerCalled = true
		},
	)
	env.SetOnNexusOperationCompletedListener(
		func(service, operation string, result converter.EncodedValue, err error) {
			completedListenerCalled = true
		},
	)

	env.ExecuteWorkflow(callerWf)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, startedListenerCalled)
	require.True(t, completedListenerCalled)
}

type workerInterceptor struct {
	interceptor.WorkerInterceptorBase
	logs []string // Store logs from the nexus interceptor when it pretends to be a logger.
}

type workflowInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	interceptor.WorkflowOutboundInterceptorBase
}

type nexusInterceptor struct {
	interceptor.NexusOperationInboundInterceptorBase
	interceptor.NexusOperationOutboundInterceptorBase
	log.Logger // Also pretend to be a logger, we'll only implement Info to verify outbound interception.
	parent     *workerInterceptor
}

func (i *workerInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	return &workflowInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{
			Next: next,
		},
	}
}

func (i *workerInterceptor) InterceptNexusOperation(
	ctx context.Context,
	next interceptor.NexusOperationInboundInterceptor,
) interceptor.NexusOperationInboundInterceptor {
	return &nexusInterceptor{
		NexusOperationInboundInterceptorBase: interceptor.NexusOperationInboundInterceptorBase{
			Next: next,
		},
		parent: i,
	}
}

func (i *workflowInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	i.WorkflowOutboundInterceptorBase.Next = outbound
	return i.WorkflowInboundInterceptorBase.Next.Init(i)
}

func (i *workflowInterceptor) ExecuteNexusOperation(
	ctx workflow.Context,
	input interceptor.ExecuteNexusOperationInput,
) workflow.NexusOperationFuture {
	input.NexusHeader["test"] = "present"
	return i.WorkflowOutboundInterceptorBase.Next.ExecuteNexusOperation(ctx, input)
}

func (i *nexusInterceptor) Init(ctx context.Context, outbound interceptor.NexusOperationOutboundInterceptor) error {
	i.NexusOperationOutboundInterceptorBase.Next = outbound
	info := nexus.ExtractHandlerInfo(ctx)
	if h := info.Header.Get("test"); h != "present" {
		return nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, `expected "test" header to be "present", got: %q`, h)
	}
	// Set for verification by the StartOperation interceptor method.
	info.Header.Set("init", "done")
	return i.NexusOperationInboundInterceptorBase.Next.Init(ctx, i)
}

func (i *nexusInterceptor) StartOperation(ctx context.Context, input interceptor.NexusStartOperationInput) (nexus.HandlerStartOperationResult[any], error) {
	if h := input.Options.Header.Get("init"); h != "done" {
		return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, `expected "init" header to be "done", got: %q`, h)
	}
	if in, ok := input.Input.(string); !ok || in != "input" {
		return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, `expected input to be a string with value "input", got: string? (%v) %q`, ok, in)
	}
	// Set for verification by the StartOperation handler method.
	input.Options.Header.Set("start", "done")
	return i.NexusOperationInboundInterceptorBase.Next.StartOperation(ctx, input)
}

func (i *nexusInterceptor) GetLogger(ctx context.Context) log.Logger {
	return i
}

// Info implements log.Logger.
func (i *nexusInterceptor) Info(msg string, keyvals ...interface{}) {
	i.parent.logs = append(i.parent.logs, msg)
}

func TestInterceptors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	tc := newTestContext(t, ctx)

	op := nexus.NewSyncOperation("op", func(ctx context.Context, input string, opts nexus.StartOperationOptions) (string, error) {
		if h := opts.Header.Get("start"); h != "done" {
			return "", nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, `expected "start" header to be "done", got: %q`, h)
		}
		temporalnexus.GetLogger(ctx).Info("logged")
		return input, nil
	})

	wf := func(ctx workflow.Context) error {
		c := workflow.NewNexusClient(tc.endpoint, "test")
		fut := c.ExecuteOperation(ctx, op, "input", workflow.NexusOperationOptions{})
		var res string

		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return fmt.Errorf("expected start to succeed: %w", err)
		}
		if exec.OperationToken != "" {
			return fmt.Errorf("expected empty operation token")
		}
		if err := fut.Get(ctx, &res); err != nil {
			return err
		}
		// If the operation didn't fail, the interceptors injected and verified the headers, the result should be an echo of the input provided.
		if res != "input" {
			return fmt.Errorf("unexpected result: %v", res)
		}
		return nil
	}

	service := nexus.NewService("test")
	service.MustRegister(op)

	t.Run("RealServer", func(t *testing.T) {
		i := &workerInterceptor{}
		w := worker.New(tc.client, tc.taskQueue, worker.Options{
			Interceptors: []interceptor.WorkerInterceptor{i},
		})
		w.RegisterNexusService(service)
		w.RegisterWorkflow(wf)
		require.NoError(t, w.Start())
		t.Cleanup(w.Stop)

		run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue: tc.taskQueue,
			// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
			// timeout to speed up the attempts.
			WorkflowTaskTimeout: time.Second,
		}, wf)
		require.NoError(t, err)
		require.NoError(t, run.Get(ctx, nil))
		require.Equal(t, []string{"logged"}, i.logs)
	})

	t.Run("TestEnv", func(t *testing.T) {
		suite := testsuite.WorkflowTestSuite{}
		env := suite.NewTestWorkflowEnvironment()
		env.SetWorkerOptions(worker.Options{
			Interceptors: []interceptor.WorkerInterceptor{
				&workerInterceptor{},
			},
		})
		env.RegisterNexusService(service)
		env.RegisterWorkflow(wf)

		env.ExecuteWorkflow(wf)
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
	})
}

type opentracingTracer struct {
	interceptor.Tracer
	mock *mocktracer.MockTracer
}

func (t *opentracingTracer) FinishedSpans() []*interceptortest.SpanInfo {
	return t.spanChildren(t.mock.FinishedSpans(), 0)
}

func (t *opentracingTracer) spanChildren(spans []*mocktracer.MockSpan, parentID int) (ret []*interceptortest.SpanInfo) {
	for _, s := range spans {
		if s.ParentID == parentID {
			ret = append(ret, interceptortest.Span(s.OperationName, t.spanChildren(spans, s.SpanContext.SpanID)...))
		}
	}
	return
}

type otelTracer struct {
	interceptor.Tracer
	rec *tracetest.SpanRecorder
}

func (t *otelTracer) FinishedSpans() []*interceptortest.SpanInfo {
	return t.spanChildren(t.rec.Ended(), trace.SpanID{})
}

func (t *otelTracer) spanChildren(spans []sdktrace.ReadOnlySpan, parentID trace.SpanID) (ret []*interceptortest.SpanInfo) {
	for _, s := range spans {
		if s.Parent().SpanID() == parentID {
			ret = append(ret, interceptortest.Span(s.Name(), t.spanChildren(spans, s.SpanContext().SpanID())...))
		}
	}
	return
}

func TestNexusTracingInterceptor(t *testing.T) {
	t.Skip("this test is flaky in CI and needs to be restructured")
	cases := []struct {
		name   string
		tracer func(t *testing.T) interceptortest.TestTracer
	}{
		{
			name: "OTel",
			tracer: func(t *testing.T) interceptortest.TestTracer {
				rec := tracetest.NewSpanRecorder()
				tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
					Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer(""),
				})
				require.NoError(t, err)
				return &otelTracer{tracer, rec}
			},
		},
		{
			name: "OpenTracing",
			tracer: func(t *testing.T) interceptortest.TestTracer {
				mock := mocktracer.New()
				tracer, err := opentracing.NewTracer(opentracing.TracerOptions{Tracer: mock})
				require.NoError(t, err)

				return &opentracingTracer{Tracer: tracer, mock: mock}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
			defer cancel()
			tracer := tc.tracer(t)
			tc := newTestContext(t, ctx, withClientInterceptors(interceptor.NewTracingInterceptor(tracer)))
			workflowID := "nexus-handler-workflow-" + uuid.NewString()

			wf := func(ctx workflow.Context) error {
				c := workflow.NewNexusClient(tc.endpoint, "test")
				opCtx, cancel := workflow.WithCancel(ctx)
				defer cancel()
				fut := c.ExecuteOperation(opCtx, workflowOp, workflowID, workflow.NexusOperationOptions{})
				if err := fut.GetNexusOperationExecution().Get(ctx, nil); err != nil {
					return fmt.Errorf("failed starting nexus operation: %w", err)
				}
				cancel()
				if err := fut.Get(ctx, nil); err == nil || !errors.As(err, new(*temporal.CanceledError)) {
					return fmt.Errorf("expected nexus operation to fail with a canceled error, got: %w", err)
				}
				return nil
			}

			w := worker.New(tc.client, tc.taskQueue, worker.Options{})
			service := nexus.NewService("test")
			service.MustRegister(workflowOp)
			w.RegisterNexusService(service)
			w.RegisterWorkflowWithOptions(wf, workflow.RegisterOptions{Name: "caller"})
			w.RegisterWorkflow(waitForCancelWorkflow)
			require.NoError(t, w.Start())
			t.Cleanup(w.Stop)

			run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				TaskQueue: tc.taskQueue,
				// The endpoint registry may take a bit to propagate to the history service, use a shorter workflow task
				// timeout to speed up the attempts.
				WorkflowTaskTimeout: time.Second,
			}, "caller")
			require.NoError(t, err)
			require.NoError(t, run.Get(ctx, nil))

			require.Equal(t, []*interceptortest.SpanInfo{
				interceptortest.Span("StartWorkflow:caller",
					interceptortest.Span("RunWorkflow:caller",
						interceptortest.Span("StartNexusOperation:test/workflow-op",
							interceptortest.Span("RunStartNexusOperationHandler:test/workflow-op",
								interceptortest.Span("StartWorkflow:waitForCancelWorkflow",
									interceptortest.Span("RunWorkflow:waitForCancelWorkflow")))))),
				// Note that the span is not attached since the server as of 1.27 does not propagate headers to the cancel
				// request.  This assertion will have to change once the server fixes this behavior. It's left here as a
				// reminder.
				interceptortest.Span("RunCancelNexusOperationHandler:test/workflow-op"),
			}, tracer.FinishedSpans())
		})
	}
}
