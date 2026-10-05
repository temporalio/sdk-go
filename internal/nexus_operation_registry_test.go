package internal

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

type registryModel struct{ Value string }

func (registryModel) TransferTypeConverter() (TransferTypeConverter, error) {
	return NewTransferTypeConverter[registryModel, commonpb.Payload](nil, nil,
		func(ctx Context, value *registryModel) (*commonpb.Payload, error) {
			return GetDataConverterFromWorkflowContext(ctx).ToPayload(value.Value)
		},
		func(ctx Context, payload *commonpb.Payload, value *registryModel) error {
			return GetDataConverterFromWorkflowContext(ctx).FromPayload(payload, &value.Value)
		})
}

// Binding records the converter installed in the workflow context. This catches
// accidental rebinding of the envelope parent to the inner payload context.
type registryBindingDC struct {
	converter.DataConverter
	scope   converter.SerializationContext
	binding converter.SerializationContext
}

func (dc *registryBindingDC) WithSerializationContext(sc converter.SerializationContext) converter.DataConverter {
	result := *dc
	result.scope = sc
	result.DataConverter = converter.WithDataConverterSerializationContext(dc.DataConverter, sc)
	return &result
}
func (dc *registryBindingDC) WithWorkflowContext(ctx Context) converter.DataConverter {
	result := *dc
	installed := makeTransferAware(getWorkflowEnvOptions(ctx).DataConverter).parent
	if bound, ok := installed.(*registryBindingDC); ok {
		result.binding = bound.scope
	}
	return &result
}
func (dc *registryBindingDC) WithContext(context.Context) converter.DataConverter { return dc }

func TestNexusOperationRegistry(t *testing.T) {
	service := "registry-test"
	registry := map[NexusOperationKey]NexusOperationRegistryEntry{}
	calls := 0
	for _, operation := range []string{"first", "second", "eager"} {
		registry[NexusOperationKey{service, operation}] = NexusOperationRegistryEntry{
			SerializationContext: func(input any) converter.SerializationContext {
				calls++
				return converter.WorkflowSerializationContext{Namespace: operation, WorkflowID: operation + ":" + input.(registryModel).Value}
			},
		}
	}
	eager := registry[NexusOperationKey{service, "eager"}]
	eager.InputToTransfer = func(ctx Context, input any) (any, error) {
		return GetDataConverterFromWorkflowContext(ctx).ToPayload(input.(registryModel).Value)
	}
	registry[NexusOperationKey{service, "eager"}] = eager
	RegisterNexusOperationRegistry(registry)
	t.Cleanup(func() { nexusOperationRegistries.Lock(); defer nexusOperationRegistries.Unlock(); clear(registry) })
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	env.SetDataConverter(&registryBindingDC{DataConverter: converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})})
	fc := newNexusCapturingFailureConverter()
	env.SetFailureConverter(fc)
	interceptor, ctx, err := newWorkflowContext(env.impl, env.impl.GetRegistry().interceptors)
	require.NoError(t, err)
	capture := &captureNexusSerializationEnv{WorkflowEnvironment: interceptor.env}
	interceptor.env = capture
	var futures []NexusOperationFuture
	var results [3]registryModel
	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		for i, operation := range []string{"first", "second", "eager"} {
			client := NewSystemNexusClient(service)
			if i == 1 {
				client = NewNexusClient("temporal-system", service)
			}
			callCtx := ctx
			if i == 2 {
				wrapper := &registryWrappingInterceptor{WorkflowOutboundInterceptorBase: WorkflowOutboundInterceptorBase{Next: interceptor}}
				callCtx = WithValue(ctx, workflowInterceptorContextKey, wrapper)
			}
			futures = append(futures, client.ExecuteOperation(callCtx, operation, registryModel{Value: "target"}, NexusOperationOptions{}))
		}
		for i := len(capture.calls) - 1; i >= 0; i-- {
			call := capture.calls[i]
			dc := call.params.dataConverter.(*transferAwareDataConverter)
			outer := dc.parent.(*registryBindingDC)
			require.IsType(t, converter.NexusSerializationContext{}, outer.scope)
			require.Nil(t, outer.binding)
			require.NotEqual(t, converter.WorkflowSerializationContext{Namespace: call.params.operation, WorkflowID: call.params.operation + ":target"}, outer.binding)
			inner := NexusOperationPayloadContext(ctx, futures[i])
			require.Same(t, call.params.payloadContext, inner)
			var wire commonpb.Payload
			require.NoError(t, outer.FromPayload(call.params.input, &wire))
			require.Equal(t, call.params.operation+":target", string(wire.Metadata["ctx-signature"]))
			payload, err := dc.ToPayload(registryModel{Value: "result"})
			require.NoError(t, err)
			call.started("token", nil)
			call.completed(payload, nil)
			failure := call.params.failureConverter.ErrorToFailure(errors.New("failure"))
			require.Error(t, call.params.failureConverter.FailureToError(failure))
		}
		for i, future := range futures {
			if i == 2 {
				require.IsType(t, &registryWrappedFuture{}, future)
				var wire commonpb.Payload
				require.NoError(t, future.Get(ctx, &wire))
				require.NoError(t, GetDataConverterFromWorkflowContext(NexusOperationPayloadContext(ctx, future)).FromPayload(&wire, &results[i].Value))
			} else {
				require.NoError(t, future.Get(ctx, &results[i]))
			}
		}
	}, func() bool { return false })
	d.interceptor = interceptor
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.Equal(t, 3, calls)
	require.Equal(t, [3]registryModel{{"result"}, {"result"}, {"result"}}, results)
	for i, conversion := range fc.captured() {
		operation := []string{"eager", "second", "first"}[i/2]
		require.Equal(t, converter.WorkflowSerializationContext{Namespace: operation, WorkflowID: operation + ":target"}, conversion.context)
	}
}

