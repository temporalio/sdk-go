package tracing

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

func Test_tracerImpl_genSpanID(t1 *testing.T) {
	tests := []struct {
		name  string
		runId string
		want  uint64
	}{
		{
			name:  "Test always the same",
			runId: "afd160fc-2591-42fa-ad33-3c8f80084961",
			want:  279256908952477392,
		},
		{
			name:  "Different runId",
			runId: "0",
			want:  13822530076732356516,
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			if first := genSpanID(tt.runId); first != tt.want {
				t1.Errorf("genSpanID() = %v, want %v", first, tt.want)
				if second := genSpanID(tt.runId); second != first {
					t1.Errorf("first genSpanID() = %v, second genSpanID() = %v. Subsequent invocations MUST return the same result", first, second)
				}
			}
		})
	}
}
