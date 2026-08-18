package googleadk_test

// Probes regression-testing how the replay gate treats the two LIVE read-only
// operations that run around history replay: query handlers and update
// validators. Both are once-per-request, so a dropped recording would be a
// real loss, not deduplication. The gate composes workflow.IsReplaying with
// !workflow.IsReadOnly (Experimental) — the Go analog of the finer replay
// predicates other SDKs expose (Python's is_replaying_history_events,
// TypeScript's isReplayingHistoryEvents) — so read-only contexts are never
// suppressed, even when a catch-up replay leaves IsReplaying true.
//
// Update validators are additionally structurally safe: the SDK skips
// validation while replaying accepted updates (internal_update.go), and a NEW
// update riding a catch-up workflow task is validated in the task's final
// live batch, so a validator always observes IsReplaying false.
//
// Query handlers inherit whatever the last processed history event left in
// the IsReplaying flag: false from a warm cache or a command-free catch-up,
// true right after a catch-up replay whenever a command event or the
// workflow-completion event trails the last workflow task. IsReadOnly is what
// keeps query-time telemetry recording in that second shape; these probes
// fail if either predicate changes what query handlers observe.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"go.temporal.io/sdk/contrib/googleadk"
)

// liveOpsControlLoop parks the workflow on a "control" signal channel until it
// receives true.
func liveOpsControlLoop(ctx workflow.Context) error {
	ch := workflow.GetSignalChannel(ctx, "control")
	for {
		var done bool
		ch.Receive(ctx, &done)
		if done {
			return nil
		}
	}
}

type queryProbeInput struct {
	// PendingCommand starts a far-future timer so a command event trails every
	// workflow task in history — the shape of an agent awaiting an Activity
	// (e.g. a pending InvokeModel) or any workflow with in-flight work.
	PendingCommand bool
}

// queryProbeReport is what the replay-probe query returns: the
// workflow.IsReplaying value observed inside the handler and the gated
// counter's Enabled result there.
type queryProbeReport struct {
	Replaying bool
	Enabled   bool
}

// queryReplayProbeWorkflow exposes a query that records one gated counter
// increment per query served and reports what the handler observed.
func queryReplayProbeWorkflow(ctx workflow.Context, in queryProbeInput) error {
	adkCtx := googleadk.NewContext(ctx)
	counter, err := otel.Meter("liveops-probe").Int64Counter("query_probe_recordings")
	if err != nil {
		return err
	}
	if err := workflow.SetQueryHandler(ctx, "replay-probe", func() (queryProbeReport, error) {
		counter.Add(adkCtx, 1)
		return queryProbeReport{
			Replaying: workflow.IsReplaying(ctx),
			Enabled:   counter.Enabled(adkCtx),
		}, nil
	}); err != nil {
		return err
	}
	if in.PendingCommand {
		_ = workflow.NewTimer(ctx, 24*time.Hour)
	}
	return liveOpsControlLoop(ctx)
}

// queryProbeHarness runs queryReplayProbeWorkflow on its own task queue with
// restartable workers, so a test can force the evicted-then-caught-up state.
type queryProbeHarness struct {
	t   *testing.T
	c   client.Client
	ctx context.Context
	tq  string
	w   worker.Worker
	run client.WorkflowRun
}

func startQueryProbe(t *testing.T, c client.Client, taskQueue string, in queryProbeInput) *queryProbeHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	h := &queryProbeHarness{t: t, c: c, ctx: ctx, tq: taskQueue}
	h.startWorker()
	t.Cleanup(func() {
		if h.w != nil {
			h.w.Stop()
		}
	})
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        taskQueue + "-" + time.Now().Format("150405.000"),
		TaskQueue: taskQueue,
	}, queryReplayProbeWorkflow, in)
	require.NoError(t, err)
	h.run = run
	return h
}

func (h *queryProbeHarness) startWorker() {
	h.t.Helper()
	// A short sticky timeout keeps the post-restart query's fall-back from the
	// dead sticky queue quick.
	w := worker.New(h.c, h.tq, worker.Options{StickyScheduleToStartTimeout: time.Second})
	w.RegisterWorkflow(queryReplayProbeWorkflow)
	require.NoError(h.t, w.Start())
	h.w = w
}

