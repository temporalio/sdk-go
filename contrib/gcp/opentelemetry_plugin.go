// Package gcp provides integrations for running Temporal workers on Google Cloud.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	metricSDK "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	traceSDK "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/worker"
)

const (
	// OpenTelemetryPluginName is the name reported for the plugin.
	OpenTelemetryPluginName = "go.temporal.io/sdk/contrib/opentelemetry"

	// OTLPExporterEndpointEnvVar is the standard OpenTelemetry environment variable for the
	// common OTLP exporter endpoint.
	OTLPExporterEndpointEnvVar = "OTEL_EXPORTER_OTLP_ENDPOINT"
	// OTELServiceNameEnvVar is the standard OpenTelemetry environment variable for service.name.
	OTELServiceNameEnvVar = "OTEL_SERVICE_NAME"
	// CloudRunWorkerPoolEnvVar contains the name of the current Cloud Run worker pool.
	CloudRunWorkerPoolEnvVar = "CLOUD_RUN_WORKER_POOL"
	// CloudRunServiceEnvVar contains the name of the current Cloud Run service.
	CloudRunServiceEnvVar = "K_SERVICE"

	// DefaultOTLPEndpoint is the local OTLP gRPC collector endpoint used by the plugin.
	DefaultOTLPEndpoint = "http://localhost:4317"
	// DefaultServiceName is used when no explicit or Cloud Run service name is available.
	DefaultServiceName = "temporal-worker"

	defaultMetricExportInterval = time.Second
	defaultFlushTimeout         = 10 * time.Second
	instrumentationScopeName    = "temporal-sdk"
)

// OpenTelemetryPluginOptions configures [NewOpenTelemetryPlugin].
type OpenTelemetryPluginOptions struct {
	// Endpoint is the OTLP gRPC endpoint used when the plugin creates providers. It must be a URL.
	// If empty, OTEL_EXPORTER_OTLP_ENDPOINT is used, followed by [DefaultOTLPEndpoint].
	Endpoint string

	// ServiceName is the OpenTelemetry service.name resource attribute. If empty, the plugin uses
	// OTEL_SERVICE_NAME, CLOUD_RUN_WORKER_POOL, K_SERVICE, then [DefaultServiceName].
	ServiceName string

	// MetricExportInterval controls how often metrics are exported. Defaults to one second.
	MetricExportInterval time.Duration

	// MeterProvider and TracerProvider supply application-owned providers. Set both or neither.
	// When set, the plugin does not create exporters and does not shut down the providers. Providers
	// that implement ForceFlush(context.Context) error are force-flushed unless FlushHook is set.
	MeterProvider  metric.MeterProvider
	TracerProvider trace.TracerProvider

	// FlushHook overrides provider force-flushing. It is used by ForceFlush, by Shutdown for
	// application-owned providers, and by the optional worker-stop flush.
	FlushHook func(context.Context) error

	// FlushOnWorkerStop force-flushes after an individual worker has stopped. It is disabled by
	// default because one client can own multiple workers; Cloud Run applications should normally
	// stop every worker and then call Shutdown once.
	FlushOnWorkerStop bool

	// FlushTimeout limits an automatic worker-stop flush. Defaults to ten seconds. It does not
	// affect ForceFlush or Shutdown, whose callers supply their own context deadlines.
	FlushTimeout time.Duration
}

// OpenTelemetryPlugin configures OpenTelemetry metrics and tracing for Temporal clients and
// workers running on Google Cloud Run. The default configuration exports OTLP gRPC telemetry to a
// collector on localhost; it does not export directly to Google Cloud.
//
// The same plugin may be used by multiple clients and workers. Call [OpenTelemetryPlugin.Shutdown]
// after all workers and clients are stopped to flush telemetry and release plugin-owned providers.
type OpenTelemetryPlugin struct {
	clientPluginBase
	workerPluginBase

	endpoint          string
	serviceName       string
	meterProvider     metric.MeterProvider
	tracerProvider    trace.TracerProvider
	forceFlush        func(context.Context) error
	shutdown          func(context.Context) error
	flushOnWorkerStop bool
	flushTimeout      time.Duration
}

