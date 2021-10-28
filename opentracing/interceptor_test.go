package opentracing_test

import (
	"testing"

	"github.com/opentracing/opentracing-go/mocktracer"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
	"go.temporal.io/sdk/opentracing"
)

func TestSpanPropagation(t *testing.T) {
	mock := mocktracer.New()
	tracer, err := opentracing.NewTracer(opentracing.TracerOptions{Tracer: mock})
	require.NoError(t, err)
	interceptortest.AssertSpanPropagation(t, &testTracer{Tracer: tracer, mock: mock})
}

type testTracer struct {
	interceptor.Tracer
	mock *mocktracer.MockTracer
}

func (t *testTracer) FinishedSpans() []*interceptortest.SpanInfo {
	return spanChildren(t.mock.FinishedSpans(), 0)
}

func spanChildren(spans []*mocktracer.MockSpan, parentID int) (ret []*interceptortest.SpanInfo) {
	for _, s := range spans {
		if s.ParentID == parentID {
			ret = append(ret, interceptortest.Span(s.OperationName, spanChildren(spans, s.SpanContext.SpanID)...))
		}
	}
	return
}
