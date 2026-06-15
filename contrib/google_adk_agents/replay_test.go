package google_adk_agents_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/worker"
)

// TestReplayAgentSession runs a real AgentSessionWorkflow on the dev server —
// with the real RunTurnActivity wired via RegisterActivities around a native
// MockModel-backed agent — then fetches its recorded history and replays it
// through WorkflowReplayer. A passing replay proves the workflow code is
// deterministic across history versions: the orchestration uses only workflow.*
// primitives (workflow.Now, the turn-level Activity, signals/updates) and never
// touches the ADK runtime, whose non-determinism is confined to the recorded
// Activity results.
func TestReplayAgentSession(t *testing.T) {
	srv := startDevServerOrSkip(t)
	c := srv.Client()
	ctx := testContext(t)

	// Wire the real turn Activity through RegisterActivities, exactly as a
	// production worker would, backed by a native adk-go runner over a MockModel.
	reg := adk.NewAgentRegistry()
	reg.Register("chat", newAgentFactory("chat", adk.NewMockModel("chat", modelMessage("reply"))))

	tq := "adk-replay-tq"
	w := worker.New(c, tq, worker.Options{})
	adk.RegisterWorkflow(w)
	adk.RegisterActivities(w, reg, adk.Options{})
	require.NoError(t, w.Start())
	defer w.Stop()

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: tq}, adk.AgentSessionWorkflow, in)
	require.NoError(t, err)

	// Drive two synchronous turns so the history holds real turn activity
	// results, then stop the workflow at a clean task boundary.
	for _, msg := range []string{"first", "second"} {
		h, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   run.GetID(),
			RunID:        run.GetRunID(),
			UpdateName:   adk.UpdateSendMessageAndWait,
			WaitForStage: client.WorkflowUpdateStageCompleted,
			Args:         []interface{}{adk.TurnRequest{Message: userMessage(msg)}},
		})
		require.NoError(t, err)
		var res adk.TurnResult
		require.NoError(t, h.Get(ctx, &res))
	}

	// Collect the recorded history (no long poll: take what is currently
	// committed, which ends at a clean WorkflowTaskCompleted boundary).
	hist := &historypb.History{}
	iter := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		ev, err := iter.Next()
		require.NoError(t, err)
		hist.Events = append(hist.Events, ev)
	}
	require.NotEmpty(t, hist.Events, "the workflow must have recorded history to replay")

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(adk.AgentSessionWorkflow)
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, hist),
		"AgentSessionWorkflow must replay its own recorded history without a non-determinism error")

	require.NoError(t, c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test complete"))
}
