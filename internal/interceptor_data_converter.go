package internal

import "go.temporal.io/sdk/converter"

// Binding returns interceptor copies so an interceptor can be shared by clients
// and workers without changing another binding's converter.
func bindClientInterceptorDataConverter(interceptor ClientInterceptor, dc converter.DataConverter) ClientInterceptor {
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}
	if binder, ok := interceptor.(interface {
		WithClientDataConverter(converter.DataConverter) ClientInterceptor
	}); ok {
		return binder.WithClientDataConverter(dc)
	}
	return interceptor
}

func bindWorkerInterceptorsDataConverter(interceptors []WorkerInterceptor, dc converter.DataConverter) []WorkerInterceptor {
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}
	bound := make([]WorkerInterceptor, len(interceptors))
	for i, interceptor := range interceptors {
		if binder, ok := interceptor.(interface {
			WithWorkerDataConverter(converter.DataConverter) WorkerInterceptor
		}); ok {
			bound[i] = binder.WithWorkerDataConverter(dc)
		} else {
			bound[i] = interceptor
		}
	}
	return bound
}