// evict simulates losing the worker: stop it, purge the process-global sticky
// cache, and start a fresh worker, so the next task replays the full history.
func (h *queryProbeHarness) evict() {
	h.t.Helper()
	h.w.Stop()
	h.w = nil
	worker.PurgeStickyWorkflowCache()
	h.startWorker()
}

// tryQuery returns the handler's report. It is require-free so it can run
// inside require.Eventually's polling goroutine.
func (h *queryProbeHarness) tryQuery() (queryProbeReport, error) {
	var report queryProbeReport
	v, err := h.c.QueryWorkflow(h.ctx, h.run.GetID(), h.run.GetRunID(), "replay-probe")
	if err != nil {
		return report, err
	}
	if err := v.Get(&report); err != nil {
		return report, err
	}
	return report, nil
}

func (h *queryProbeHarness) query() queryProbeReport {
	h.t.Helper()
	// Dev-server RPCs can transiently fail on overloaded CI runners
	// (context canceled / unavailable while pollers reconnect); retry
	// briefly and only fail on a persistent error.
	var report queryProbeReport
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		report, err = h.tryQuery()
		if err == nil {
			return report
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	require.NoError(h.t, err)
	return report
}

func (h *queryProbeHarness) signal(done bool) {
	h.t.Helper()
	require.NoError(h.t, h.c.SignalWorkflow(h.ctx, h.run.GetID(), h.run.GetRunID(), "control", done))
}

// TestQueryHandlerGateOnCommandFreeHistory: with no command event trailing
// the last workflow task (a bare signal await, e.g. HITL), a catch-up replay
// ends on the WorkflowTaskCompleted, which Go classifies as non-replay — the
// query handler observes IsReplaying false, and its telemetry records without
// even needing the IsReadOnly leg of the gate.
func TestQueryHandlerGateOnCommandFreeHistory(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	capture := newTelemetryCapture()
	tp, lp, mp := capture.providers()
	pointGlobalsAt(t,
		googleadk.NewReplaySafeTracerProvider(tp),
		googleadk.NewReplaySafeLoggerProvider(lp),
		googleadk.NewReplaySafeMeterProvider(mp),
	)

	h := startQueryProbe(t, c, "adk-telemetry-query-signal", queryProbeInput{PendingCommand: false})

	rep := h.query()
	require.False(t, rep.Replaying, "a query served from the live worker must not observe IsReplaying")
	require.True(t, rep.Enabled, "the gated counter must report Enabled true inside a query handler")
	require.EqualValues(t, 1, collectInt64Sum(t, capture.reader, "query_probe_recordings"))

	h.evict()
	rep = h.query()
	require.False(t, rep.Replaying,
		"after a catch-up replay of a command-free history the trailing WorkflowTaskCompleted resets the flag, so the query observes IsReplaying false")
	require.True(t, rep.Enabled)
	require.EqualValues(t, 2, collectInt64Sum(t, capture.reader, "query_probe_recordings"),
		"the post-catch-up query recording must survive the gate")

	h.signal(true)
	require.NoError(t, h.run.Get(h.ctx, nil))
}

// TestQueryHandlerGateWithPendingCommandOrCompletion regression-tests the
// IsReadOnly leg of the gate in the two history shapes where IsReplaying
// alone used to drop live telemetry: when a command event trails the last
// workflow task (pending timer/Activity — an agent mid-model-call) or the
// workflow is complete, a catch-up replay leaves IsReplaying true when the
// query handler runs. IsReadOnly excludes the handler from suppression, so
// the once-per-request recording survives regardless of the trailing history
// shape or worker cache state.
func TestQueryHandlerGateWithPendingCommandOrCompletion(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	capture := newTelemetryCapture()
	tp, lp, mp := capture.providers()
	pointGlobalsAt(t,
		googleadk.NewReplaySafeTracerProvider(tp),
		googleadk.NewReplaySafeLoggerProvider(lp),
		googleadk.NewReplaySafeMeterProvider(mp),
	)

	h := startQueryProbe(t, c, "adk-telemetry-query-timer", queryProbeInput{PendingCommand: true})

	rep := h.query()
	require.False(t, rep.Replaying, "a query served from the live worker must not observe IsReplaying")
	require.True(t, rep.Enabled)
	require.EqualValues(t, 1, collectInt64Sum(t, capture.reader, "query_probe_recordings"))

	h.evict()
	rep = h.query()
	require.True(t, rep.Replaying,
		"with a pending command event the catch-up replay leaves IsReplaying true and Go core does not reset it before running the query handler")
	require.True(t, rep.Enabled,
		"IsReadOnly must exclude the query handler from the gate even while IsReplaying reads true")
	require.EqualValues(t, 2, collectInt64Sum(t, capture.reader, "query_probe_recordings"),
		"the live query-time recording must survive the gate: IsReplaying && !IsReadOnly is false inside a query handler")

	// The next live workflow task flips the flag back; queries record on both
	// sides of it.
	h.signal(false)
	require.Eventually(t, func() bool {
		rep, err := h.tryQuery()
		return err == nil && !rep.Replaying
	}, 20*time.Second, 250*time.Millisecond,
		"after the live signal workflow task the query must observe IsReplaying false again")
	afterWake := collectInt64Sum(t, capture.reader, "query_probe_recordings")
	require.GreaterOrEqual(t, afterWake, int64(3),
		"every query served — stale-flag polls included — must have recorded")

	// Completed workflow, evicted worker: the catch-up replay ends on the
	// workflow's completion and IsReplaying stays true — the recording must
	// still survive.
	h.signal(true)
	require.NoError(t, h.run.Get(h.ctx, nil))
	h.evict()
	rep = h.query()
	require.True(t, rep.Replaying, "a query replayed to completion observes IsReplaying true")
	require.True(t, rep.Enabled)
	require.Equal(t, afterWake+1, collectInt64Sum(t, capture.reader, "query_probe_recordings"),
		"the completed-run query recording must survive the gate too")
}

// ----------------------------------------------------------------------------
// Update validators.
// ----------------------------------------------------------------------------

// updateProbeObservations collects, host-side, what the validator and handler
// observed across live workers and replayer passes in this process.
type updateProbeObservations struct {
	mu                 sync.Mutex
	validatorReplaying []bool
	handlerRuns        int
}

func (o *updateProbeObservations) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.validatorReplaying = nil
	o.handlerRuns = 0
}

