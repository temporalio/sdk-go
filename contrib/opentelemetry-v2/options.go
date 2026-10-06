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

func newOptions(pluginOptions PluginOptions) *options {
	opts := &options{PluginOptions: pluginOptions}

	if opts.TextMapPropagator == nil {
		opts.TextMapPropagator = DefaultTextMapPropagator
	}

	if opts.TracerOptions.HeaderKey == "" {
		opts.TracerOptions.HeaderKey = defaultHeaderKey
	}

	return opts
}

type options struct {
	PluginOptions
}

func (o *options) Options() tracing.TracerOptions {
	return o.TracerOptions
}
