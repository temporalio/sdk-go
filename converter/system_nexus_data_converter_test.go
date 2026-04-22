package converter

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	workflowservicev1 "go.temporal.io/api/workflowservice/v1"
	systemnexus "go.temporal.io/api/workflowservice/v1/workflowservicenexus/json"
	"google.golang.org/protobuf/proto"
)

func TestSystemNexusDataConverter(t *testing.T) {
	t.Parallel()

	dc := NewSystemNexusDataConverter()
	value := systemnexus.SignalWithStartWorkflowExecutionRequest{
		Namespace:  "default",
		WorkflowID: "workflow-id",
		SignalName: "signal-name",
		Input: &systemnexus.Payloads{
			Payloads: []systemnexus.Payload{{
				Data: "d29ya2Zsb3ctaW5wdXQ=",
				Metadata: map[string]string{
					"encoding": "anNvbi9wbGFpbg==",
				},
			}},
		},
	}

	payload, err := dc.ToPayload(value)
	require.NoError(t, err)
	require.Equal(t, []byte(MetadataEncodingProtoJSON), payload.Metadata[MetadataEncoding])
	require.Equal(
		t,
		[]byte((&workflowservicev1.SignalWithStartWorkflowExecutionRequest{}).ProtoReflect().Descriptor().FullName()),
		payload.Metadata[MetadataMessageType],
	)

	var decoded systemnexus.SignalWithStartWorkflowExecutionRequest
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, value, decoded)

	protoMessage, err := systemnexus.ToTemporalNexusProto(value)
	require.NoError(t, err)
	decodedProto := &workflowservicev1.SignalWithStartWorkflowExecutionRequest{}
	require.NoError(t, GetDefaultDataConverter().FromPayload(payload, decodedProto))
	require.True(t, proto.Equal(protoMessage, decodedProto))
}

func TestSystemNexusDataConverterToPayloads(t *testing.T) {
	t.Parallel()

	dc := NewSystemNexusDataConverter()
	payloads, err := dc.ToPayloads(
		systemnexus.SignalWithStartWorkflowExecutionResponse{RunID: "run-id"},
	)
	require.NoError(t, err)
	require.Len(t, payloads.Payloads, 1)
	require.Equal(t, []byte(MetadataEncodingProtoJSON), payloads.Payloads[0].Metadata[MetadataEncoding])

	var response systemnexus.SignalWithStartWorkflowExecutionResponse
	require.NoError(t, dc.FromPayloads(&commonpb.Payloads{Payloads: payloads.Payloads}, &response))
	require.Equal(t, "run-id", response.RunID)
}

func TestSystemNexusDataConverterRejectsUnannotatedTypes(t *testing.T) {
	t.Parallel()

	dc := NewSystemNexusDataConverter()

	_, err := dc.ToPayload("plain-value")
	require.Error(t, err)
	require.ErrorContains(t, err, "annotated generated types")

	payload, err := GetDefaultDataConverter().ToPayload("plain-value")
	require.NoError(t, err)

	var plain string
	err = dc.FromPayload(payload, &plain)
	require.Error(t, err)
	require.ErrorContains(t, err, "annotated generated types")
}
