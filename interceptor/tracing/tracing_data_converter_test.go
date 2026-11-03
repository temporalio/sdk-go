package tracing

import (
	"context"
	"errors"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type headerTestTracer struct {
	Tracer
	options TracerOptions
}

func (t *headerTestTracer) Options() TracerOptions { return t.options }
func (*headerTestTracer) MarshalSpan(TracerSpanRef) (map[string]string, error) {
	return map[string]string{"trace": "parent"}, nil
}
func (*headerTestTracer) UnmarshalSpan(data map[string]string) (TracerSpanRef, error) {
	return data, nil
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
			tracer := &headerTestTracer{options: TracerOptions{HeaderKey: "trace", UseDataConverter: custom}}
			i := &tracingInterceptor{dataConverter: dc}
			dc := i.dataConverterOrDefault(tracer)
			header := map[string]*commonpb.Payload{}
			require.NoError(t, i.writeSpanToHeader(tracer, dc, nil, header))
			if custom {
				require.Equal(t, "binary/zlib", string(header["trace"].Metadata[converter.MetadataEncoding]))
			} else {
				expected, err := converter.GetDefaultDataConverter().ToPayload(map[string]string{"trace": "parent"})
				require.NoError(t, err)
				require.True(t, proto.Equal(expected, header["trace"]))
			}
			span, err := i.readSpanFromHeader(tracer, dc, header)
			require.NoError(t, err)
			require.Equal(t, map[string]string{"trace": "parent"}, span)
			span, err = i.readSpanFromHeader(tracer, dc, nil)
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
	tracer := &headerTestTracer{options: TracerOptions{HeaderKey: "trace", UseDataConverter: true}}
	i := &tracingInterceptor{dataConverter: dc}
	header := map[string]*commonpb.Payload{}
	require.ErrorIs(t, i.writeSpanToHeader(tracer, dc, nil, header), conversionErr)
	require.Empty(t, header)
	header["trace"] = &commonpb.Payload{}
	_, err := i.readSpanFromHeader(tracer, dc, header)
	require.ErrorIs(t, err, conversionErr)
	_, err = parentFromHeader(tracer, func() (TracerSpanRef, error) {
		return i.readSpanFromHeader(tracer, dc, header)
	})
	require.ErrorIs(t, err, conversionErr)
	tracer.options.AllowInvalidParentSpans = true
	span, err := parentFromHeader(tracer, func() (TracerSpanRef, error) {
		return i.readSpanFromHeader(tracer, dc, header)
	})
	require.NoError(t, err)
	require.Nil(t, span)
	nexusHeader := nexus.Header{}
	require.NoError(t, i.writeSpanToNexusHeader(tracer, nil, nexusHeader))
	span, err = i.readSpanFromNexusHeader(tracer, nexusHeader)
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

type headerWorkflowTracer struct {
	WorkflowTracer
	options TracerOptions
}

func (t *headerWorkflowTracer) Options() TracerOptions { return t.options }
func (*headerWorkflowTracer) MarshalSpan(TracerSpanRef) (map[string]string, error) {
	return map[string]string{"trace": "parent"}, nil
}
func (*headerWorkflowTracer) UnmarshalSpan(data map[string]string) (TracerSpanRef, error) {
	return data, nil
}

type headerContextInterceptor struct {
	interceptor.WorkerInterceptorBase
	check func(workflow.Context) error
}

func (i *headerContextInterceptor) InterceptWorkflow(_ workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	return &headerContextInboundInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next}, check: i.check,
	}
}

type headerContextInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	check func(workflow.Context) error
}

func (i *headerContextInboundInterceptor) ExecuteWorkflow(ctx workflow.Context, in *interceptor.ExecuteWorkflowInput) (any, error) {
	if err := i.check(ctx); err != nil {
		return nil, err
	}
	return i.Next.ExecuteWorkflow(ctx, in)
}

func TestTracingDataConverterContextBinding(t *testing.T) {
	codec := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	dc := &contextHeaderConverter{DataConverter: codec, t: t}
	opts := TracerOptions{HeaderKey: "trace", UseDataConverter: true}
	tracer := &headerTestTracer{options: opts}
	i := &tracingInterceptor{dataConverter: dc}
	ctx := context.WithValue(t.Context(), headerContextKey{}, "value")
	_, err := i.headerReader(tracer, ctx)()
	require.NoError(t, err)
	conversionErr := errors.New("bound converter error")
	dc.DataConverter = failingHeaderConverter{DataConverter: codec, err: conversionErr}
	require.ErrorIs(t, i.headerWriter(tracer, ctx)(nil), conversionErr)
	require.Equal(t, 2, dc.goCalls)
	dc.DataConverter = codec

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	check := &headerContextInterceptor{check: func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, headerContextKey{}, "value")
		tracer := &headerWorkflowTracer{options: opts}
		require.NotNil(t, interceptor.WorkflowHeader(ctx))
		require.NoError(t, i.workflowHeaderWriter(tracer, ctx)(nil))
		require.Equal(t, "binary/zlib", string(interceptor.WorkflowHeader(ctx)["trace"].Metadata[converter.MetadataEncoding]))
		span, err := i.workflowHeaderReader(tracer, ctx)()
		require.Equal(t, map[string]string{"trace": "parent"}, span)
		return err
	}}
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{check}})
	env.ExecuteWorkflow(func(workflow.Context) error { return nil })
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 2, dc.workflowCalls)
}

func TestTracingDataConverterPerTracer(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	for _, enabled := range []bool{false, true} {
		tracer := &headerTestTracer{options: TracerOptions{HeaderKey: "trace", UseDataConverter: enabled}}
		workflowTracer := &headerWorkflowTracer{options: TracerOptions{HeaderKey: "trace", UseDataConverter: !enabled}}
		clientInterceptor, workerInterceptor := NewTracingInterceptor(tracer, workflowTracer)
		client := clientInterceptor.(*tracingClientInterceptor).WithClientDataConverter(dc).(*tracingClientInterceptor)
		worker := workerInterceptor.(*tracingWorkerInterceptor).WithWorkerDataConverter(dc).(*tracingWorkerInterceptor)
		for _, root := range []*tracingInterceptor{client.root, worker.root} {
			for _, selected := range []tracerCommon{tracer, workflowTracer} {
				header := map[string]*commonpb.Payload{}
				require.NoError(t, root.writeSpanToHeader(selected, root.dataConverterOrDefault(selected), nil, header))
				encoding := "json/plain"
				if selected.Options().UseDataConverter {
					encoding = "binary/zlib"
				}
				require.Equal(t, encoding, string(header["trace"].Metadata[converter.MetadataEncoding]))
			}
		}
		require.Same(t, converter.GetDefaultDataConverter(), clientInterceptor.(*tracingClientInterceptor).root.dataConverter)
		require.Same(t, converter.GetDefaultDataConverter(), workerInterceptor.(*tracingWorkerInterceptor).root.dataConverter)
	}
}
