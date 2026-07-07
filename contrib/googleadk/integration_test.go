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

// End-to-end integration tests that need a real Temporal server: the streaming
// side channel (workflowstreams publishes chunks via a signal to the parent
// workflow, which a unit-test mock client cannot service) and history replay.
// They boot a local dev server via testsuite.StartDevServer and SKIP — a genuine
// capability skip, not an operator env gate — when the dev-server binary or
// network is unavailable (e.g. a sandboxed CI). The default unit suite already
// covers the durable agent loop, aggregation, determinism providers, and failure
// classification without a server.

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/workflowstreams"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/model"

	"go.temporal.io/sdk/contrib/googleadk"
)

const integrationTaskQueue = "google-adk-integration"

// devServer starts a local Temporal dev server, or skips the test if one cannot
// be started (no binary, no network). The returned client and cleanup are valid
// only when the test was not skipped.
func devServer(t *testing.T) (client.Client, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Redirect the dev-server process's stdio to io.Discard rather than letting it
	// inherit the test binary's os.Stdout/os.Stderr (testsuite's default on every
	// platform). On Windows, DevServer.Stop() shuts the server down with a console
	// CTRL_BREAK event, which fails when the test process has no attached console
	// (as under `go test` in CI) — Stop() then returns early without killing the
	// process. A lingering server that inherited our stdio would hold the pipe
	// `go test` reads, tripping its "test I/O incomplete after exiting" WaitDelay
	// check and failing the package even though every test passed. Detaching its
	// stdio makes that harmless (and the CI runner reaps the orphan on job exit).
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Skipf("dev server unavailable (capability skip): %v", err)
	}
	return srv.Client(), func() { _ = srv.Stop() }
}

// startWorker boots a real worker for agentRunWorkflow with the plugin Activities
// wired from cfg.
func startWorker(t *testing.T, c client.Client, cfg googleadk.Config) worker.Worker {
	t.Helper()
	w := worker.New(c, integrationTaskQueue, worker.Options{})
	w.RegisterWorkflow(agentRunWorkflow)
	acts, err := googleadk.NewActivities(cfg)
	require.NoError(t, err)
	acts.Register(w)
	require.NoError(t, w.Start())
	return w
}

// TestStreamingIntegration is the end-to-end streaming proof: with StreamingTopic
// set, the InvokeModel Activity drives the model in streaming mode and PUBLISHES
// each chunk into the workflow's stream (asserted via the workflowstreams offset
// query, which works even after completion), while the aggregated final response
// is returned into the workflow so the agent loop stays deterministic.
func TestStreamingIntegration(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	cm := &chunkedModel{name: "stream-model", chunks: []string{"Hello", ", ", "world"}}
	w := startWorker(t, c, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"stream-model": func(context.Context, string) (model.LLM, error) { return cm, nil },
		},
	})
	defer w.Stop()

	ctx := context.Background()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "adk-streaming-" + time.Now().Format("150405.000"),
		TaskQueue: integrationTaskQueue,
	}, agentRunWorkflow, runInput{
		ModelName:      "stream-model",
		UserMessage:    "greet",
		StreamingTopic: "run-stream",
	})
	require.NoError(t, err)

	var res runResult
	require.NoError(t, run.Get(ctx, &res))
	assert.Contains(t, res.Texts, "Hello, world", "streamed chunks aggregate into one final response")
	assert.True(t, cm.streamed(), "StreamingTopic must drive the model in streaming mode")

	// The chunks were published into the workflow stream: the offset query returns
	// the number of appended items (>= the number of streamed chunks).
	val, err := c.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), workflowstreams.OffsetQueryName)
	require.NoError(t, err)
	var offset int64
	require.NoError(t, val.Get(&offset))
	assert.GreaterOrEqual(t, offset, int64(len(cm.chunks)), "every streamed chunk must be published to the topic")
}

// TestReplaySingleAndMultiAgent runs real single-agent and multi-agent workflows
// against the dev server, then replays each recorded history with
// worker.WorkflowReplayer — the canonical determinism guarantee. A replay failure
// here is exactly the non-determinism the plugin's NewContext (time/uuid/task
// providers) exists to prevent.
func TestReplaySingleAndMultiAgent(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	w := startWorker(t, c, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"root-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "transfer_to_agent", map[string]any{"agent_name": "specialist"}),
				googleadk.TextResponse("(root fallback)"),
			),
			"specialist-model": scriptedModelFactory(googleadk.TextResponse("specialist answer")),
			"solo-model":       scriptedModelFactory(googleadk.TextResponse("hello from solo")),
		},
	})
	defer w.Stop()

	ctx := context.Background()
	inputs := map[string]runInput{
		"adk-replay-solo-" + time.Now().Format("150405.000"): {
			ModelName:   "solo-model",
			UserMessage: "hi",
		},
		"adk-replay-multi-" + time.Now().Format("150405.000"): {
			ModelName:   "root-model",
			UserMessage: "handle this",
			SubAgents: []subAgentSpec{{
				Name:        "specialist",
				Description: "handles specialist tasks",
				ModelName:   "specialist-model",
			}},
		},
	}

	type execution struct{ id, runID string }
	var executions []execution
	for id, in := range inputs {
		run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: integrationTaskQueue}, agentRunWorkflow, in)
		require.NoError(t, err)
		var res runResult
		require.NoError(t, run.Get(ctx, &res))
		executions = append(executions, execution{run.GetID(), run.GetRunID()})
	}

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(agentRunWorkflow)
	for _, e := range executions {
		err := replayer.ReplayWorkflowExecution(ctx, c.WorkflowService(), nil, "default", sdkworkflow.Execution{
			ID:    e.id,
			RunID: e.runID,
		})
		require.NoError(t, err, "recorded history for %s must replay deterministically", e.id)
	}
}
