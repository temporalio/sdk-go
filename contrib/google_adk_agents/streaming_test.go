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
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"

	"google.golang.org/genai"

	"google.golang.org/adk/model"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

// chunkedModel is a streaming-capable fake model. When called with stream=true it
// yields its text in partial chunks (the InvokeModel Activity aggregates them and
// publishes each to the streaming topic); with stream=false it yields a single
// complete response, matching how a real model behaves on a non-streaming call.
// It records the stream flag it last saw so a test can prove which path ran.
type chunkedModel struct {
	name   string
	chunks []string

	mu        sync.Mutex
	sawStream bool
}

func (m *chunkedModel) Name() string { return m.name }

func (m *chunkedModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	m.sawStream = stream
	m.mu.Unlock()
	return func(yield func(*model.LLMResponse, error) bool) {
		if !stream {
			// Non-streaming: one complete response carrying the full message.
			yield(&model.LLMResponse{
				Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: strings.Join(m.chunks, "")}}},
				TurnComplete: true,
			}, nil)
			return
		}
		for _, c := range m.chunks {
			resp := &model.LLMResponse{
				Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: c}}},
				Partial: true,
			}
			if !yield(resp, nil) {
				return
			}
		}
	}
}

func (m *chunkedModel) streamed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sawStream
}

// TestNonStreamingDoesNotStream is the default-path guarantee: with no
// StreamingTopic set, the InvokeModel Activity calls the model in non-streaming
// mode (stream=false) and the agent loop still completes. The real streaming
// publish path (which requires a live Temporal server for the workflowstreams
// side channel) is exercised by TestStreamingIntegration in integration_test.go.
func TestNonStreamingDoesNotStream(t *testing.T) {
	cm := &chunkedModel{name: "fake-model", chunks: []string{"x", "y"}}

	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": func(context.Context, string) (model.LLM, error) { return cm, nil },
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{ModelName: "fake-model", UserMessage: "hi"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "xy", "non-streaming returns one complete response")
	assert.False(t, cm.streamed(), "without StreamingTopic the model must not be streamed")
	assert.Equal(t, 1, counter.get(googleadk.InvokeModelActivityName))
}
