package internal

import (
	"context"
	"testing"

	"github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	jaeger_config "github.com/uber/jaeger-client-go/config"
	commonpb "go.temporal.io/temporal-proto/common"
	"go.uber.org/zap"
)

func TestTracingContextPropagator(t *testing.T) {
	t.Parallel()
	tracer, closer, err := jaeger_config.Configuration{ServiceName: "test-service"}.NewTracer()
	require.NoError(t, err)
	defer func() { _ = closer.Close() }()
	ctxProp := NewTracingContextPropagator(zap.NewNop(), tracer)

	span := tracer.StartSpan("test-operation")
	ctx := context.Background()
	ctx = opentracing.ContextWithSpan(ctx, span)
	header := &commonpb.Header{
		Fields: map[string][]byte{},
	}

	err = ctxProp.Inject(ctx, NewHeaderWriter(header))
	require.NoError(t, err)

	returnCtx := context.Background()
	returnCtx, err = ctxProp.Extract(returnCtx, NewHeaderReader(header))
	require.NoError(t, err)

	spanCtx := returnCtx.Value(activeSpanContextKey)
	assert.NotNil(t, spanCtx)
}

func TestTracingContextPropagatorNoSpan(t *testing.T) {
	t.Parallel()
	ctxProp := NewTracingContextPropagator(zap.NewNop(), opentracing.NoopTracer{})

	header := &commonpb.Header{
		Fields: map[string][]byte{},
	}
	err := ctxProp.Inject(context.Background(), NewHeaderWriter(header))
	require.NoError(t, err)

	returnCtx := context.Background()
	_, err = ctxProp.Extract(returnCtx, NewHeaderReader(header))
	assert.NoError(t, err)
}

func TestTracingContextPropagatorWorkflowContext(t *testing.T) {
	t.Parallel()
	tracer, closer, err := jaeger_config.Configuration{ServiceName: "test-service"}.NewTracer()
	require.NoError(t, err)
	defer func() { _ = closer.Close() }()
	ctxProp := NewTracingContextPropagator(zap.NewNop(), tracer)

	span := tracer.StartSpan("test-operation")
	assert.NotNil(t, span.Context())
	ctx := contextWithSpan(Background(), span.Context())
	header := &commonpb.Header{
		Fields: map[string][]byte{},
	}

	err = ctxProp.InjectFromWorkflow(ctx, NewHeaderWriter(header))
	require.NoError(t, err)

	returnCtx, err := ctxProp.ExtractToWorkflow(Background(), NewHeaderReader(header))
	require.NoError(t, err)

	returnCtx2, err := ctxProp.ExtractToWorkflow(Background(), NewHeaderReader(header))
	require.NoError(t, err)

	newSpanContext := spanFromContext(returnCtx)
	assert.NotNil(t, newSpanContext)
	newSpanContext2 := spanFromContext(returnCtx2)
	assert.NotNil(t, newSpanContext2)
	assert.Equal(t, newSpanContext2, newSpanContext)
}

func TestTracingContextPropagatorWorkflowContextNoSpan(t *testing.T) {
	t.Parallel()
	ctxProp := NewTracingContextPropagator(zap.NewNop(), opentracing.NoopTracer{})

	header := &commonpb.Header{
		Fields: map[string][]byte{},
	}
	err := ctxProp.InjectFromWorkflow(Background(), NewHeaderWriter(header))
	require.NoError(t, err)

	returnCtx := Background()
	_, err = ctxProp.ExtractToWorkflow(returnCtx, NewHeaderReader(header))
	assert.NoError(t, err)
}
