// Package tracing provides Datadog tracing utilities for dd-trace-go v2
package tracing

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// TracerOptions are options provided to NewInterceptor
type TracerOptions struct {
	// DisableSignalTracing can be set to disable signal tracing.
	DisableSignalTracing bool

	// DisableQueryTracing can be set to disable query tracing.
	DisableQueryTracing bool

	// DisableUpdateTracing can be set to disable update tracing.
	DisableUpdateTracing bool

	// OnFinish sets finish options.
	// If unset, this will use [tracer.WithError]
	// in case [interceptor.TracerFinishSpanOptions.Error] is non-nil and not [workflow.IsContinueAsNewError].
	OnFinish func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption
}

// NewTracingInterceptor convenience method that wraps a NewTracer() with a tracing interceptor
func NewTracingInterceptor(opts TracerOptions) interceptor.Interceptor {
	return interceptor.NewTracingInterceptor(NewTracer(opts))

}

// NewTracer creates an interceptor for setting on client options
// that implements Datadog tracing for workflows.
func NewTracer(opts TracerOptions) interceptor.Tracer {
	if opts.OnFinish == nil {
		opts.OnFinish = func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption {
			var finishOpts []tracer.FinishOption

			if err := options.Error; err != nil && !workflow.IsContinueAsNewError(err) {
				finishOpts = append(finishOpts, tracer.WithError(err))
			}

			return finishOpts
		}
	}

	return &tracerImpl{
		opts: TracerOptions{
			DisableSignalTracing: opts.DisableSignalTracing,
			DisableQueryTracing:  opts.DisableQueryTracing,
			DisableUpdateTracing: opts.DisableUpdateTracing,
			OnFinish:             opts.OnFinish,
		},
	}
}

type contextKey string

const (
	activeSpanContextKey contextKey = "dd_trace_span"
	headerKey                       = string(activeSpanContextKey)
)

type tracerImpl struct {
	interceptor.BaseTracer
	// DisableSignalTracing can be set to disable signal tracing.
	opts TracerOptions
}

func (t *tracerImpl) Options() interceptor.TracerOptions {
	return interceptor.TracerOptions{
		SpanContextKey:       activeSpanContextKey,
		HeaderKey:            headerKey,
		DisableSignalTracing: t.opts.DisableSignalTracing,
		DisableQueryTracing:  t.opts.DisableQueryTracing,
	}
}

func (t *tracerImpl) UnmarshalSpan(m map[string]string) (interceptor.TracerSpanRef, error) {
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

func (t *tracerImpl) MarshalSpan(span interceptor.TracerSpan) (map[string]string, error) {
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(span.(*tracerSpan).Span.Context(), carrier); err != nil {
		return nil, err
	}
	return carrier, nil
}

func (t *tracerImpl) SpanFromContext(ctx context.Context) interceptor.TracerSpan {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return nil
	}
	return &tracerSpan{OnFinish: t.opts.OnFinish, Span: span}
}

func (t *tracerImpl) ContextWithSpan(ctx context.Context, span interceptor.TracerSpan) context.Context {
	return tracer.ContextWithSpan(ctx, span.(*tracerSpan).Span)
}

// SpanFromWorkflowContext extracts the DataDog Span object from the workflow context.
func SpanFromWorkflowContext(ctx workflow.Context) (*tracer.Span, bool) {
	val := ctx.Value(activeSpanContextKey)
	if val == nil {
		return tracer.SpanFromContext(nil)
	}

	if span, ok := val.(*tracerSpan); ok {
		return span.Span, true
	}

	return tracer.SpanFromContext(nil)
}

func genSpanID(idempotencyKey string) uint64 {
	h := fnv.New64()
	// Write() always writes all bytes and never fails; the count and error result are for implementing io.Writer.
	_, _ = h.Write([]byte(idempotencyKey))
	return h.Sum64()
}

func (t *tracerImpl) StartSpan(options *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	// Build common start options
	startOpts := []tracer.StartSpanOption{
		tracer.ResourceName(options.Name),
		tracer.StartTime(options.Time),
	}
	// Set a deterministic span ID for workflows which are long-running and cross process boundaries
	if options.IdempotencyKey != "" {
		startOpts = append(startOpts, tracer.WithSpanID(genSpanID(options.IdempotencyKey)))
	}
	// Add tags to start options
	for k, v := range options.Tags {
		// Display Temporal tags in a nested group in Datadog APM
		tagKey := "temporal." + strings.TrimPrefix(k, "temporal")
		startOpts = append(startOpts, tracer.Tag(tagKey, v))
	}

	var s *tracer.Span

	switch opParent := options.Parent.(type) {
	case *tracerSpan:
		// Use StartChild when we have an actual parent span
		s = opParent.Span.StartChild(t.SpanName(options), startOpts...)
	case *tracerSpanCtx:
		// Use WithStartSpanConfig to set parent from extracted SpanContext
		startOpts = append(startOpts, tracer.WithStartSpanConfig(&tracer.StartSpanConfig{
			Parent: opParent.SpanContext,
		}))
		s = tracer.StartSpan(t.SpanName(options), startOpts...)
	default:
		// No parent or unexpected type - create a root span
		s = tracer.StartSpan(t.SpanName(options), startOpts...)
	}

	return &tracerSpan{OnFinish: t.opts.OnFinish, Span: s}, nil
}

func (t *tracerImpl) GetLogger(logger log.Logger, ref interceptor.TracerSpanRef) log.Logger {
	spanRef, ok := ref.(*tracerSpan)
	if !ok {
		logger.Error(fmt.Sprintf("Error injecting TraceID in GetLogger: TracerSpanRef is type %T, expected *tracerSpan", ref))
		return logger
	}
	return log.With(logger, "dd.trace_id", spanRef.TraceID(), "dd.span_id", spanRef.SpanID())
}

func (t *tracerImpl) SpanName(options *interceptor.TracerStartSpanOptions) string {
	return fmt.Sprintf("temporal.%s", options.Operation)
}

type tracerSpan struct {
	Span     *tracer.Span
	OnFinish func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption
}
type tracerSpanCtx struct {
	*tracer.SpanContext
}

func (t *tracerSpan) SpanID() uint64 {
	return t.Span.Context().SpanID()
}

func (t *tracerSpan) TraceID() string {
	return t.Span.Context().TraceID()
}

func (t *tracerSpan) ForeachBaggageItem(handler func(k string, v string) bool) {
	t.Span.Context().ForeachBaggageItem(handler)
}

func (t *tracerSpan) Finish(options *interceptor.TracerFinishSpanOptions) {
	opts := t.OnFinish(options)

	t.Span.Finish(opts...)
}
