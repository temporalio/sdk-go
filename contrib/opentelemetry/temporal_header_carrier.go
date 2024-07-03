package opentelemetry

import (
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// TemporalHeaderCarrier adapts temporal header to satisfy the Carrier interface.
type TemporalHeaderCarrier map[string]*commonpb.Payload

// Get returns the value associated with the passed key.
func (h TemporalHeaderCarrier) Get(key string) string {
	value := ""
	payload := h[key]

	if err := converter.GetDefaultDataConverter().FromPayload(payload, &value); err != nil {
		//log.Warn(context.Background(), "error during temporal data conversion from payload", log.Str("error", err.Error()))
		return ""
	}

	return value
}

// Keys lists the keys stored in this carrier.
func (h TemporalHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}

	return keys
}

// Set stores the key-value pair.
func (h TemporalHeaderCarrier) Set(key string, value string) {
	// Convert value to payload
	payload, err := converter.GetDefaultDataConverter().ToPayload(value)
	if err != nil {
		//log.Warn(context.Background(), "error during temporal data conversion to payload", log.Str("error", err.Error()))
		return
	}
	h[key] = payload
}
