package opentelemetry_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	sdkactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	temporalotel "go.temporal.io/sdk/contrib/opentelemetry-v2"
)

// logLine returns the first captured log line containing the given message.
func logLine(t *testing.T, logger *ilog.MemoryLogger, msg string) string {
	t.Helper()
	for _, line := range logger.Lines() {
		if strings.Contains(line, msg) {
			return line
		}
	}
	require.FailNowf(t, "log entry not captured", "no entry with message %q", msg)
	return ""
}

// spanIDs returns the trace/span IDs of the first recorded span with the given name.
func spanIDs(t *testing.T, recorder *tracetest.SpanRecorder, name string) (traceID, spanID string) {
	t.Helper()
	for _, s := range recorder.Ended() {
		if s.Name() == name {
			return s.SpanContext().TraceID().String(), s.SpanContext().SpanID().String()
		}
	}
	require.FailNowf(t, "span not recorded", "no span named %q", name)
	return "", ""
}

func loggerActivity(ctx context.Context) error {
	sdkactivity.GetLogger(ctx).Info("logging activity")
	return nil
}

func loggerWorkflow(ctx workflow.Context) error {
	workflow.GetLogger(ctx).Info("logging workflow")
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	return workflow.ExecuteActivity(actx, loggerActivity).Get(actx, nil)
}

// customSpanLoggerWorkflow opens a user span and logs inside it. The log must
// carry the custom span's IDs, not the SDK RunWorkflow span's.
func customSpanLoggerWorkflow(ctx workflow.Context) error {
	spanCtx, span := temporalotel.NewTracer(otel.GetTracerProvider(), "app").Start(ctx, "custom")
	defer span.End()

	workflow.GetLogger(spanCtx).Info("logging in custom span")
	return nil
}

func TestGetLoggerTraceFields(t *testing.T) {
	srv := devServer(t)

	newEnv := func(t *testing.T) (*tracetest.SpanRecorder, *ilog.MemoryLogger, client.Client, worker.Worker, string) {
		t.Helper()
		recorder := tracetest.NewSpanRecorder()

		processor := sdktrace.WithSpanProcessor(recorder)
		prev := otel.GetTracerProvider()
		otel.SetTracerProvider(temporalotel.NewTracerProvider(processor))
		t.Cleanup(func() { otel.SetTracerProvider(prev) })

		plugin, shutdown, err := temporalotel.NewPlugin(temporalotel.PluginOptions{
			ProviderOptions: []sdktrace.TracerProviderOption{processor},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = shutdown(context.Background()) })

		logger := ilog.NewMemoryLogger()
		c, err := client.Dial(client.Options{
			HostPort: srv.FrontendHostPort(),
			Logger:   logger,
			Plugins:  []client.Plugin{plugin},
		})
		require.NoError(t, err)
		t.Cleanup(c.Close)

		taskQueue := "otel-logger-" + uuid.NewString()
		w := worker.New(c, taskQueue, worker.Options{})
		t.Cleanup(w.Stop)
		return recorder, logger, c, w, taskQueue
	}

	t.Run("workflow and activity", func(t *testing.T) {
		recorder, logger, c, w, taskQueue := newEnv(t)
		w.RegisterWorkflow(loggerWorkflow)
		w.RegisterActivity(loggerActivity)
		require.NoError(t, w.Start())

		run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
			ID:        "otel-logger-workflow-" + uuid.NewString(),
			TaskQueue: taskQueue,
		}, loggerWorkflow)
		require.NoError(t, err)
		require.NoError(t, run.Get(context.Background(), nil))

		wfLine := logLine(t, logger, "logging workflow")
		wantTrace, wantSpan := spanIDs(t, recorder, "RunWorkflow:loggerWorkflow")
		require.Contains(t, wfLine, wantTrace)
		require.Contains(t, wfLine, wantSpan)

		actLine := logLine(t, logger, "logging activity")
		wantActTrace, wantActSpan := spanIDs(t, recorder, "RunActivity:loggerActivity")
		require.Contains(t, actLine, wantActTrace)
		require.Contains(t, actLine, wantActSpan)
	})

	t.Run("custom span", func(t *testing.T) {
		recorder, logger, c, w, taskQueue := newEnv(t)
		w.RegisterWorkflow(customSpanLoggerWorkflow)
		require.NoError(t, w.Start())

		run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
			ID:        "otel-custom-span-" + uuid.NewString(),
			TaskQueue: taskQueue,
		}, customSpanLoggerWorkflow)
		require.NoError(t, err)
		require.NoError(t, run.Get(context.Background(), nil))

		line := logLine(t, logger, "logging in custom span")
		customTrace, customSpan := spanIDs(t, recorder, "custom")
		require.Contains(t, line, customTrace)
		require.Contains(t, line, customSpan)

		// They share the same active trace ID, but different active span ID.
		sdkTrace, sdkSpan := spanIDs(t, recorder, "RunWorkflow:customSpanLoggerWorkflow")
		require.Contains(t, line, sdkTrace)
		require.NotContains(t, line, sdkSpan)
	})
}
