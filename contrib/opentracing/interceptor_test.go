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

package opentracing_test

import (
	"testing"

	"github.com/opentracing/opentracing-go/mocktracer"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/contrib/opentracing"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
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
