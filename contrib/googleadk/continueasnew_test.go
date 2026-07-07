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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// canResult is the serializable output of continueAsNewWorkflow.
type canResult struct {
	// SnapshotEvents / SnapshotStateKV capture what ExportSession produced.
	SnapshotEvents int
	SnapshotFlag   string
	// ImportedEvents / ImportedFlag are what ImportSession rebuilt in a fresh
	// service — they must match the snapshot.
	ImportedEvents int
	ImportedFlag   string
	// SecondTurnPriorContext is the number of prior-history Contents the model saw
	// on the continued turn; > 0 proves the resumed session carried context.
	SecondTurnPriorContext int
	// SecondTurnText is the model text produced on the continued turn.
	SecondTurnText []string
}

// priorContextModel records the number of Contents in the LLMRequest it last
// saw (into lastRequestContents), so a test can prove the continued turn's
// request carried prior history. It replays scripted responses like a FakeModel.
type priorContextModel struct {
	inner *googleadk.FakeModel
}

func (m *priorContextModel) Name() string { return "prior-context-model" }

func (m *priorContextModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if req != nil {
		lastRequestContents = len(req.Contents)
	}
	return m.inner.GenerateContent(ctx, req, stream)
}

// priorContextFactory returns a ModelFactory yielding a shared priorContextModel
// that replays the given responses and records the request-Contents count.
func priorContextFactory(responses ...*model.LLMResponse) googleadk.ModelFactory {
	pcm := &priorContextModel{inner: googleadk.NewFakeModel(responses...)}
	return func(context.Context, string) (model.LLM, error) { return pcm, nil }
}

// continueAsNewWorkflow runs a first turn, exports the session, imports it into a
// fresh in-memory service, and runs a second turn against the imported session —
// the shape a real continue-as-new boundary takes (export before
// ContinueAsNewError, import at the top of the next run). It reports enough for
// the test to assert the events+state round-trip and that the continued turn
// sees prior context.
func continueAsNewWorkflow(ctx workflow.Context) (canResult, error) {
	adkCtx := googleadk.NewContext(ctx)
	var res canResult

	// A tool that writes a session-state key, so the snapshot carries non-empty
	// session-scoped state to round-trip.
	remember, err := funcTool("remember", func(tctx agent.Context, _ map[string]any) (map[string]any, error) {
		if serr := tctx.State().Set("topic", "temporal-durability"); serr != nil {
			return nil, serr
		}
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		return res, err
	}

	// --- First run against service A. ---
	svcA := session.InMemoryService()
	agentA, err := canAgent(remember)
	if err != nil {
		return res, err
	}
	runnerA, err := runner.New(runner.Config{
		AppName:           "test-app",
		Agent:             agentA,
		SessionService:    svcA,
		AutoCreateSession: true,
	})
	if err != nil {
		return res, err
	}
	msg := genai.NewContentFromText("remember the topic", genai.RoleUser)
	for _, rerr := range runnerA.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return res, rerr
		}
	}

	// --- Export the session (as a workflow would before continue-as-new). ---
	snap, err := googleadk.ExportSession(adkCtx, svcA, "test-app", "user-1", "session-1")
	if err != nil {
		return res, err
	}
	res.SnapshotEvents = len(snap.Events)
	if v, ok := snap.State["topic"].(string); ok {
		res.SnapshotFlag = v
	}

	// --- Import into a fresh service B (as the next run would at its top). ---
	svcB := session.InMemoryService()
	imported, err := googleadk.ImportSession(adkCtx, svcB, snap)
	if err != nil {
		return res, err
	}
	// Read the rebuilt session back to prove events+state round-tripped.
	for range imported.Events().All() {
		res.ImportedEvents++
	}
	if v, err := imported.State().Get("topic"); err == nil {
		if sv, ok := v.(string); ok {
			res.ImportedFlag = sv
		}
	}

	// --- Continue the conversation on the imported session. ---
	lastRequestContents = 0
	agentB, err := canAgentModel()
	if err != nil {
		return res, err
	}
	runnerB, err := runner.New(runner.Config{
		AppName:        "test-app",
		Agent:          agentB,
		SessionService: svcB,
	})
	if err != nil {
		return res, err
	}
	follow := genai.NewContentFromText("what were we discussing?", genai.RoleUser)
	for ev, rerr := range runnerB.Run(adkCtx, "user-1", "session-1", follow, agent.RunConfig{}) {
		if rerr != nil {
			return res, rerr
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.Text != "" {
				res.SecondTurnText = append(res.SecondTurnText, p.Text)
			}
		}
	}
	// The imported history (prior user turn + tool call/response + model reply)
	// minus the single new user message is the "prior context" the model saw.
	res.SecondTurnPriorContext = lastRequestContents - 1
	return res, nil
}

// lastRequestContents records the number of Contents in the request the
// priorContextModel last saw. Reset before the continued turn (the workflow does
// not run concurrently).
var lastRequestContents int

func canAgent(t tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "root assistant",
		Model:       googleadk.NewModel("fake-model"),
		Instruction: "be helpful",
		Tools:       []tool.Tool{t},
	})
}

func canAgentModel() (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "root assistant",
		Model:       googleadk.NewModel("prior-context-model"),
		Instruction: "be helpful",
	})
}

// TestContinueAsNewRoundTrip proves the continue-as-new state carry: a turn's
// session is exported to a serializable snapshot, imported into a fresh session
// service, and a subsequent turn on the imported session sees the prior context —
// events and session-scoped state round-trip intact.
func TestContinueAsNewRoundTrip(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(continueAsNewWorkflow)
	wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "remember", map[string]any{}),
				googleadk.TextResponse("remembered the topic"),
			),
			"prior-context-model": priorContextFactory(
				googleadk.TextResponse("continued: still on temporal-durability"),
			),
		},
	})

	env.ExecuteWorkflow(continueAsNewWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res canResult
	require.NoError(t, env.GetWorkflowResult(&res))

	// The snapshot captured a non-empty history and the session-scoped state key.
	assert.Greater(t, res.SnapshotEvents, 0, "snapshot must capture the conversation history")
	assert.Equal(t, "temporal-durability", res.SnapshotFlag, "session-scoped state must be captured")

	// ImportSession rebuilt the same events and state in a fresh service.
	assert.Equal(t, res.SnapshotEvents, res.ImportedEvents, "events must round-trip")
	assert.Equal(t, res.SnapshotFlag, res.ImportedFlag, "state must round-trip")

	// The continued turn saw prior history and produced a fresh answer.
	assert.Greater(t, res.SecondTurnPriorContext, 0, "the continued turn must see prior context")
	assert.Contains(t, res.SecondTurnText, "continued: still on temporal-durability")
}