type registryWrappedFuture struct {
	NexusOperationFuture
	ctx Context
}

func (f *registryWrappedFuture) NexusOperationPayloadContext() Context {
	return NexusOperationPayloadContext(f.ctx, f.NexusOperationFuture)
}

type registryWrappingInterceptor struct {
	WorkflowOutboundInterceptorBase
}

func (i *registryWrappingInterceptor) ExecuteNexusOperation(ctx Context, input ExecuteNexusOperationInput) NexusOperationFuture {
	return &registryWrappedFuture{NexusOperationFuture: i.Next.ExecuteNexusOperation(ctx, input), ctx: ctx}
}

type registryInputInterceptor struct {
	WorkflowOutboundInterceptorBase
	seen any
}

func (i *registryInputInterceptor) ExecuteNexusOperation(ctx Context, input ExecuteNexusOperationInput) NexusOperationFuture {
	i.seen = input.Input
	input.Input = registryModel{Value: "replacement"}
	return i.Next.ExecuteNexusOperation(ctx, input)
}

func TestNexusOperationRegistryAfterInterceptors(t *testing.T) {
	key := NexusOperationKey{Service: "registry-interceptor", Operation: "eager"}
	var selected, converted any
	conversionErr := errors.New("input transfer failed")
	registry := map[NexusOperationKey]NexusOperationRegistryEntry{key: {
		SerializationContext: func(input any) converter.SerializationContext {
			selected = input
			return converter.WorkflowSerializationContext{WorkflowID: input.(registryModel).Value}
		},
		InputToTransfer: func(ctx Context, input any) (any, error) {
			converted = input
			return nil, conversionErr
		},
	}}
	RegisterNexusOperationRegistry(registry)
	t.Cleanup(func() { nexusOperationRegistries.Lock(); defer nexusOperationRegistries.Unlock(); clear(registry) })
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	interceptor, ctx, err := newWorkflowContext(env.impl, nil)
	require.NoError(t, err)
	capture := &captureNexusSerializationEnv{WorkflowEnvironment: interceptor.env}
	interceptor.env = capture
	replacer := &registryInputInterceptor{WorkflowOutboundInterceptorBase: WorkflowOutboundInterceptorBase{Next: interceptor}}
	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		ctx = WithValue(ctx, workflowInterceptorContextKey, replacer)
		future := NewSystemNexusClient(key.Service).ExecuteOperation(ctx, key.Operation, registryModel{Value: "original"}, NexusOperationOptions{})
		require.ErrorIs(t, future.Get(ctx, nil), conversionErr)
		require.ErrorIs(t, future.GetNexusOperationExecution().Get(ctx, nil), conversionErr)
	}, func() bool { return false })
	d.interceptor = interceptor
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.Equal(t, registryModel{Value: "original"}, replacer.seen)
	require.Equal(t, registryModel{Value: "replacement"}, selected)
	require.Equal(t, selected, converted)
	require.Empty(t, capture.calls)
}

