package toolregistry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolRegistry_Dispatch(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "greet", Description: "d", InputSchema: map[string]any{}},
		func(inp map[string]any) (string, error) {
			return "hello " + inp["name"].(string), nil
		})

	result, err := reg.Dispatch("greet", map[string]any{"name": "world"})
	require.NoError(t, err)
	require.Equal(t, "hello world", result)
}

func TestToolRegistry_UnknownTool(t *testing.T) {
	reg := NewToolRegistry()
	_, err := reg.Dispatch("missing", map[string]any{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing")
}

func TestToolRegistry_ToAnthropic(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ToolDef{
		Name:        "my_tool",
		Description: "does something",
		InputSchema: map[string]any{"type": "object"},
	}, func(map[string]any) (string, error) { return "ok", nil })

	result := reg.ToAnthropic()
	require.Len(t, result, 1)
	require.Equal(t, "my_tool", result[0]["name"])
	require.Equal(t, "does something", result[0]["description"])
	require.Equal(t, map[string]any{"type": "object"}, result[0]["input_schema"])
}

func TestToolRegistry_ToOpenAI(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ToolDef{
		Name:        "my_tool",
		Description: "does something",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"x": map[string]any{"type": "string"}},
		},
	}, func(map[string]any) (string, error) { return "ok", nil })

	result := reg.ToOpenAI()
	require.Len(t, result, 1)
	require.Equal(t, "function", result[0]["type"])
	fn := result[0]["function"].(map[string]any)
	require.Equal(t, "my_tool", fn["name"])
	require.Equal(t, "does something", fn["description"])
}

func TestToolRegistry_Defs_ReturnsCopy(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "a", Description: "d", InputSchema: map[string]any{}},
		func(map[string]any) (string, error) { return "", nil })

	defs := reg.Defs()
	require.Len(t, defs, 1)

	// Mutating the returned slice must not affect the registry.
	defs[0].Name = "modified"
	require.Equal(t, "a", reg.Defs()[0].Name)
}

func TestToolRegistry_MultipleTools(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "alpha", Description: "a", InputSchema: map[string]any{}},
		func(map[string]any) (string, error) { return "a", nil })
	reg.Register(ToolDef{Name: "beta", Description: "b", InputSchema: map[string]any{}},
		func(map[string]any) (string, error) { return "b", nil })

	defs := reg.Defs()
	require.Len(t, defs, 2)
	require.Equal(t, "alpha", defs[0].Name)
	require.Equal(t, "beta", defs[1].Name)
}

// ── RunToolLoop ───────────────────────────────────────────────────────────────

func TestRunToolLoop_SingleDone(t *testing.T) {
	reg := NewToolRegistry()
	provider := NewMockProvider([]MockResponse{Done("finished")})

	msgs, err := RunToolLoop(context.Background(), provider, reg, "hello")
	require.NoError(t, err)
	// user + assistant
	require.Len(t, msgs, 2)
	require.Equal(t, "user", msgs[0]["role"])
	require.Equal(t, "hello", msgs[0]["content"])
	require.Equal(t, "assistant", msgs[1]["role"])
}

func TestRunToolLoop_WithToolCall(t *testing.T) {
	collected := []string{}
	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "collect", Description: "d", InputSchema: map[string]any{}},
		func(inp map[string]any) (string, error) {
			collected = append(collected, inp["v"].(string))
			return "ok", nil
		})

	provider := NewMockProvider([]MockResponse{
		ToolCall("collect", map[string]any{"v": "first"}),
		ToolCall("collect", map[string]any{"v": "second"}),
		Done("all done"),
	}).WithRegistry(reg)

	msgs, err := RunToolLoop(context.Background(), provider, reg, "go")
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, collected)

	// user, assistant+tool_result (x2), final assistant
	require.Greater(t, len(msgs), 4)
}

func TestRunToolLoop_EmptyResponses(t *testing.T) {
	reg := NewToolRegistry()
	provider := NewMockProvider([]MockResponse{})

	msgs, err := RunToolLoop(context.Background(), provider, reg, "prompt")
	require.NoError(t, err)
	// provider returns done immediately, so only the user message is present
	require.Len(t, msgs, 1)
}

func TestFromMCPTools(t *testing.T) {
	tools := []MCPTool{
		{
			Name:        "read_file",
			Description: "Read a file from disk",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
				"required":   []string{"path"},
			},
		},
		{
			Name:        "list_dir",
			Description: "List directory contents",
			InputSchema: nil, // should default to empty object schema
		},
	}

	reg := FromMCPTools(tools)

	defs := reg.Defs()
	require.Len(t, defs, 2)

	require.Equal(t, "read_file", defs[0].Name)
	require.Equal(t, "Read a file from disk", defs[0].Description)
	require.Equal(t, "object", defs[0].InputSchema["type"])

	require.Equal(t, "list_dir", defs[1].Name)
	require.Equal(t, "object", defs[1].InputSchema["type"]) // nil schema defaulted

	// No-op handlers return empty string without error.
	result, err := reg.Dispatch("read_file", map[string]any{"path": "/etc/hosts"})
	require.NoError(t, err)
	require.Equal(t, "", result)
}
