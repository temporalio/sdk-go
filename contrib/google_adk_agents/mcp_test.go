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

package googleadk_test

import (
	"context"
	"iter"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"

	"google.golang.org/genai"

	"google.golang.org/adk/model"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

// recordingModel wraps a FakeModel and records the tool declarations carried in
// each LLMRequest it receives. Because LLMRequest.Tools is json:"-" but
// LLMRequest.Config.Tools (the genai declarations) crosses the wire, this proves
// the MCP tool's full parameter schema reached the model via ListMcpTools.
type recordingModel struct {
	inner *googleadk.FakeModel

	mu    sync.Mutex
	decls []*genai.FunctionDeclaration
}

func newRecordingModel(name string, responses ...*model.LLMResponse) *recordingModel {
	return &recordingModel{inner: googleadk.NewFakeModel(responses...).WithName(name)}
}

func (m *recordingModel) Name() string { return m.inner.Name() }

func (m *recordingModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	if req.Config != nil {
		for _, gt := range req.Config.Tools {
			if gt != nil {
				m.decls = append(m.decls, gt.FunctionDeclarations...)
			}
		}
	}
	m.mu.Unlock()
	return m.inner.GenerateContent(ctx, req, stream)
}

// declFor returns the recorded declaration with the given name, if any.
func (m *recordingModel) declFor(name string) *genai.FunctionDeclaration {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.decls {
		if d != nil && d.Name == name {
			return d
		}
	}
	return nil
}

// searchSchema is the parameter schema the fake MCP server advertises for its
// "search" tool. ListMcpTools must carry it across to the model.
func searchSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"query": {Type: genai.TypeString, Description: "the search query"},
		},
		Required: []string{"query"},
	}
}

// TestMcpListToolsCarriesParameters proves NewMCPToolset advertises the full
// declaration (name + description + PARAMETERS) of each remote MCP tool, not just
// {name, description}: the parameter schema travels ListMcpTools -> packTool ->
// the InvokeModel request and is observed by the model.
func TestMcpListToolsCarriesParameters(t *testing.T) {
	rm := newRecordingModel("fake-model",
		googleadk.FunctionCallResponse("c1", "search", map[string]any{"query": "temporal"}),
		googleadk.TextResponse("found it"),
	)
	server := googleadk.NewFakeMCPServer("search-server").AddTool(
		"search", "search the web", searchSchema(),
		func(args map[string]any) (map[string]any, error) {
			return map[string]any{"hits": 1}, nil
		},
	)

	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": func(context.Context, string) (model.LLM, error) { return rm, nil },
		},
		MCPToolsets: map[string]googleadk.MCPFactory{"search-server": server.Factory()},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "search the web",
		MCPToolset:  "search-server",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// The model saw the MCP tool with its full parameter schema.
	decl := rm.declFor("search")
	require.NotNil(t, decl, "model never saw the MCP tool declaration")
	require.NotNil(t, decl.Parameters, "MCP tool declaration carried no parameters")
	assert.Contains(t, decl.Parameters.Properties, "query")

	// The tools were listed (>=1, packed each turn) and called once.
	assert.GreaterOrEqual(t, counter.get(googleadk.ListMcpToolsActivityName), 1)
	assert.Equal(t, 1, counter.get(googleadk.CallMcpToolActivityName))
}

// TestMcpToolCallRoundTrip proves a model-issued MCP tool call is short-circuited
// into the CallMcpTool Activity, executed worker-side against the live (here,
// fake) toolset, and its result fed back into the agent loop.
func TestMcpToolCallRoundTrip(t *testing.T) {
	var (
		mu      sync.Mutex
		gotArgs map[string]any
	)
	server := googleadk.NewFakeMCPServer("search-server").AddTool(
		"search", "search the web", searchSchema(),
		func(args map[string]any) (map[string]any, error) {
			mu.Lock()
			gotArgs = args
			mu.Unlock()
			return map[string]any{"answer": "42"}, nil
		},
	)

	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "search", map[string]any{"query": "meaning of life"}),
				googleadk.TextResponse("the answer is 42"),
			),
		},
		MCPToolsets: map[string]googleadk.MCPFactory{"search-server": server.Factory()},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "what is the meaning of life",
		MCPToolset:  "search-server",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.ToolResponses, "search")
	assert.Contains(t, res.Texts, "the answer is 42")
	assert.Equal(t, 1, counter.get(googleadk.CallMcpToolActivityName))

	mu.Lock()
	assert.Equal(t, "meaning of life", gotArgs["query"])
	mu.Unlock()
}
