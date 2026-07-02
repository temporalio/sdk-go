package workflow

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

// TestSignalWithStartEnvelopeVisitedByAPIGoProxy verifies the end-to-end
// contract between the generated system Nexus bindings and temporal-api-go's
// payload visitor: a request built by the generated bindings, serialized
// proto-binary into a ScheduleNexusOperationCommandAttributes.Input, has its
// inner payloads (workflow args, signal args, memo) visited when proxy
// .VisitPayloads traverses the command -- while the envelope itself is left as
// a proto-binary payload.
func TestSignalWithStartEnvelopeVisitedByAPIGoProxy(t *testing.T) {
	req := SignalWithStartWorkflowRequest{
		Workflow:   "my-workflow",
		Id:         "wf-id",
		TaskQueue:  "tq",
		Signal:     "my-signal",
		Args:       []any{"workflow-arg"},
		SignalArgs: []any{"signal-arg"},
		Memo:       map[string]any{"memo-key": "memo-value"},
	}

	// Encode the request proto as the SDK does for system Nexus envelopes:
	// through the proto (binary) converter, which produces a binary/protobuf
	// payload tagged with the message type. The api-go visitor reads that
	// messageType metadata to decode the envelope -- no registry required.
	envelope, err := converter.NewProtoPayloadConverter().ToPayload(req.ToProto())
	require.NoError(t, err)
	require.Equal(t, []byte("binary/protobuf"), envelope.GetMetadata()["encoding"])
	require.Equal(t,
		[]byte("temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest"),
		envelope.GetMetadata()["messageType"],
	)

	attrs := &commandpb.ScheduleNexusOperationCommandAttributes{
		Endpoint:  Endpoint,
		Service:   ServiceName,
		Operation: SignalWithStartWorkflowOp,
		Input:     envelope,
	}

	var seen []string
	err = proxy.VisitPayloads(context.Background(), &commandpb.Command{
		Attributes: &commandpb.Command_ScheduleNexusOperationCommandAttributes{
			ScheduleNexusOperationCommandAttributes: attrs,
		},
	}, proxy.VisitPayloadsOptions{
		Visitor: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			out := make([]*commonpb.Payload, len(p))
			for i, pl := range p {
				seen = append(seen, string(pl.GetData()))
				// Tag each inner payload, as a codec would, to confirm write-back.
				cloned := proto.Clone(pl).(*commonpb.Payload)
				if cloned.Metadata == nil {
					cloned.Metadata = map[string][]byte{}
				}
				cloned.Metadata["visited"] = []byte("true")
				out[i] = cloned
			}
			return out, nil
		},
	})
	require.NoError(t, err)

	// The default data converter encodes string args as `"workflow-arg"` etc.
	sort.Strings(seen)
	require.Equal(t, []string{`"memo-value"`, `"signal-arg"`, `"workflow-arg"`}, seen)

	// The envelope must still be a proto-binary payload that round-trips, with
	// its inner payloads now carrying the visitor's "visited" tag.
	require.Equal(t, []byte("binary/protobuf"), attrs.Input.GetMetadata()["encoding"])
	var decoded = SignalWithStartWorkflowRequest{}.ToProto()
	require.NoError(t, proto.Unmarshal(attrs.Input.GetData(), decoded))
	require.Equal(t, []byte("true"), decoded.GetInput().GetPayloads()[0].GetMetadata()["visited"])
	require.Equal(t, []byte("true"), decoded.GetSignalInput().GetPayloads()[0].GetMetadata()["visited"])
	require.Equal(t, []byte("true"), decoded.GetMemo().GetFields()["memo-key"].GetMetadata()["visited"])
}
