package gcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
)

func TestOpenTelemetryPluginDefaults(t *testing.T) {
	clearEnvironment(t)

	plugin := newTestPlugin(t, OpenTelemetryPluginOptions{})
	require.Equal(t, DefaultOTLPEndpoint, plugin.Endpoint())
	require.Equal(t, DefaultServiceName, plugin.ServiceName())
	require.Equal(t, OpenTelemetryPluginName, plugin.Name())
	require.Equal(t, 60*time.Second, defaultMetricExportInterval)
}

func TestOpenTelemetryPluginResolutionPrecedence(t *testing.T) {
	clearEnvironment(t)
	t.Setenv(CloudRunServiceEnvVar, "cloud-run-service")
	require.Equal(t, "cloud-run-service", newTestPlugin(t, OpenTelemetryPluginOptions{}).ServiceName())

	t.Setenv(CloudRunWorkerPoolEnvVar, "worker-pool")
	require.Equal(t, "worker-pool", newTestPlugin(t, OpenTelemetryPluginOptions{}).ServiceName())

	t.Setenv(OTELServiceNameEnvVar, "otel-service")
	t.Setenv(OTLPExporterEndpointEnvVar, "http://collector:4317")
	plugin := newTestPlugin(t, OpenTelemetryPluginOptions{})
	require.Equal(t, "otel-service", plugin.ServiceName())
	require.Equal(t, "http://collector:4317", plugin.Endpoint())

	plugin = newTestPlugin(t, OpenTelemetryPluginOptions{
		Endpoint:    "https://explicit-collector:4317",
		ServiceName: "explicit-service",
	})
	require.Equal(t, "explicit-service", plugin.ServiceName())
	require.Equal(t, "https://explicit-collector:4317", plugin.Endpoint())
}

func TestOpenTelemetryPluginIgnoresEmptyEnvironmentValues(t *testing.T) {
	clearEnvironment(t)
	t.Setenv(OTELServiceNameEnvVar, " ")
	t.Setenv(CloudRunWorkerPoolEnvVar, "")
	t.Setenv(CloudRunServiceEnvVar, "cloud-run-service")

	require.Equal(t, "cloud-run-service", newTestPlugin(t, OpenTelemetryPluginOptions{}).ServiceName())
}

func TestOpenTelemetryPluginConfiguresClient(t *testing.T) {
	plugin := newTestPlugin(t, OpenTelemetryPluginOptions{})
	existingInterceptor := &interceptor.InterceptorBase{}
	clientOptions := client.Options{
		Interceptors: []interceptor.ClientInterceptor{existingInterceptor},
	}

	err := plugin.ConfigureClient(context.Background(), client.PluginConfigureClientOptions{
		ClientOptions: &clientOptions,
	})
	require.NoError(t, err)
	require.IsType(t, temporalotel.MetricsHandler{}, clientOptions.MetricsHandler)
	require.Len(t, clientOptions.Interceptors, 2)
	require.Same(t, existingInterceptor, clientOptions.Interceptors[0])
	_, isWorkerInterceptor := clientOptions.Interceptors[1].(interceptor.WorkerInterceptor)
	require.True(t, isWorkerInterceptor)
}

func TestOpenTelemetryPluginForceFlushesProviders(t *testing.T) {
	meterProvider := &forceFlushingMeterProvider{MeterProvider: metricnoop.NewMeterProvider()}
	tracerProvider := &forceFlushingTracerProvider{TracerProvider: tracenoop.NewTracerProvider()}
	plugin, err := NewOpenTelemetryPlugin(context.Background(), OpenTelemetryPluginOptions{
		MeterProvider:  meterProvider,
		TracerProvider: tracerProvider,
	})
	require.NoError(t, err)

	require.NoError(t, plugin.ForceFlush(context.Background()))
	require.Equal(t, 1, meterProvider.flushes)
	require.Equal(t, 1, tracerProvider.flushes)
	require.Same(t, meterProvider, plugin.MeterProvider())
	require.Same(t, tracerProvider, plugin.TracerProvider())
}