type clientPluginBase struct{ client.PluginBase }
type workerPluginBase struct{ worker.PluginBase }

var _ client.Plugin = (*OpenTelemetryPlugin)(nil)
var _ worker.Plugin = (*OpenTelemetryPlugin)(nil)

// NewOpenTelemetryPlugin creates an OpenTelemetry plugin with Google Cloud Run defaults.
//
// If MeterProvider and TracerProvider are not supplied, this creates OTLP gRPC metric and trace
// exporters and providers owned by the plugin. The collector should perform GCP resource detection
// and export telemetry to Google Cloud.
func NewOpenTelemetryPlugin(
	ctx context.Context,
	options OpenTelemetryPluginOptions,
) (*OpenTelemetryPlugin, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if options.MetricExportInterval < 0 {
		return nil, fmt.Errorf("metric export interval must not be negative")
	}
	if options.FlushTimeout < 0 {
		return nil, fmt.Errorf("flush timeout must not be negative")
	}
	if (options.MeterProvider == nil) != (options.TracerProvider == nil) {
		return nil, fmt.Errorf("meter provider and tracer provider must be set together")
	}

	metricExportInterval := options.MetricExportInterval
	if metricExportInterval == 0 {
		metricExportInterval = defaultMetricExportInterval
	}
	flushTimeout := options.FlushTimeout
	if flushTimeout == 0 {
		flushTimeout = defaultFlushTimeout
	}

	endpoint := resolveEndpoint(options.Endpoint, os.Getenv)
	serviceName := resolveServiceName(options.ServiceName, os.Getenv)
	meterProvider := options.MeterProvider
	tracerProvider := options.TracerProvider
	providersOwned := meterProvider == nil

	if providersOwned {
		var err error
		meterProvider, tracerProvider, err = createProviders(
			ctx,
			endpoint,
			serviceName,
			metricExportInterval,
		)
		if err != nil {
			return nil, err
		}
	}

	forceFlush := options.FlushHook
	if forceFlush == nil {
		forceFlush = func(ctx context.Context) error {
			return runConcurrently(
				ctx,
				func(ctx context.Context) error { return forceFlushProvider(ctx, meterProvider) },
				func(ctx context.Context) error { return forceFlushProvider(ctx, tracerProvider) },
			)
		}
	}

	shutdown := forceFlush
	if providersOwned {
		metricProvider := meterProvider.(*metricSDK.MeterProvider)
		traceProvider := tracerProvider.(*traceSDK.TracerProvider)
		shutdown = func(ctx context.Context) error {
			return runConcurrently(ctx, metricProvider.Shutdown, traceProvider.Shutdown)
		}
	}

	return &OpenTelemetryPlugin{
		endpoint:          endpoint,
		serviceName:       serviceName,
		meterProvider:     meterProvider,
		tracerProvider:    tracerProvider,
		forceFlush:        forceFlush,
		shutdown:          shutdown,
		flushOnWorkerStop: options.FlushOnWorkerStop,
		flushTimeout:      flushTimeout,
	}, nil
}

// Name returns the plugin name.
func (*OpenTelemetryPlugin) Name() string { return OpenTelemetryPluginName }

// Endpoint returns the resolved OTLP endpoint.
func (p *OpenTelemetryPlugin) Endpoint() string { return p.endpoint }

// ServiceName returns the resolved OpenTelemetry service name.
func (p *OpenTelemetryPlugin) ServiceName() string { return p.serviceName }

// MeterProvider returns the provider used for Temporal metrics.
func (p *OpenTelemetryPlugin) MeterProvider() metric.MeterProvider { return p.meterProvider }

// TracerProvider returns the provider used for Temporal traces.
func (p *OpenTelemetryPlugin) TracerProvider() trace.TracerProvider { return p.tracerProvider }

// ForceFlush exports buffered metrics and traces without shutting down the providers.
func (p *OpenTelemetryPlugin) ForceFlush(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return p.forceFlush(ctx)
}

