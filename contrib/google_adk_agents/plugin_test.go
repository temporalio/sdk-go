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
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

// TestMultiAgentSubAgents drives a real two-agent tree through the plugin: the
// root model emits transfer_to_agent (run in-workflow, NOT as an Activity), and
// the specialist sub-agent then produces the answer — its model call still a
// durable Activity.
func TestMultiAgentSubAgents(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"root-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "transfer_to_agent", map[string]any{"agent_name": "specialist"}),
				googleadk.TextResponse("(root fallback)"),
			),
			"specialist-model": scriptedModelFactory(googleadk.TextResponse("specialist answer")),
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "root-model",
		UserMessage: "please handle this specialist task",
		SubAgents: []subAgentSpec{{
			Name:        "specialist",
			Description: "handles specialist tasks",
			ModelName:   "specialist-model",
		}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "specialist answer")
	assert.Contains(t, res.Authors, "specialist", "the specialist sub-agent should author an event")

	// transfer_to_agent is a pure control tool: it runs in-workflow and is never
	// routed through the CallTool Activity.
	assert.Equal(t, 0, counter.get(googleadk.CallToolActivityName))
	// Two durable model calls: the root turn that transfers, and the specialist.
	assert.GreaterOrEqual(t, counter.get(googleadk.InvokeModelActivityName), 2)
}

// errorModel is a model.LLM that always fails with a genai.APIError of the given
// HTTP status, so InvokeModel classifies the failure per Temporal's retry
// contract.
type errorModel struct {
	name string
	code int
}

func (m *errorModel) Name() string { return m.name }

func (m *errorModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(nil, &genai.APIError{Code: m.code, Message: "boom"})
	}
}

// TestModelErrorClassification proves a non-retryable upstream status (HTTP 400)
// is surfaced as a non-retryable ApplicationError tagged ErrorTypeModel, so the
// run fails fast and the caller can classify it without string-matching.
func TestModelErrorClassification(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"broken-model": func(context.Context, string) (model.LLM, error) {
				return &errorModel{name: "broken-model", code: 400}, nil
			},
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{ModelName: "broken-model", UserMessage: "hi"})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err, "a non-retryable model error must fail the run")
	assert.True(t, googleadk.IsNonRetryable(err), "HTTP 400 must be classified non-retryable")

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "error must be a Temporal ApplicationError")
	assert.Equal(t, googleadk.ErrorTypeModel, appErr.Type())
}

// boomActivityWorkflow wires a tool whose worker-side handler always fails, under
// a bounded retry policy, to prove (a) Temporal retries the tool Activity up to
// the cap and (b) ADK then feeds the exhausted error back to the model (which
// recovers), so the run completes.
func boomActivityWorkflow(ctx workflow.Context) (runResult, error) {
	boom, err := stubTool("boom", "always fails")
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		opts: googleadk.Options{
			ToolActivityOptions: workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval: time.Millisecond,
					MaximumAttempts: 3,
				},
			},
		},
		modelName:   "fake-model",
		userMessage: "call boom",
		tools:       []tool.Tool{boom},
	})
}

// TestToolErrorRetryThenFail proves the tool Activity is retried to the cap (3
// attempts) on a retryable failure, then — per ADK's tool-error contract — the
// error is handed back to the model rather than failing the workflow.
func TestToolErrorRetryThenFail(t *testing.T) {
	boomHandler, err := funcTool("boom", func(agent.Context, map[string]any) (map[string]any, error) {
		return nil, errors.New("transient boom")
	})
	require.NoError(t, err)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(boomActivityWorkflow)
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "boom", map[string]any{}),
				googleadk.TextResponse("recovered after tool failure"),
			),
		},
		Tools: []tool.Tool{boomHandler},
	})

	env.ExecuteWorkflow(boomActivityWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "recovered after tool failure")
	// The retryable tool error was retried up to the cap before being surfaced.
	assert.Equal(t, 3, counter.get(googleadk.CallToolActivityName))
}
