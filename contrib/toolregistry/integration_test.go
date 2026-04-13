package toolregistry

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func makeRecordRegistry(t *testing.T) (*ToolRegistry, *[]string) {
	t.Helper()
	collected := &[]string{}
	reg := NewToolRegistry()
	reg.Register(ToolDef{
		Name:        "record",
		Description: "Record a value",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required":   []string{"value"},
		},
	}, func(inp map[string]any) (string, error) {
		*collected = append(*collected, inp["value"].(string))
		return "recorded", nil
	})
	return reg, collected
}

func TestIntegration_Anthropic(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("RUN_INTEGRATION_TESTS not set")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	require.NotEmpty(t, apiKey, "ANTHROPIC_API_KEY required")

	reg, collected := makeRecordRegistry(t)
	cfg := AnthropicConfig{APIKey: apiKey}
	provider := NewAnthropicProvider(cfg, reg,
		"You must call record() exactly once with value='hello'.")

	_, err := RunToolLoop(context.Background(), provider, reg, "",
		"Please call the record tool with value='hello'.")
	require.NoError(t, err)
	require.Contains(t, *collected, "hello")
}

func TestIntegration_OpenAI(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("RUN_INTEGRATION_TESTS not set")
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	require.NotEmpty(t, apiKey, "OPENAI_API_KEY required")

	reg, collected := makeRecordRegistry(t)
	cfg := OpenAIConfig{APIKey: apiKey}
	provider := NewOpenAIProvider(cfg, reg,
		"You must call record() exactly once with value='hello'.")

	_, err := RunToolLoop(context.Background(), provider, reg, "",
		"Please call the record tool with value='hello'.")
	require.NoError(t, err)
	require.Contains(t, *collected, "hello")
}
