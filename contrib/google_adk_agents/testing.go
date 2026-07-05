// Copyright 2026 Google LLC, Temporal Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.

package googleadk

import (
	"context"
	"iter"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
)

// FakeModel is a model.LLM that replays a scripted sequence of responses, so
// users can test their workflows through the plugin without a live LLM endpoint.
// Register it via a ModelFactory keyed by its Name. Each call to GenerateContent
// yields the next scripted response (the last one repeats once exhausted).
type FakeModel struct {
	name      string
	responses []*model.LLMResponse

	mu   sync.Mutex
	next int
}

// NewFakeModel builds a FakeModel named "fake-model" that replays responses in
// order. Use FunctionCallResponse / TextResponse to construct the script.
func NewFakeModel(responses ...*model.LLMResponse) *FakeModel {
	return &FakeModel{name: "fake-model", responses: responses}
}

// WithName overrides the model name (the key used in Config.Models and set on
// LLMRequest.Model). It returns the receiver for chaining.
func (m *FakeModel) WithName(name string) *FakeModel {
	m.name = name
	return m
}

// Name implements model.LLM.
func (m *FakeModel) Name() string { return m.name }

// GenerateContent implements model.LLM by yielding the next scripted response.
func (m *FakeModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.mu.Lock()
		var resp *model.LLMResponse
		switch {
		case len(m.responses) == 0:
			resp = TextResponse("")
		case m.next < len(m.responses):
			resp = m.responses[m.next]
			m.next++
		default:
			resp = m.responses[len(m.responses)-1]
		}
		m.mu.Unlock()
		yield(resp, nil)
	}
}

// TextResponse builds a final text LLMResponse.
func TextResponse(text string) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{Text: text}},
		},
		TurnComplete: true,
	}
}

// FunctionCallResponse builds an LLMResponse that asks the agent to call the
// named tool with the given arguments. The id ties the call to its response.
func FunctionCallResponse(id, name string, args map[string]any) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{
			Role: genai.RoleModel,
			Parts: []*genai.Part{{
				FunctionCall: &genai.FunctionCall{ID: id, Name: name, Args: args},
			}},
		},
	}
}

// FakeMCPServer is an in-memory tool.Toolset standing in for a real MCP server,
// for use as an MCPFactory result in tests. Register tools with AddTool; the
// toolset advertises their declarations and runs their handlers — exactly the
// surface ListMcpTools and CallMcpTool exercise.
type FakeMCPServer struct {
	name  string
	tools []tool.Tool
}

// NewFakeMCPServer returns an empty FakeMCPServer with the given toolset name.
func NewFakeMCPServer(name string) *FakeMCPServer {
	return &FakeMCPServer{name: name}
}

// AddTool registers a tool on the fake server. handler is invoked when the tool
// runs worker-side; schema describes its parameters (may be nil).
func (s *FakeMCPServer) AddTool(name, description string, schema *genai.Schema, handler func(args map[string]any) (map[string]any, error)) *FakeMCPServer {
	s.tools = append(s.tools, &fakeMCPTool{
		decl:    &genai.FunctionDeclaration{Name: name, Description: description, Parameters: schema},
		handler: handler,
	})
	return s
}

// Name implements tool.Toolset.
func (s *FakeMCPServer) Name() string { return s.name }

// Tools implements tool.Toolset.
func (s *FakeMCPServer) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return s.tools, nil
}

// Factory returns an MCPFactory that yields this server, for Config.MCPToolsets.
func (s *FakeMCPServer) Factory() MCPFactory {
	return func(context.Context) (tool.Toolset, error) { return s, nil }
}

type fakeMCPTool struct {
	decl    *genai.FunctionDeclaration
	handler func(args map[string]any) (map[string]any, error)
}

func (t *fakeMCPTool) Name() string                            { return t.decl.Name }
func (t *fakeMCPTool) Description() string                     { return t.decl.Description }
func (t *fakeMCPTool) IsLongRunning() bool                     { return false }
func (t *fakeMCPTool) Declaration() *genai.FunctionDeclaration { return t.decl }

func (t *fakeMCPTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	m, _ := args.(map[string]any)
	return t.handler(m)
}
