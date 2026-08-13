package opentelemetry

import (
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type tracerSpanRef struct {
	trace.SpanContext
	baggage.Baggage
}

type tracerSpan struct {
	trace.Span
	baggage.Baggage
}

func (t *tracerSpan) Finish(opts *tracing.TracerFinishSpanOptions) {
	if opts.Error != nil {
		t.RecordError(opts.Error)

		// Benign application errors do not mark spans as failed.
		appError, _ := opts.Error.(*temporal.ApplicationError)
		isBenign := appError != nil && appError.Category() == temporal.ApplicationErrorCategoryBenign
		if !isBenign {
			t.SetStatus(codes.Error, opts.Error.Error())
		}
	}
	t.End()
}

// interceptorWorkflowSpan suppresses Finish calls during replay.
type interceptorWorkflowSpan struct {
	*tracerSpan
	ctx workflow.Context
}

func (s *interceptorWorkflowSpan) Finish(opts *tracing.TracerFinishSpanOptions) {
	if workflow.IsReplaying(s.ctx) {
		return
	}
	s.tracerSpan.Finish(opts)
}

// workflowSpan suppresses End calls during replay.
type workflowSpan struct {
	*tracerSpan
	ctx workflow.Context
}

func (s *workflowSpan) End(options ...trace.SpanEndOption) {
	if workflow.IsReplaying(s.ctx) {
		return
	}
	s.tracerSpan.Span.End(options...)
}

// asTracerSpan unwraps a live tracerSpan. It returns nil for unknown span types.
func asTracerSpan(ref tracing.TracerSpanRef) *tracerSpan {
	switch p := ref.(type) {
	case *tracerSpan:
		return p
	case *interceptorWorkflowSpan:
		return p.tracerSpan
	case *workflowSpan:
		return p.tracerSpan
	}
	return nil
}
