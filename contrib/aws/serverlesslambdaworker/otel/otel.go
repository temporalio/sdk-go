// Package otel provides a convenience helper for configuring OpenTelemetry
// metrics and tracing on a Temporal client running inside AWS Lambda.
//
// Use [ApplyDefaults] inside a [serverlesslambdaworker.ConfigureWorkerContext.MutateClientOptions]
// callback to set up the OTel metrics handler and tracing interceptor using the
// global OTel providers (typically configured by AWS Distro for OpenTelemetry).
package otel

import (
	otelsdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

// ApplyDefaults configures OTel metrics and tracing on the given client
// options. It uses the provided MeterProvider for metrics and the global OTel
// TracerProvider for tracing.
//
// Call this from a MutateClientOptions callback:
//
//	ctx.MutateClientOptions(func(opts *client.Options) {
//	    if err := otel.ApplyDefaults(opts, meterProvider); err != nil {
//	        // handle error
//	    }
//	})
func ApplyDefaults(opts *client.Options, meterProvider *otelsdkmetric.MeterProvider) error {
	// Set up metrics handler.
	opts.MetricsHandler = temporalotel.NewMetricsHandler(temporalotel.MetricsHandlerOptions{
		Meter: meterProvider.Meter("temporal-sdk"),
	})

	// Set up tracing interceptor.
	tracingInterceptor, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{})
	if err != nil {
		return err
	}
	opts.Interceptors = append(opts.Interceptors, tracingInterceptor)

	return nil
}

// ApplyMetrics configures only OTel metrics (no tracing) on the given client
// options.
func ApplyMetrics(opts *client.Options, meterProvider *otelsdkmetric.MeterProvider) {
	opts.MetricsHandler = temporalotel.NewMetricsHandler(temporalotel.MetricsHandlerOptions{
		Meter: meterProvider.Meter("temporal-sdk"),
	})
}

// ApplyTracing configures only OTel tracing (no metrics) on the given client
// options.
func ApplyTracing(opts *client.Options) error {
	tracingInterceptor, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{})
	if err != nil {
		return err
	}
	opts.Interceptors = append(opts.Interceptors, tracingInterceptor)
	return nil
}

// Verify interface compliance at compile time.
var _ interceptor.Interceptor = (interceptor.Interceptor)(nil)
