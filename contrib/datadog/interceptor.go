package datadog

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

var (
	_ interceptor.Tracer     = tracerImpl{}
	_ interceptor.TracerSpan = &tracerSpan{}
)

// NewInterceptor creates an interceptor for setting on client options
// that implements Datadog tracing for workflows.
func NewInterceptor() interceptor.Tracer {
	return tracerImpl{}
}

type contextKey string

const (
	activeSpanContextKey contextKey = "dd_trace_span"
	headerKey                       = string(activeSpanContextKey)
)

type tracerImpl struct {
	interceptor.BaseTracer
}

func (tracerImpl) Options() interceptor.TracerOptions {
	return interceptor.TracerOptions{
		SpanContextKey: activeSpanContextKey,
		HeaderKey:      headerKey,
	}
}

func (tracerImpl) UnmarshalSpan(m map[string]string) (interceptor.TracerSpanRef, error) {
	var carrier tracer.TextMapCarrier = m
	ctx, err := tracer.Extract(carrier)
	if err != nil {
		if errors.Is(err, tracer.ErrSpanContextNotFound) {
			// If there is no span, return nothing, but don't error out. This is
			// a legitimate place where a span does not exist in the headers
			return nil, nil
		}
		return nil, err
	}
	return &tracerSpanCtx{ctx}, nil
}

func (tracerImpl) MarshalSpan(span interceptor.TracerSpan) (map[string]string, error) {
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(span.(*tracerSpan).Context(), carrier); err != nil {
		return nil, err
	}
	return carrier, nil
}

func (tracerImpl) SpanFromContext(ctx context.Context) interceptor.TracerSpan {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return nil
	}
	return &tracerSpan{Span: span}
}

func (tracerImpl) ContextWithSpan(ctx context.Context, span interceptor.TracerSpan) context.Context {
	return tracer.ContextWithSpan(ctx, span.(*tracerSpan).Span)
}

func (tracerImpl) StartSpan(options *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	startOpts := []tracer.StartSpanOption{
		tracer.ResourceName(options.Name),
		tracer.StartTime(options.Time),
	}

	// Set a deterministic span ID for workflows which are long-running and cross process boundaries
	if options.Operation == "RunWorkflow" {
		rid, ok := options.Tags["temporalRunID"]
		if !ok {
			return nil, fmt.Errorf("missing required tag `temporalRunID` on `RunWorkflow` span")
		}
		h := fnv.New64()
		// Write() always writes all bytes and never fails; the count and error result are for implementing io.Writer.
		_, _ = h.Write([]byte(fmt.Sprintf("workflow:%s", rid)))
		startOpts = append(startOpts, tracer.WithSpanID(h.Sum64()))
	}

	// Add parent span to start options
	var parent ddtrace.SpanContext
	switch opParent := options.Parent.(type) {
	case nil:
	case *tracerSpan:
		parent = opParent.Context()
	case *tracerSpanCtx:
		parent = opParent
	default:
		return nil, fmt.Errorf("unrecognized parent type %T", opParent)
	}
	if parent != nil {
		// Convert the parent context into a full trace (required, otherwise
		// the implementation of StartSpan() will NOT respect ChildOf()
		// The runtime type of parentTrace will be tracer.spanContext (note the
		// starting lowercase on "spanContext", that's an internal struct)
		parentTrace, err := tracer.Extract(newSpanContextReader(parent))
		if err != nil {
			return nil, err
		}
		startOpts = append(startOpts, tracer.ChildOf(parentTrace))
	}

	// Add tags to start options
	for k, v := range options.Tags {
		// Display Temporal tags in a nested group in Datadog APM
		tagKey := strings.Replace(k, "temporal", "temporal.", 1)
		startOpts = append(startOpts, tracer.Tag(tagKey, v))
	}

	// Start and return span
	s := tracer.StartSpan(options.Operation+":"+options.Name, startOpts...)
	return &tracerSpan{Span: s}, nil
}

func (tracerImpl) GetLogger(logger log.Logger, ref interceptor.TracerSpanRef) log.Logger {
	spanRef, ok := ref.(*tracerSpan)
	if !ok {
		logger.Error(fmt.Sprintf("Error injecting TraceID in GetLogger: TracerSpanRef is type %T, expected *tracerSpan", ref))
		return logger
	}
	return log.With(logger, "dd.trace_id", spanRef.TraceID(), "dd.span_id", spanRef.SpanID())
}

func newSpanContextReader(parent ddtrace.SpanContext) spanContextReader {
	carrier := map[string]string{
		tracer.DefaultTraceIDHeader:  strconv.FormatUint(parent.TraceID(), 10),
		tracer.DefaultParentIDHeader: strconv.FormatUint(parent.SpanID(), 10),
	}

	// attach baggage items
	parent.ForeachBaggageItem(func(k, v string) bool {
		carrier[tracer.DefaultBaggageHeaderPrefix+k] = v
		return true
	})

	return spanContextReader{
		keyMap: carrier,
	}
}

type spanContextReader struct {
	keyMap map[string]string
}

// ForeachKey implements tracer.TextMapReader
func (r spanContextReader) ForeachKey(handler func(key string, value string) error) error {
	for k, v := range r.keyMap {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

type tracerSpan struct {
	ddtrace.Span
}
type tracerSpanCtx struct {
	ddtrace.SpanContext
}

func (t *tracerSpan) SpanID() uint64 {
	return t.Span.Context().SpanID()
}

func (t *tracerSpan) TraceID() uint64 {
	return t.Span.Context().TraceID()
}

func (t *tracerSpan) ForeachBaggageItem(handler func(k string, v string) bool) {
	t.Span.Context().ForeachBaggageItem(handler)
}

func (t *tracerSpan) Finish(options *interceptor.TracerFinishSpanOptions) {
	var opts []tracer.FinishOption
	if err := options.Error; err != nil && !workflow.IsContinueAsNewError(err) {
		opts = append(opts, tracer.WithError(err))
	}
	t.Span.Finish(opts...)
}
