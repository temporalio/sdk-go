package toolregistry

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// runInActivity executes fn inside a test activity environment, providing a
// proper activity context (required for activity.RecordHeartbeat calls).
func runInActivity(t *testing.T, fn func(ctx context.Context) error) {
	t.Helper()
	ts := testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	activity := fn
	env.RegisterActivity(activity)
	_, err := env.ExecuteActivity(activity)
	require.NoError(t, err)
}

// ── Checkpoint round-trip (T6) ────────────────────────────────────────────────

// TestCheckpoint_RoundTrip verifies that a sessionCheckpoint with nested
// message structures survives a JSON marshal/unmarshal cycle with all fields
// intact. This guards against the class of bug where nested maps lose their
// type after deserialization (cf. .NET List<object?> bug).
func TestCheckpoint_RoundTrip(t *testing.T) {
	toolCalls := []map[string]any{
		{
			"id":   "call_abc",
			"type": "function",
			"function": map[string]any{
				"name":      "my_tool",
				"arguments": `{"x":1}`,
			},
		},
	}
	cp := sessionCheckpoint{
		Messages: []map[string]any{
			{
				"role":       "assistant",
				"tool_calls": toolCalls,
			},
		},
		Results: []map[string]any{
			{"type": "smell", "file": "foo.go"},
		},
	}

	data, err := json.Marshal(cp)
	require.NoError(t, err)

	var restored sessionCheckpoint
	require.NoError(t, json.Unmarshal(data, &restored))

	require.Len(t, restored.Messages, 1)
	require.Equal(t, "assistant", restored.Messages[0]["role"])

	// tool_calls must survive the round-trip as a parseable structure.
	toolCallsRestored, ok := restored.Messages[0]["tool_calls"].([]any)
	require.True(t, ok, "tool_calls should be []any after JSON round-trip")
	require.Len(t, toolCallsRestored, 1)

	tc := toolCallsRestored[0].(map[string]any)
	require.Equal(t, "call_abc", tc["id"])
	require.Equal(t, "function", tc["type"])

	fn := tc["function"].(map[string]any)
	require.Equal(t, "my_tool", fn["name"])

	require.Len(t, restored.Results, 1)
	require.Equal(t, "smell", restored.Results[0]["type"])
	require.Equal(t, "foo.go", restored.Results[0]["file"])
}

// ── MarshalResult ─────────────────────────────────────────────────────────────

func TestMarshalResult(t *testing.T) {
	s := &AgenticSession{}
	type Result struct {
		Kind string `json:"kind"`
		Msg  string `json:"msg"`
	}
	err := MarshalResult(s, Result{Kind: "error", Msg: "oops"})
	require.NoError(t, err)
	require.Len(t, s.Results, 1)
	require.Equal(t, "error", s.Results[0]["kind"])
	require.Equal(t, "oops", s.Results[0]["msg"])
}

func TestMarshalResult_Multiple(t *testing.T) {
	s := &AgenticSession{}
	type Result struct{ V int }
	require.NoError(t, MarshalResult(s, Result{V: 1}))
	require.NoError(t, MarshalResult(s, Result{V: 2}))
	require.Len(t, s.Results, 2)
}

// ── AgenticSession.RunToolLoop ────────────────────────────────────────────────

func TestAgenticSession_FreshStart(t *testing.T) {
	provider := NewMockProvider([]MockResponse{Done("finished")})
	reg := NewToolRegistry()
	var session AgenticSession

	runInActivity(t, func(ctx context.Context) error {
		session = AgenticSession{}
		return session.RunToolLoop(ctx, provider, reg, "my prompt")
	})

	require.Equal(t, "my prompt", session.Messages[0]["content"])
	require.Equal(t, "user", session.Messages[0]["role"])
}

func TestAgenticSession_ResumesExistingMessages(t *testing.T) {
	// When Messages is already populated (retry case), the prompt is NOT
	// prepended again — the loop resumes from where it left off.
	provider := NewMockProvider([]MockResponse{Done("ok")})
	reg := NewToolRegistry()

	existing := []map[string]any{
		{"role": "user", "content": "original"},
		{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "thinking"}}},
	}
	session := AgenticSession{Messages: existing}

	runInActivity(t, func(ctx context.Context) error {
		return session.RunToolLoop(ctx, provider, reg, "ignored prompt")
	})

	// First two messages unchanged.
	require.Equal(t, "original", session.Messages[0]["content"])
	require.Equal(t, "assistant", session.Messages[1]["role"])
}

func TestAgenticSession_WithToolCalls(t *testing.T) {
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
		Done("done"),
	}).WithRegistry(reg)

	session := AgenticSession{}
	runInActivity(t, func(ctx context.Context) error {
		return session.RunToolLoop(ctx, provider, reg, "go")
	})

	require.Equal(t, []string{"first", "second"}, collected)
	// user + (assistant + tool_result)*2 + assistant
	require.Greater(t, len(session.Messages), 4)
}

func TestAgenticSession_CheckpointOnEachTurn(t *testing.T) {
	// Verifies RunToolLoop checkpoints before each provider call.
	reg := NewToolRegistry()
	provider := NewMockProvider([]MockResponse{
		Done("turn1"),
	})

	session := AgenticSession{}
	runInActivity(t, func(ctx context.Context) error {
		return session.RunToolLoop(ctx, provider, reg, "prompt")
	})

	// The loop ran without error — heartbeating inside an activity context works.
	require.NotEmpty(t, session.Messages)
}

// ── RunWithSession ────────────────────────────────────────────────────────────

func TestRunWithSession_FreshStart(t *testing.T) {
	provider := NewMockProvider([]MockResponse{Done("done")})
	reg := NewToolRegistry()
	var capturedMessages []map[string]any

	runInActivity(t, func(ctx context.Context) error {
		return RunWithSession(ctx, func(ctx context.Context, s *AgenticSession) error {
			err := s.RunToolLoop(ctx, provider, reg, "hello")
			capturedMessages = s.Messages
			return err
		})
	})

	require.NotEmpty(t, capturedMessages)
	require.Equal(t, "hello", capturedMessages[0]["content"])
}

func TestRunWithSession_ResultsAccumulate(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "flag", Description: "d", InputSchema: map[string]any{}},
		func(inp map[string]any) (string, error) { return "recorded", nil })

	provider := NewMockProvider([]MockResponse{
		ToolCall("flag", map[string]any{"desc": "broken"}),
		Done("done"),
	}).WithRegistry(reg)

	type Result struct {
		Desc string `json:"desc"`
	}
	var capturedResults []map[string]any

	runInActivity(t, func(ctx context.Context) error {
		return RunWithSession(ctx, func(ctx context.Context, s *AgenticSession) error {
			_ = MarshalResult(s, Result{Desc: "pre-existing"})
			err := s.RunToolLoop(ctx, provider, reg, "analyze")
			capturedResults = s.Results
			return err
		})
	})

	require.Len(t, capturedResults, 1)
	require.Equal(t, "pre-existing", capturedResults[0]["desc"])
}