func TestOpenTelemetryPluginDefersWorkerStopFlushByDefault(t *testing.T) {
	var calls []string
	plugin := newTestPlugin(t, OpenTelemetryPluginOptions{
		FlushHook: func(context.Context) error {
			calls = append(calls, "flush")
			return nil
		},
	})

	plugin.StopWorker(
		context.Background(),
		worker.PluginStopWorkerOptions{},
		func(context.Context, worker.PluginStopWorkerOptions) { calls = append(calls, "stop") },
	)
	require.Equal(t, []string{"stop"}, calls)

	require.NoError(t, plugin.ForceFlush(context.Background()))
	require.Equal(t, []string{"stop", "flush"}, calls)
}

func TestOpenTelemetryPluginCanFlushOnWorkerStop(t *testing.T) {
	var calls []string
	plugin := newTestPlugin(t, OpenTelemetryPluginOptions{
		FlushOnWorkerStop: true,
		FlushTimeout:      time.Second,
		FlushHook: func(ctx context.Context) error {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			calls = append(calls, "flush")
			return nil
		},
	})

	plugin.StopWorker(
		context.Background(),
		worker.PluginStopWorkerOptions{},
		func(context.Context, worker.PluginStopWorkerOptions) { calls = append(calls, "stop") },
	)
	require.Equal(t, []string{"stop", "flush"}, calls)
}

func TestOpenTelemetryPluginValidatesOptions(t *testing.T) {
	_, err := NewOpenTelemetryPlugin(context.Background(), OpenTelemetryPluginOptions{
		MeterProvider: metricnoop.NewMeterProvider(),
	})
	require.EqualError(t, err, "meter provider and tracer provider must be set together")

	_, err = NewOpenTelemetryPlugin(context.Background(), OpenTelemetryPluginOptions{
		MeterProvider:        metricnoop.NewMeterProvider(),
		TracerProvider:       tracenoop.NewTracerProvider(),
		MetricExportInterval: -time.Second,
	})
	require.EqualError(t, err, "metric export interval must not be negative")

	_, err = NewOpenTelemetryPlugin(context.Background(), OpenTelemetryPluginOptions{
		MeterProvider:  metricnoop.NewMeterProvider(),
		TracerProvider: tracenoop.NewTracerProvider(),
		FlushTimeout:   -time.Second,
	})
	require.EqualError(t, err, "flush timeout must not be negative")

	//lint:ignore SA1012 Verify that the constructor rejects a nil context.
	_, err = NewOpenTelemetryPlugin(nil, OpenTelemetryPluginOptions{})
	require.EqualError(t, err, "context is required")
}

func TestOpenTelemetryPluginFlushHookError(t *testing.T) {
	expectedErr := errors.New("flush failed")
	plugin := newTestPlugin(t, OpenTelemetryPluginOptions{
		FlushHook: func(context.Context) error { return expectedErr },
	})

	require.ErrorIs(t, plugin.ForceFlush(context.Background()), expectedErr)
	require.ErrorIs(t, plugin.Shutdown(context.Background()), expectedErr)
}

func newTestPlugin(t *testing.T, options OpenTelemetryPluginOptions) *OpenTelemetryPlugin {
	t.Helper()
	if options.MeterProvider == nil && options.TracerProvider == nil {
		options.MeterProvider = metricnoop.NewMeterProvider()
		options.TracerProvider = tracenoop.NewTracerProvider()
	}
	plugin, err := NewOpenTelemetryPlugin(context.Background(), options)
	require.NoError(t, err)
	return plugin
}

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		OTLPExporterEndpointEnvVar,
		OTELServiceNameEnvVar,
		CloudRunWorkerPoolEnvVar,
		CloudRunServiceEnvVar,
	} {
		t.Setenv(name, "")
	}
}

type forceFlushingMeterProvider struct {
	metric.MeterProvider
	flushes int
}

func (p *forceFlushingMeterProvider) ForceFlush(context.Context) error {
	p.flushes++
	return nil
}

type forceFlushingTracerProvider struct {
	trace.TracerProvider
	flushes int
}

func (p *forceFlushingTracerProvider) ForceFlush(context.Context) error {
	p.flushes++
	return nil
}
