package main

import "testing"

func TestRewriteSystemNexusClient(t *testing.T) {
	input := `NewNexusClient("__temporal_system", "temporal.api.workflowservice.v1.WorkflowService")
commonconverter.SystemNexusConfig
internal.NexusOperationKey
internal.NexusOperationRegistryEntry
internal.UpdateNexusOperationRegistry`
	want := `internal.NewSystemNexusClient("temporal.api.workflowservice.v1.WorkflowService")
commonconverter.SystemNexusContext
internal.SystemNexusRegistryKey
internal.SystemNexusRegistryEntry
internal.UpdateSystemNexusRegistry`

	got, err := rewriteSystemNexusClient([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("rewritten bindings = %q, want %q", got, want)
	}
}
