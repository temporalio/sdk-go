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
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// ModelFactory reconstructs a model.LLM worker-side from the model name carried
// in LLMRequest.Model. API keys and other credentials are captured in the
// closure and therefore live only on the worker — they are never serialized
// into Activity inputs.
type ModelFactory func(ctx context.Context, modelName string) (model.LLM, error)

// MCPFactory constructs a live MCP tool.Toolset worker-side. The real
// mcptoolset.New(...) — which holds a network session and spawns goroutines —
// lives here, behind the Activity boundary, never in the workflow.
type MCPFactory func(ctx context.Context) (tool.Toolset, error)

// Config declares the worker-side registries the Activities consult when a
// model, tool or MCP call crosses the Activity boundary. Construct it once at
// worker start and hand it to NewActivities.
type Config struct {
	// Models maps a model name (the string the user sets on their model.LLM and
	// that ADK copies into LLMRequest.Model) to a factory that rebuilds it
	// worker-side. The InvokeModel Activity looks the factory up by name.
	Models map[string]ModelFactory

	// Tools is the set of runnable tools (functiontool.New(...), ActivityAsTool,
	// etc.) the agent may call. The CallTool Activity dispatches to the matching
	// tool by name. Every tool must be runnable (expose a Run method).
	Tools []tool.Tool

	// MCPToolsets maps the logical name passed to NewMCPToolset to a factory that
	// builds the live stateless MCP toolset worker-side. The ListMcpTools and
	// CallMcpTool Activities use it.
	MCPToolsets map[string]MCPFactory
}

// Activities holds the worker-side registries and implements the four Temporal
// Activities the plugin dispatches: InvokeModel, CallTool, ListMcpTools and
// CallMcpTool. Build one with NewActivities and register it with Register.
type Activities struct {
	models map[string]ModelFactory
	tools  map[string]tool.Tool
	mcp    map[string]MCPFactory
}

// NewActivities validates cfg and returns the worker-side Activities. It returns
// an error if a registered tool is nil or not runnable, so misconfiguration
// surfaces at worker start rather than mid-run inside an Activity.
func NewActivities(cfg Config) (*Activities, error) {
	a := &Activities{
		models: make(map[string]ModelFactory, len(cfg.Models)),
		tools:  make(map[string]tool.Tool, len(cfg.Tools)),
		mcp:    make(map[string]MCPFactory, len(cfg.MCPToolsets)),
	}
	for name, f := range cfg.Models {
		if f == nil {
			return nil, fmt.Errorf("google_adk_agents: nil ModelFactory registered for model %q", name)
		}
		a.models[name] = f
	}
	for _, t := range cfg.Tools {
		if t == nil {
			return nil, fmt.Errorf("google_adk_agents: nil tool registered in Config.Tools")
		}
		if _, ok := t.(runnable); !ok {
			return nil, fmt.Errorf("google_adk_agents: tool %q is not runnable (no Run method); "+
				"only functiontool.New(...) tools and ActivityAsTool(...) can execute as Activities", t.Name())
		}
		a.tools[t.Name()] = t
	}
	for name, f := range cfg.MCPToolsets {
		if f == nil {
			return nil, fmt.Errorf("google_adk_agents: nil MCPFactory registered for toolset %q", name)
		}
		a.mcp[name] = f
	}
	return a, nil
}

// Register wires InvokeModel, CallTool, ListMcpTools and CallMcpTool onto the
// worker under their stable Activity names. Call it once per worker that should
// be able to service ADK model/tool calls.
func (a *Activities) Register(r worker.Registry) {
	r.RegisterActivityWithOptions(a.InvokeModel, activity.RegisterOptions{Name: InvokeModelActivityName})
	r.RegisterActivityWithOptions(a.CallTool, activity.RegisterOptions{Name: CallToolActivityName})
	r.RegisterActivityWithOptions(a.ListMcpTools, activity.RegisterOptions{Name: ListMcpToolsActivityName})
	r.RegisterActivityWithOptions(a.CallMcpTool, activity.RegisterOptions{Name: CallMcpToolActivityName})
}