func TestNexusOperationRegistryExternalInputWithoutSelectedContext(t *testing.T) {
	// This external model deliberately has no transfer converter of its own.
	type externalRequest struct{ Value string }
	for _, endpoint := range []string{systemNexusEndpoint, "temporal-system"} {
		for _, nilCallback := range []bool{true, false} {
			name := endpoint + "/nil-result"
			if nilCallback {
				name = endpoint + "/nil-callback"
			}
			t.Run(name, func(t *testing.T) {
				key := NexusOperationKey{Service: t.Name(), Operation: "external"}
				env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
				interceptor, ctx, err := newWorkflowContext(env.impl, nil)
				require.NoError(t, err)
				callerDC := converter.WithDataConverterSerializationContext(
					converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{}),
					converter.WorkflowSerializationContext{WorkflowID: "caller"},
				)
				ctx = WithDataConverter(ctx, callerDC)
				calls := 0
				entry := NexusOperationRegistryEntry{
					InputType: reflect.TypeFor[externalRequest](),
					InputToTransfer: func(transferCtx Context, input any) (any, error) {
						calls++
						require.Same(t, ctx, transferCtx)
						require.Equal(t, externalRequest{Value: "native"}, input)
						return GetDataConverterFromWorkflowContext(transferCtx).ToPayload(input.(externalRequest).Value)
					},
				}
				if !nilCallback {
					entry.SerializationContext = func(input any) converter.SerializationContext {
						require.Equal(t, externalRequest{Value: "native"}, input)
						return nil
					}
				}
				registry := map[NexusOperationKey]NexusOperationRegistryEntry{key: entry}
				RegisterNexusOperationRegistry(registry)
				t.Cleanup(func() { nexusOperationRegistries.Lock(); defer nexusOperationRegistries.Unlock(); clear(registry) })
				params, err := interceptor.prepareNexusOperationParams(ctx, ExecuteNexusOperationInput{
					Client: nexusClient{endpoint, key.Service}, Operation: key.Operation, Input: externalRequest{Value: "native"},
				})
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.Nil(t, params.payloadContext)
				require.Equal(t, endpoint+":"+key.Service+":"+key.Operation, string(params.input.Metadata["ctx-signature"]))
				var wire commonpb.Payload
				require.NoError(t, params.dataConverter.FromPayload(params.input, &wire))
				require.Equal(t, "caller", string(wire.Metadata["ctx-signature"]))
				var value string
				require.NoError(t, callerDC.FromPayload(&wire, &value))
				require.Equal(t, "native", value)

				conversionErr := errors.New("external conversion failed")
				entry.InputToTransfer = func(transferCtx Context, input any) (any, error) {
					require.Same(t, ctx, transferCtx)
					return nil, conversionErr
				}
				registry[key] = entry
				_, err = interceptor.prepareNexusOperationParams(ctx, ExecuteNexusOperationInput{
					Client: nexusClient{endpoint, key.Service}, Operation: key.Operation, Input: externalRequest{Value: "native"},
				})
				require.ErrorIs(t, err, conversionErr)
			})
		}
	}
}

