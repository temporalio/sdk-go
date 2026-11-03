package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
)

type converterBindingInterceptor struct {
	InterceptorBase
	dc       converter.DataConverter
	clientDC *converter.DataConverter
}

func (i *converterBindingInterceptor) WithClientDataConverter(dc converter.DataConverter) ClientInterceptor {
	bound := *i
	bound.dc = dc
	return &bound
}

func (i *converterBindingInterceptor) WithWorkerDataConverter(dc converter.DataConverter) WorkerInterceptor {
	bound := *i
	bound.dc = dc
	return &bound
}

func (i *converterBindingInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	*i.clientDC = i.dc
	return next
}

func TestInterceptorDataConverterClientAndWorkerBinding(t *testing.T) {
	var clientDC converter.DataConverter
	shared := &converterBindingInterceptor{clientDC: &clientDC}
	for range 2 {
		dc := converter.NewCompositeDataConverter(converter.NewJSONPayloadConverter())
		c, err := NewLazyClient(ClientOptions{
			DataConverter: dc,
			Interceptors:  []ClientInterceptor{shared},
		})
		require.NoError(t, err)
		client := c.(*WorkflowClient)
		require.Same(t, client.dataConverter, clientDC)
		require.Same(t, shared, client.workerInterceptors[0])

		worker := NewAggregatedWorker(client, "converter-binding-test", WorkerOptions{
			Interceptors: []WorkerInterceptor{shared},
		})
		require.Len(t, worker.registry.interceptors, 2)
		for _, bound := range worker.registry.interceptors {
			require.NotSame(t, shared, bound)
			require.Same(t, client.dataConverter, bound.(*converterBindingInterceptor).dc)
		}
		require.Nil(t, shared.dc)
		c.Close()
	}
}

func TestInterceptorDataConverterReplayerBinding(t *testing.T) {
	shared := &converterBindingInterceptor{}
	for _, dc := range []converter.DataConverter{
		nil,
		converter.NewCompositeDataConverter(converter.NewJSONPayloadConverter()),
	} {
		replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{
			DataConverter: dc,
			Interceptors:  []WorkerInterceptor{shared},
		})
		require.NoError(t, err)
		bound := replayer.registry.interceptors[0].(*converterBindingInterceptor)
		require.Same(t, replayer.dataConverter, bound.dc)
		require.NotNil(t, bound.dc)
		require.Nil(t, shared.dc)
	}
}

func TestInterceptorDataConverterTestEnvironmentBinding(t *testing.T) {
	for _, converterFirst := range []bool{false, true} {
		var suite WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		dc := converter.NewCompositeDataConverter(converter.NewJSONPayloadConverter())
		shared := &converterBindingInterceptor{}
		if converterFirst {
			env.SetDataConverter(dc)
		}
		env.SetWorkerOptions(WorkerOptions{Interceptors: []WorkerInterceptor{shared}})
		if !converterFirst {
			env.SetDataConverter(dc)
		}
		bound := env.impl.registry.interceptors[0].(*converterBindingInterceptor)
		require.Same(t, env.impl.dataConverter, bound.dc)
		originalDC := bound.dc
		env.SetDataConverter(nil)
		rebound := env.impl.registry.interceptors[0].(*converterBindingInterceptor)
		require.NotNil(t, rebound.dc)
		require.NotSame(t, bound, rebound)
		require.Same(t, originalDC, bound.dc)
		require.Nil(t, shared.dc)
	}
}
