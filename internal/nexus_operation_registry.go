package internal

import (
	"fmt"
	"reflect"

	"go.temporal.io/sdk/converter"
)

type NexusOperationKey struct {
	Service, Operation string
}

type NexusOperationRegistryEntry struct {
	InputType            reflect.Type
	SerializationContext func(any) converter.SerializationContext
	InputToTransfer      func(Context, any) (any, error)
}

var nexusOperationRegistry = make(map[NexusOperationKey]NexusOperationRegistryEntry)

func RegisterNexusOperationRegistry(registry map[NexusOperationKey]NexusOperationRegistryEntry) {
	for key, entry := range registry {
		if _, ok := nexusOperationRegistry[key]; ok {
			panic(fmt.Sprintf("Nexus operation registry already contains service %q operation %q", key.Service, key.Operation))
		}
		nexusOperationRegistry[key] = entry
	}
}

func lookupNexusOperationRegistryEntry(service, operation string) NexusOperationRegistryEntry {
	return nexusOperationRegistry[NexusOperationKey{Service: service, Operation: operation}]
}
