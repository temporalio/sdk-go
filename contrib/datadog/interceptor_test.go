package datadog

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"go.temporal.io/sdk/internal/interceptortest"
)

type testTracer struct {
	tracerImpl
	mocktracer.Tracer
}

func (t testTracer) FinishedSpans() []*interceptortest.SpanInfo {
	return spanChildren(t.Tracer.FinishedSpans(), 0)
}

func spanChildren(spans []mocktracer.Span, parentId uint64) (ret []*interceptortest.SpanInfo) {
	for _, s := range spans {
		if s.ParentID() == parentId {
			spanName := s.OperationName()
			ret = append(ret, interceptortest.Span(spanName, spanChildren(spans, s.SpanID())...))
		}
	}
	return
}

func TestSpanPropagation(t *testing.T) {
	// Start the mock tracer.
	mt := mocktracer.Start()
	defer mt.Stop()

	testTracer := testTracer{
		tracerImpl: tracerImpl{},
		Tracer:     mt,
	}
	interceptortest.RunTestWorkflow(t, testTracer)
	interceptortest.AssertSpanPropagation(t, testTracer)
}