// Shutdown flushes telemetry and shuts down providers created by the plugin. Application-owned
// providers are not shut down; for them, Shutdown only runs FlushHook or ForceFlush on providers
// that support it. Call this after every worker and client using the plugin has stopped.
func (p *OpenTelemetryPlugin) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return p.shutdown(ctx)
}

// ConfigureClient installs the OpenTelemetry metrics handler and tracing interceptor.
func (p *OpenTelemetryPlugin) ConfigureClient(
	_ context.Context,
	options client.PluginConfigureClientOptions,
) error {
	if options.ClientOptions == nil {
		return fmt.Errorf("client options are required")
	}

	tracingInterceptor, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{
		Tracer: p.tracerProvider.Tracer(instrumentationScopeName),
	})
	if err != nil {
		return fmt.Errorf("creating OpenTelemetry tracing interceptor: %w", err)
	}

	options.ClientOptions.MetricsHandler = temporalotel.NewMetricsHandler(
		temporalotel.MetricsHandlerOptions{
			Meter: p.meterProvider.Meter(instrumentationScopeName),
		},
	)
	options.ClientOptions.Interceptors = append(
		options.ClientOptions.Interceptors,
		tracingInterceptor,
	)
	return nil
}

// StopWorker optionally force-flushes after the worker has stopped. Automatic flushing is disabled
// by default; applications should normally call Shutdown after stopping every worker.
func (p *OpenTelemetryPlugin) StopWorker(
	ctx context.Context,
	options worker.PluginStopWorkerOptions,
	next func(context.Context, worker.PluginStopWorkerOptions),
) {
	next(ctx, options)
	if !p.flushOnWorkerStop {
		return
	}

	flushCtx, cancel := context.WithTimeout(context.Background(), p.flushTimeout)
	defer cancel()
	_ = p.ForceFlush(flushCtx)
}

func createProviders(
	ctx context.Context,
	endpoint string,
	serviceName string,
	metricExportInterval time.Duration,
) (*metricSDK.MeterProvider, *traceSDK.TracerProvider, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OpenTelemetry resource: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(endpoint))
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP metric exporter: %w", err)
	}
	metricProvider := metricSDK.NewMeterProvider(
		metricSDK.WithReader(metricSDK.NewPeriodicReader(
			metricExporter,
			metricSDK.WithInterval(metricExportInterval),
		)),
		metricSDK.WithResource(res),
	)

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(endpoint))
	if err != nil {
		_ = metricProvider.Shutdown(context.Background())
		return nil, nil, fmt.Errorf("creating OTLP trace exporter: %w", err)
	}
	traceProvider := traceSDK.NewTracerProvider(
		traceSDK.WithBatcher(traceExporter),
		traceSDK.WithResource(res),
	)
	return metricProvider, traceProvider, nil
}

func forceFlushProvider(ctx context.Context, provider any) error {
	forceFlusher, _ := provider.(interface {
		ForceFlush(context.Context) error
	})
	if forceFlusher == nil {
		return nil
	}
	return forceFlusher.ForceFlush(ctx)
}

func runConcurrently(ctx context.Context, functions ...func(context.Context) error) error {
	results := make(chan error, len(functions))
	for _, function := range functions {
		go func() { results <- function(ctx) }()
	}

	errs := make([]error, 0, len(functions))
	for range functions {
		errs = append(errs, <-results)
	}
	return errors.Join(errs...)
}

func resolveEndpoint(explicit string, getenv func(string) string) string {
	if explicit != "" {
		return explicit
	}
	if endpoint := nonEmptyEnv(getenv, OTLPExporterEndpointEnvVar); endpoint != "" {
		return endpoint
	}
	return DefaultOTLPEndpoint
}

func resolveServiceName(explicit string, getenv func(string) string) string {
	if explicit != "" {
		return explicit
	}
	for _, name := range []string{
		OTELServiceNameEnvVar,
		CloudRunWorkerPoolEnvVar,
		CloudRunServiceEnvVar,
	} {
		if value := nonEmptyEnv(getenv, name); value != "" {
			return value
		}
	}
	return DefaultServiceName
}

func nonEmptyEnv(getenv func(string) string, name string) string {
	value := getenv(name)
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return value
}
