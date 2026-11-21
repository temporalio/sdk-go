package internal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
)

func TestSystemNexusStorageTargetMetadata(t *testing.T) {
	entry := systemNexusRegistry[SystemNexusRegistryKey{
		Service:   "temporal.api.workflowservice.v1.WorkflowService",
		Operation: "SignalWithStartWorkflowExecution",
	}]
	require.NotNil(t, entry.StorageTarget)
	target := entry.StorageTarget(systemnexus_signalWithStartWorkflowRequest{
		Namespace: "destination-namespace", ID: "destination-id", Workflow: "DestinationWorkflow",
	})
	require.Equal(t, converter.StorageDriverWorkflowInfo{
		Namespace: "destination-namespace", WorkflowID: "destination-id", WorkflowType: "DestinationWorkflow",
	}, target)
	encoded, err := json.Marshal(target)
	require.NoError(t, err)
	caller := converter.StorageDriverWorkflowInfo{
		Namespace: "worker-namespace", WorkflowID: "caller-id", RunID: "caller-run",
	}
	for _, tt := range []struct {
		name      string
		endpoint  string
		metadata  []byte
		want      converter.StorageDriverWorkflowInfo
		wantError bool
	}{
		{name: "destination", endpoint: systemNexusEndpoint, metadata: encoded, want: target},
		{name: "ordinary Nexus", endpoint: "ordinary", metadata: encoded, want: caller},
		{name: "no target factory", endpoint: systemNexusEndpoint, want: caller},
		{name: "invalid metadata", endpoint: systemNexusEndpoint, metadata: []byte("{"), wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := extstore.WithStorageTarget(t.Context(), caller)
			resolved, err := (&commandAwarePayloadVisitor{}).ContextHook(base,
				&commandpb.ScheduleNexusOperationCommandAttributes{
					Endpoint: tt.endpoint,
					Input:    &commonpb.Payload{Metadata: map[string][]byte{systemNexusStorageTargetMetadataKey: tt.metadata}},
				})
			if tt.wantError {
				require.ErrorContains(t, err, "decode System Nexus storage target")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, extstore.StorageTargetFromContext(resolved))
			}
			require.Equal(t, caller, extstore.StorageTargetFromContext(base))
		})
	}
}
