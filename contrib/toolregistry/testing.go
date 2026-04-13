package toolregistry

import (
	"context"
	"fmt"
	"math/rand/v2"
)

// Dispatcher is satisfied by [*ToolRegistry] and [*FakeToolRegistry].
// Pass a [*FakeToolRegistry] to [MockProvider.WithRegistry] to record which
// tool calls the scripted responses trigger.
type Dispatcher interface {
	Dispatch(name string, input map[string]any) (string, error)
}

// MockResponse is a scripted provider response produced by [ToolCall] or [Done].
// The internal fields are not exported; construct values with those factories.
type MockResponse struct {
	stop    bool
	content []map[string]any
}

// ToolCall returns a [MockResponse] that makes a single tool call.
// If callID is empty or omitted, a random ID is generated.
//
// Example:
//
//	provider := toolregistry.NewMockProvider([]toolregistry.MockResponse{
//	    toolregistry.ToolCall("flag", map[string]any{"desc": "broken"}),
//	    toolregistry.Done("done"),
//	})
func ToolCall(toolName string, toolInput map[string]any, callID ...string) MockResponse {
	id := fmt.Sprintf("test_%08x", rand.Uint32())
	if len(callID) > 0 && callID[0] != "" {
		id = callID[0]
	}
	return MockResponse{
		stop: false,
		content: []map[string]any{
			{"type": "tool_use", "id": id, "name": toolName, "input": toolInput},
		},
	}
}

// Done returns a [MockResponse] that ends the loop with the given text.
// If text is omitted, "Done." is used.
func Done(text ...string) MockResponse {
	t := "Done."
	if len(text) > 0 {
		t = text[0]
	}
	return MockResponse{
		stop:    true,
		content: []map[string]any{{"type": "text", "text": t}},
	}
}

// MockProvider implements [Provider] using pre-scripted responses. No LLM API
// calls are made. Responses are consumed in order; once exhausted the loop
// stops cleanly.
//
// Use [MockProvider.WithRegistry] to inject a [FakeToolRegistry] if you need
// to record which tool calls the scripted responses trigger.
//
// Example:
//
//	provider := toolregistry.NewMockProvider([]toolregistry.MockResponse{
//	    toolregistry.ToolCall("greet", map[string]any{"name": "world"}),
//	    toolregistry.Done("said hello"),
//	})
type MockProvider struct {
	responses []MockResponse
	index     int
	registry  Dispatcher
}

// NewMockProvider creates a MockProvider backed by an empty [ToolRegistry].
// Register handlers on the embedded registry via [MockProvider.WithRegistry]
// if any scripted response triggers a tool call.
func NewMockProvider(responses []MockResponse) *MockProvider {
	return &MockProvider{
		responses: responses,
		registry:  NewToolRegistry(),
	}
}

// WithRegistry replaces the dispatch registry and returns p for chaining.
// Accepts [*ToolRegistry] or [*FakeToolRegistry].
func (p *MockProvider) WithRegistry(r Dispatcher) *MockProvider {
	p.registry = r
	return p
}

// RunTurn implements [Provider]. Returns the next scripted response.
func (p *MockProvider) RunTurn(_ context.Context, _ []map[string]any, _ []ToolDef) ([]map[string]any, bool, error) {
	if p.index >= len(p.responses) {
		return nil, true, nil
	}
	resp := p.responses[p.index]
	p.index++

	var newMsgs []map[string]any
	newMsgs = append(newMsgs, map[string]any{"role": "assistant", "content": resp.content})

	if !resp.stop {
		var toolResults []map[string]any
		for _, block := range resp.content {
			if block["type"] == "tool_use" {
				name, _ := block["name"].(string)
				id, _ := block["id"].(string)
				input, _ := block["input"].(map[string]any)
				result, err := p.registry.Dispatch(name, input)
				if err != nil {
					result = fmt.Sprintf("error: %s", err)
				}
				toolResults = append(toolResults, map[string]any{
					"type":        "tool_result",
					"tool_use_id": id,
					"content":     result,
				})
			}
		}
		if len(toolResults) > 0 {
			newMsgs = append(newMsgs, map[string]any{"role": "user", "content": toolResults})
		}
	}

	return newMsgs, resp.stop, nil
}

// DispatchCall records a single invocation on [FakeToolRegistry].
type DispatchCall struct {
	Name  string
	Input map[string]any
}

// FakeToolRegistry wraps [ToolRegistry] and records every [Dispatch] call.
// Useful for asserting which tools were called and with what inputs.
//
// Example:
//
//	fake := toolregistry.NewFakeToolRegistry()
//	fake.Register(def, handler)
//	fake.Dispatch("greet", map[string]any{"name": "world"})
//	// fake.Calls == [{Name:"greet", Input:{"name":"world"}}]
type FakeToolRegistry struct {
	*ToolRegistry
	// Calls holds every Dispatch invocation in order.
	Calls []DispatchCall
}

// NewFakeToolRegistry creates an empty FakeToolRegistry.
func NewFakeToolRegistry() *FakeToolRegistry {
	return &FakeToolRegistry{ToolRegistry: NewToolRegistry()}
}

// Dispatch records the call then delegates to the underlying registry.
func (f *FakeToolRegistry) Dispatch(name string, input map[string]any) (string, error) {
	f.Calls = append(f.Calls, DispatchCall{Name: name, Input: input})
	return f.ToolRegistry.Dispatch(name, input)
}

// MockAgenticSession is a pre-canned session that returns fixed issues without
// any LLM calls. Use it to test code that calls [RunWithSession] and inspects
// session.Issues without an API key or a Temporal server.
//
// Example:
//
//	s := &toolregistry.MockAgenticSession{
//	    Issues: []map[string]any{{"type": "missing", "symbol": "x"}},
//	}
//	_ = s.RunToolLoop(ctx, nil, nil, "sys", "prompt")
//	// s.Issues still contains the pre-canned entry
type MockAgenticSession struct {
	Messages []map[string]any
	Issues   []map[string]any
}

// RunToolLoop is a no-op — it does not call any LLM or record a heartbeat.
func (s *MockAgenticSession) RunToolLoop(_ context.Context, _ Provider, _ *ToolRegistry, _, prompt string) error {
	if len(s.Messages) == 0 {
		s.Messages = []map[string]any{{"role": "user", "content": prompt}}
	}
	return nil
}

// CrashAfterTurns implements [Provider] and returns an error after N complete
// turns. Use it in integration tests to verify that [AgenticSession] resumes
// from a heartbeat checkpoint after a simulated crash.
//
// Example:
//
//	// First invocation returns an error after 2 turns.
//	// Second invocation (retry) resumes from the last checkpoint.
//	provider := &toolregistry.CrashAfterTurns{N: 2}
type CrashAfterTurns struct {
	// N is the number of turns to complete before returning an error.
	N     int
	count int
}

// RunTurn implements [Provider]. Returns an error once more than N turns have run.
func (c *CrashAfterTurns) RunTurn(_ context.Context, _ []map[string]any, _ []ToolDef) ([]map[string]any, bool, error) {
	c.count++
	if c.count > c.N {
		return nil, false, fmt.Errorf("CrashAfterTurns: simulated crash after %d turns", c.N)
	}
	newMsgs := []map[string]any{
		{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "..."}}},
	}
	return newMsgs, c.count >= c.N, nil
}
