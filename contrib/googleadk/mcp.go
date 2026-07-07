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

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
)

// MCPToolsetOptions configures NewMCPToolset.
type MCPToolsetOptions struct {
	// Name is the logical toolset name. It must match a key in
	// Config.MCPToolsets so the worker-side factory can be found.
	Name string
	// ToolFilter optionally restricts which advertised MCP tools are exposed to
	// the model. It runs on the workflow side over declaration-only proxy tools.
	ToolFilter tool.Predicate
	// ActivityOptions overrides the per-call Activity options for the ListMcpTools
	// and CallMcpTool Activities. A zero StartToCloseTimeout falls back to the
	// default one-minute tool timeout.
	ActivityOptions workflow.ActivityOptions
}

// NewMCPToolset returns a workflow-side, stateless proxy for an MCP toolset. It
// advertises the remote tools (full declarations, including parameters) via the
// ListMcpTools Activity and executes each call via the CallMcpTool Activity. The
// real, stateful mcptoolset.New(...) — which holds a live network session — runs
// worker-side behind those Activities, never in the workflow.
//
// Add the returned Toolset to your agent like any other:
//
//	a, _ := llmagent.New(llmagent.Config{
//	    Model:    myModel,
//	    Toolsets: []tool.Toolset{googleadk.NewMCPToolset(googleadk.MCPToolsetOptions{Name: "filesystem"})},
//	})
func NewMCPToolset(opts MCPToolsetOptions) tool.Toolset {
	return &mcpToolset{name: opts.Name, filter: opts.ToolFilter, activityOptions: opts.ActivityOptions}
}

// mcpToolset is the workflow-side proxy. It implements tool.Toolset and ADK's
// internal RequestProcessor (so its proxy tools are packed into the model
// request).
type mcpToolset struct {
	name            string
	filter          tool.Predicate
	activityOptions workflow.ActivityOptions
}

func (m *mcpToolset) Name() string { return m.name }

// Tools lists the remote MCP tools via the ListMcpTools Activity and wraps each
// as a declaration-only proxy tool.
func (m *mcpToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	wfCtx, ok := workflowContext(ctx)
	if !ok {
		return nil, errMissingContext
	}
	actx := workflow.WithActivityOptions(wfCtx, resolveToolActivityOptions(m.activityOptions, ""))
	var decls []*genai.FunctionDeclaration
	if err := workflow.ExecuteActivity(actx, ListMcpToolsActivityName, mcpListInput{Toolset: m.name}).Get(wfCtx, &decls); err != nil {
		return nil, err
	}
	tools := make([]tool.Tool, 0, len(decls))
	for _, d := range decls {
		pt := &mcpProxyTool{toolset: m.name, decl: d, activityOptions: m.activityOptions}
		if m.filter == nil || m.filter(ctx, pt) {
			tools = append(tools, pt)
		}
	}
	return tools, nil
}

// ProcessRequest packs every advertised proxy tool's declaration into the model
// request. ADK calls this during toolset preprocessing.
func (m *mcpToolset) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	tools, err := m.Tools(ctx)
	if err != nil {
		return err
	}
	for _, t := range tools {
		if err := packTool(req, t.(*mcpProxyTool)); err != nil {
			return err
		}
	}
	return nil
}

// mcpProxyTool is a declaration-only stand-in for a remote MCP tool. The model
// sees its declaration; when the model calls it, Run (invoked in-workflow by
// ADK) dispatches the CallMcpTool Activity so the live, stateful MCP session
// runs worker-side, never in the workflow.
type mcpProxyTool struct {
	toolset         string
	decl            *genai.FunctionDeclaration
	activityOptions workflow.ActivityOptions
}

func (t *mcpProxyTool) Name() string                            { return t.decl.Name }
func (t *mcpProxyTool) Description() string                     { return t.decl.Description }
func (t *mcpProxyTool) IsLongRunning() bool                     { return false }
func (t *mcpProxyTool) Declaration() *genai.FunctionDeclaration { return t.decl }

func (t *mcpProxyTool) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	return packTool(req, t)
}

// Run executes in-workflow and dispatches the CallMcpTool Activity, which builds
// the live MCP toolset worker-side and runs the named tool over a reconstructed,
// read-only context. MCP calls are inherently I/O, so they always run as an
// Activity.
func (t *mcpProxyTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	wfCtx, ok := workflowContext(ctx)
	if !ok {
		return nil, errMissingContext
	}
	ao := resolveToolActivityOptions(t.activityOptions, "")
	ao.Summary = toolSummary(ctx, t.Name())
	actx := workflow.WithActivityOptions(wfCtx, ao)
	argsMap, _ := args.(map[string]any)
	in := mcpCallInput{Toolset: t.toolset, Tool: t.Name(), Args: argsMap, Ctx: snapshotContext(ctx)}
	var res map[string]any
	if err := workflow.ExecuteActivity(actx, CallMcpToolActivityName, in).Get(wfCtx, &res); err != nil {
		return nil, err
	}
	if res == nil {
		res = map[string]any{}
	}
	return res, nil
}

