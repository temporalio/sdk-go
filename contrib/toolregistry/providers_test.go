package toolregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropicSDK "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/require"
)

// TestAnthropicProvider_HandlerError_SetsIsError verifies that when a tool handler
// returns an error, the resulting tool_result message carries "is_error": true and
// the loop does not propagate the error to the caller.
func TestAnthropicProvider_HandlerError_SetsIsError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		if callCount == 1 {
			// First call: model requests the "boom" tool.
			fmt.Fprint(w, `{
				"id": "msg_1",
				"type": "message",
				"role": "assistant",
				"content": [{"type": "tool_use", "id": "c1", "name": "boom", "input": {}}],
				"model": "claude-sonnet-4-6",
				"stop_reason": "tool_use",
				"usage": {"input_tokens": 10, "output_tokens": 5}
			}`)
		} else {
			// Second call: model stops.
			fmt.Fprint(w, `{
				"id": "msg_2",
				"type": "message",
				"role": "assistant",
				"content": [{"type": "text", "text": "done"}],
				"model": "claude-sonnet-4-6",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 20, "output_tokens": 5}
			}`)
		}
	}))
	defer server.Close()

	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "boom", Description: "d", InputSchema: map[string]any{}},
		func(_ map[string]any) (string, error) {
			return "", fmt.Errorf("intentional failure")
		})

	client := anthropicSDK.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL),
	)
	provider := NewAnthropicProvider(AnthropicConfig{Client: &client}, reg, "sys")

	messages := []map[string]any{{"role": "user", "content": "go"}}
	newMsgs, done, err := provider.RunTurn(context.Background(), messages, reg.Defs())
	require.NoError(t, err)
	require.False(t, done)

	// newMsgs: assistant message + user(tool_results) message
	require.Len(t, newMsgs, 2)
	toolResultMsg := newMsgs[1]
	require.Equal(t, "user", toolResultMsg["role"])

	results, ok := toolResultMsg["content"].([]map[string]any)
	require.True(t, ok, "content should be []map[string]any")
	require.Len(t, results, 1)

	tr := results[0]
	require.Equal(t, "tool_result", tr["type"])
	require.Equal(t, true, tr["is_error"], "is_error should be true when handler errors")
	content, _ := tr["content"].(string)
	require.True(t, strings.Contains(content, "intentional failure"), "error message should be in content")
}

// TestAnthropicProvider_HandlerSuccess_NoIsError verifies that successful handlers
// do not set is_error.
func TestAnthropicProvider_HandlerSuccess_NoIsError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		if callCount == 1 {
			fmt.Fprint(w, `{
				"id": "msg_1", "type": "message", "role": "assistant",
				"content": [{"type": "tool_use", "id": "c1", "name": "ok_tool", "input": {}}],
				"model": "claude-sonnet-4-6", "stop_reason": "tool_use",
				"usage": {"input_tokens": 10, "output_tokens": 5}
			}`)
		} else {
			fmt.Fprint(w, `{
				"id": "msg_2", "type": "message", "role": "assistant",
				"content": [{"type": "text", "text": "done"}],
				"model": "claude-sonnet-4-6", "stop_reason": "end_turn",
				"usage": {"input_tokens": 20, "output_tokens": 5}
			}`)
		}
	}))
	defer server.Close()

	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "ok_tool", Description: "d", InputSchema: map[string]any{}},
		func(_ map[string]any) (string, error) { return "success", nil })

	client := anthropicSDK.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL),
	)
	provider := NewAnthropicProvider(AnthropicConfig{Client: &client}, reg, "sys")

	messages := []map[string]any{{"role": "user", "content": "go"}}
	newMsgs, done, err := provider.RunTurn(context.Background(), messages, reg.Defs())
	require.NoError(t, err)
	require.False(t, done)

	results, ok := newMsgs[1]["content"].([]map[string]any)
	require.True(t, ok)
	tr := results[0]

	data, _ := json.Marshal(tr)
	require.NotContains(t, string(data), "is_error", "is_error should not appear on success")
}
