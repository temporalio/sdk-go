package googleadk_test

// End-to-end integration tests that need a real Temporal server: the streaming
// side channel (workflowstreams publishes chunks via a signal to the parent
// workflow, which a unit-test mock client cannot service) and history replay.
// They boot a local dev server via testsuite.StartDevServer, downloading the CLI
// on first use, and FAIL when it cannot be started (no binary, no network), so a
// run that never exercised them cannot pass. The default unit suite already
// covers the durable agent loop, aggregation, determinism providers, and failure
// classification without a server.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
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

// devServerState remembers how the test binary's first StartDevServer went.
// That first start also downloads the CLI, which on the macos-intel CI runner
// alone takes longer than 30s, and a failed download is not cached: under a
// flat per-test deadline every dev-server test downloaded for 30s and skipped,
// and nine such skips overran the 5m package timeout. The first start therefore
// gets a longer deadline and one retry, and if both attempts fail this and
// every later dev-server test fail instead of skipping, so a job cannot go
// green without exercising them.
var devServerState struct {
	sync.Mutex
	attempted bool
	err       error
}

// devServer starts a local Temporal dev server and fails the test if one cannot
// be started (no binary, no network).
func devServer(t *testing.T) (client.Client, func()) {
	t.Helper()
	devServerState.Lock()
	defer devServerState.Unlock()
	if devServerState.err != nil {
		t.Fatalf("dev server unavailable, see the first dev-server test failure: %v", devServerState.err)
	}
	attempts, timeout := 1, 30*time.Second
	if !devServerState.attempted {
		attempts, timeout = 2, 90*time.Second
	}
	// Redirect the dev-server process's stdio to io.Discard rather than letting it
	// inherit the test binary's os.Stdout/os.Stderr (testsuite's default on every
	// platform). On Windows, DevServer.Stop() shuts the server down with a console
	// CTRL_BREAK event, which fails when the test process has no attached console
	// (as under `go test` in CI) — Stop() then returns early without killing the
	// process. A lingering server that inherited our stdio would hold the pipe
	// `go test` reads, tripping its "test I/O incomplete after exiting" WaitDelay
	// check and failing the package even though every test passed. Detaching its
	// stdio makes that harmless (and the CI runner reaps the orphan on job exit).
	opts := testsuite.DevServerOptions{Stdout: io.Discard, Stderr: io.Discard}
	var srv *testsuite.DevServer
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		var startErr error
		srv, startErr = testsuite.StartDevServer(ctx, opts)
		cancel()
		if startErr == nil {
			err = nil
			break
		}
		err = errors.Join(err, fmt.Errorf("attempt %d: %w", attempt, startErr))
	}
	if !devServerState.attempted {
		devServerState.attempted, devServerState.err = true, err
	}
	if err != nil {
		t.Fatalf("dev server unavailable (first use downloads the Temporal CLI, which needs network): %v", err)
	}
	return srv.Client(), func() { _ = srv.Stop() }
}

// startWorker boots a real worker for the test workflows with the plugin
// Activities wired from cfg, plus the guarded-trio activities the
// multi-decision HITL workflow dispatches.
func startWorker(t *testing.T, c client.Client, cfg googleadk.Config) worker.Worker {
	t.Helper()
	w := worker.New(c, integrationTaskQueue, worker.Options{})
	w.RegisterWorkflow(agentRunWorkflow)
	w.RegisterWorkflow(multiConfirmHitlWorkflow)
	registerGuardedTrio(w)
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

// TestReplaySingleAndMultiAgent runs real single-agent, multi-agent, and
// multi-decision HITL workflows against the dev server, then replays each
// recorded history with worker.WorkflowReplayer — the canonical determinism
// guarantee. A replay failure here is exactly the non-determinism the plugin's
// NewContext (time/uuid/task providers) exists to prevent.
func TestReplaySingleAndMultiAgent(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	models := map[string]googleadk.ModelFactory{
		"root-model": scriptedModelFactory(
			googleadk.FunctionCallResponse("c1", "transfer_to_agent", map[string]any{"agent_name": "specialist"}),
			googleadk.TextResponse("(root fallback)"),
		),
		"specialist-model": scriptedModelFactory(googleadk.TextResponse("specialist answer")),
		"solo-model":       scriptedModelFactory(googleadk.TextResponse("hello from solo")),
	}
	for name, factory := range multiConfirmModels() {
		models[name] = factory
	}
	w := startWorker(t, c, googleadk.Config{Models: models})
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

	// The multi-decision confirmation resume: one turn pauses on three guarded
	// ActivityAsTool tools and a single batched ConfirmationResponse — with the
	// decisions deliberately rotated to (gamma, alpha, beta) — approves all of
	// them: the shape whose re-queue order was Go-map-random (and therefore not
	// replay-stable) before the adk/v2 minimum required in go.mod. The strict
	// order assertion pins that the resumed responses follow the confirmations'
	// request order, not the decisions' position, so a resume that silently did
	// nothing (or re-dispatched in decision order) fails here rather than
	// replaying cleanly below. It drives its own two-pass workflow rather than
	// agentRunWorkflow, so it is started directly and appended to the same
	// replay set.
	mcRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "adk-replay-multiconfirm-" + time.Now().Format("150405.000"),
		TaskQueue: integrationTaskQueue,
	}, multiConfirmHitlWorkflow)
	require.NoError(t, err)
	var mcRes multiConfirmResult
	require.NoError(t, mcRun.Get(ctx, &mcRes))
	require.Equal(t, 3, mcRes.PendingCount, "all three guarded tools must pause before the batched resume")
	require.Equal(t, []string{"guarded_alpha", "guarded_beta", "guarded_gamma"}, mcRes.ResumedToolResponses,
		"resumed responses must follow the confirmations' request order, not the rotated decision order")
	executions = append(executions, execution{mcRun.GetID(), mcRun.GetRunID()})

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(agentRunWorkflow)
	replayer.RegisterWorkflow(multiConfirmHitlWorkflow)
	for _, e := range executions {
		err := replayer.ReplayWorkflowExecution(ctx, c.WorkflowService(), nil, "default", sdkworkflow.Execution{
			ID:    e.id,
			RunID: e.runID,
		})
		require.NoError(t, err, "recorded history for %s must replay deterministically", e.id)
	}
}
