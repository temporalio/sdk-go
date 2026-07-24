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
	// TracerOptions configure tracing installed by the plugin.
	TracerOptions TracerOptions

	// ProviderOptions configure the tracer provider the plugin builds (exporters,
	// resources, etc.). Workflow spans started by the interceptor use
	// deterministic IDs so they survive retries and replays; client and activity
	// spans fall back to random IDs.
	ProviderOptions []sdktrace.TracerProviderOption

	// MetricsHandlerOptions, if non-nil, installs an OpenTelemetry metrics
	// handler on the client, overriding any existing metrics handler. If nil,
	// client metrics are left untouched.
	MetricsHandlerOptions *MetricsHandlerOptions
}

// NewPlugin creates a plugin implementing both
// [go.temporal.io/sdk/client.Plugin] and [go.temporal.io/sdk/worker.Plugin]
// that configures OpenTelemetry tracing on the client and on workers created
// from that client.
//
// The plugin builds a NewTracerProvider so interceptor workflow spans get
// deterministic IDs. For custom spans inside workflow code, use NewTracer with
// that same kind of provider (see NewTracerProvider). The returned function
// shuts the provider down; call it when the process exits.
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
