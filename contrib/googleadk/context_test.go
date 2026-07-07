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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/platform"
	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
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

// concurrencyProbe records the peak number of tool Activities running at once.
// Each Activity blocks at a barrier until `target` have entered (or a timeout
// elapses), so concurrent fan-out reaches the barrier (max == target) while
// sequential fan-out never does (max == 1). The two fan-out tools are exposed as
// ActivityAsTool tools, so their handlers run worker-side on real goroutines —
// where a wall-clock barrier is meaningful — while the fan-out scheduling being
// exercised is exactly NewContext's workflow.Go TaskRunner.
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

// activeProbe is the probe the fan-out activities signal. It is set by each
// fan-out test before executing its workflow (the tests do not run in parallel).
var activeProbe *concurrencyProbe

// probeArgs is the (empty) argument struct for the fan-out probe activities;
// ActivityAsTool infers the tool schema from it.
type probeArgs struct{}

// probeActivityT1 / probeActivityT2 are two distinct Temporal activities so the
// agent can call two independent tools that fan out in parallel. Each signals
// the active probe.
func probeActivityT1(context.Context, probeArgs) (map[string]any, error) {
	activeProbe.enter()
	return map[string]any{"ok": true}, nil
}

func probeActivityT2(context.Context, probeArgs) (map[string]any, error) {
	activeProbe.enter()
	return map[string]any{"ok": true}, nil
}

// fanoutWorkflow wires probeActivityT1/T2 as ActivityAsTool tools and drives the
// agent. Sequential selects NewContext's sequential fan-out.
func fanoutWorkflow(ctx workflow.Context, sequential bool) (runResult, error) {
	ao := workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}
	t1, err := googleadk.ActivityAsTool(probeActivityT1, googleadk.ActivityToolOptions{Name: "t1", Description: "tool one", ActivityOptions: ao})
	if err != nil {
		return runResult{}, err
	}
	t2, err := googleadk.ActivityAsTool(probeActivityT2, googleadk.ActivityToolOptions{Name: "t2", Description: "tool two", ActivityOptions: ao})
	if err != nil {
		return runResult{}, err
	}
	var ctxOpts []googleadk.ContextOption
	if sequential {
		ctxOpts = append(ctxOpts, googleadk.WithSequentialToolFanout())
	}
	return runAgent(ctx, agentBuild{
		ctxOpts:     ctxOpts,
		modelName:   "fake-model",
		userMessage: "call both",
		tools:       []tool.Tool{t1, t2},
	})
}

// newFanoutEnv builds an env with fanoutWorkflow and the probe activities
// registered.
func newFanoutEnv(t *testing.T, s *testsuite.WorkflowTestSuite) (*testsuite.TestWorkflowEnvironment, *activityCounter) {
	t.Helper()
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(fanoutWorkflow)
	env.RegisterActivityWithOptions(probeActivityT1, activity.RegisterOptions{Name: "t1"})
	env.RegisterActivityWithOptions(probeActivityT2, activity.RegisterOptions{Name: "t2"})
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(twoFunctionCalls(), googleadk.TextResponse("done")),
		},
	})
	return env, counter
}

// TestConcurrentFanoutSchedulesParallel flips to the default concurrent
// TaskRunner (workflow.Go) and observes two tool Activities running at the same
// time when a single LLM turn fans out to two tool calls.
func TestConcurrentFanoutSchedulesParallel(t *testing.T) {
	activeProbe = newConcurrencyProbe(2, 3*time.Second)

	var s testsuite.WorkflowTestSuite
	env, counter := newFanoutEnv(t, &s)
	env.ExecuteWorkflow(fanoutWorkflow, false)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, 2, activeProbe.max(), "concurrent fan-out should run both tools at once")
	assert.Equal(t, 1, counter.get("t1"))
	assert.Equal(t, 1, counter.get("t2"))
	assert.Equal(t, 2, counter.get(googleadk.InvokeModelActivityName))
}

// TestSequentialFanoutSchedulesSerially flips WithSequentialToolFanout and
// observes the two tool calls never overlap (peak concurrency 1), proving the
// option changes the dispatch path rather than merely being stored.
func TestSequentialFanoutSchedulesSerially(t *testing.T) {
	// Target 2 is never reached in sequential mode, so each handler hits the
	// (short) timeout; peak concurrency stays at 1.
	activeProbe = newConcurrencyProbe(2, 300*time.Millisecond)

	var s testsuite.WorkflowTestSuite
	env, counter := newFanoutEnv(t, &s)
	env.ExecuteWorkflow(fanoutWorkflow, true)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, 1, activeProbe.max(), "sequential fan-out should never overlap tool calls")
	assert.Equal(t, 1, counter.get("t1"))
	assert.Equal(t, 1, counter.get("t2"))
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
