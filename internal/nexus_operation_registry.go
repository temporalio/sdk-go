package internal

import (
	"fmt"

	"go.temporal.io/sdk/converter"
)

var systemNexusRegistry = make(map[SystemNexusRegistryKey]SystemNexusRegistryEntry)

type SystemNexusRegistryKey struct {
	// Service is the fully-qualified name of the temporal service that the
	// operation belongs to, e.g. "temporal.api.workflowservice.v1.WorkflowService".
	Service string
	// Operation is the name of an RPC op, e.g. "SignalWithStartWorkflowExecution".
	Operation string
}

type SystemNexusRegistryEntry struct {
	// SerializationContext computes a serialization context for the given
	// system nexus request. The SDK uses that context to encode payloads
	// inside the request.
	SerializationContext func(request any) converter.SerializationContext
}

// UpdateSystemNexusRegistry merges newEntries into the system nexus registry,
// panicking if any of its keys are already in the registry. The function is
// exported so other packages in the SDK can register System Nexus operations.
// But it shouldn't be accessible outside the SDK.
func UpdateSystemNexusRegistry(newEntries map[SystemNexusRegistryKey]SystemNexusRegistryEntry) {
	for key, entry := range newEntries {
		if _, ok := systemNexusRegistry[key]; ok {
			panic(fmt.Sprintf("Nexus operation registry already contains service %q operation %q", key.Service, key.Operation))
		}
		systemNexusRegistry[key] = entry
	}
}