// mcpListInput is the serializable payload for ListMcpTools.
type mcpListInput struct {
	Toolset string
}

// mcpCallInput is the serializable payload for CallMcpTool.
type mcpCallInput struct {
	Toolset string
	Tool    string
	Args    map[string]any
	Ctx     contextSnapshot
}

// ListMcpTools returns the full declarations (name + description + parameters)
// of the tools in the named MCP toolset, by constructing the live toolset
// worker-side and listing it. Returning full declarations — not just
// {name, description} — lets the model produce well-formed arguments.
func (a *Activities) ListMcpTools(ctx context.Context, in mcpListInput) ([]*genai.FunctionDeclaration, error) {
	log := activity.GetLogger(ctx)
	ts, err := a.mcpToolset(ctx, in.Toolset)
	if err != nil {
		return nil, err
	}
	tools, err := ts.Tools(newActivityToolContext(ctx, contextSnapshot{}))
	if err != nil {
		return nil, newApplicationError(ErrorTypeMCP, true, err,
			"list tools for MCP toolset %q: %v", in.Toolset, err)
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		if d, ok := t.(interface {
			Declaration() *genai.FunctionDeclaration
		}); ok {
			decls = append(decls, d.Declaration())
		}
	}
	log.Debug("listed MCP tools", "toolset", in.Toolset, "count", len(decls))
	return decls, nil
}

// CallMcpTool executes a single tool of the named MCP toolset worker-side over a
// reconstructed, read-only ToolContext.
func (a *Activities) CallMcpTool(ctx context.Context, in mcpCallInput) (map[string]any, error) {
	log := activity.GetLogger(ctx)
	ts, err := a.mcpToolset(ctx, in.Toolset)
	if err != nil {
		return nil, err
	}
	tctx := newActivityToolContext(ctx, in.Ctx)
	tools, err := ts.Tools(tctx)
	if err != nil {
		return nil, newApplicationError(ErrorTypeMCP, true, err,
			"list tools for MCP toolset %q: %v", in.Toolset, err)
	}
	for _, t := range tools {
		if t.Name() != in.Tool {
			continue
		}
		rt, ok := t.(runnable)
		if !ok {
			return nil, newApplicationError(ErrorTypeMCP, false, nil,
				"MCP tool %q is not runnable", in.Tool)
		}
		log.Debug("running MCP tool", "toolset", in.Toolset, "tool", in.Tool)
		res, runErr := rt.Run(tctx, in.Args)
		if runErr != nil {
			return nil, classifyToolError(ErrorTypeMCP, in.Tool, runErr)
		}
		return res, nil
	}
	return nil, newApplicationError(ErrorTypeMCP, false, nil,
		"tool %q not found in MCP toolset %q", in.Tool, in.Toolset)
}

func (a *Activities) mcpToolset(ctx context.Context, name string) (tool.Toolset, error) {
	f, ok := a.mcp[name]
	if !ok {
		return nil, newApplicationError(ErrorTypeMCP, false, nil,
			"no MCP toolset %q registered; add it to Config.MCPToolsets", name)
	}
	ts, err := f(ctx)
	if err != nil {
		return nil, newApplicationError(ErrorTypeMCP, true, err,
			"construct MCP toolset %q: %v", name, err)
	}
	return ts, nil
}

// packTool advertises a proxy tool's declaration to the model via ADK's public
// tool-declaration packer (tool/toolutils), recording the live tool under
// req.Tools (json:"-", so it never crosses the wire) so ADK can later resolve it
// to dispatch. It is idempotent: ADK runs both toolPreprocess (each expanded
// toolset tool's ProcessRequest) and toolsetPreprocess (the toolset's own
// ProcessRequest), so a toolset tool is packed twice with identical
// declarations. A name already present is skipped, rather than erroring as
// toolutils.PackTool does on a duplicate.
func packTool(req *model.LLMRequest, t toolutils.Tool) error {
	if req.Tools != nil {
		if _, ok := req.Tools[t.Name()]; ok {
			return nil
		}
	}
	return toolutils.PackTool(req, t)
}
