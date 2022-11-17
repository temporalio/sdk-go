// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
package tracing

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
)

type testTracer struct {
	interceptor.Tracer
	mt mocktracer.Tracer
}

func (t testTracer) SpanName(options *interceptor.TracerStartSpanOptions) string {
	return t.Tracer.(*tracerImpl).SpanName(options)
}

func (t testTracer) FinishedSpans() []*interceptortest.SpanInfo {
	return spanChildren(t.mt.FinishedSpans(), 0)
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
	impl := NewTracer(TracerOptions{})
	testTracer := testTracer{
		Tracer: impl,
		mt:     mt,
	}
	interceptortest.RunTestWorkflow(t, testTracer)
	interceptortest.AssertSpanPropagation(t, testTracer)
}
func TestSpanName(t *testing.T) {
	// Start the mock tracer.
	mt := mocktracer.Start()
	defer mt.Stop()
	impl := NewTracer(TracerOptions{})
	testTracer := testTracer{
		Tracer: impl,
		mt:     mt,
	}
	interceptortest.RunTestWorkflow(t, testTracer)
	// Ensure the naming scheme follows "temporal.${operation}"
	require.Equal(t, "temporal.RunWorkflow", testTracer.FinishedSpans()[0].Name)

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
			want:  11306945927996332141,
		},
		{
			name:  "Different runId",
			runId: "0",
			want:  12638153115695167471,
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			// Ensure that if we generate spans for two different consecutive runs, they are consistent
			// given the same input parameters (runId)
			if first := genSpanID(tt.runId); first != tt.want {
				t1.Errorf("genSpanID() = %v, want %v", first, tt.want)
				if second := genSpanID(tt.runId); second != first {
					t1.Errorf("first genSpanID() = %v, second genSpanID() = %v. Subsequent invocations MUST return the same result", first, second)
				}
			}
		})
	}
}
