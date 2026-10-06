// Package otel provides convenience helpers for configuring OpenTelemetry
// metrics and tracing on a Temporal client running inside AWS Lambda.
//
// Use [ApplyDefaults] inside a [lambdaworker.RunWorker] configure callback for a
// batteries-included setup that creates OTLP gRPC exporters and an AWS X-Ray ID
// generator, suitable for use with the AWS Distro for OpenTelemetry (ADOT) Lambda layer.
//
// Use [ApplyDefaultsWithProviders] if you need to supply your own MeterProvider and TracerProvider.
//
// The provider-neutral OpenTelemetry glue used here lives in
// go.temporal.io/sdk/contrib/opentelemetry/otlpworker; this package layers the
// AWS Lambda specific policy (X-Ray trace IDs, AWS_LAMBDA_FUNCTION_NAME service
// resolution, host:port insecure OTLP endpoint, per-invocation flushing) on top.
package otel

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelsdkmetric "go.opentelemetry.io/otel/sdk/metric"
	otelsdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry/otlpworker"
)

// ShutdownRegistrar accepts a function to be called at the end of each Lambda invocation.
// [lambdaworker.Options] implements this interface.
type ShutdownRegistrar interface {
	OnShutdown(func(context.Context) error)
}

// Options configures the behavior of [ApplyDefaults].
type Options struct {
	// MetricExportInterval controls how often metrics are exported. Defaults to 10 seconds.
	MetricExportInterval time.Duration

	// ServiceName sets the OTel service name resource attribute. If empty, defaults to the
	// OTEL_SERVICE_NAME environment variable, then AWS_LAMBDA_FUNCTION_NAME, then
	// "temporal-lambda-worker".
	ServiceName string

	// CollectorEndpoint sets the OTLP gRPC collector endpoint (e.g. "localhost:4317").
	// If empty, defaults to the OTEL_EXPORTER_OTLP_ENDPOINT environment variable, then
	// "localhost:4317".
	CollectorEndpoint string

	// MetricExporterOptions are additional options passed to the OTLP gRPC metric exporter.
	// By default [otlpmetricgrpc.WithInsecure] is prepended; set this to override that
	// default (e.g. to use TLS).
	MetricExporterOptions []otlpmetricgrpc.Option

	// TraceExporterOptions are additional options passed to the OTLP gRPC trace exporter.
	// By default [otlptracegrpc.WithInsecure] is prepended; set this to override that
	// default (e.g. to use TLS).
	TraceExporterOptions []otlptracegrpc.Option
}

// ApplyDefaults configures OTel metrics and tracing on the given client options using AWS Lambda
// defaults. It creates OTLP gRPC exporters (insecure, defaulting to the localhost:4317 endpoint
// expected by the ADOT collector Lambda layer) and an AWS X-Ray compatible trace ID generator.
//
// The collector endpoint and service name can be set via [Options], or fall back to environment
// variables (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SERVICE_NAME / AWS_LAMBDA_FUNCTION_NAME).
//
// ApplyDefaults registers a per-invocation ForceFlush hook on the given [ShutdownRegistrar] so
// that pending metrics and traces are exported before each Lambda invocation completes. It calls
// only ForceFlush (not Shutdown) so the providers remain usable across warm-start invocations.
// Permanent provider shutdown is unnecessary in Lambda since the runtime terminates the process.
//
// Call this from a [lambdaworker.RunWorker] configure callback, passing the
// [lambdaworker.Options] as the [ShutdownRegistrar].
// If you need more control, see [ApplyDefaultsWithProviders].
func ApplyDefaults(
	ctx ShutdownRegistrar, opts *client.Options, options Options,
) error {
	metricExportInterval := options.MetricExportInterval
	if metricExportInterval == 0 {
		metricExportInterval = 10 * time.Second
	}
	serviceName := resolveServiceName(options)
	endpoint := resolveEndpoint(options)

	// Build plugin-owned providers. otlpworker builds a shared resource so metrics and traces
	// carry the same service.name, uses an insecure host:port OTLP endpoint, an X-Ray compatible
	// trace ID generator, a metrics PeriodicReader (no batch), and a trace batch processor. If
	// trace-exporter creation fails, it shuts the meter provider down before returning.
	meterProvider, tracerProvider, err := otlpworker.NewProviders(context.Background(), otlpworker.Config{
		ServiceName:           serviceName,
		Endpoint:              endpoint,
		EndpointMode:          otlpworker.EndpointHostPort,
		Insecure:              true,
		MetricExportInterval:  metricExportInterval,
		MetricExporterOptions: options.MetricExporterOptions,
		TraceExporterOptions:  options.TraceExporterOptions,
		TraceIDGenerator:      xray.NewIDGenerator(),
	})
	if err != nil {
		return err
	}

	// If ApplyDefaultsWithProviders fails, shut down both providers to stop the periodic reader
	// goroutine and release the underlying gRPC connections. Use Background — the invocation
	// context may already be cancelled.
	success := false
	defer func() {
		if !success {
			_ = otlpworker.Shutdown(context.Background(), meterProvider, tracerProvider)
		}
	}()

	if err := ApplyDefaultsWithProviders(ctx, opts, meterProvider, tracerProvider); err != nil {
		return err
	}

	success = true
	return nil
}

