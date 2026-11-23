package internal

import (
	"context"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

type nexusInputInterceptor struct {
	WorkerInterceptorBase
	NexusOperationInboundInterceptorBase
	modify func(*NexusStartOperationInput)
}

func (i *nexusInputInterceptor) InterceptNexusOperation(_ context.Context, next NexusOperationInboundInterceptor) NexusOperationInboundInterceptor {
	return &nexusInputInterceptor{
		NexusOperationInboundInterceptorBase: NexusOperationInboundInterceptorBase{Next: next},
		modify:                               i.modify,
	}
}

func (i *nexusInputInterceptor) StartOperation(ctx context.Context, input NexusStartOperationInput) (nexus.HandlerStartOperationResult[any], error) {
	i.modify(&input)
	return i.Next.StartOperation(ctx, input)
}

type nexusInputCodec struct {
	converter.PayloadCodec
	decodes int
}

func (c *nexusInputCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	c.decodes++
	return c.PayloadCodec.Decode(payloads)
}

// TestNexusInterceptorRawInput checks that interceptors can replace raw input for both
// named and dynamic operations. Temporal-style RawValue consumers decode codecs once;
// direct Nexus handlers can still consume a concrete type through their full converter.
func TestNexusInterceptorRawInput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		dynamic     bool
		typed       bool
		wantDecodes int
	}{
		{name: "named raw", wantDecodes: 1},
		{name: "named typed", typed: true, wantDecodes: 1},
		{name: "dynamic raw", dynamic: true, wantDecodes: 1},
		{name: "dynamic typed", dynamic: true, typed: true, wantDecodes: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := converter.GetDefaultDataConverter()
			codec := &nexusInputCodec{PayloadCodec: converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true})}
			codecDC := converter.NewCodecDataConverter(dc, codec)
			payload, err := codecDC.ToPayload("original")
			require.NoError(t, err)
			replacement, err := dc.ToPayload("intercepted")
			require.NoError(t, err)
			echo := func(_ context.Context, raw converter.RawValue, _ nexus.StartOperationOptions) (any, error) {
				var value string
				err := dc.FromPayload(raw.Payload(), &value)
				return value, err
			}
			service := nexus.NewService("tools")
			if tc.dynamic {
				require.NoError(t, service.RegisterDynamic(nexus.NewSyncOperation("", func(ctx context.Context, input *nexus.LazyValue, opts nexus.StartOperationOptions) (any, error) {
					if tc.typed {
						var value string
						err := input.Consume(&value)
						return value, err
					}
					var raw converter.RawValue
					if err := input.Consume(&raw); err != nil {
						return nil, err
					}
					return echo(ctx, raw, opts)
				})))
			} else if tc.typed {
				service.MustRegister(nexus.NewSyncOperation("echo", func(_ context.Context, value string, _ nexus.StartOperationOptions) (any, error) {
					return value, nil
				}))
			} else {
				service.MustRegister(nexus.NewSyncOperation("echo", echo))
			}
			intercepted := false
			interceptor := &nexusInputInterceptor{modify: func(input *NexusStartOperationInput) {
				intercepted = true
				if tc.typed && !tc.dynamic {
					require.Equal(t, "original", input.Input)
					input.Input = "intercepted"
					return
				}
				raw, ok := input.Input.(converter.RawValue)
				require.True(t, ok, "interceptor received %T", input.Input)
				var value string
				require.NoError(t, dc.FromPayload(raw.Payload(), &value))
				require.Equal(t, "original", value)
				input.Input = converter.NewRawValue(replacement)
			}}
			registry := nexus.NewServiceRegistry()
			registry.MustRegister(service)
			registry.Use(nexusMiddleware([]WorkerInterceptor{interceptor}))
			handler, err := registry.NewHandler()
			require.NoError(t, err)
			serializer := &payloadSerializer{converter: codecDC, payload: payload}
			ctx := ContextWithNexusOperationContext(t.Context(), &NexusOperationContext{inputSerializer: serializer})
			result, err := handler.StartOperation(ctx, "tools", "echo", nexus.NewLazyValue(
				serializer, &nexus.Reader{ReadCloser: emptyReaderNopCloser},
			), nexus.StartOperationOptions{})
			require.NoError(t, err)
			require.True(t, intercepted)
			require.Equal(t, "intercepted", result.(*nexus.HandlerStartOperationResultSync[any]).Value)
			require.Equal(t, tc.wantDecodes, codec.decodes)
		})
	}
}
