package internal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

type registryModel struct{ Value string }

func (registryModel) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return converter.NewContextualTransferTypeConverter[registryModel, commonpb.Payload](nil, nil,
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
	binding Context
	bound   *[]*registryBindingDC
}

func (dc *registryBindingDC) WithSerializationContext(sc converter.SerializationContext) converter.DataConverter {
	result := *dc
	result.scope = sc
	result.DataConverter = converter.WithDataConverterSerializationContext(dc.DataConverter, sc)
	return &result
}
func (dc *registryBindingDC) WithWorkflowContext(ctx Context) converter.DataConverter {
	result := *dc
	result.binding = ctx
	*result.bound = append(*result.bound, &result)
	return &result
}
func (dc *registryBindingDC) WithContext(context.Context) converter.DataConverter { return dc }

func TestNexusOperationRegistry(t *testing.T) {
	service := "registry-test"
	registry := map[NexusOperationKey]NexusOperationRegistryEntry{}
	calls := 0
	for _, operation := range []string{"first", "second", "wrapped"} {
		registry[NexusOperationKey{service, operation}] = NexusOperationRegistryEntry{
			SerializationContext: func(input any) converter.SerializationContext {
				calls++
				return converter.WorkflowSerializationContext{Namespace: operation, WorkflowID: operation + ":" + input.(registryModel).Value}
			},
		}
	}
	RegisterNexusOperationRegistry(registry)
	t.Cleanup(func() {
		for key := range registry {
			delete(nexusOperationRegistry, key)
		}
	})
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	var bound []*registryBindingDC
	env.SetDataConverter(&registryBindingDC{
		DataConverter: converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{}),
		bound:         &bound,
	})
	fc := newNexusCapturingFailureConverter()
	env.SetFailureConverter(fc)
	interceptor, ctx, err := newWorkflowContext(env.impl, env.impl.GetRegistry().interceptors)
	require.NoError(t, err)
	capture := &captureNexusSerializationEnv{WorkflowEnvironment: interceptor.env}
	interceptor.env = capture
	var futures []NexusOperationFuture
	var results [3]registryModel
	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		for i, operation := range []string{"first", "second", "wrapped"} {
			client := NewSystemNexusClient(service)
			if i == 1 {
				client = NewNexusClient("temporal-system", service)
			} else if i == 2 {
				client = NewNexusClient("ordinary", service)
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
			dc := call.params.dataConverter
			var outer *registryBindingDC
			for _, binding := range bound {
				if scope, ok := binding.scope.(converter.NexusSerializationContext); ok && scope.Operation == call.params.operation {
					require.Same(t, getWorkflowEnvOptions(ctx).DataConverter, getWorkflowEnvOptions(binding.binding).DataConverter)
					outer = binding
				}
			}
			require.NotNil(t, outer)
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
			}
			require.NoError(t, future.Get(ctx, &results[i]))
		}
	}, func() bool { return false })
	d.interceptor = interceptor
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.Equal(t, 3, calls)
	require.Equal(t, [3]registryModel{{"result"}, {"result"}, {"result"}}, results)
	for i, conversion := range fc.captured() {
		operation := []string{"wrapped", "second", "first"}[i/2]
		require.Equal(t, converter.WorkflowSerializationContext{Namespace: operation, WorkflowID: operation + ":target"}, conversion.context)
	}
}

type registryWrappedFuture struct {
	NexusOperationFuture
}

type registryWrappingInterceptor struct {
	WorkflowOutboundInterceptorBase
}

