package opentelemetry

import (
	"go.opentelemetry.io/otel/propagation"

	"go.temporal.io/sdk/interceptor/tracing"
)

// DefaultTextMapPropagator is used when TracerOptions.TextMapPropagator is unset.
var DefaultTextMapPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

const defaultHeaderKey = "_tracer-data"

// TracerOptions configure tracing for NewPlugin.
//
// NOTE: Experimental
type TracerOptions struct {
	// DisableSignalTracing disables signal tracing.
	DisableSignalTracing bool

	// DisableQueryTracing disables query tracing.
	DisableQueryTracing bool

	// DisableUpdateTracing disables update tracing.
	DisableUpdateTracing bool

	// DisableBaggage disables baggage propagation.
	DisableBaggage bool

	// AllowInvalidParentSpans ignores malformed parent headers during migrations.
	AllowInvalidParentSpans bool

	// TextMapPropagator serializes spans. It defaults to
	// DefaultTextMapPropagator, not the OpenTelemetry global.
	TextMapPropagator propagation.TextMapPropagator

	// HeaderKey stores serialized spans. It defaults to "_tracer-data".
	HeaderKey string
}

type tracerConfig struct {
	options *TracerOptions
}

func newTracerConfig(options TracerOptions) tracerConfig {
	if options.TextMapPropagator == nil {
		options.TextMapPropagator = DefaultTextMapPropagator
	}
	if options.HeaderKey == "" {
		options.HeaderKey = defaultHeaderKey
	}
	return tracerConfig{options: &options}
}

func (c *tracerConfig) Options() tracing.TracerOptions {
	return tracing.TracerOptions{
		HeaderKey:               c.options.HeaderKey,
		DisableSignalTracing:    c.options.DisableSignalTracing,
		DisableQueryTracing:     c.options.DisableQueryTracing,
		DisableUpdateTracing:    c.options.DisableUpdateTracing,
		AllowInvalidParentSpans: c.options.AllowInvalidParentSpans,
	}
}
