package converter

import (
	"errors"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	systemnexus "go.temporal.io/api/workflowservice/v1/workflowservicenexus/json"
)

// SystemNexusDataConverter converts annotated generated system Nexus envelopes
// through their backing proto messages. It is intentionally narrow and only
// supports annotated generated system Nexus request/response types.
type SystemNexusDataConverter struct {
	protoJSON *ProtoJSONPayloadConverter
}

// NewSystemNexusDataConverter returns a data converter for annotated generated
// system Nexus outer envelopes.
func NewSystemNexusDataConverter() DataConverter {
	return &SystemNexusDataConverter{
		protoJSON: NewProtoJSONPayloadConverter(),
	}
}

func (c *SystemNexusDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	if value == nil || systemnexus.GetTemporalNexusProtoMessage(value) == nil {
		return nil, errors.New("system nexus data converter only supports annotated generated types")
	}
	message, err := systemnexus.ToTemporalNexusProto(value)
	if err != nil {
		return nil, err
	}
	return c.protoJSON.ToPayload(message)
}

func (c *SystemNexusDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	if payload == nil {
		return nil
	}
	if valuePtr == nil {
		return errors.New("system nexus data converter requires a destination value")
	}
	message := systemnexus.GetTemporalNexusProtoMessage(valuePtr)
	if message == nil {
		return errors.New("system nexus data converter only supports annotated generated types")
	}
	if err := c.protoJSON.FromPayload(payload, message); err != nil {
		return err
	}
	return systemnexus.FromTemporalNexusProto(message, valuePtr)
}

func (c *SystemNexusDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	if len(values) == 0 {
		return nil, nil
	}

	result := &commonpb.Payloads{}
	for i, value := range values {
		rawValue, ok := value.(RawValue)
		if ok {
			result.Payloads = append(result.Payloads, rawValue.Payload())
			continue
		}
		payload, err := c.ToPayload(value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		result.Payloads = append(result.Payloads, payload)
	}
	return result, nil
}

func (c *SystemNexusDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return nil
	}
	for i, payload := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		rawValue, ok := valuePtrs[i].(*RawValue)
		if ok {
			*rawValue = NewRawValue(payload)
			continue
		}
		if err := c.FromPayload(payload, valuePtrs[i]); err != nil {
			return fmt.Errorf("payload item %d: %w", i, err)
		}
	}
	return nil
}

func (c *SystemNexusDataConverter) ToString(input *commonpb.Payload) string {
	return c.protoJSON.ToString(input)
}

func (c *SystemNexusDataConverter) ToStrings(input *commonpb.Payloads) []string {
	if input == nil {
		return nil
	}
	result := make([]string, len(input.Payloads))
	for i, payload := range input.Payloads {
		result[i] = c.ToString(payload)
	}
	return result
}

var _ DataConverter = (*SystemNexusDataConverter)(nil)
