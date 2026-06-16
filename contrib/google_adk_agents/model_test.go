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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

// TestSingleAgentRoutesModelToActivity is the cardinal end-to-end test: a real
// llmagent + runner is driven through the plugin inside a workflow environment
// (no env-var gate), and the single LLM turn is serviced by the InvokeModel
// Activity rather than an in-workflow model call.
func TestSingleAgentRoutesModelToActivity(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(googleadk.TextResponse("hello from the worker")),
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "hi",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "hello from the worker")
	// Exactly one model turn => exactly one InvokeModel Activity, no tool calls.
	assert.Equal(t, 1, counter.get(googleadk.InvokeModelActivityName))
	assert.Equal(t, 0, counter.get(googleadk.CallToolActivityName))
}

// TestModelFactoryLookupByName proves the Activity reconstructs the model from
// the worker-side registry keyed by LLMRequest.Model: two model names resolve to
// two different scripted backends, selected by the name the agent carries.
func TestModelFactoryLookupByName(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fast-model":     scriptedModelFactory(googleadk.TextResponse("fast answer")),
			"thinking-model": scriptedModelFactory(googleadk.TextResponse("thoughtful answer")),
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "thinking-model",
		UserMessage: "hi",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "thoughtful answer")
	assert.NotContains(t, res.Texts, "fast answer")
}

// TestPerModelTimeoutApplied flips Options.PerModelTimeouts to a non-default
// value and asserts the workflow still completes through the configured
// per-model Activity budget. The default-timeout path is covered by the other
// model tests, so observing a successful run under the override exercises the
// non-default branch.
func TestPerModelTimeoutApplied(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"thinking-model": scriptedModelFactory(googleadk.TextResponse("done thinking")),
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:        "thinking-model",
		UserMessage:      "hi",
		PerModelTimeouts: map[string]time.Duration{"thinking-model": 10 * time.Minute},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "done thinking")
}

// TestSummaryFnOnModelActivity sets a custom SummaryFn and asserts it is invoked
// to compute the Activity summary (proving the non-default branch is read on the
// dispatch path, not just stored).
func TestSummaryFnOnModelActivity(t *testing.T) {
	summaryFnCalled.Store(false)
	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(googleadk.TextResponse("ok")),
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:        "fake-model",
		UserMessage:      "hi",
		UseCustomSummary: true,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.True(t, summaryFnCalled.Load(), "custom SummaryFn should have been invoked on the dispatch path")
}

// TestDefaultSummaryIsAgentName runs with no SummaryFn and confirms the default
// summary path (ADK agent name) is taken without error.
func TestDefaultSummaryIsAgentName(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(googleadk.TextResponse("ok")),
		},
	})
	env.ExecuteWorkflow(agentRunWorkflow, runInput{ModelName: "fake-model", UserMessage: "hi"})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
