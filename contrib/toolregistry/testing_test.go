package toolregistry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// ── MockProvider ──────────────────────────────────────────────────────────────

func TestMockProvider_DispatchesToolCallsAndCompletes(t *testing.T) {
	collected := []string{}
	reg := NewToolRegistry()
	reg.Register(ToolDef{Name: "collect", Description: "d", InputSchema: map[string]any{}},
		func(inp map[string]any) (string, error) {
			collected = append(collected, inp["value"].(string))
			return "ok", nil
		})

	provider := NewMockProvider([]MockResponse{
		ToolCall("collect", map[string]any{"value": "first"}),
		ToolCall("collect", map[string]any{"value": "second"}),
		Done("all done"),
	}).WithRegistry(reg)

	ctx := context.Background()
	msgs := []map[string]any{{"role": "user", "content": "go"}}
	for {
		newMsgs, done, err := provider.RunTurn(ctx, msgs, nil)
		require.NoError(t, err)
		msgs = append(msgs, newMsgs...)
		if done {
			break
		}
	}

	require.Equal(t, []string{"first", "second"}, collected)
}

func TestMockProvider_StopsOnDone(t *testing.T) {
	provider := NewMockProvider([]MockResponse{Done("finished")})
	ctx := context.Background()
	messages := []map[string]any{{"role": "user", "content": "x"}}

	newMsgs, done, err := provider.RunTurn(ctx, messages, nil)
	require.NoError(t, err)
	require.True(t, done)
	require.Len(t, newMsgs, 1)
	require.Equal(t, "assistant", newMsgs[0]["role"])
}

func TestMockProvider_ExhaustedResponsesStopCleanly(t *testing.T) {
	provider := NewMockProvider([]MockResponse{})
	ctx := context.Background()

	newMsgs, done, err := provider.RunTurn(ctx, nil, nil)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, newMsgs)
}

func TestMockProvider_ToolCallUsesCallID(t *testing.T) {
	provider := NewMockProvider([]MockResponse{
		ToolCall("greet", map[string]any{}, "my-custom-id"),
		Done(),
	})
	ctx := context.Background()

	newMsgs, done, err := provider.RunTurn(ctx, nil, nil)
	require.NoError(t, err)
	require.False(t, done)

	content := newMsgs[0]["content"].([]map[string]any)
	require.Equal(t, "my-custom-id", content[0]["id"])
}

func TestMockProvider_RunsWithRunToolLoop(t *testing.T) {
	reg := NewToolRegistry()
	provider := NewMockProvider([]MockResponse{Done("done")})

	msgs, err := RunToolLoop(context.Background(), provider, reg, "sys", "prompt")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(msgs), 2)
}

// ── FakeToolRegistry ──────────────────────────────────────────────────────────

func TestFakeToolRegistry_RecordsDispatchCalls(t *testing.T) {
	fake := NewFakeToolRegistry()
	fake.Register(ToolDef{Name: "greet", Description: "d", InputSchema: map[string]any{}},
		func(inp map[string]any) (string, error) { return "ok", nil })

	fake.Dispatch("greet", map[string]any{"name": "world"})   //nolint:errcheck
	fake.Dispatch("greet", map[string]any{"name": "temporal"}) //nolint:errcheck

	require.Equal(t, []DispatchCall{
		{Name: "greet", Input: map[string]any{"name": "world"}},
		{Name: "greet", Input: map[string]any{"name": "temporal"}},
	}, fake.Calls)
}

func TestFakeToolRegistry_DelegatestoHandler(t *testing.T) {
	fake := NewFakeToolRegistry()
	fake.Register(ToolDef{Name: "echo", Description: "d", InputSchema: map[string]any{}},
		func(inp map[string]any) (string, error) {
			return inp["v"].(string), nil
		})

	result, err := fake.Dispatch("echo", map[string]any{"v": "hello"})
	require.NoError(t, err)
	require.Equal(t, "hello", result)
}

func TestFakeToolRegistry_UnknownToolStillErrors(t *testing.T) {
	fake := NewFakeToolRegistry()
	_, err := fake.Dispatch("unknown", map[string]any{})
	require.Error(t, err)
	// Call is still recorded even on error.
	require.Len(t, fake.Calls, 1)
}

func TestFakeToolRegistry_SatisfiesDispatcher(t *testing.T) {
	// Verify *FakeToolRegistry satisfies the Dispatcher interface so it can be
	// passed to MockProvider.WithRegistry.
	fake := NewFakeToolRegistry()
	fake.Register(ToolDef{Name: "ping", Description: "d", InputSchema: map[string]any{}},
		func(map[string]any) (string, error) { return "pong", nil })

	provider := NewMockProvider([]MockResponse{
		ToolCall("ping", map[string]any{}),
		Done(),
	}).WithRegistry(fake)

	_, err := RunToolLoop(context.Background(), provider, fake.ToolRegistry, "sys", "p")
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	require.Equal(t, "ping", fake.Calls[0].Name)
}

// ── CrashAfterTurns ───────────────────────────────────────────────────────────

func TestCrashAfterTurns_ErrorsAfterN(t *testing.T) {
	c := &CrashAfterTurns{N: 1}
	ctx := context.Background()

	// First turn: succeeds.
	_, done, err := c.RunTurn(ctx, nil, nil)
	require.NoError(t, err)
	require.True(t, done)

	// Second turn: error.
	_, _, err = c.RunTurn(ctx, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "simulated crash")
}

func TestCrashAfterTurns_CompletesNTurnsBeforeCrash(t *testing.T) {
	c := &CrashAfterTurns{N: 2}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		newMsgs, _, err := c.RunTurn(ctx, nil, nil)
		require.NoError(t, err)
		require.Len(t, newMsgs, 1)
	}

	_, _, err := c.RunTurn(ctx, nil, nil)
	require.Error(t, err)
}

func TestCrashAfterTurns_ImplementsProvider(t *testing.T) {
	var _ Provider = &CrashAfterTurns{}
}

// ── MockAgenticSession ────────────────────────────────────────────────────────

func TestMockAgenticSession_NoOpRunToolLoop(t *testing.T) {
	s := &MockAgenticSession{
		Issues: []map[string]any{{"type": "deprecated", "symbol": "old_fn"}},
	}
	err := s.RunToolLoop(context.Background(), nil, nil, "sys", "prompt")
	require.NoError(t, err)
	// Issues unchanged — no LLM calls.
	require.Len(t, s.Issues, 1)
	require.Equal(t, "deprecated", s.Issues[0]["type"])
}

func TestMockAgenticSession_SetsFirstMessage(t *testing.T) {
	s := &MockAgenticSession{}
	_ = s.RunToolLoop(context.Background(), nil, nil, "sys", "my prompt")
	require.Len(t, s.Messages, 1)
	require.Equal(t, "my prompt", s.Messages[0]["content"])
}

func TestMockAgenticSession_EmptyByDefault(t *testing.T) {
	s := &MockAgenticSession{}
	require.Empty(t, s.Issues)
	require.Empty(t, s.Messages)
}
