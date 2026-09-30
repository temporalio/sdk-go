package internal

import (
	"fmt"
	"reflect"
	"sync"

	"go.temporal.io/sdk/converter"
)

// NexusOperationKey identifies a system Nexus operation independently of its models.
// This API is experimental and intended for generated SDK integrations.
type NexusOperationKey struct {
	Service, Operation string
}

// NexusOperationRegistryEntry describes conversion of a system Nexus operation's inner payloads.
// This API is experimental and intended for generated SDK integrations.
type NexusOperationRegistryEntry struct {
	// InputType optionally restricts both callbacks to native inputs assignable to
	// this type. Nonmatching inputs (including untyped nil) retain ordinary Nexus
	// conversion, allowing raw wire requests to bypass native-model callbacks.
	// A nil InputType allows all inputs. Generated entries should use reflect.TypeFor[I]().
	InputType reflect.Type
	// SerializationContext is evaluated once against the native input, after workflow
	// interceptors. A nil callback or returned context preserves the caller's
	// converter scope, without skipping InputToTransfer.
	// Unlike Python's reevaluation, Go retains this selection for input, result, and
	// failure conversion for the lifetime of the operation. The callback must be
	// deterministic and safe for concurrent workflow executions.
	SerializationContext func(any) converter.SerializationContext
	// InputToTransfer optionally converts an external native model before envelope
	// encoding. For inputs accepted by InputType, it runs regardless of whether SerializationContext
	// selects a context. It receives the selected inner workflow context, or the
	// original caller context and converter when no context is selected. Models
	// with transfer converters do not need this callback. It must obey workflow rules.
	InputToTransfer func(Context, any) (any, error)
}

var nexusOperationRegistries struct {
	sync.RWMutex
	registries []map[NexusOperationKey]NexusOperationRegistryEntry
}

// RegisterNexusOperationRegistry registers system Nexus conversion policies at init
// time. It panics on duplicate keys, including keys in previously registered maps.
// Registration and lookup are concurrency safe. The SDK retains the map; callers
// must treat it and its policies as immutable after registration and register all
// policies before running workflows. Tests may temporarily replace entries only
// when no concurrent lookup or workflow execution is possible.
// This API is experimental and intended for generated SDK integrations.
func RegisterNexusOperationRegistry(registry map[NexusOperationKey]NexusOperationRegistryEntry) {
	nexusOperationRegistries.Lock()
	defer nexusOperationRegistries.Unlock()
	for key := range registry {
		for _, existing := range nexusOperationRegistries.registries {
			if _, ok := existing[key]; ok {
				panic(fmt.Sprintf("Nexus operation registry already contains service %q operation %q", key.Service, key.Operation))
			}
		}
	}
	nexusOperationRegistries.registries = append(nexusOperationRegistries.registries, registry)
}

func lookupNexusOperationRegistryEntry(endpoint, service, operation string) NexusOperationRegistryEntry {
	if endpoint != systemNexusEndpoint && endpoint != "temporal-system" {
		return NexusOperationRegistryEntry{}
	}
	nexusOperationRegistries.RLock()
	defer nexusOperationRegistries.RUnlock()
	key := NexusOperationKey{Service: service, Operation: operation}
	for _, registry := range nexusOperationRegistries.registries {
		if info, ok := registry[key]; ok {
			return info
		}
	}
	return NexusOperationRegistryEntry{}
}

// NexusOperationPayloadContext returns the SDK-captured inner workflow context for
// eager generated result adapters. It recognizes the optional carrier contract
// interface{ NexusOperationPayloadContext() Context }. If the future does not
// implement that contract or returns nil, this helper returns ctx.
//
// Interceptors wrapping futures must explicitly forward the carrier method by
// calling this helper with their captured workflow context and underlying future.
// Embedding NexusOperationFuture alone does not forward the method, because the
// carrier is deliberately not part of that public interface. A selected context
// retains the operation's original workflow context, not the context passed to Get.
// This API is experimental and intended for generated SDK integrations.
func NexusOperationPayloadContext(ctx Context, future NexusOperationFuture) Context {
	if carrier, ok := future.(interface{ NexusOperationPayloadContext() Context }); ok {
		if payloadContext := carrier.NexusOperationPayloadContext(); payloadContext != nil {
			return payloadContext
		}
	}
	return ctx
}

func (f *nexusOperationFutureImpl) NexusOperationPayloadContext() Context {
	return f.payloadContext
}