func TestNexusOperationRegistryInputType(t *testing.T) {
	type nativeRequest struct{ Value string }
	wire := &commonpb.Payload{Data: []byte("raw wire request")}
	for _, endpoint := range []string{systemNexusEndpoint, "temporal-system"} {
		for _, tc := range []struct {
			name          string
			inputType     reflect.Type
			input         any
			wantCallbacks bool
		}{
			{name: "raw proto", inputType: reflect.TypeFor[nativeRequest](), input: wire},
			{name: "untyped nil", inputType: reflect.TypeFor[nativeRequest]()},
			{name: "wrong native type", inputType: reflect.TypeFor[nativeRequest](), input: "native"},
			{name: "matching native", inputType: reflect.TypeFor[nativeRequest](), input: nativeRequest{"native"}, wantCallbacks: true},
			{name: "assignable interface", inputType: reflect.TypeFor[any](), input: nativeRequest{"native"}, wantCallbacks: true},
			{name: "unrestricted proto", input: wire, wantCallbacks: true},
			{name: "unrestricted nil", wantCallbacks: true},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				key := NexusOperationKey{Service: t.Name(), Operation: "operation"}
				policyCalls, transferCalls := 0, 0
				registry := map[NexusOperationKey]NexusOperationRegistryEntry{key: {
					InputType: tc.inputType,
					SerializationContext: func(input any) converter.SerializationContext {
						policyCalls++
						require.Equal(t, tc.input, input)
						return converter.WorkflowSerializationContext{WorkflowID: "target"}
					},
					InputToTransfer: func(ctx Context, input any) (any, error) {
						transferCalls++
						require.Equal(t, tc.input, input)
						return "converted", nil
					},
				}}
				RegisterNexusOperationRegistry(registry)
				t.Cleanup(func() { nexusOperationRegistries.Lock(); defer nexusOperationRegistries.Unlock(); clear(registry) })
				env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
				env.SetDataConverter(converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{}))
				interceptor, ctx, err := newWorkflowContext(env.impl, nil)
				require.NoError(t, err)
				capture := &captureNexusSerializationEnv{WorkflowEnvironment: interceptor.env}
				interceptor.env = capture
				d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
					future := nexusClient{endpoint, key.Service}.ExecuteOperation(ctx, key.Operation, tc.input, NexusOperationOptions{})
					require.Len(t, capture.calls, 1)
					call := capture.calls[0]
					want := tc.input
					if tc.wantCallbacks {
						want = "converted"
						require.NotNil(t, call.params.payloadContext)
					} else {
						require.Nil(t, call.params.payloadContext)
						require.Same(t, ctx, NexusOperationPayloadContext(ctx, future))
					}
					ordinaryDC := WithRootDataConverterSerializationContext(ctx, converter.NexusSerializationContext{
						Endpoint: endpoint, Service: key.Service, Operation: key.Operation,
					})
					wantPayload, err := ordinaryDC.ToPayload(want)
					require.NoError(t, err)
					require.Equal(t, wantPayload, call.params.input)
					call.started("token", nil)
					call.completed(wantPayload, nil)
					require.NoError(t, future.Get(ctx, nil))
				}, func() bool { return false })
				d.interceptor = interceptor
				defer d.Close()
				requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
				wantCalls := 0
				if tc.wantCallbacks {
					wantCalls = 1
				}
				require.Equal(t, wantCalls, policyCalls)
				require.Equal(t, wantCalls, transferCalls)
			})
		}
	}
}

func TestNexusOperationPayloadContextCarrierFallback(t *testing.T) {
	ctx := Background()
	future := &nexusOperationFutureImpl{}
	require.Same(t, ctx, NexusOperationPayloadContext(ctx, future))
	// Embedding the public future interface alone must remain valid, without
	// implicitly opting into the carrier contract.
	wrapped := struct{ NexusOperationFuture }{future}
	require.Same(t, ctx, NexusOperationPayloadContext(ctx, wrapped))
}

func TestNexusOperationRegistryFallbackAndDuplicates(t *testing.T) {
	key := NexusOperationKey{Service: "registry-fallback", Operation: "nil"}
	registry := map[NexusOperationKey]NexusOperationRegistryEntry{key: {}}
	RegisterNexusOperationRegistry(registry)
	t.Cleanup(func() { nexusOperationRegistries.Lock(); defer nexusOperationRegistries.Unlock(); clear(registry) })
	require.Panics(t, func() { RegisterNexusOperationRegistry(map[NexusOperationKey]NexusOperationRegistryEntry{key: {}}) })
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	interceptor, ctx, err := newWorkflowContext(env.impl, nil)
	require.NoError(t, err)
	for _, endpoint := range []string{systemNexusEndpoint, "temporal-system", "ordinary"} {
		for _, operation := range []string{"nil", "missing"} {
			params, err := interceptor.prepareNexusOperationParams(ctx, ExecuteNexusOperationInput{Client: nexusClient{endpoint, key.Service}, Operation: operation, Input: "native"})
			require.NoError(t, err)
			require.Nil(t, params.payloadContext)
		}
	}
	registry[key] = NexusOperationRegistryEntry{
		SerializationContext: func(any) converter.SerializationContext { panic("ordinary endpoint must not look up policy") },
		InputToTransfer:      func(Context, any) (any, error) { panic("ordinary endpoint must not convert input") },
	}
	_, err = interceptor.prepareNexusOperationParams(ctx, ExecuteNexusOperationInput{Client: NewNexusClient("ordinary", key.Service), Operation: key.Operation, Input: "native"})
	require.NoError(t, err)
	require.Same(t, ctx, NexusOperationPayloadContext(ctx, nil))
	registry[key] = NexusOperationRegistryEntry{}
}
