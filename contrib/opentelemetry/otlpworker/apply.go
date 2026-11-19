package otlpworker

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
)

// Apply installs both the OpenTelemetry metrics handler and tracing interceptor
// on the given client options, using the supplied providers. It is equivalent to
// calling [ApplyMetrics] followed by [ApplyTracing].
func Apply(opts *client.Options, mp metric.MeterProvider, tp trace.TracerProvider) error {
	ApplyMetrics(opts, mp)
	return ApplyTracing(opts, tp)
}

// ApplyMetrics sets the go.temporal.io/sdk/contrib/opentelemetry metrics handler
// on the given client options using a meter from the supplied provider.
func ApplyMetrics(opts *client.Options, mp metric.MeterProvider) {
	opts.MetricsHandler = temporalotel.NewMetricsHandler(temporalotel.MetricsHandlerOptions{
		Meter: mp.Meter(InstrumentationScopeName),
	})
}

// ApplyTracing appends the go.temporal.io/sdk/contrib/opentelemetry tracing
// interceptor to the given client options using a tracer from the supplied
// provider.
func ApplyTracing(opts *client.Options, tp trace.TracerProvider) error {
	tracingInterceptor, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{
		Tracer: tp.Tracer(InstrumentationScopeName),
	})
	if err != nil {
		return err
	}
	opts.Interceptors = append(opts.Interceptors, tracingInterceptor)
	return nil
}
