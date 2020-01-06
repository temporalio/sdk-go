// Copyright (c) 2017 Uber Technologies, Inc.
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

package internal

import (
	"context"
	"testing"

	"github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
	jaeger_config "github.com/uber/jaeger-client-go/config"
	"go.uber.org/zap"

	commonproto "go.temporal.io/temporal-proto/common"
)

func TestTracingContextPropagator(t *testing.T) {
	config := jaeger_config.Configuration{}
	closer, err := config.InitGlobalTracer("test-service")
	assert.NoError(t, err)
	defer func() { _ = closer.Close() }()
	tracer := opentracing.GlobalTracer()
	ctxProp := NewTracingContextPropagator(zap.NewNop(), tracer)

	span := tracer.StartSpan("test-operation")
	ctx := context.Background()
	ctx = opentracing.ContextWithSpan(ctx, span)
	header := &commonproto.Header{
		Fields: map[string][]byte{},
	}

	err = ctxProp.Inject(ctx, NewHeaderWriter(header))
	assert.NoError(t, err)

	returnCtx := context.Background()
	returnCtx, err = ctxProp.Extract(returnCtx, NewHeaderReader(header))
	assert.NoError(t, err)

	spanCtx := returnCtx.Value(activeSpanContextKey)
	assert.NotNil(t, spanCtx)
}

func TestTracingContextPropagatorNoSpan(t *testing.T) {
	ctxProp := NewTracingContextPropagator(zap.NewNop(), opentracing.NoopTracer{})

	header := &commonproto.Header{
		Fields: map[string][]byte{},
	}
	err := ctxProp.Inject(context.Background(), NewHeaderWriter(header))
	assert.NoError(t, err)

	returnCtx := context.Background()
	_, err = ctxProp.Extract(returnCtx, NewHeaderReader(header))
	assert.NoError(t, err)
}

func TestTracingContextPropagatorWorkflowContext(t *testing.T) {
	config := jaeger_config.Configuration{}
	closer, err := config.InitGlobalTracer("test-service")
	assert.NoError(t, err)
	defer func() { _ = closer.Close() }()
	tracer := opentracing.GlobalTracer()
	ctxProp := NewTracingContextPropagator(zap.NewNop(), tracer)

	span := tracer.StartSpan("test-operation")
	ctx := contextWithSpan(Background(), span.Context())
	header := &commonproto.Header{
		Fields: map[string][]byte{},
	}

	err = ctxProp.InjectFromWorkflow(ctx, NewHeaderWriter(header))
	assert.NoError(t, err)

	returnCtx := Background()
	returnCtx, err = ctxProp.ExtractToWorkflow(returnCtx, NewHeaderReader(header))
	assert.NoError(t, err)

	newSpanContext := spanFromContext(returnCtx)
	assert.NotNil(t, newSpanContext)
	assert.Equal(t, span.Context(), newSpanContext)
}

func TestTracingContextPropagatorWorkflowContextNoSpan(t *testing.T) {
	ctxProp := NewTracingContextPropagator(zap.NewNop(), opentracing.NoopTracer{})

	header := &commonproto.Header{
		Fields: map[string][]byte{},
	}
	err := ctxProp.InjectFromWorkflow(Background(), NewHeaderWriter(header))
	assert.NoError(t, err)

	returnCtx := Background()
	_, err = ctxProp.ExtractToWorkflow(returnCtx, NewHeaderReader(header))
	assert.NoError(t, err)
}
