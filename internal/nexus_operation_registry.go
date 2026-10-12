package internal

import (
	"fmt"

	"go.temporal.io/sdk/converter"
)

type NexusOperationKey struct {
	Service, Operation string
}

type NexusOperationRegistryEntry struct {
	SerializationContext func(any) converter.SerializationContext
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
