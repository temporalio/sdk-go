// Package toolregistry provides LLM tool-calling primitives for Temporal activities.
//
// Define tools once with [ToolRegistry], use them with Anthropic or OpenAI, and
// run complete multi-turn tool-calling conversations with [RunToolLoop].
//
// For crash-safe sessions that survive activity retries, use [RunWithSession].
//
// Example:
//
//	reg := toolregistry.NewToolRegistry()
//	reg.Register(toolregistry.ToolDef{
//	    Name:        "flag_issue",
//	    Description: "Flag a problem found in analysis",
//	    InputSchema: map[string]any{
//	        "type":       "object",
//	        "properties": map[string]any{"description": map[string]any{"type": "string"}},
//	        "required":   []string{"description"},
//	    },
//	}, func(inp map[string]any) (string, error) {
//	    issues = append(issues, inp["description"].(string))
//	    return "recorded", nil
//	})
//
//	cfg := toolregistry.AnthropicConfig{APIKey: os.Getenv("ANTHROPIC_API_KEY")}
//	provider := toolregistry.NewAnthropicProvider(cfg, reg)
//	_, err := toolregistry.RunToolLoop(ctx, provider, reg, prompt)
package toolregistry

import (
	"context"
	"fmt"
)

// ToolDef defines an LLM tool in Anthropic's tool_use JSON format.
// The same definition is used for both Anthropic and OpenAI; [ToolRegistry]
// converts the schema for each provider.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Handler is called when the model invokes a tool. It receives the parsed
// tool input and returns a string result or an error.
type Handler func(input map[string]any) (string, error)

// Provider is the interface implemented by LLM provider adapters. Each
// adapter handles one LLM API's wire format for tool calling.
//
// Implementations must be safe to call from a single goroutine (one activity
// goroutine owns each session — no concurrent RunTurn calls on one Provider).
type Provider interface {
	// RunTurn executes one turn of the conversation.
	//
	// It sends the full message history plus the registered tools to the LLM,
	// and returns the new messages to append (assistant response and any tool
	// results), whether the loop is done, and any error.
	//
	// Callers append the returned messages to their history before the next call.
	RunTurn(ctx context.Context, messages []map[string]any, tools []ToolDef) (newMessages []map[string]any, done bool, err error)
}

// ToolRegistry maps tool names to definitions and handlers.
//
// Tools are registered in Anthropic's tool_use format. The registry exports
// them for Anthropic or OpenAI and dispatches incoming tool calls to the
// appropriate handler.
//
// A ToolRegistry is not safe for concurrent modification; build it before
// passing it to concurrent activities.
type ToolRegistry struct {
	defs     []ToolDef
	handlers map[string]Handler
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		handlers: make(map[string]Handler),
	}
}

// MCPTool is an MCP-compatible tool descriptor.
// Any struct with Name, Description, and InputSchema fields satisfies this shape.
type MCPTool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// FromMCPTools creates a [ToolRegistry] from a list of MCP tool descriptors.
//
// Each tool is registered with a no-op handler (returning an empty string).
// Override handlers by calling [ToolRegistry.Register] with the same name after
// construction.
func FromMCPTools(tools []MCPTool) *ToolRegistry {
	reg := NewToolRegistry()
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		name := t.Name
		reg.Register(ToolDef{
			Name:        name,
			Description: t.Description,
			InputSchema: schema,
		}, func(_ map[string]any) (string, error) { return "", nil })
	}
	return reg
}

// Register adds a tool definition and its handler to the registry.
func (r *ToolRegistry) Register(def ToolDef, handler Handler) {
	r.defs = append(r.defs, def)
	r.handlers[def.Name] = handler
}

// Dispatch calls the handler registered for name with the given input.
//
// Returns an error if no handler is registered for name.
func (r *ToolRegistry) Dispatch(name string, input map[string]any) (string, error) {
	h, ok := r.handlers[name]
	if !ok {
		return "", fmt.Errorf("toolregistry: unknown tool %q", name)
	}
	return h(input)
}

// Defs returns a copy of the registered tool definitions.
func (r *ToolRegistry) Defs() []ToolDef {
	defs := make([]ToolDef, len(r.defs))
	copy(defs, r.defs)
	return defs
}

// ToAnthropic returns tool definitions in Anthropic tool_use format.
// Each map has "name", "description", and "input_schema" keys, matching
// the format expected by the Anthropic Messages API.
func (r *ToolRegistry) ToAnthropic() []map[string]any {
	result := make([]map[string]any, len(r.defs))
	for i, d := range r.defs {
		result[i] = map[string]any{
			"name":         d.Name,
			"description":  d.Description,
			"input_schema": d.InputSchema,
		}
	}
	return result
}

// ToOpenAI returns tool definitions in OpenAI function-calling format.
func (r *ToolRegistry) ToOpenAI() []map[string]any {
	result := make([]map[string]any, len(r.defs))
	for i, d := range r.defs {
		result[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        d.Name,
				"description": d.Description,
				"parameters":  d.InputSchema,
			},
		}
	}
	return result
}

// RunToolLoop runs a complete multi-turn LLM tool-calling loop.
//
// This is the primary entry point for simple, non-resumable loops. For
// crash-safe sessions with heartbeat checkpointing, use [RunWithSession].
//
// Returns the full message history on completion, or the history up to the
// point of failure along with the error.
func RunToolLoop(
	ctx context.Context,
	provider Provider,
	registry *ToolRegistry,
	prompt string,
) ([]map[string]any, error) {
	messages := []map[string]any{
		{"role": "user", "content": prompt},
	}
	for {
		newMsgs, done, err := provider.RunTurn(ctx, messages, registry.defs)
		if err != nil {
			return messages, err
		}
		messages = append(messages, newMsgs...)
		if done {
			return messages, nil
		}
	}
}
