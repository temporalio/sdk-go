package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/temporal"
)

// PluginName is the name registered for the OpenTelemetry plugin.
const PluginName = "opentelemetry"

// PluginOptions are options for NewPlugin.
//
// NOTE: Experimental
type PluginOptions struct {
	// TracerOptions configure generic Temporal tracing behavior.
	TracerOptions tracing.TracerOptions

	// DisableBaggage disables baggage propagation.
	DisableBaggage bool

	// TextMapPropagator serializes spans. It defaults to
	// DefaultTextMapPropagator, not the OpenTelemetry global. Implementations
	// must be thread-safe.
	TextMapPropagator propagation.TextMapPropagator

	// MetricsHandlerOptions replaces the client's metrics handler when set.
	MetricsHandlerOptions *MetricsHandlerOptions
}

// NewPlugin configures OpenTelemetry tracing for a client and workers.
//
// NewPlugin requires the global tracer provider to be created by [NewReplaySafeTracerProvider].
//
// NOTE: Experimental
func NewPlugin(options PluginOptions) (*temporal.SimplePlugin, error) {
	clientInterceptor, workerInterceptor := newTracingInterceptors(options)

	simpleOptions := temporal.SimplePluginOptions{
		Name:               PluginName,
		ClientInterceptors: []interceptor.ClientInterceptor{clientInterceptor},
		WorkerInterceptors: []interceptor.WorkerInterceptor{workerInterceptor},
	}

	if options.MetricsHandlerOptions != nil {
		handler := NewMetricsHandler(*options.MetricsHandlerOptions)
		simpleOptions.ConfigureClient = func(ctx context.Context, configureOptions client.PluginConfigureClientOptions) error {
			configureOptions.ClientOptions.MetricsHandler = handler
			return nil
		}
	}

	return temporal.NewSimplePlugin(simpleOptions)
}