func (o *updateProbeObservations) observeValidator(replaying bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.validatorReplaying = append(o.validatorReplaying, replaying)
}

func (o *updateProbeObservations) noteHandlerRun() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.handlerRuns++
}

func (o *updateProbeObservations) snapshot() ([]bool, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]bool(nil), o.validatorReplaying...), o.handlerRuns
}

var updateProbeObs updateProbeObservations

// updateReplayProbeWorkflow registers an update whose validator and handler
// each record one gated counter increment; the validator also reports the
// workflow.IsReplaying it observed and the handler counts every execution
// (live and replayed) host-side.
func updateReplayProbeWorkflow(ctx workflow.Context) error {
	adkCtx := googleadk.NewContext(ctx)
	meter := otel.Meter("liveops-probe")
	validatorCounter, err := meter.Int64Counter("validator_probe_recordings")
	if err != nil {
		return err
	}
	handlerCounter, err := meter.Int64Counter("update_handler_recordings")
	if err != nil {
		return err
	}
	err = workflow.SetUpdateHandlerWithOptions(ctx, "replay-probe-update",
		func(ctx workflow.Context, n int) error {
			updateProbeObs.noteHandlerRun()
			handlerCounter.Add(adkCtx, 1)
			return nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, n int) error {
				updateProbeObs.observeValidator(workflow.IsReplaying(ctx))
				validatorCounter.Add(adkCtx, 1)
				return nil
			},
		},
	)
	if err != nil {
		return err
	}
	return liveOpsControlLoop(ctx)
}