// ApplyDefaultsWithProviders configures OTel metrics and tracing on the given client options using
// the provided MeterProvider and TracerProvider. It registers a per-invocation ForceFlush hook on
// the given [ShutdownRegistrar]. Use this instead of [ApplyDefaults] when you need full control
// over the OTel provider configuration.
//
// Call this from a [lambdaworker.RunWorker] configure callback, passing the
// [lambdaworker.Options] as the [ShutdownRegistrar].
func ApplyDefaultsWithProviders(
	ctx ShutdownRegistrar,
	opts *client.Options,
	meterProvider *otelsdkmetric.MeterProvider,
	tracerProvider *otelsdktrace.TracerProvider,
) error {
	ApplyMetrics(opts, meterProvider)
	if err := ApplyTracing(opts, tracerProvider); err != nil {
		return err
	}
	ctx.OnShutdown(func(flushCtx context.Context) error {
		return otlpworker.ForceFlush(flushCtx, meterProvider, tracerProvider)
	})
	return nil
}

// ApplyMetrics configures only OTel metrics (no tracing) on the given client
// options.
func ApplyMetrics(opts *client.Options, meterProvider *otelsdkmetric.MeterProvider) {
	otlpworker.ApplyMetrics(opts, meterProvider)
}

// ApplyTracing configures only OTel tracing (no metrics) on the given client
// options.
func ApplyTracing(opts *client.Options, tracerProvider *otelsdktrace.TracerProvider) error {
	return otlpworker.ApplyTracing(opts, tracerProvider)
}

// resolveServiceName resolves the OTel service name from Options and the
// environment: explicit Options.ServiceName, then OTEL_SERVICE_NAME, then
// AWS_LAMBDA_FUNCTION_NAME, then "temporal-lambda-worker".
func resolveServiceName(options Options) string {
	return otlpworker.FirstNonEmptyEnv(
		options.ServiceName, os.Getenv,
		[]string{"OTEL_SERVICE_NAME", "AWS_LAMBDA_FUNCTION_NAME"},
		"temporal-lambda-worker",
	)
}

// resolveEndpoint resolves the OTLP collector endpoint from Options and the
// environment: explicit Options.CollectorEndpoint, then OTEL_EXPORTER_OTLP_ENDPOINT,
// then "" — an empty endpoint leaves the OTLP exporter default (localhost:4317)
// in place.
func resolveEndpoint(options Options) string {
	return otlpworker.FirstNonEmptyEnv(
		options.CollectorEndpoint, os.Getenv,
		[]string{"OTEL_EXPORTER_OTLP_ENDPOINT"},
		"",
	)
}
