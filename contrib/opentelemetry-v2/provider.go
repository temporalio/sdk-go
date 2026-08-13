package opentelemetry

import (
	"context"
	cryptorand "crypto/rand"
	"io"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/workflow"
)

const interceptorRandomStream = "go.temporal.io/sdk/contrib/opentelemetry-v2/interceptor"
const applicationRandomStream = "go.temporal.io/sdk/contrib/opentelemetry-v2/application"

type replaySafeTracerProvider struct {
	*sdktrace.TracerProvider
}

// NewReplaySafeTracerProvider creates a provider whose ID generator reads from an
// io.Reader attached to the span-start context (workflow.GetRandom for
// sequenced workflow spans, or crypto/rand.Reader otherwise). Any ID generator
// supplied in opts is intentionally overridden. Install the result with
// otel.SetTracerProvider so NewPlugin interceptors and Tracer share it.
// Direct otel.Tracer calls are not workflow-safe; workflow code must use
// Tracer. The caller owns the provider and must shut it down after its
// clients and workers stop.
//
// NOTE: Experimental
func NewReplaySafeTracerProvider(opts ...sdktrace.TracerProviderOption) *replaySafeTracerProvider {
	opts = append(opts, sdktrace.WithIDGenerator(&generator{}))
	return &replaySafeTracerProvider{sdktrace.NewTracerProvider(opts...)}
}

func newReplaySafeTracer(name string) trace.Tracer {
	provider, ok := otel.GetTracerProvider().(*replaySafeTracerProvider)
	if !ok {
		panic("global tracer provider must be created by NewReplaySafeTracerProvider")
	}
	return provider.Tracer(name)
}

// otelRandomKey carries the io.Reader used to fill span and trace IDs.
type otelRandomKey struct{}

type generator struct{}

func interceptorReader(ctx workflow.Context) io.Reader {
	if workflow.IsReadOnly(ctx) {
		return cryptorand.Reader
	}
	return workflow.GetRandomStream(ctx, interceptorRandomStream)
}

func applicationReader(ctx workflow.Context, tracerName string) io.Reader {
	if workflow.IsReadOnly(ctx) {
		return cryptorand.Reader
	}
	// Isolate each tracer's ID stream so other tracers can be added or removed without changing its IDs.
	return workflow.GetRandomStream(ctx, applicationRandomStream+"/"+tracerName)
}

func (g *generator) NewSpanID(ctx context.Context, _ trace.TraceID) trace.SpanID {
	var id trace.SpanID
	for !id.IsValid() {
		readRandom(ctx, id[:])
	}
	return id
}

func (g *generator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	var id trace.TraceID
	for !id.IsValid() {
		readRandom(ctx, id[:])
	}
	return id, g.NewSpanID(ctx, id)
}

func readRandom(ctx context.Context, p []byte) {
	r, _ := ctx.Value(otelRandomKey{}).(io.Reader)
	if r == nil {
		r = cryptorand.Reader
	}
	_, _ = io.ReadFull(r, p)
}
