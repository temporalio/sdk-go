package google_adk_agents_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/worker"
	"google.golang.org/adk/session"
)

// TestTurnActivityScheduledOncePerTurn proves the workflow schedules exactly
// one RunTurnActivity per turn and does NOT re-execute it on replay.
//
// The sticky workflow cache is set to 0, so the workflow is evicted after
// every workflow task and the full history is replayed from scratch on the next
// task. A counting turn Activity then reveals whether activities are re-run on
// replay: if they were, the counter would exceed the number of turns. It must
// equal the number of turns exactly — activity results come from history, not
// re-execution. This is the side-effect determinism guarantee the plugin's
// turn-as-activity design depends on.
func TestTurnActivityScheduledOncePerTurn(t *testing.T) {
	srv := startDevServerOrSkip(t)
	c := srv.Client()
	ctx := testContext(t)

	// Disable the sticky cache so every workflow task forces a full replay
	// from history; restore the SDK default afterwards.
	worker.SetStickyWorkflowCacheSize(0)
	t.Cleanup(func() { worker.SetStickyWorkflowCacheSize(10000) })

	var calls int64
	turn := func(_ context.Context, in adk.TurnInput) (adk.TurnResult, error) {
		atomic.AddInt64(&calls, 1)
		events := append(append([]*session.Event{}, in.PriorEvents...), stubEvent("reply"))
		return adk.TurnResult{Events: events}, nil
	}

	tq := "adk-side-effects-tq"
	w := worker.New(c, tq, worker.Options{})
	adk.RegisterWorkflow(w)
	w.RegisterActivityWithOptions(turn, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})
	require.NoError(t, w.Start())
	defer w.Stop()

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: tq}, adk.AgentSessionWorkflow, in)
	require.NoError(t, err)

	const turns = 3
	for i := 0; i < turns; i++ {
		h, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   run.GetID(),
			RunID:        run.GetRunID(),
			UpdateName:   adk.UpdateSendMessageAndWait,
			WaitForStage: client.WorkflowUpdateStageCompleted,
			Args:         []interface{}{adk.TurnRequest{Message: userMessage(fmt.Sprintf("turn-%d", i))}},
		})
		require.NoError(t, err)
		var res adk.TurnResult
		require.NoError(t, h.Get(ctx, &res))
		require.True(t, containsText(res.Events, "reply"), "each turn must return its reply")
	}

	require.Equal(t, int64(turns), atomic.LoadInt64(&calls),
		"with MaxCachedWorkflows:0 the workflow replays every task; the turn Activity must be scheduled exactly once per turn and its result replayed from history, never re-executed")

	// The session workflow never completes on its own; terminate it so the
	// dev server can shut down cleanly.
	require.NoError(t, c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test complete"))
}
