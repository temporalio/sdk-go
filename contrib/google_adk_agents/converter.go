package google_adk_agents

import (
	"bytes"
	"encoding/json"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// NewDataConverter returns a DataConverter that losslessly round-trips the
// adk-go types that cross the Activity boundary — *genai.Content,
// []*session.Event, agent.RunConfig, TurnInput and TurnResult — and renders
// them as readable JSON in workflow history.
//
// These genai/session types are plain Go structs with JSON tags (not
// proto-backed), so JSON is lossless. This converter differs from the default
// JSON converter only in that it disables HTML escaping, so the model text,
// function-call arguments and tool output stored in history stay readable
// (no < / & noise). Pass it on both the client and worker:
//
//	c, err := client.Dial(client.Options{DataConverter: google_adk_agents.NewDataConverter()})
//
// The worker inherits the converter from the client it is created with.
func NewDataConverter() converter.DataConverter {
	return converter.NewCompositeDataConverter(
		converter.NewNilPayloadConverter(),
		converter.NewByteSlicePayloadConverter(),
		converter.NewProtoJSONPayloadConverter(),
		converter.NewProtoPayloadConverter(),
		newReadableJSONPayloadConverter(),
	)
}

// readableJSONPayloadConverter is a JSON payload converter that does not
// HTML-escape its output. It is wire-compatible with the SDK's default JSON
// converter (same encoding metadata) so payloads it writes can be read back
// by any worker, and vice versa.
type readableJSONPayloadConverter struct{}

func newReadableJSONPayloadConverter() *readableJSONPayloadConverter {
	return &readableJSONPayloadConverter{}
}

// ToPayload JSON-encodes value without HTML escaping.
func (c *readableJSONPayloadConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("google_adk_agents: unable to encode payload: %w", err)
	}
	// json.Encoder.Encode appends a trailing newline; trim it for parity with
	// json.Marshal output.
	data := bytes.TrimRight(buf.Bytes(), "\n")
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON),
		},
		Data: data,
	}, nil
}

// FromPayload JSON-decodes the payload into valuePtr.
func (c *readableJSONPayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	if err := json.Unmarshal(payload.GetData(), valuePtr); err != nil {
		return fmt.Errorf("google_adk_agents: unable to decode payload: %w", err)
	}
	return nil
}

// ToString renders the payload as its JSON text.
func (c *readableJSONPayloadConverter) ToString(payload *commonpb.Payload) string {
	return string(payload.GetData())
}

// Encoding returns the JSON encoding identifier.
func (c *readableJSONPayloadConverter) Encoding() string {
	return converter.MetadataEncodingJSON
}
