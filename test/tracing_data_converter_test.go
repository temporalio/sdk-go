package test_test

import (
	"context"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func codecTracingActivity(context.Context) error { return nil }

func codecTracingWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	return workflow.ExecuteActivity(ctx, codecTracingActivity).Get(ctx, nil)
}

func (ts *IntegrationTestSuite) TestOpenTelemetryTracingDataConverter() {
	ts.testOpenTelemetryTracingDataConverter(true)
}

func (ts *IntegrationTestSuite) TestOpenTelemetryTracingDataConverterDisabled() {
	ts.testOpenTelemetryTracingDataConverter(false)
}

func (ts *IntegrationTestSuite) testOpenTelemetryTracingDataConverter(useDataConverter bool) {
	var recorder tracetest.SpanRecorder
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(&recorder)).Tracer("codec-tracing")
	tracing, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{
		Tracer: tracer, UseDataConverter: useDataConverter,
	})
	ts.Require().NoError(err)
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = dc
		options.Interceptors = []interceptor.ClientInterceptor{tracing}
	})
	ts.Require().NoError(err)
	defer c.Close()

	taskQueue := "codec-tracing-" + ts.T().Name()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(codecTracingWorkflow)
	w.RegisterActivity(codecTracingActivity)
	ts.Require().NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	ctx, root := tracer.Start(ctx, "root")
	defer root.End()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: taskQueue, TaskQueue: taskQueue,
	}, codecTracingWorkflow)
	ts.Require().NoError(err)
	ts.Require().NoError(run.Get(ctx, nil))

	actual := interceptortest.Span("root")
	ts.addOpenTelemetryChildren(root.SpanContext().SpanID(), actual, recorder.Ended())
	ts.Equal(interceptortest.Span("root",
		interceptortest.Span("StartWorkflow:codecTracingWorkflow",
			interceptortest.Span("RunWorkflow:codecTracingWorkflow",
				interceptortest.Span("StartActivity:codecTracingActivity",
					interceptortest.Span("RunActivity:codecTracingActivity"))))), actual)

	history := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var events []*historypb.HistoryEvent
	var headers int
	for history.HasNext() {
		event, err := history.Next()
		ts.Require().NoError(err)
		events = append(events, event)
		var header *commonpb.Header
		switch {
		case event.GetWorkflowExecutionStartedEventAttributes() != nil:
			header = event.GetWorkflowExecutionStartedEventAttributes().GetHeader()
		case event.GetActivityTaskScheduledEventAttributes() != nil:
			header = event.GetActivityTaskScheduledEventAttributes().GetHeader()
		default:
			continue
		}
		payload := header.GetFields()["_tracer-data"]
		ts.Require().NotNil(payload)
		headerDC := converter.GetDefaultDataConverter()
		if useDataConverter {
			ts.Equal("binary/zlib", string(payload.Metadata[converter.MetadataEncoding]))
			headerDC = dc
		} else {
			ts.Equal("json/plain", string(payload.Metadata[converter.MetadataEncoding]))
		}
		var data map[string]string
		ts.Require().NoError(headerDC.FromPayload(payload, &data))
		ts.Contains(data["traceparent"], root.SpanContext().TraceID().String())
		headers++
	}
	ts.Equal(2, headers, "both client and workflow trace headers must honor the converter option")

	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		DataConverter: dc,
		Interceptors:  []interceptor.WorkerInterceptor{tracing},
	})
	ts.Require().NoError(err)
	replayer.RegisterWorkflow(codecTracingWorkflow)
	ts.Require().NoError(replayer.ReplayWorkflowHistoryWithOptions(nil, &historypb.History{Events: events},
		worker.ReplayWorkflowHistoryOptions{OriginalExecution: workflow.Execution{ID: run.GetID()}}))
}
