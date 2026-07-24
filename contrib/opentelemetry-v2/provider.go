package opentelemetry

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/workflow"
)

// These prefixes preserve span identity across SDK versions.
const (
	sdkIDPrefix = "tracing.sdk"
	appIDPrefix = "tracing.app"
)

// NewTracerProvider creates a provider with replay-stable workflow span IDs.
// Spans without an ID key receive random IDs.
//
// NOTE: Experimental
func NewTracerProvider(opts ...sdktrace.TracerProviderOption) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(append(opts, sdktrace.WithIDGenerator(&generator{}))...)
}

// otelIdKey carries the value hashed into span and trace IDs.
type otelIdKey struct{}

// idStream generates deterministic per-run keys from a shared counter.
type idStream struct {
	prefix  string
	counter uint64
}

func (s *idStream) next(ctx workflow.Context) string {
	info := workflow.GetInfo(ctx)
	if info == nil {
		return ""
	}

	s.counter++
	return fmt.Sprintf("%s:%s:%s:%s:%d",
		s.prefix,
		info.Namespace,
		info.WorkflowExecution.ID,
		info.WorkflowExecution.RunID,
		s.counter)
}

// generator hashes an ID key or falls back to random IDs.
type generator struct{}

func (g *generator) NewSpanID(ctx context.Context, _ trace.TraceID) trace.SpanID {
	id := g.idFromContext(ctx)
	return g.spanID(id)
}

func (g *generator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	id := g.idFromContext(ctx)
	return g.traceID(id), g.spanID(id)
}

func (g *generator) idFromContext(ctx context.Context) string {
	if id, _ := ctx.Value(otelIdKey{}).(string); id != "" {
		return id
	}
	return uuid.NewString()
}

// Prefixes keep span and trace hashes in separate domains.
func (g *generator) spanID(id string) trace.SpanID {
	sum := sha256.Sum256([]byte("span:" + id))
	var sid trace.SpanID
	copy(sid[:], sum[:8])
	return sid
}

func (g *generator) traceID(id string) trace.TraceID {
	sum := sha256.Sum256([]byte("trace:" + id))
	var tid trace.TraceID
	copy(tid[:], sum[:16])
	return tid
}
