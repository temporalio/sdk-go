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

// Idempotency key prefixes for deterministic span/trace IDs. The format is
// load-bearing: changing it breaks span identity across SDK versions for
// in-flight workflows.
const (
	sdkIDPrefix = "tracing.sdk" // plugin/interceptor workflow spans
	appIDPrefix = "tracing.app" // user spans from NewTracer
)

// NewTracerProvider creates a TracerProvider whose ID generator produces
// deterministic workflow span and trace IDs (stable across retries and
// replays) when an id key is present on the start context. Spans started
// without a key — client/activity spans, and StartUnsequenced — get random
// IDs so they never collide with the deterministic stream.
//
// NOTE: Experimental
func NewTracerProvider(opts ...sdktrace.TracerProviderOption) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(append(opts, sdktrace.WithIDGenerator(&generator{}))...)
}

// otelIdKey carries the opaque id the generator hashes into span/trace IDs.
type otelIdKey struct{}

// idStream mints per-run deterministic keys from workflow identity, a prefix,
// and a monotonically increasing counter. One stream is shared per prefix on a
// workflow.Context so concurrent Tracers do not reuse the same counter value.
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

// generator implements sdk/trace.IDGenerator. When otelIdKey is set it hashes
// that id into fixed span and trace IDs; otherwise it uses a random UUID so
// unsequenced and non-workflow spans stay unique without advancing any stream.
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

// span: / trace: prefixes domain-separate the two IDs derived from the same key.
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