func (i *registryWrappingInterceptor) ExecuteNexusOperation(ctx Context, input ExecuteNexusOperationInput) NexusOperationFuture {
	return &registryWrappedFuture{NexusOperationFuture: i.Next.ExecuteNexusOperation(ctx, input)}
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
	key := NexusOperationKey{Service: "registry-interceptor", Operation: "operation"}
	var selected any
	registry := map[NexusOperationKey]NexusOperationRegistryEntry{key: {
		SerializationContext: func(input any) converter.SerializationContext {
			selected = input
			return converter.WorkflowSerializationContext{WorkflowID: input.(registryModel).Value}
		},
	}}
	RegisterNexusOperationRegistry(registry)
	t.Cleanup(func() { delete(nexusOperationRegistry, key) })
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	env.SetDataConverter(converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{}))
	interceptor, ctx, err := newWorkflowContext(env.impl, nil)
	require.NoError(t, err)
	capture := &captureNexusSerializationEnv{WorkflowEnvironment: interceptor.env}
	interceptor.env = capture
	replacer := &registryInputInterceptor{WorkflowOutboundInterceptorBase: WorkflowOutboundInterceptorBase{Next: interceptor}}
	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		ctx = WithValue(ctx, workflowInterceptorContextKey, replacer)
		future := NewSystemNexusClient(key.Service).ExecuteOperation(ctx, key.Operation, registryModel{Value: "original"}, NexusOperationOptions{})
		require.Len(t, capture.calls, 1)
		call := capture.calls[0]
		var wire commonpb.Payload
		require.NoError(t, call.params.dataConverter.FromPayload(call.params.input, &wire))
		require.Equal(t, "replacement", string(wire.Metadata["ctx-signature"]))
		var value string
		targetDC := withRootDataConverterSerializationContext(ctx, converter.WorkflowSerializationContext{WorkflowID: "replacement"})
		require.NoError(t, targetDC.FromPayload(&wire, &value))
		require.Equal(t, "replacement", value)
		call.started("token", nil)
		call.completed(nil, nil)
		require.NoError(t, future.Get(ctx, nil))
		require.NoError(t, future.GetNexusOperationExecution().Get(ctx, nil))
	}, func() bool { return false })
	d.interceptor = interceptor
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.Equal(t, registryModel{Value: "original"}, replacer.seen)
	require.Equal(t, registryModel{Value: "replacement"}, selected)
}

func TestNexusOperationRegistryMissingEntry(t *testing.T) {
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	interceptor, ctx, err := newWorkflowContext(env.impl, nil)
	require.NoError(t, err)
	for _, endpoint := range []string{systemNexusEndpoint, "temporal-system", "ordinary"} {
		params, err := interceptor.prepareNexusOperationParams(ctx, ExecuteNexusOperationInput{
			Client: nexusClient{endpoint, t.Name()}, Operation: "missing", Input: "native",
		})
		require.NoError(t, err)
		var value string
		require.NoError(t, params.dataConverter.FromPayload(params.input, &value))
		require.Equal(t, "native", value)
	}
}

func TestNexusOperationRegistryMergesEntries(t *testing.T) {
	first := NexusOperationKey{Service: t.Name(), Operation: "first"}
	second := NexusOperationKey{Service: t.Name(), Operation: "second"}
	t.Cleanup(func() {
		delete(nexusOperationRegistry, first)
		delete(nexusOperationRegistry, second)
	})
	sc := converter.WorkflowSerializationContext{WorkflowID: "target"}
	entry := NexusOperationRegistryEntry{
		SerializationContext: func(any) converter.SerializationContext { return sc },
	}
	registry := map[NexusOperationKey]NexusOperationRegistryEntry{first: entry}
	RegisterNexusOperationRegistry(registry)
	clear(registry)
	RegisterNexusOperationRegistry(map[NexusOperationKey]NexusOperationRegistryEntry{second: entry})
	require.Equal(t, sc, nexusOperationRegistry[first].SerializationContext(registryModel{}))
	require.Equal(t, sc, nexusOperationRegistry[second].SerializationContext(registryModel{}))
	require.PanicsWithValue(t,
		fmt.Sprintf("Nexus operation registry already contains service %q operation %q", first.Service, first.Operation),
		func() {
			RegisterNexusOperationRegistry(map[NexusOperationKey]NexusOperationRegistryEntry{first: entry})
		},
	)
	require.Equal(t, sc, nexusOperationRegistry[first].SerializationContext(registryModel{}))
}
