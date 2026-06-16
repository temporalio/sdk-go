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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

// toolSpec describes a stub tool the in-workflow agent advertises to the model.
// Its handler is a no-op: the real handler lives worker-side in the Activities
// registry, keyed by the same name.
type toolSpec struct {
	Name        string
	Description string
}

// subAgentSpec describes a child agent in a SubAgents tree.
type subAgentSpec struct {
	Name        string
	Description string
	ModelName   string
}

// runInput is the serializable input to agentRunWorkflow.
type runInput struct {
	ModelName        string
	UserMessage      string
	Tools            []toolSpec
	SubAgents        []subAgentSpec
	MCPToolset       string
	Sequential       bool
	StreamingTopic   string
	PerModelTimeouts map[string]time.Duration
	UseCustomSummary bool
}

// runResult is the serializable output of agentRunWorkflow.
type runResult struct {
	Texts         []string
	FunctionCalls []string
	ToolResponses []string
	Authors       []string
}

// summaryFnCalled is flipped by the custom SummaryFn installed when
// runInput.UseCustomSummary is set, so a test can prove the SummaryFn ran on the
// dispatch path rather than merely being stored on Options.
var summaryFnCalled atomic.Bool

// inWorkflowToolRan is flipped by the pure-compute tool that
// inWorkflowToolWorkflow opts into in-workflow execution, proving its handler ran
// inside the workflow coroutine rather than in a CallTool Activity.
var inWorkflowToolRan atomic.Bool

// agentBuild is the fully-built input to runAgent: a real ADK agent tree plus the
// plugin options and context options used to drive it.
type agentBuild struct {
	opts        googleadk.Options
	ctxOpts     []googleadk.ContextOption
	modelName   string
	userMessage string
	tools       []tool.Tool
	toolsets    []tool.Toolset
	subAgents   []agent.Agent
}

// runAgent builds a real ADK llmagent + runner, wires the Temporal plugin and
// bridged context, and drives runner.Run — the native ADK entry point — entirely
// inside the workflow. Every model and tool call is short-circuited into a
// Temporal Activity by the plugin.
func runAgent(ctx workflow.Context, b agentBuild) (runResult, error) {
	pl, err := googleadk.Plugin(b.opts)
	if err != nil {
		return runResult{}, err
	}

	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "root assistant",
		Model:       googleadk.NewFakeModel().WithName(b.modelName),
		Instruction: "be helpful",
		Tools:       b.tools,
		Toolsets:    b.toolsets,
		SubAgents:   b.subAgents,
	})
	if err != nil {
		return runResult{}, err
	}

	r, err := runner.New(runner.Config{
		AppName:           "test-app",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		PluginConfig:      runner.PluginConfig{Plugins: []*plugin.Plugin{pl}},
		AutoCreateSession: true,
	})
	if err != nil {
		return runResult{}, err
	}

	runCfg := agent.RunConfig{}
	if b.opts.StreamingTopic != "" {
		runCfg.StreamingMode = agent.StreamingModeSSE
		// Streaming to external consumers requires the workflow-side stream server
		// so the InvokeModel Activity's published chunks have somewhere to land.
		if err := googleadk.StreamServer(ctx); err != nil {
			return runResult{}, err
		}
	}

	adkCtx := googleadk.NewContext(ctx, b.ctxOpts...)
	msg := genai.NewContentFromText(b.userMessage, genai.RoleUser)

	var res runResult
	for ev, rerr := range r.Run(adkCtx, "user-1", "session-1", msg, runCfg) {
		if rerr != nil {
			return res, rerr
		}
		if ev == nil {
			continue
		}
		res.Authors = append(res.Authors, ev.Author)
		if ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if p.Text != "" {
				res.Texts = append(res.Texts, p.Text)
			}
			if p.FunctionCall != nil {
				res.FunctionCalls = append(res.FunctionCalls, p.FunctionCall.Name)
			}
			if p.FunctionResponse != nil {
				res.ToolResponses = append(res.ToolResponses, p.FunctionResponse.Name)
			}
		}
	}
	return res, nil
}

