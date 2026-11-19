package otel

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry/otlpworker"
	"go.temporal.io/sdk/worker"
)

const (
	// PluginName is the name reported for the plugin.
	PluginName = "temporal-cloudrun-opentelemetry"

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

	// Match the OpenTelemetry SDK default. A shorter interval can cause a collector batch processor
	// to combine multiple cumulative snapshots of the same series into one Google Monitoring write,
	// which Google Managed Service for Prometheus rejects as duplicate time series.
	defaultMetricExportInterval = 60 * time.Second
	defaultFlushTimeout         = 10 * time.Second
)

// PluginOptions configures [NewPlugin].
type PluginOptions struct {
	// Endpoint is the OTLP gRPC endpoint used when the plugin creates providers. It must be a URL.
	// If empty, OTEL_EXPORTER_OTLP_ENDPOINT is used, followed by [DefaultOTLPEndpoint].
	Endpoint string

	// ServiceName is the OpenTelemetry service.name resource attribute. If empty, the plugin uses
	// OTEL_SERVICE_NAME, CLOUD_RUN_WORKER_POOL, K_SERVICE, then [DefaultServiceName].
	ServiceName string

	// MetricExportInterval controls how often metrics are exported. Defaults to 60 seconds.
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

// Plugin configures OpenTelemetry metrics and tracing for Temporal clients and
// workers running on Google Cloud Run. The default configuration exports OTLP gRPC telemetry to a
// collector on localhost; it does not export directly to Google Cloud.
//
// The same plugin may be used by multiple clients and workers. Call [Plugin.Shutdown]
// after all workers and clients are stopped to flush telemetry and release plugin-owned providers.
type Plugin struct {
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

var _ client.Plugin = (*Plugin)(nil)
var _ worker.Plugin = (*Plugin)(nil)

// NewPlugin creates an OpenTelemetry plugin with Google Cloud Run defaults.
//
// If MeterProvider and TracerProvider are not supplied, this creates OTLP gRPC metric and trace
// exporters and providers owned by the plugin. The collector should perform GCP resource detection
// and export telemetry to Google Cloud.
func NewPlugin(
	ctx context.Context,
	options PluginOptions,
) (*Plugin, error) {
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

	endpoint := otlpworker.FirstNonEmptyEnv(
		options.Endpoint, os.Getenv,
		[]string{OTLPExporterEndpointEnvVar},
		DefaultOTLPEndpoint,
	)
	serviceName := otlpworker.FirstNonEmptyEnv(
		options.ServiceName, os.Getenv,
		[]string{OTELServiceNameEnvVar, CloudRunWorkerPoolEnvVar, CloudRunServiceEnvVar},
		DefaultServiceName,
	)
	meterProvider := options.MeterProvider
	tracerProvider := options.TracerProvider
	providersOwned := meterProvider == nil

	if providersOwned {
		mp, tp, err := otlpworker.NewProviders(ctx, otlpworker.Config{
			ServiceName:          serviceName,
			Endpoint:             endpoint,
			EndpointMode:         otlpworker.EndpointURL,
			MetricExportInterval: metricExportInterval,
		})
		if err != nil {
			return nil, err
		}
		meterProvider = mp
		tracerProvider = tp
	}

	forceFlush := options.FlushHook
	if forceFlush == nil {
		forceFlush = func(ctx context.Context) error {
			return otlpworker.ForceFlush(ctx, meterProvider, tracerProvider)
		}
	}

	shutdown := forceFlush
	if providersOwned {
		metricProvider := meterProvider.(*sdkmetric.MeterProvider)
		traceProvider := tracerProvider.(*sdktrace.TracerProvider)
		shutdown = func(ctx context.Context) error {
			return otlpworker.Shutdown(ctx, metricProvider, traceProvider)
		}
	}

	return &Plugin{
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
func (*Plugin) Name() string { return PluginName }

// Endpoint returns the resolved OTLP endpoint.
func (p *Plugin) Endpoint() string { return p.endpoint }

// ServiceName returns the resolved OpenTelemetry service name.
func (p *Plugin) ServiceName() string { return p.serviceName }

// MeterProvider returns the provider used for Temporal metrics.
func (p *Plugin) MeterProvider() metric.MeterProvider { return p.meterProvider }

// TracerProvider returns the provider used for Temporal traces.
func (p *Plugin) TracerProvider() trace.TracerProvider { return p.tracerProvider }

// ForceFlush exports buffered metrics and traces without shutting down the providers.
func (p *Plugin) ForceFlush(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return p.forceFlush(ctx)
}

// Shutdown flushes telemetry and shuts down providers created by the plugin. Application-owned
// providers are not shut down; for them, Shutdown only runs FlushHook or ForceFlush on providers
// that support it. Call this after every worker and client using the plugin has stopped.
func (p *Plugin) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return p.shutdown(ctx)
}

// ConfigureClient installs the OpenTelemetry metrics handler and tracing interceptor.
func (p *Plugin) ConfigureClient(
	_ context.Context,
	options client.PluginConfigureClientOptions,
) error {
	if options.ClientOptions == nil {
		return fmt.Errorf("client options are required")
	}
	if err := otlpworker.Apply(options.ClientOptions, p.meterProvider, p.tracerProvider); err != nil {
		return fmt.Errorf("configuring OpenTelemetry client options: %w", err)
	}
	return nil
}

// StopWorker optionally force-flushes after the worker has stopped. Automatic flushing is disabled
// by default; applications should normally call Shutdown after stopping every worker.
func (p *Plugin) StopWorker(
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
