package interceptor

import (
	"context"
	"errors"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type headerTestTracer struct {
	Tracer
	options TracerOptions
}

func (t *headerTestTracer) Options() TracerOptions { return t.options }
func (*headerTestTracer) MarshalSpan(TracerSpan) (map[string]string, error) {
	return map[string]string{"trace": "parent"}, nil
}
func (*headerTestTracer) UnmarshalSpan(data map[string]string) (TracerSpanRef, error) {
	return data, nil
}
func (*headerTestTracer) StartSpan(*TracerStartSpanOptions) (TracerSpan, error) {
	return nopSpan{}, nil
}

func TestTracingDataConverter(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
			converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
		if custom {
			name = "codec"
		}
		t.Run(name, func(t *testing.T) {
			tracer := &headerTestTracer{options: TracerOptions{
				SpanContextKey: "span", HeaderKey: "trace", UseDataConverter: custom,
			}}
			i := NewTracingInterceptor(tracer).(*tracingInterceptor)
			if dc != nil {
				i = i.WithClientDataConverter(dc).(*tracingInterceptor)
			}
			header := map[string]*commonpb.Payload{}
			require.NoError(t, i.writeSpanToHeader(i.dataConverterOrDefault(), nil, header))
			if custom {
				require.Equal(t, "binary/zlib", string(header["trace"].Metadata[converter.MetadataEncoding]))
			} else {
				expected, err := converter.GetDefaultDataConverter().ToPayload(map[string]string{"trace": "parent"})
				require.NoError(t, err)
				require.True(t, proto.Equal(expected, header["trace"]))
			}
			span, err := i.readSpanFromHeader(i.dataConverterOrDefault(), header)
			require.NoError(t, err)
			require.Equal(t, map[string]string{"trace": "parent"}, span)
			span, err = i.readSpanFromHeader(i.dataConverterOrDefault(), nil)
			require.NoError(t, err)
			require.Nil(t, span)
		})
	}
}

type failingHeaderConverter struct {
	converter.DataConverter
	err error
}

func (d failingHeaderConverter) ToPayload(any) (*commonpb.Payload, error) { return nil, d.err }
func (d failingHeaderConverter) FromPayload(*commonpb.Payload, any) error { return d.err }

func TestTracingDataConverterErrorsAndNexus(t *testing.T) {
	conversionErr := errors.New("trace codec unavailable")
	dc := failingHeaderConverter{DataConverter: converter.GetDefaultDataConverter(), err: conversionErr}
	i := NewTracingInterceptor(&headerTestTracer{options: TracerOptions{
		SpanContextKey: "span", HeaderKey: "trace", UseDataConverter: true,
	}}).(*tracingInterceptor)
	i = i.WithClientDataConverter(dc).(*tracingInterceptor)
	header := map[string]*commonpb.Payload{}
	require.ErrorIs(t, i.writeSpanToHeader(dc, nil, header), conversionErr)
	require.Empty(t, header)
	header["trace"] = &commonpb.Payload{}
	_, err := i.readSpanFromHeader(dc, header)
	require.ErrorIs(t, err, conversionErr)
	_, err = i.startSpan(t.Context(), &TracerStartSpanOptions{FromHeader: true},
		func() (TracerSpanRef, error) { return i.readSpanFromHeader(dc, header) }, nil)
	require.ErrorIs(t, err, conversionErr)
	i.options.AllowInvalidParentSpans = true
	_, err = i.startSpan(t.Context(), &TracerStartSpanOptions{FromHeader: true},
		func() (TracerSpanRef, error) { return i.readSpanFromHeader(dc, header) }, nil)
	require.NoError(t, err)

	nexusHeader := nexus.Header{}
	require.NoError(t, i.writeSpanToNexusHeader(nil, nexusHeader))
	span, err := i.readSpanFromNexusHeader(nexusHeader)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"trace": "parent"}, span)
}

type contextHeaderConverter struct {
	converter.DataConverter
	t                      *testing.T
	goCalls, workflowCalls int
}

type headerContextKey struct{}

func (d *contextHeaderConverter) WithContext(ctx context.Context) converter.DataConverter {
	require.Equal(d.t, "value", ctx.Value(headerContextKey{}))
	d.goCalls++
	return d.DataConverter
}

func (d *contextHeaderConverter) WithWorkflowContext(ctx workflow.Context) converter.DataConverter {
	require.Equal(d.t, "value", ctx.Value(headerContextKey{}))
	d.workflowCalls++
	return d.DataConverter
}

type headerContextInterceptor struct {
	WorkerInterceptorBase
	check func(workflow.Context) error
}

func (i *headerContextInterceptor) InterceptWorkflow(_ workflow.Context, next WorkflowInboundInterceptor) WorkflowInboundInterceptor {
	return &headerContextInboundInterceptor{WorkflowInboundInterceptorBase: WorkflowInboundInterceptorBase{Next: next}, check: i.check}
}

type headerContextInboundInterceptor struct {
	WorkflowInboundInterceptorBase
	check func(workflow.Context) error
}

func (i *headerContextInboundInterceptor) ExecuteWorkflow(ctx workflow.Context, in *ExecuteWorkflowInput) (any, error) {
	if err := i.check(ctx); err != nil {
		return nil, err
	}
	return i.Next.ExecuteWorkflow(ctx, in)
}

func TestTracingDataConverterContextBinding(t *testing.T) {
	codec := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	dc := &contextHeaderConverter{DataConverter: codec, t: t}
	i := NewTracingInterceptor(&headerTestTracer{options: TracerOptions{
		SpanContextKey: "span", HeaderKey: "trace", UseDataConverter: true,
	}}).(*tracingInterceptor)
	i = i.WithClientDataConverter(dc).(*tracingInterceptor)
	ctx := context.WithValue(t.Context(), headerContextKey{}, "value")
	_, err := i.headerReader(ctx)()
	require.NoError(t, err)
	conversionErr := errors.New("bound converter error")
	dc.DataConverter = failingHeaderConverter{DataConverter: codec, err: conversionErr}
	require.ErrorIs(t, i.headerWriter(ctx)(nil), conversionErr)
	require.Equal(t, 2, dc.goCalls)
	dc.DataConverter = codec

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	check := &headerContextInterceptor{check: func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, headerContextKey{}, "value")
		require.NotNil(t, WorkflowHeader(ctx))
		require.NoError(t, i.workflowHeaderWriter(ctx)(nil))
		require.Equal(t, "binary/zlib", string(WorkflowHeader(ctx)["trace"].Metadata[converter.MetadataEncoding]))
		span, err := i.workflowHeaderReader(ctx)()
		require.Equal(t, map[string]string{"trace": "parent"}, span)
		return err
	}}
	env.SetWorkerOptions(worker.Options{Interceptors: []WorkerInterceptor{check}})
	env.ExecuteWorkflow(func(workflow.Context) error { return nil })
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 2, dc.workflowCalls)
}
