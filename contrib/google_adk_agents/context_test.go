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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/platform"
	"google.golang.org/adk/tool"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

// twoFunctionCalls is a single LLM response carrying two parallel function calls,
// so ADK fans them out through platform.RunTasks (the seam NewContext binds to
// workflow.Go / sequential execution).
func twoFunctionCalls() *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{
			Role: genai.RoleModel,
			Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "t1", Args: map[string]any{}}},
				{FunctionCall: &genai.FunctionCall{ID: "c2", Name: "t2", Args: map[string]any{}}},
			},
		},
	}
}

// concurrencyProbe records the peak number of tool handlers running at once. Each
// handler blocks at a barrier until `target` handlers have entered (or a timeout
// elapses), so concurrent fan-out reaches the barrier (max == target) while
// sequential fan-out never does (max == 1).
type concurrencyProbe struct {
	mu      sync.Mutex
	active  int
	maxSeen int
	target  int
	waitFor time.Duration
	reached chan struct{}
	once    sync.Once
}

func newConcurrencyProbe(target int, waitFor time.Duration) *concurrencyProbe {
	return &concurrencyProbe{target: target, waitFor: waitFor, reached: make(chan struct{})}
}

func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	p.active++
	if p.active > p.maxSeen {
		p.maxSeen = p.active
	}
	if p.active >= p.target {
		p.once.Do(func() { close(p.reached) })
	}
	p.mu.Unlock()

	select {
	case <-p.reached:
	case <-time.After(p.waitFor):
	}

	p.mu.Lock()
	p.active--
	p.mu.Unlock()
}

func (p *concurrencyProbe) max() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxSeen
}

// probeTool builds a worker-side tool whose handler signals the probe.
func probeTool(t *testing.T, name string, p *concurrencyProbe) tool.Tool {
	t.Helper()
	ft, err := funcTool(name, func(agent.ToolContext, map[string]any) (map[string]any, error) {
		p.enter()
		return map[string]any{"ok": true}, nil
	})
	require.NoError(t, err)
	return ft
}

// TestConcurrentFanoutSchedulesParallel flips to the default concurrent
// TaskRunner (workflow.Go) and observes two tool Activities running at the same
// time when a single LLM turn fans out to two tool calls.
func TestConcurrentFanoutSchedulesParallel(t *testing.T) {
	probe := newConcurrencyProbe(2, 3*time.Second)

	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(twoFunctionCalls(), googleadk.TextResponse("done")),
		},
		Tools: []tool.Tool{probeTool(t, "t1", probe), probeTool(t, "t2", probe)},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "call both",
		Tools:       []toolSpec{{Name: "t1", Description: "tool one"}, {Name: "t2", Description: "tool two"}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, 2, probe.max(), "concurrent fan-out should run both tools at once")
	assert.Equal(t, 2, counter.get(googleadk.CallToolActivityName))
	assert.Equal(t, 2, counter.get(googleadk.InvokeModelActivityName))
}

// TestSequentialFanoutSchedulesSerially flips WithSequentialToolFanout and
// observes the two tool calls never overlap (peak concurrency 1), proving the
// option changes the dispatch path rather than merely being stored.
func TestSequentialFanoutSchedulesSerially(t *testing.T) {
	// Target 2 is never reached in sequential mode, so each handler hits the
	// (short) timeout; peak concurrency stays at 1.
	probe := newConcurrencyProbe(2, 300*time.Millisecond)

	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(twoFunctionCalls(), googleadk.TextResponse("done")),
		},
		Tools: []tool.Tool{probeTool(t, "t1", probe), probeTool(t, "t2", probe)},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "call both",
		Tools:       []toolSpec{{Name: "t1", Description: "tool one"}, {Name: "t2", Description: "tool two"}},
		Sequential:  true,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, 1, probe.max(), "sequential fan-out should never overlap tool calls")
	assert.Equal(t, 2, counter.get(googleadk.CallToolActivityName))
}

// timeProviderWorkflow reports whether ADK's platform.Now, bound by NewContext,
// resolves to the workflow's deterministic clock.
func timeProviderWorkflow(ctx workflow.Context) (bool, error) {
	adkCtx := googleadk.NewContext(ctx)
	return platform.Now(adkCtx).Equal(workflow.Now(ctx)), nil
}

// TestDeterministicTimeProvider proves NewContext binds ADK's time source to
// workflow.Now, so every ADK event timestamp is replay-stable.
func TestDeterministicTimeProvider(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(timeProviderWorkflow)
	env.ExecuteWorkflow(timeProviderWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var equal bool
	require.NoError(t, env.GetWorkflowResult(&equal))
	assert.True(t, equal, "platform.Now must resolve to workflow.Now")
}

// uuidWorkflow generates n IDs through ADK's platform.NewUUID, bound by
// NewContext to the deterministic seeded generator.
func uuidWorkflow(ctx workflow.Context, n int) ([]string, error) {
	adkCtx := googleadk.NewContext(ctx)
	ids := make([]string, n)
	for i := range ids {
		ids[i] = platform.NewUUID(adkCtx)
	}
	return ids, nil
}

// TestDeterministicUUIDProvider proves NewContext installs a deterministic UUID
// generator: the IDs are well-formed and unique within a run, and (because they
// derive from a single SideEffect seed plus a counter) replay-stable — the
// replay test exercises the cross-replay guarantee end to end.
func TestDeterministicUUIDProvider(t *testing.T) {
	const n = 8
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(uuidWorkflow)
	env.ExecuteWorkflow(uuidWorkflow, n)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var ids []string
	require.NoError(t, env.GetWorkflowResult(&ids))
	require.Len(t, ids, n)

	seen := map[string]bool{}
	for _, id := range ids {
		_, err := uuid.Parse(id)
		require.NoError(t, err, "id %q is not a valid UUID", id)
		assert.False(t, seen[id], "duplicate UUID %q", id)
		seen[id] = true
	}
}
