package workflowstreams

import (
	"encoding/base64"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

// encodePayloadWire encodes a Payload to the base64-of-proto wire format shared
// across the Go, Python, and TypeScript packages.
func encodePayloadWire(payload *commonpb.Payload) (string, error) {
	b, err := proto.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("workflowstreams: marshal payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// decodePayloadWire decodes the base64-of-proto wire format back to a Payload.
func decodePayloadWire(wire string) (*commonpb.Payload, error) {
	b, err := base64.StdEncoding.DecodeString(wire)
	if err != nil {
		return nil, fmt.Errorf("workflowstreams: decode base64 payload: %w", err)
	}
	payload := &commonpb.Payload{}
	if err := proto.Unmarshal(b, payload); err != nil {
		return nil, fmt.Errorf("workflowstreams: unmarshal payload: %w", err)
	}
	return payload, nil
}

// payloadWireSize estimates the contribution of a single encoded item to a poll
// response. encoded is already base64 (its on-wire representation).
func payloadWireSize(encoded, topic string) int {
	return len(encoded) + len(topic)
}
