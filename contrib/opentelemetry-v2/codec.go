package opentelemetry

import (
	"context"
	"maps"
	"slices"
	"strings"

	"go.temporal.io/sdk/interceptor/tracing"
)

type textMapCarrier map[string]string

// Get uses a case-insensitive fallback because Nexus headers use HTTP header semantics.
// See https://opentelemetry.io/docs/specs/otel/context/api-propagators/#get.
func (t textMapCarrier) Get(key string) string {
	if value, ok := t[key]; ok {
		return value
	}
	for field, value := range t {
		if strings.EqualFold(field, key) {
			return value
		}
	}
	return ""
}

func (t textMapCarrier) Set(key string, value string) { t[key] = value }

func (t textMapCarrier) Keys() []string { return slices.Collect(maps.Keys(t)) }

type spanCodec struct {
	contextBridge *contextBridge
}

func (c *spanCodec) MarshalSpan(ref tracing.TracerSpanRef) (map[string]string, error) {
	ctx := c.contextBridge.ContextWithSpan(context.Background(), ref)
	data := textMapCarrier{}
	c.contextBridge.options.TextMapPropagator.Inject(ctx, data)
	return data, nil
}

func (c *spanCodec) UnmarshalSpan(m map[string]string) (tracing.TracerSpanRef, error) {
	ctx := c.contextBridge.options.TextMapPropagator.Extract(context.Background(), textMapCarrier(m))
	return c.contextBridge.SpanFromContext(ctx), nil
}
