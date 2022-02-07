// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
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

package interceptor_test

import (
	"context"
	"testing"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
)

type testTracer struct {
	interceptor.BaseTracer
	T *testing.T
}

type testSpan struct{}

func (t testSpan) Finish(options *interceptor.TracerFinishSpanOptions) {}

type testSpanRef struct{}

func (t testTracer) Options() interceptor.TracerOptions {
	return interceptor.TracerOptions{
		SpanContextKey:       "test-tracer",
		HeaderKey:            "test-tracer",
		DisableSignalTracing: false,
		DisableQueryTracing:  false,
	}
}

func (t *testTracer) UnmarshalSpan(m map[string]string) (interceptor.TracerSpanRef, error) {
	return testSpanRef{}, nil
}

func (t *testTracer) MarshalSpan(span interceptor.TracerSpan) (map[string]string, error) {
	return map[string]string{}, nil
}

func (t *testTracer) SpanFromContext(ctx context.Context) interceptor.TracerSpan {
	return testSpan{}
}

func (t *testTracer) ContextWithSpan(ctx context.Context, span interceptor.TracerSpan) context.Context {
	return ctx
}

func (t *testTracer) StartSpan(options *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	// Require start time to be set
	if options.Time.IsZero() {
		switch options.Operation {
		case "RunWorkflow", "RunActivity":
		// Do nothing; the test env doesn't set these at the moment.
		default:
			t.T.Errorf("Got zero value for span start time: %v", options)
		}
	}
	return testSpan{}, nil
}

func TestSpanTimestamps(t *testing.T) {
	interceptortest.RunTestWorkflow(t, &testTracer{T: t})
}