// TestUpdateValidatorTelemetryIsNeverReplaySuppressed: validators run exactly
// once per update, always live, always observing IsReplaying false — including
// an update delivered right after a full catch-up replay on a fresh worker —
// so validator-time telemetry always passes the gate. Replays (worker catch-up
// and the workflow replayer) re-execute accepted update HANDLERS, whose
// emissions the gate suppresses, but never validators.
func TestUpdateValidatorTelemetryIsNeverReplaySuppressed(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	capture := newTelemetryCapture()
	tp, lp, mp := capture.providers()
	pointGlobalsAt(t,
		googleadk.NewReplaySafeTracerProvider(tp),
		googleadk.NewReplaySafeLoggerProvider(lp),
		googleadk.NewReplaySafeMeterProvider(mp),
	)
	updateProbeObs.reset()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	const taskQueue = "adk-telemetry-update-validator"

	var w worker.Worker
	newWorker := func() {
		w = worker.New(c, taskQueue, worker.Options{StickyScheduleToStartTimeout: time.Second})
		w.RegisterWorkflow(updateReplayProbeWorkflow)
		require.NoError(t, w.Start())
	}
	newWorker()
	t.Cleanup(func() {
		if w != nil {
			w.Stop()
		}
	})

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        taskQueue + "-" + time.Now().Format("150405.000"),
		TaskQueue: taskQueue,
	}, updateReplayProbeWorkflow)
	require.NoError(t, err)

	sendUpdate := func(n int) {
		t.Helper()
		handle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   run.GetID(),
			RunID:        run.GetRunID(),
			UpdateName:   "replay-probe-update",
			Args:         []interface{}{n},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		require.NoError(t, err)
		require.NoError(t, handle.Get(ctx, nil))
	}

	// Live update on a warm worker.
	sendUpdate(1)
	observed, handlerRuns := updateProbeObs.snapshot()
	require.Equal(t, []bool{false}, observed, "a live validator must observe IsReplaying false")
	require.Equal(t, 1, handlerRuns)
	require.EqualValues(t, 1, collectInt64Sum(t, capture.reader, "validator_probe_recordings"),
		"live validator telemetry must pass the gate")
	require.EqualValues(t, 1, collectInt64Sum(t, capture.reader, "update_handler_recordings"))

	// Update delivered to a fresh worker: its workflow task replays the whole
	// history first (re-executing update 1's accepted handler) and then
	// validates update 2 live in the same task.
	w.Stop()
	w = nil
	worker.PurgeStickyWorkflowCache()
	newWorker()
	sendUpdate(2)

	observed, handlerRuns = updateProbeObs.snapshot()
	require.Equal(t, []bool{false, false}, observed,
		"a validator running right after a catch-up replay must observe IsReplaying false, and the replayed accepted update must not re-validate")
	require.Equal(t, 3, handlerRuns,
		"the catch-up replay re-executes update 1's handler (2 live + 1 replayed), proving replay really happened while the validator did not re-run")
	require.EqualValues(t, 2, collectInt64Sum(t, capture.reader, "validator_probe_recordings"),
		"validator telemetry after a catch-up replay must pass the gate — the wrongly-dropped case does not exist for Go validators")
	require.EqualValues(t, 2, collectInt64Sum(t, capture.reader, "update_handler_recordings"),
		"the replayed handler execution's recording is suppressed; only the two live executions record")

	require.NoError(t, c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "control", true))
	require.NoError(t, run.Get(ctx, nil))

	// Replayer passes re-execute both accepted update handlers each time and
	// must never run a validator or add a recording.
	replayTelemetryHistory(t, c, updateReplayProbeWorkflow, run.GetID(), run.GetRunID())
	observed, handlerRuns = updateProbeObs.snapshot()
	require.Equal(t, []bool{false, false}, observed, "the workflow replayer must never run validators")
	require.Equal(t, 3+2*telemetryReplays, handlerRuns, "each replayer pass re-executes both update handlers")
	require.EqualValues(t, 2, collectInt64Sum(t, capture.reader, "validator_probe_recordings"))
	require.EqualValues(t, 2, collectInt64Sum(t, capture.reader, "update_handler_recordings"),
		"replayer passes add no gated recordings")
}
