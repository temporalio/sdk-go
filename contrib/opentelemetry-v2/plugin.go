package opentelemetry

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
)

// PluginName is the name registered for the OpenTelemetry plugin.
const PluginName = "opentelemetry"

// PluginOptions are options for NewPlugin.
//
// NOTE: Experimental
type PluginOptions struct {
	// TracerOptions configure plugin tracing.
	TracerOptions TracerOptions

	// ProviderOptions configure the plugin's tracer provider.
	ProviderOptions []sdktrace.TracerProviderOption

	// MetricsHandlerOptions replaces the client's metrics handler when set.
	MetricsHandlerOptions *MetricsHandlerOptions
}

// NewPlugin configures OpenTelemetry tracing for a client and its workers.
//
// Use NewTracer with the same provider for custom workflow spans. Call the
// returned shutdown function on exit.
//
// NOTE: Experimental
func NewPlugin(options PluginOptions) (*temporal.SimplePlugin, func(context.Context) error, error) {
	provider := NewTracerProvider(options.ProviderOptions...)

	tracingInterceptor := newTracingInterceptor(options.TracerOptions, provider)

	simpleOptions := temporal.SimplePluginOptions{
		Name:               PluginName,
		ClientInterceptors: []interceptor.ClientInterceptor{tracingInterceptor},
	}

	if options.MetricsHandlerOptions != nil {
		handler := NewMetricsHandler(*options.MetricsHandlerOptions)
		simpleOptions.ConfigureClient = func(ctx context.Context, configureOptions client.PluginConfigureClientOptions) error {
			configureOptions.ClientOptions.MetricsHandler = handler
			return nil
		}
	}

	plugin, err := temporal.NewSimplePlugin(simpleOptions)
	if err != nil {
		return nil, nil, err
	}

	shutdown := func(ctx context.Context) error {
		return provider.Shutdown(ctx)
	}

	return plugin, shutdown, nil
}