// agentRunWorkflow assembles an agentBuild from a serializable runInput and runs
// it. Stub tools/sub-agents are declaration-only; the real handlers live
// worker-side in the Activities registry.
func agentRunWorkflow(ctx workflow.Context, in runInput) (runResult, error) {
	opts := googleadk.Options{
		StreamingTopic:   in.StreamingTopic,
		PerModelTimeouts: in.PerModelTimeouts,
	}
	if in.UseCustomSummary {
		opts.SummaryFn = func(*model.LLMRequest) string {
			summaryFnCalled.Store(true)
			return "custom-summary"
		}
	}

	var tools []tool.Tool
	for _, ts := range in.Tools {
		t, terr := stubTool(ts.Name, ts.Description)
		if terr != nil {
			return runResult{}, terr
		}
		tools = append(tools, t)
	}

	var toolsets []tool.Toolset
	if in.MCPToolset != "" {
		toolsets = append(toolsets, googleadk.NewMCPToolset(googleadk.MCPToolsetOptions{Name: in.MCPToolset}))
	}

	var subAgents []agent.Agent
	for _, sa := range in.SubAgents {
		child, cerr := llmagent.New(llmagent.Config{
			Name:        sa.Name,
			Description: sa.Description,
			Model:       googleadk.NewFakeModel().WithName(sa.ModelName),
			Instruction: "child agent",
		})
		if cerr != nil {
			return runResult{}, cerr
		}
		subAgents = append(subAgents, child)
	}

	var ctxOpts []googleadk.ContextOption
	if in.Sequential {
		ctxOpts = append(ctxOpts, googleadk.WithSequentialToolFanout())
	}

	return runAgent(ctx, agentBuild{
		opts:        opts,
		ctxOpts:     ctxOpts,
		modelName:   in.ModelName,
		userMessage: in.UserMessage,
		tools:       tools,
		toolsets:    toolsets,
		subAgents:   subAgents,
	})
}

// ----------------------------------------------------------------------------
// Test helpers.
// ----------------------------------------------------------------------------

// stubTool builds a declaration-only function tool whose handler is a no-op. The
// in-workflow agent advertises it to the model; its real worker-side twin runs in
// the CallTool Activity.
func stubTool(name, description string) (tool.Tool, error) {
	return functiontool.New[map[string]any, map[string]any](
		functiontool.Config{Name: name, Description: description},
		func(agent.ToolContext, map[string]any) (map[string]any, error) { return nil, nil },
	)
}

// funcTool builds a real function tool with the given handler.
func funcTool(name string, handler func(agent.ToolContext, map[string]any) (map[string]any, error)) (tool.Tool, error) {
	return functiontool.New[map[string]any, map[string]any](
		functiontool.Config{Name: name, Description: name + " tool"},
		handler,
	)
}

// testRegistry adapts a TestWorkflowEnvironment to worker.Registry so the real
// Activities.Register path (the exported worker-wiring helper) is exercised by
// the tests. The Nexus method is unused here.
type testRegistry struct {
	*testsuite.TestWorkflowEnvironment
}

func (testRegistry) RegisterNexusService(*nexus.Service) {}

// activityCounter records how many times each Activity type is scheduled, so
// tests can assert exact per-Activity counts (the side-effects criterion).
type activityCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newActivityCounter() *activityCounter {
	return &activityCounter{counts: map[string]int{}}
}

func (c *activityCounter) record(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[name]++
}

func (c *activityCounter) get(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[name]
}

// wireActivities registers the plugin Activities (via the real Register path)
// onto env and installs an activity-start counter.
func wireActivities(t *testing.T, env *testsuite.TestWorkflowEnvironment, cfg googleadk.Config) *activityCounter {
	t.Helper()
	acts, err := googleadk.NewActivities(cfg)
	require.NoError(t, err)
	acts.Register(testRegistry{env})

	counter := newActivityCounter()
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		counter.record(info.ActivityType.Name)
	})
	return counter
}

// newEnv builds a test workflow environment with agentRunWorkflow registered and
// the plugin Activities wired from cfg.
func newEnv(t *testing.T, s *testsuite.WorkflowTestSuite, cfg googleadk.Config) (*testsuite.TestWorkflowEnvironment, *activityCounter) {
	t.Helper()
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(agentRunWorkflow)
	counter := wireActivities(t, env, cfg)
	return env, counter
}

// scriptedModelFactory returns a ModelFactory that yields a single shared
// FakeModel replaying the given scripted responses. Sharing one instance across
// Activity invocations lets the scripted sequence advance turn by turn (turn 1
// returns the first response, turn 2 the second, ...).
func scriptedModelFactory(responses ...*model.LLMResponse) googleadk.ModelFactory {
	fm := googleadk.NewFakeModel(responses...)
	return func(context.Context, string) (model.LLM, error) {
		return fm, nil
	}
}

// recordingTool builds a real worker-side function tool whose handler records
// the args it was called with and returns the supplied result.
func recordingTool(t *testing.T, name string, result map[string]any, onCall func(map[string]any)) tool.Tool {
	t.Helper()
	ft, err := funcTool(name, func(_ agent.ToolContext, args map[string]any) (map[string]any, error) {
		if onCall != nil {
			onCall(args)
		}
		return result, nil
	})
	require.NoError(t, err)
	return ft
}
