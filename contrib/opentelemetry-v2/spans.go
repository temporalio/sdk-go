package opentelemetry

import (
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type tracerSpan struct {
	trace.Span
	baggage.Baggage
}

var _ tracing.TracerSpan = (*tracerSpan)(nil)
var _ tracing.TracerSpanRef = (*tracerSpan)(nil)

func (t *tracerSpan) Finish(opts *tracing.TracerFinishSpanOptions) {
	if !t.Span.IsRecording() {
		return
	}

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

func asTracerSpan(ref tracing.TracerSpanRef) *tracerSpan {
	switch span := ref.(type) {
	case *tracerSpan:
		return span
	case *interceptorWorkflowSpan:
		if span != nil {
			return span.tracerSpan
		}
	case *workflowSpan:
		if span != nil {
			return span.tracerSpan
		}
	}
	return nil
}
