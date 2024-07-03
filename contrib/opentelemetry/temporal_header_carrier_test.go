package opentelemetry

import (
	"github.com/stretchr/testify/assert"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"testing"
)

func TestTemporalHeaderCarrier_Get(t *testing.T) {
	tests := []struct {
		name      string
		carrier   TemporalHeaderCarrier
		wantValue string
	}{
		{
			name:      "empty metadata",
			carrier:   TemporalHeaderCarrier(map[string]*commonpb.Payload{}),
			wantValue: "",
		},
		{
			name: "with no matching key",
			carrier: TemporalHeaderCarrier(map[string]*commonpb.Payload{
				"other_key": encodeString(t, "value"),
			}),
			wantValue: "",
		},
		{
			name: "with matching key",
			carrier: TemporalHeaderCarrier(map[string]*commonpb.Payload{
				"key": encodeString(t, "value1"),
			}),
			wantValue: "value1",
		},
	}

	for _, test := range tests {
		tt := test
		t.Run(tt.name, func(t *testing.T) {
			val := tt.carrier.Get("key")
			assert.Equal(t, tt.wantValue, val)
		})
	}
}

func TestTemporalHeaderCarrier_Keys(t *testing.T) {
	tests := []struct {
		name         string
		carrier      TemporalHeaderCarrier
		expectedKeys []string
	}{
		{
			name:         "empty metadata",
			carrier:      TemporalHeaderCarrier(map[string]*commonpb.Payload{}),
			expectedKeys: []string{},
		},
		{
			name: "metadata with keys",
			carrier: TemporalHeaderCarrier(map[string]*commonpb.Payload{
				"key1": encodeString(t, "value1"),
				"key2": encodeString(t, "value2"),
			}),
			expectedKeys: []string{"key1", "key2"},
		},
	}

	for _, test := range tests {
		tt := test
		t.Run(tt.name, func(t *testing.T) {
			val := tt.carrier.Keys()
			assert.ElementsMatch(t, tt.expectedKeys, val)
		})
	}
}

func TestTemporalHeaderCarrier_Set(t *testing.T) {
	m := map[string]*commonpb.Payload{}
	carrier := TemporalHeaderCarrier(m)

	carrier.Set("test", "value")

	assert.Equal(t, "value", carrier.Get("test"))
	assert.Equal(t, encodeString(t, "value"), m["test"])
}

func encodeString(t *testing.T, s string) *commonpb.Payload {
	p, err := converter.GetDefaultDataConverter().ToPayload(s)
	assert.NoError(t, err)
	return p
}
