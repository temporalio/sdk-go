package toolregistry

import (
	"context"
	"encoding/json"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

// AnthropicConfig configures an [AnthropicProvider].
type AnthropicConfig struct {
	// APIKey is the Anthropic API key. Required unless Client is set.
	APIKey string
	// Model is the model name. Defaults to "claude-sonnet-4-6".
	Model string
	// BaseURL overrides the Anthropic API base URL (e.g. for proxies).
	BaseURL string
	// Client is a pre-constructed anthropic.Client. When set, APIKey and
	// BaseURL are ignored. Useful for testing without API calls.
	Client *anthropic.Client
}

// AnthropicProvider implements [Provider] for the Anthropic Messages API.
type AnthropicProvider struct {
	client   anthropic.Client
	model    string
	system   string
	registry *ToolRegistry
}

// NewAnthropicProvider creates an AnthropicProvider from the given config.
func NewAnthropicProvider(cfg AnthropicConfig, registry *ToolRegistry, system string) *AnthropicProvider {
	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	var client anthropic.Client
	if cfg.Client != nil {
		client = *cfg.Client
	} else {
		opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
		if cfg.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(cfg.BaseURL))
		}
		client = anthropic.NewClient(opts...)
	}
	return &AnthropicProvider{client: client, model: model, system: system, registry: registry}
}

// RunTurn implements [Provider]. It sends one turn to Anthropic, dispatches
// any tool calls, and returns the new messages to append.
func (p *AnthropicProvider) RunTurn(ctx context.Context, messages []map[string]any, tools []ToolDef) ([]map[string]any, bool, error) {
	// Convert map messages to Anthropic MessageParam slice via JSON round-trip.
	msgParams, err := mapsToAnthropicMessages(messages)
	if err != nil {
		return nil, false, fmt.Errorf("toolregistry: marshal messages: %w", err)
	}

	// Convert ToolDef slice to Anthropic ToolUnionParam slice.
	toolParams, err := toolDefsToAnthropicTools(tools)
	if err != nil {
		return nil, false, fmt.Errorf("toolregistry: marshal tools: %w", err)
	}

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: p.system}},
		Tools:     toolParams,
		Messages:  msgParams,
	})
	if err != nil {
		return nil, false, fmt.Errorf("toolregistry: anthropic api: %w", err)
	}

	// Convert response content blocks to plain maps for storage.
	contentMaps, err := contentBlocksToMaps(resp.Content)
	if err != nil {
		return nil, false, fmt.Errorf("toolregistry: marshal response: %w", err)
	}

	var newMsgs []map[string]any
	newMsgs = append(newMsgs, map[string]any{"role": "assistant", "content": contentMaps})

	// Collect tool calls.
	var toolCalls []map[string]any
	for _, block := range contentMaps {
		if block["type"] == "tool_use" {
			toolCalls = append(toolCalls, block)
		}
	}

	if len(toolCalls) == 0 || resp.StopReason == anthropic.StopReasonEndTurn {
		return newMsgs, true, nil
	}

	// Dispatch each tool call and collect results.
	toolResults := make([]map[string]any, 0, len(toolCalls))
	for _, call := range toolCalls {
		name, _ := call["name"].(string)
		id, _ := call["id"].(string)
		input, _ := call["input"].(map[string]any)
		if input == nil {
			// input may be stored as json.RawMessage; parse it.
			if raw, ok := call["input"]; ok {
				if data, err := json.Marshal(raw); err == nil {
					json.Unmarshal(data, &input) //nolint:errcheck
				}
			}
		}
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
	newMsgs = append(newMsgs, map[string]any{"role": "user", "content": toolResults})
	return newMsgs, false, nil
}

// OpenAIConfig configures an [OpenAIProvider].
type OpenAIConfig struct {
	// APIKey is the OpenAI API key. Required unless Client is set.
	APIKey string
	// Model is the model name. Defaults to "gpt-4o".
	Model string
	// BaseURL overrides the OpenAI API base URL.
	BaseURL string
	// Client is a pre-constructed *openai.Client. When set, APIKey and BaseURL
	// are ignored. Useful for testing.
	Client *openai.Client
}

// OpenAIProvider implements [Provider] for the OpenAI Chat Completions API.
type OpenAIProvider struct {
	client   *openai.Client
	model    string
	system   string
	registry *ToolRegistry
}

