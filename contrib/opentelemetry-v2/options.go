package opentelemetry

import (
	"go.opentelemetry.io/otel/propagation"

	"go.temporal.io/sdk/interceptor/tracing"
)

// DefaultTextMapPropagator is used when PluginOptions.TextMapPropagator is unset.
var DefaultTextMapPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

const defaultHeaderKey = "_tracer-data"

type tracerConfig struct {
	options *PluginOptions
}

func newTracerConfig(options PluginOptions) tracerConfig {
	if options.TextMapPropagator == nil {
		options.TextMapPropagator = DefaultTextMapPropagator
	}
	if options.TracerOptions.HeaderKey == "" {
		options.TracerOptions.HeaderKey = defaultHeaderKey
	}
	return tracerConfig{options: &options}
}

func (c *tracerConfig) Options() tracing.TracerOptions {
	return c.options.TracerOptions
}
