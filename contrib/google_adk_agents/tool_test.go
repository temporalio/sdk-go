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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

// TestFunctionToolRunsAsActivity is an end-to-end test of the tool path: the
// model scripts a function call, the plugin short-circuits BeforeToolCallback
// into the CallTool Activity, the worker-side tool runs and returns a result,
// and the model's next turn produces the final answer.
func TestFunctionToolRunsAsActivity(t *testing.T) {
	var gotArgs map[string]any
	var mu sync.Mutex
	echo := recordingTool(t, "echo", map[string]any{"echoed": "hi"}, func(args map[string]any) {
		mu.Lock()
		gotArgs = args
		mu.Unlock()
	})

	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "echo", map[string]any{"msg": "hi"}),
				googleadk.TextResponse("done"),
			),
		},
		Tools: []tool.Tool{echo},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "please echo",
		Tools:       []toolSpec{{Name: "echo", Description: "echoes input"}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.FunctionCalls, "echo")
	assert.Contains(t, res.Texts, "done")

	mu.Lock()
	assert.Equal(t, "hi", gotArgs["msg"])
	mu.Unlock()

	// Two model turns (initial + after tool) and exactly one tool call.
	assert.Equal(t, 2, counter.get(googleadk.InvokeModelActivityName))
	assert.Equal(t, 1, counter.get(googleadk.CallToolActivityName))
}

// TestActivityAsToolRoundTrip proves the ActivityAsTool adopter primitive: an
// existing Temporal activity is exposed to the agent as a tool and dispatched
// directly (by its own activity name), not through the generic CallTool registry.
func TestActivityAsToolRoundTrip(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(activityAsToolWorkflow)
	env.RegisterActivityWithOptions(weatherActivity, activity.RegisterOptions{Name: "weather"})

	// The model registry still serves the LLM turns.
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "weather", map[string]any{"city": "Paris"}),
				googleadk.TextResponse("it is sunny in Paris"),
			),
		},
	})

	env.ExecuteWorkflow(activityAsToolWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "it is sunny in Paris")
	// The user's own activity was dispatched directly, not via CallTool.
	assert.Equal(t, 1, counter.get("weather"))
	assert.Equal(t, 0, counter.get(googleadk.CallToolActivityName))
}

// TestInWorkflowToolRunsInWorkflow proves the Options.InWorkflowToolNames opt-in:
// a pure-compute function tool named in that list has its Run executed inside the
// workflow coroutine instead of being dispatched to the CallTool Activity. The
// tool is deliberately absent from the worker-side Tools registry, so a clean
// completion — with its result fed back to the model and zero CallTool Activities
// scheduled — proves it never crossed the Activity boundary.
func TestInWorkflowToolRunsInWorkflow(t *testing.T) {
	inWorkflowToolRan.Store(false)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(inWorkflowToolWorkflow)
	// Only the model factory is wired worker-side; "compute" is intentionally not
	// in the Tools registry, so a CallTool dispatch would fail as an unknown tool.
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "compute", map[string]any{}),
				googleadk.TextResponse("done"),
			),
		},
	})

	env.ExecuteWorkflow(inWorkflowToolWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.FunctionCalls, "compute")
	assert.Contains(t, res.ToolResponses, "compute")
	assert.Contains(t, res.Texts, "done")

	// The handler ran inside the workflow coroutine, and the model still took its
	// two turns — but no CallTool Activity was ever scheduled.
	assert.True(t, inWorkflowToolRan.Load(), "in-workflow tool handler must have executed")
	assert.Equal(t, 2, counter.get(googleadk.InvokeModelActivityName))
	assert.Equal(t, 0, counter.get(googleadk.CallToolActivityName))
}

// TestToolStateViewIsImmutable proves the snapshot shipped to a tool Activity
// exposes session state as read-only: a worker-side mutation attempt fails
// loudly rather than silently evaporating.
func TestToolStateViewIsImmutable(t *testing.T) {
	mutateErr := make(chan error, 1)
	mutating, err := mutatingStateTool(mutateErr)
	require.NoError(t, err)

	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "mutate", map[string]any{}),
				googleadk.TextResponse("done"),
			),
		},
		Tools: []tool.Tool{mutating},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "mutate state",
		Tools:       []toolSpec{{Name: "mutate", Description: "tries to mutate state"}},
	})

	require.True(t, env.IsWorkflowCompleted())

	// The state mutation attempt failed loudly with a read-only error rather than
	// silently evaporating. (ADK feeds the tool error back to the model, so the
	// run itself still completes.)
	select {
	case got := <-mutateErr:
		require.Error(t, got)
		assert.Contains(t, got.Error(), "read-only")
		assert.True(t, googleadk.IsNonRetryable(got), "read-only state violation must be non-retryable")
	case <-time.After(time.Second):
		t.Fatal("tool was never invoked")
	}
}

// ----------------------------------------------------------------------------
// Helpers specific to the tool tests.
// ----------------------------------------------------------------------------

// weatherArgs is the input struct for weatherActivity; ActivityAsTool infers the
// tool's parameter schema from it.
type weatherArgs struct {
	City string `json:"city"`
}

// weatherActivity is an ordinary Temporal activity the user already owns.
func weatherActivity(_ context.Context, in weatherArgs) (map[string]any, error) {
	return map[string]any{"forecast": "sunny in " + in.City}, nil
}

// activityAsToolWorkflow wires weatherActivity into the agent via ActivityAsTool.
func activityAsToolWorkflow(ctx workflow.Context) (runResult, error) {
	weatherTool, err := googleadk.ActivityAsTool(weatherActivity, googleadk.ActivityToolOptions{
		Name:        "weather",
		Description: "look up the weather for a city",
		ActivityOptions: workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		},
	})
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		modelName:   "fake-model",
		userMessage: "weather in Paris",
		tools:       []tool.Tool{weatherTool},
	})
}

// inWorkflowToolWorkflow registers a pure-compute function tool and opts it into
// in-workflow execution via Options.InWorkflowToolNames, then runs the agent. The
// handler flips inWorkflowToolRan and returns a constant result; the tool is not
// registered worker-side, so it can only succeed by running in-workflow.
func inWorkflowToolWorkflow(ctx workflow.Context) (runResult, error) {
	compute, err := funcTool("compute", func(agent.Context, map[string]any) (map[string]any, error) {
		inWorkflowToolRan.Store(true)
		return map[string]any{"result": "computed-in-workflow"}, nil
	})
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		opts:        googleadk.Options{InWorkflowToolNames: []string{"compute"}},
		modelName:   "fake-model",
		userMessage: "compute it",
		tools:       []tool.Tool{compute},
	})
}

// mutatingStateTool returns a worker-side tool that attempts to mutate the
// read-only state view and reports the resulting error on the channel.
func mutatingStateTool(report chan<- error) (tool.Tool, error) {
	return funcTool("mutate", func(tctx agent.Context, _ map[string]any) (map[string]any, error) {
		err := tctx.State().Set("k", "v")
		report <- err
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	})
}
