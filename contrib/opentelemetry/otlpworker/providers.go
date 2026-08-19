package otlpworker

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// EndpointMode selects how [Config.Endpoint] is interpreted when building OTLP
// gRPC exporters.
type EndpointMode int

const (
	// EndpointURL treats Endpoint as a URL and passes it to WithEndpointURL. The
	// URL scheme (http:// versus https://) determines whether the connection is
	// insecure, so [Config.Insecure] is normally left unset in this mode.
	EndpointURL EndpointMode = iota

	// EndpointHostPort treats Endpoint as a host:port and passes it to
	// WithEndpoint. Use [Config.Insecure] to select a plaintext connection. An
	// empty Endpoint leaves the exporter default endpoint in place.
	EndpointHostPort
)

// Config describes how [NewProviders] should build OTLP gRPC metric and trace
// providers. It carries no cloud-specific policy; callers layer that on top.
type Config struct {
	// ServiceName is the OpenTelemetry service.name resource attribute applied to
	// both providers. Callers should resolve it (for example with
	// [FirstNonEmptyEnv]) before constructing the config.
	ServiceName string

	// Endpoint is the OTLP gRPC collector endpoint. Its interpretation depends on
	// EndpointMode.
	Endpoint string

	// EndpointMode selects how Endpoint is applied. The zero value is
	// [EndpointURL].
	EndpointMode EndpointMode

	// Insecure prepends WithInsecure() to both exporters. It is typically used
	// with [EndpointHostPort]; with [EndpointURL] the scheme already selects
	// security.
	Insecure bool

	// MetricExportInterval controls the PeriodicReader export interval. When zero
	// or negative the OpenTelemetry SDK default is used; callers that want a
	// specific default should pass it explicitly.
	MetricExportInterval time.Duration

	// MetricExporterOptions are appended to the OTLP metric exporter options,
	// after any WithInsecure() and before the endpoint option so the endpoint
	// wins.
	MetricExporterOptions []otlpmetricgrpc.Option

	// TraceExporterOptions are appended to the OTLP trace exporter options, after
	// any WithInsecure() and before the endpoint option so the endpoint wins.
	TraceExporterOptions []otlptracegrpc.Option

	// TraceIDGenerator, when set, is installed on the tracer provider (for
	// example an X-Ray compatible generator on AWS Lambda).
	TraceIDGenerator sdktrace.IDGenerator

	// ResourceOptions, when set, are applied to an additional resource that is
	// merged over the default resource and service.name. Later attributes win, so
	// these can override service.name if desired.
	ResourceOptions []resource.Option
}

// NewProviders builds an OTLP gRPC metric provider and trace provider that share
// a resource carrying service.name.
//
// Metrics use a [sdkmetric.PeriodicReader] (no batch processor); traces use a
// batch span processor via WithBatcher. If the trace exporter cannot be created,
// the already-created meter provider is shut down before returning so its
// periodic reader goroutine and gRPC connection are released.
func NewProviders(ctx context.Context, cfg Config) (*sdkmetric.MeterProvider, *sdktrace.TracerProvider, error) {
	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, metricExporterOptions(cfg)...)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP metric exporter: %w", err)
	}
	readerOptions := []sdkmetric.PeriodicReaderOption{}
	if cfg.MetricExportInterval > 0 {
		readerOptions = append(readerOptions, sdkmetric.WithInterval(cfg.MetricExportInterval))
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, readerOptions...)),
		sdkmetric.WithResource(res),
	)

	traceExporter, err := otlptracegrpc.New(ctx, traceExporterOptions(cfg)...)
	if err != nil {
		// Roll back the meter provider so its periodic reader goroutine and gRPC
		// connection are released. Use Background because ctx may be cancelled.
		_ = meterProvider.Shutdown(context.Background())
		return nil, nil, fmt.Errorf("creating OTLP trace exporter: %w", err)
	}
	traceOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	}
	if cfg.TraceIDGenerator != nil {
		traceOptions = append(traceOptions, sdktrace.WithIDGenerator(cfg.TraceIDGenerator))
	}
	traceProvider := sdktrace.NewTracerProvider(traceOptions...)

	return meterProvider, traceProvider, nil
}

// buildResource merges the default resource with service.name and any caller
// supplied resource options, so metrics and traces share one resource.
func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OpenTelemetry resource: %w", err)
	}
	if len(cfg.ResourceOptions) == 0 {
		return res, nil
	}
	extra, err := resource.New(ctx, cfg.ResourceOptions...)
	if err != nil {
		return nil, fmt.Errorf("creating OpenTelemetry resource: %w", err)
	}
	res, err = resource.Merge(res, extra)
	if err != nil {
		return nil, fmt.Errorf("merging OpenTelemetry resource: %w", err)
	}
	return res, nil
}

func metricExporterOptions(cfg Config) []otlpmetricgrpc.Option {
	var options []otlpmetricgrpc.Option
	if cfg.Insecure {
		options = append(options, otlpmetricgrpc.WithInsecure())
	}
	options = append(options, cfg.MetricExporterOptions...)
	if cfg.Endpoint != "" {
		switch cfg.EndpointMode {
		case EndpointHostPort:
			options = append(options, otlpmetricgrpc.WithEndpoint(cfg.Endpoint))
		default:
			options = append(options, otlpmetricgrpc.WithEndpointURL(cfg.Endpoint))
		}
	}
	return options
}

func traceExporterOptions(cfg Config) []otlptracegrpc.Option {
	var options []otlptracegrpc.Option
	if cfg.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}
	options = append(options, cfg.TraceExporterOptions...)
	if cfg.Endpoint != "" {
		switch cfg.EndpointMode {
		case EndpointHostPort:
			options = append(options, otlptracegrpc.WithEndpoint(cfg.Endpoint))
		default:
			options = append(options, otlptracegrpc.WithEndpointURL(cfg.Endpoint))
		}
	}
	return options
}
