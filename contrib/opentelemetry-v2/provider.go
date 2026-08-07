package opentelemetry

import (
	"context"
	cryptorand "crypto/rand"
	"io"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/workflow"
)

const randomName = "go.temporal.io/sdk/contrib/opentelemetry-v2"

// NewTracerProvider creates a provider whose ID generator reads from an
// io.Reader attached to the span-start context (workflow.GetRandom for
// sequenced workflow spans, or crypto/rand.Reader otherwise). Any ID generator
// supplied in opts is intentionally overridden. Install the result with
// otel.SetTracerProvider so NewPlugin interceptors and Tracer share it.
// Direct otel.Tracer calls are not workflow-safe; workflow code must use
// Tracer. The caller owns the provider and must shut it down after its
// clients and workers stop.
//
// NOTE: Experimental
func NewTracerProvider(opts ...sdktrace.TracerProviderOption) *sdktrace.TracerProvider {
	opts = append(opts, sdktrace.WithIDGenerator(&generator{}))
	return sdktrace.NewTracerProvider(opts...)
}

// otelRandomKey carries the io.Reader used to fill span and trace IDs.
type otelRandomKey struct{}

type generator struct{}

func workflowRandomReader(ctx workflow.Context) io.Reader {
	if workflow.IsReadOnly(ctx) {
		return cryptorand.Reader
	}
	return workflow.GetRandom(ctx, randomName)
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
