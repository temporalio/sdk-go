package workflowstreams

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

func TestPayloadWireRoundTrip(t *testing.T) {
	payload, err := converter.GetDefaultDataConverter().ToPayload("hello")
	require.NoError(t, err)

	wire, err := encodePayloadWire(payload)
	require.NoError(t, err)

	got, err := decodePayloadWire(wire)
	require.NoError(t, err)
	require.True(t, proto.Equal(payload, got))

	// The decoded payload still carries its encoding metadata so a consumer can
	// decode it back to the original value.
	var s string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(got, &s))
	require.Equal(t, "hello", s)
}

func TestPayloadWireFormatIsBase64OfProto(t *testing.T) {
	payload := &commonpb.Payload{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"hi"`),
	}
	wire, err := encodePayloadWire(payload)
	require.NoError(t, err)

	// Wire format is base64-of-marshaled-proto; decoding base64 then proto must
	// reproduce the payload. This is the contract shared with the Python and
	// TypeScript packages.
	raw, err := base64.StdEncoding.DecodeString(wire)
	require.NoError(t, err)
	var decoded commonpb.Payload
	require.NoError(t, proto.Unmarshal(raw, &decoded))
	require.True(t, proto.Equal(payload, &decoded))
}

func TestDecodePayloadWireRejectsBadBase64(t *testing.T) {
	_, err := decodePayloadWire("not valid base64!!!")
	require.Error(t, err)
}

func TestBinaryPayloadRoundTrip(t *testing.T) {
	payload, err := converter.GetDefaultDataConverter().ToPayload([]byte{0x00, 0x01, 0xff})
	require.NoError(t, err)

	wire, err := encodePayloadWire(payload)
	require.NoError(t, err)
	got, err := decodePayloadWire(wire)
	require.NoError(t, err)

	var b []byte
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(got, &b))
	require.Equal(t, []byte{0x00, 0x01, 0xff}, b)
}

func TestWorkflowStreamProtocolVector(t *testing.T) {
	const inputJSON = `{"topics":["events"],"from_offset":3}`
	const resultJSON = `{"items":[{"topic":"events","data":"ChgKCGVuY29kaW5nEgxiaW5hcnkvcGxhaW4SA2FiYw==","offset":3}],"next_offset":4,"more_ready":false}`
	input, err := json.Marshal(PollInput{Topics: []string{"events"}, FromOffset: 3})
	require.NoError(t, err)
	require.JSONEq(t, inputJSON, string(input))
	payload, err := converter.GetDefaultDataConverter().ToPayload([]byte("abc"))
	require.NoError(t, err)
	wire, err := encodePayloadWire(payload)
	require.NoError(t, err)
	result, err := json.Marshal(PollResult{Items: []WireItem{{Topic: "events", Data: wire, Offset: 3}}, NextOffset: 4})
	require.NoError(t, err)
	require.JSONEq(t, resultJSON, string(result))
}
