package otlpworker

import (
	"testing"

	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

func TestApplyMetrics(t *testing.T) {
	opts := &client.Options{}
	ApplyMetrics(opts, metricnoop.NewMeterProvider())
	require.IsType(t, temporalotel.MetricsHandler{}, opts.MetricsHandler)
}

func TestApplyTracing(t *testing.T) {
	existing := &interceptor.InterceptorBase{}
	opts := &client.Options{Interceptors: []interceptor.ClientInterceptor{existing}}

	require.NoError(t, ApplyTracing(opts, tracenoop.NewTracerProvider()))
	require.Len(t, opts.Interceptors, 2)
	require.Same(t, existing, opts.Interceptors[0])
	_, isWorkerInterceptor := opts.Interceptors[1].(interceptor.WorkerInterceptor)
	require.True(t, isWorkerInterceptor)
}

func TestApplyInstallsBoth(t *testing.T) {
	opts := &client.Options{}
	require.NoError(t, Apply(opts, metricnoop.NewMeterProvider(), tracenoop.NewTracerProvider()))
	require.IsType(t, temporalotel.MetricsHandler{}, opts.MetricsHandler)
	require.Len(t, opts.Interceptors, 1)
}
