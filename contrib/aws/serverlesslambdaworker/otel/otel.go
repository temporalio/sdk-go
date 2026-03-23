// Package otel provides convenience helpers for configuring OpenTelemetry
// metrics and tracing on a Temporal client running inside AWS Lambda.
//
// Use [ApplyDefaults] inside a [serverlesslambdaworker.ConfigureWorkerContext.MutateClientOptions]
// callback for a batteries-included setup that creates OTLP gRPC exporters and an AWS X-Ray ID
// generator, suitable for use with the AWS Distro for OpenTelemetry (ADOT) Lambda layer.
//
// Use [ApplyDefaultsWithProviders] if you need to supply your own MeterProvider and TracerProvider.
package otel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelsdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	otelsdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

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
}

// ApplyDefaults configures OTel metrics and tracing on the given client options using AWS Lambda
// defaults. It creates OTLP gRPC exporters (insecure, defaulting to the localhost:4317 endpoint
// expected by the ADOT collector Lambda layer) and an AWS X-Ray compatible trace ID generator.
//
// The collector endpoint and service name can be set via [Options], or fall back to environment
// variables (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SERVICE_NAME / AWS_LAMBDA_FUNCTION_NAME).
//
// The returned [ShutdownFunc] flushes pending metrics and traces, then shuts down the providers.
// Register it with [serverlesslambdaworker.ConfigureWorkerContext.OnShutdown] to ensure telemetry
// is exported before each Lambda invocation completes.
//
// Call this from a [serverlesslambdaworker.ConfigureWorkerContext.MutateClientOptions] callback.
// If you need more control, see [ApplyDefaultsWithProviders].
func ApplyDefaults(opts *client.Options, options Options) (func(ctx context.Context) error, error) {
	if options.MetricExportInterval == 0 {
		options.MetricExportInterval = 10 * time.Second
	}
	if options.ServiceName == "" {
		options.ServiceName = os.Getenv("OTEL_SERVICE_NAME")
	}
	if options.ServiceName == "" {
		options.ServiceName = os.Getenv("AWS_LAMBDA_FUNCTION_NAME")
	}
	if options.ServiceName == "" {
		options.ServiceName = "temporal-lambda-worker"
	}
	if options.CollectorEndpoint == "" {
		options.CollectorEndpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}

	var grpcOpts []otlpmetricgrpc.Option
	grpcOpts = append(grpcOpts, otlpmetricgrpc.WithInsecure())
	var traceGrpcOpts []otlptracegrpc.Option
	traceGrpcOpts = append(traceGrpcOpts, otlptracegrpc.WithInsecure())
	if options.CollectorEndpoint != "" {
		grpcOpts = append(grpcOpts, otlpmetricgrpc.WithEndpoint(options.CollectorEndpoint))
		traceGrpcOpts = append(traceGrpcOpts, otlptracegrpc.WithEndpoint(options.CollectorEndpoint))
	}

	metricExporter, err := otlpmetricgrpc.New(context.Background(), grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP metric exporter: %w", err)
	}
	meterProvider := otelsdkmetric.NewMeterProvider(
		otelsdkmetric.WithReader(otelsdkmetric.NewPeriodicReader(metricExporter,
			otelsdkmetric.WithInterval(options.MetricExportInterval))))

	traceExporter, err := otlptracegrpc.New(context.Background(), traceGrpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP trace exporter: %w", err)
	}
	serviceName := options.ServiceName
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(serviceName))
	tracerProvider := otelsdktrace.NewTracerProvider(
		otelsdktrace.WithBatcher(traceExporter),
		otelsdktrace.WithIDGenerator(xray.NewIDGenerator()),
		otelsdktrace.WithResource(res))

	if err := ApplyDefaultsWithProviders(opts, meterProvider, tracerProvider); err != nil {
		return nil, err
	}

	shutdown := func(ctx context.Context) error {
		return errors.Join(
			meterProvider.ForceFlush(ctx),
			tracerProvider.ForceFlush(ctx),
			meterProvider.Shutdown(ctx),
			tracerProvider.Shutdown(ctx),
		)
	}
	return shutdown, nil
}

// ApplyDefaultsWithProviders configures OTel metrics and tracing on the given client options using
// the provided MeterProvider and TracerProvider. Use this instead of [ApplyDefaults] when you need
// full control over the OTel provider configuration.
//
// Call this from a [serverlesslambdaworker.ConfigureWorkerContext.MutateClientOptions] callback.
func ApplyDefaultsWithProviders(
	opts *client.Options,
	meterProvider *otelsdkmetric.MeterProvider,
	tracerProvider *otelsdktrace.TracerProvider,
) error {
	ApplyMetrics(opts, meterProvider)
	return ApplyTracing(opts, tracerProvider)
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
func ApplyTracing(opts *client.Options, tracerProvider *otelsdktrace.TracerProvider) error {
	tracingInterceptor, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{
		Tracer: tracerProvider.Tracer("temporal-sdk"),
	})
	if err != nil {
		return err
	}
	opts.Interceptors = append(opts.Interceptors, tracingInterceptor)
	return nil
}

// Verify interface compliance at compile time.
var _ interceptor.Interceptor = (interceptor.Interceptor)(nil)
