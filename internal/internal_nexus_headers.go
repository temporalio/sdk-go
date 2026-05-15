// Reserved Temporal headers carrying Nexus identity from the Nexus task
// handler to the spawned workflow on a workflow-backed Nexus operation.
//
// These three keys form the SDK's wire contract for propagating Nexus identity
// across the workflow-start boundary. Their values are raw binary/plain
// payloads (no DataConverter applied at the encode site) so a user codec keyed
// by Endpoint cannot encrypt the very payload that carries the Endpoint name
// (bootstrap circularity). All Temporal SDKs MUST agree on identical key
// names; cross-SDK changes require coordinated SDK upgrades.

package internal

import (
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
)

// Reserved Nexus identity header keys. Lowercase, __temporal_ reserved prefix
// per internal/internal_utils.go. Cross-SDK contract — Java/Python/TS adopt
// identical strings.
const (
	NexusEndpointHeaderKey  = "__temporal_nexus_endpoint"
	NexusServiceHeaderKey   = "__temporal_nexus_service"
	NexusOperationHeaderKey = "__temporal_nexus_operation"
)

// NewRawStringHeaderPayload builds a binary/plain payload carrying value as
// raw UTF-8 bytes. Used for Nexus identity headers so a user codec keyed by
// Endpoint cannot encrypt the very payload that carries the Endpoint name
// (bootstrap circularity). Do NOT route through DataConverter.ToPayload.
func NewRawStringHeaderPayload(value string) *commonpb.Payload {
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte(converter.MetadataEncodingBinary),
		},
		Data: []byte(value),
	}
}

// nexusIdentityFromCallbacks returns endpoint/service/operation/ok by scanning
// the supplied callbacks for the first Nexus callback whose header carries the
// three reserved __temporal_nexus_* keys. The handler-side writer at
// temporalnexus/operation.go mirrors these onto the callback header so the
// spawned workflow can recover Nexus identity without routing payloads through
// the workflow's data converter.
func nexusIdentityFromCallbacks(callbacks []*commonpb.Callback) (endpoint, service, operation string, ok bool) {
	for _, cb := range callbacks {
		ncb := cb.GetNexus()
		if ncb == nil {
			continue
		}
		h := ncb.GetHeader()
		ep, okE := h[NexusEndpointHeaderKey]
		svc, okS := h[NexusServiceHeaderKey]
		op, okO := h[NexusOperationHeaderKey]
		if okE && okS && okO {
			return ep, svc, op, true
		}
	}
	return "", "", "", false
}