// NewOpenAIProvider creates an OpenAIProvider from the given config.
func NewOpenAIProvider(cfg OpenAIConfig, registry *ToolRegistry, system string) *OpenAIProvider {
	model := cfg.Model
	if model == "" {
		model = "gpt-4o"
	}
	client := cfg.Client
	if client == nil {
		oacfg := openai.DefaultConfig(cfg.APIKey)
		if cfg.BaseURL != "" {
			oacfg.BaseURL = cfg.BaseURL
		}
		client = openai.NewClientWithConfig(oacfg)
	}
	return &OpenAIProvider{client: client, model: model, system: system, registry: registry}
}

// RunTurn implements [Provider] for OpenAI.
func (p *OpenAIProvider) RunTurn(ctx context.Context, messages []map[string]any, _ []ToolDef) ([]map[string]any, bool, error) {
	// Build full message list with system prefix.
	full := make([]map[string]any, 0, len(messages)+1)
	full = append(full, map[string]any{"role": "system", "content": p.system})
	full = append(full, messages...)

	chatMsgs, err := mapsToOpenAIMessages(full)
	if err != nil {
		return nil, false, fmt.Errorf("toolregistry: marshal messages: %w", err)
	}

	oaiTools := make([]openai.Tool, 0, len(p.registry.defs))
	for _, d := range p.registry.defs {
		params, _ := json.Marshal(d.InputSchema)
		oaiTools = append(oaiTools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  json.RawMessage(params),
			},
		})
	}

	resp, err := p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: chatMsgs,
		Tools:    oaiTools,
	})
	if err != nil {
		return nil, false, fmt.Errorf("toolregistry: openai api: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, true, nil
	}
	choice := resp.Choices[0]
	msg := choice.Message

	msgMap := map[string]any{"role": "assistant", "content": msg.Content}
	if len(msg.ToolCalls) > 0 {
		calls := make([]map[string]any, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			calls[i] = map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				},
			}
		}
		msgMap["tool_calls"] = calls
	}

	var newMsgs []map[string]any
	newMsgs = append(newMsgs, msgMap)

	if len(msg.ToolCalls) == 0 || choice.FinishReason == openai.FinishReasonStop || choice.FinishReason == openai.FinishReasonLength {
		return newMsgs, true, nil
	}

	for _, tc := range msg.ToolCalls {
		var input map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &input) //nolint:errcheck
		result, err := p.registry.Dispatch(tc.Function.Name, input)
		if err != nil {
			result = fmt.Sprintf("error: %s", err)
		}
		newMsgs = append(newMsgs, map[string]any{
			"role":         "tool",
			"tool_call_id": tc.ID,
			"content":      result,
		})
	}
	return newMsgs, false, nil
}

// ── JSON conversion helpers ───────────────────────────────────────────────────

// mapsToAnthropicMessages converts a []map[string]any message history to
// []anthropic.MessageParam via JSON round-trip.
func mapsToAnthropicMessages(msgs []map[string]any) ([]anthropic.MessageParam, error) {
	data, err := json.Marshal(msgs)
	if err != nil {
		return nil, err
	}
	var params []anthropic.MessageParam
	return params, json.Unmarshal(data, &params)
}

// mapsToOpenAIMessages converts a []map[string]any message history to
// []openai.ChatCompletionMessage via JSON round-trip.
func mapsToOpenAIMessages(msgs []map[string]any) ([]openai.ChatCompletionMessage, error) {
	data, err := json.Marshal(msgs)
	if err != nil {
		return nil, err
	}
	var chatMsgs []openai.ChatCompletionMessage
	return chatMsgs, json.Unmarshal(data, &chatMsgs)
}

// contentBlocksToMaps converts Anthropic response content blocks to
// []map[string]any via JSON round-trip for heartbeat-safe storage.
func contentBlocksToMaps(blocks []anthropic.ContentBlockUnion) ([]map[string]any, error) {
	data, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	var maps []map[string]any
	return maps, json.Unmarshal(data, &maps)
}

// toolDefsToAnthropicTools converts []ToolDef to []anthropic.ToolUnionParam.
// Each ToolDef is marshalled to a ToolParam via JSON round-trip and then
// wrapped in ToolUnionParam.OfTool.
func toolDefsToAnthropicTools(defs []ToolDef) ([]anthropic.ToolUnionParam, error) {
	result := make([]anthropic.ToolUnionParam, len(defs))
	for i, def := range defs {
		data, err := json.Marshal(def)
		if err != nil {
			return nil, err
		}
		var tp anthropic.ToolParam
		if err := json.Unmarshal(data, &tp); err != nil {
			return nil, err
		}
		result[i] = anthropic.ToolUnionParam{OfTool: &tp}
	}
	return result, nil
}
