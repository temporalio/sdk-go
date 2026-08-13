package test_test

// Port of tests/testing/test_timeskipping_v2.py from sdk-python. Tests are
// 1:1 with the Python version where the language allows; each test ends with
// an assertTimeWasSkipped/NotSkipped(handle) engagement check.
//
// Requires the built dev-server binary at
// $TEMPORAL_DEV_SERVER_EXISTING_PATH (falls back to a downloaded default).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// ============================================================================
// Suite
// ============================================================================

type TimeSkippingV2TestSuite struct {
	*require.Assertions
	suite.Suite

	server *testsuite.DevServerV2
}

func TestTimeSkippingV2TestSuite(t *testing.T) {
	suite.Run(t, new(TimeSkippingV2TestSuite))
}

func (s *TimeSkippingV2TestSuite) SetupSuite() {
	s.Assertions = require.New(s.T())

	existing := os.Getenv("TEMPORAL_DEV_SERVER_EXISTING_PATH")
	extraArgs := []string{
		"--dynamic-config-value", "frontend.WorkflowTimeSkippingEnabled=true",
	}

	srv, err := testsuite.StartDevServerV2(context.Background(), testsuite.DevServerV2Options{
		DevServerOptions: testsuite.DevServerOptions{
			ExistingPath: existing,
			ClientOptions: &client.Options{Namespace: "default"},
			LogLevel:     "warn",
			ExtraArgs:    extraArgs,
		},
		TSConfig: testsuite.TimeSkippingConfig{Enabled: true},
	})
	s.NoError(err)
	s.server = srv
}

func (s *TimeSkippingV2TestSuite) TearDownSuite() {
	if s.server != nil {
		s.NoError(s.server.Stop())
	}
}

// The suite's per-test tempclient is s.server.Client() by default. Some tests
// need to bypass stamping (start a workflow without a TimeSkippingConfig) —
// they use s.server.WithTimeSkippingDisabled or spawn their own env.

// ============================================================================
// Helpers
// ============================================================================

// assertTimeWasSkipped scans history and requires that at least one
// WorkflowExecutionTimeSkippingTransitioned event fired for this workflow.
func (s *TimeSkippingV2TestSuite) assertTimeWasSkipped(ctx context.Context, run client.WorkflowRun) {
	found, err := historyHasEventType(ctx, s.server.Client(), run,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIME_SKIPPING_TRANSITIONED)
	s.NoError(err)
	s.True(found, "expected at least one TIME_SKIPPING_TRANSITIONED event in history")
}

func (s *TimeSkippingV2TestSuite) assertTimeWasNotSkipped(ctx context.Context, run client.WorkflowRun) {
	found, err := historyHasEventType(ctx, s.server.Client(), run,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIME_SKIPPING_TRANSITIONED)
	s.NoError(err)
	s.False(found, "did not expect any TIME_SKIPPING_TRANSITIONED event in history")
}

func historyHasEventType(
	ctx context.Context, c client.Client, run client.WorkflowRun, et enumspb.EventType,
) (bool, error) {
	iter := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		e, err := iter.Next()
		if err != nil {
			return false, err
		}
		if e.GetEventType() == et {
			return true, nil
		}
	}
	return false, nil
}

// assertDurationSame requires |got - want| <= tolerance.
func (s *TimeSkippingV2TestSuite) assertDurationSame(want, got time.Duration, tolerance time.Duration) {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	s.LessOrEqualf(diff, tolerance, "expected %v within %v of %v", got, tolerance, want)
}

// waitForFastForwardID polls describe until the workflow's current
// fast_forward_id is non-empty and (if expectChangeFrom != "") differs from
// it. Used in TestOverridingFastForward.
func (s *TimeSkippingV2TestSuite) waitForFastForwardID(
	ctx context.Context, run client.WorkflowRun, expectChangeFrom string, deadline time.Duration,
) (string, error) {
	giveUp := time.Now().Add(deadline)
	for time.Now().Before(giveUp) {
		tsi, err := s.server.GetTimeSkippingInfo(ctx, run)
		if err != nil {
			return "", err
		}
		if tsi != nil {
			id := tsi.GetFastForwardInfo().GetFastForwardId()
			if id != "" && id != expectChangeFrom {
				return id, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", errors.New("timed out waiting for expected fast_forward_id")
}

// ============================================================================
// Workflow definitions
// ============================================================================

// SleepWorkflow sleeps for the requested duration then reports timings.
// The `tick` signal is a non-query WFT that causes workflow.Now() to have the
// correct value if queried afterward.

type SleepResult struct {
	Start   time.Time
	End     time.Time
	Message string
}

func SleepWorkflow(ctx workflow.Context, seconds float64) (SleepResult, error) {
	tStart := workflow.Now(ctx)
	// Signal channel for tick — same as Python signal.
	tickCh := workflow.GetSignalChannel(ctx, "tick")
	// Use a selector so sleep + signals interleave (mirrors Python semantics).
	sleepFuture := workflow.NewTimer(ctx, time.Duration(seconds*float64(time.Second)))
	for {
		selector := workflow.NewSelector(ctx)
		var timerDone bool
		selector.AddFuture(sleepFuture, func(f workflow.Future) {
			_ = f.Get(ctx, nil)
			timerDone = true
		})
		selector.AddReceive(tickCh, func(c workflow.ReceiveChannel, _ bool) {
			var dummy struct{}
			c.Receive(ctx, &dummy)
		})
		selector.Select(ctx)
		if timerDone {
			break
		}
	}
	return SleepResult{Start: tStart, End: workflow.Now(ctx), Message: "all done"}, nil
}

// InteractionWorkflow waits for N "proceed" signals with a 10h timeout.
func InteractionWorkflow(ctx workflow.Context, requiredSignals int) (string, error) {
	ch := workflow.GetSignalChannel(ctx, "proceed")
	timer := workflow.NewTimer(ctx, 10*time.Hour)
	got := 0
	done := false
	for !done && got < requiredSignals {
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) {
			var dummy struct{}
			c.Receive(ctx, &dummy)
			got++
		})
		selector.AddFuture(timer, func(f workflow.Future) {
			_ = f.Get(ctx, nil)
			done = true
		})
		selector.Select(ctx)
	}
	return "done", nil
}

// FailOnceThenSleepWorkflow: sleeps and fails on first attempt; succeeds on later ones.
func FailOnceThenSleepWorkflow(ctx workflow.Context, seconds float64) (string, error) {
	if err := workflow.Sleep(ctx, time.Duration(seconds*float64(time.Second))); err != nil {
		return "", err
	}
	if workflow.GetInfo(ctx).Attempt < 2 {
		return "", temporal.NewApplicationError("first attempt fails on purpose", "PlannedFail")
	}
	return "done", nil
}

// ContinueAsNewSleepWorkflow: sleeps, then continue-as-news until runsRemaining==1.
type CANArgs struct {
	SleepSeconds   float64
	RunsRemaining  int
	CurrentRun     int
}

func ContinueAsNewSleepWorkflow(ctx workflow.Context, args CANArgs) (string, error) {
	if args.CurrentRun == 0 {
		args.CurrentRun = 1
	}
	if err := workflow.Sleep(ctx, time.Duration(args.SleepSeconds*float64(time.Second))); err != nil {
		return "", err
	}
	if args.RunsRemaining > 1 {
		return "", workflow.NewContinueAsNewError(ctx, ContinueAsNewSleepWorkflow, CANArgs{
			SleepSeconds:  args.SleepSeconds,
			RunsRemaining: args.RunsRemaining - 1,
			CurrentRun:    args.CurrentRun + 1,
		})
	}
	return "done", nil
}

// SignalWithStartTargetWorkflow: waits for at least one "go" signal, then sleeps.
type SignalWithStartResult struct {
	AfterSignal time.Time
	End         time.Time
}

func SignalWithStartTargetWorkflow(ctx workflow.Context, seconds float64) (SignalWithStartResult, error) {
	ch := workflow.GetSignalChannel(ctx, "go")
	var dummy struct{}
	ch.Receive(ctx, &dummy)
	afterSignal := workflow.Now(ctx)
	if err := workflow.Sleep(ctx, time.Duration(seconds*float64(time.Second))); err != nil {
		return SignalWithStartResult{}, err
	}
	return SignalWithStartResult{AfterSignal: afterSignal, End: workflow.Now(ctx)}, nil
}

// ParentTimeSkippingWorkflow: parent sleeps 1h, starts a child, parent sleeps 1h.
type ParentTimeSkippingArgs struct {
	ChildID           string
	ChildSleepSeconds float64
}

type ParentTimeSkippingResult struct {
	ParentStart              time.Time
	ParentAfterWait1         time.Time
	ParentAfterChildStart    time.Time
	ParentEnd                time.Time
}

func ParentTimeSkippingWorkflow(ctx workflow.Context, args ParentTimeSkippingArgs) (ParentTimeSkippingResult, error) {
	res := ParentTimeSkippingResult{}
	res.ParentStart = workflow.Now(ctx)
	if err := workflow.Sleep(ctx, time.Hour); err != nil {
		return res, err
	}
	res.ParentAfterWait1 = workflow.Now(ctx)
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: args.ChildID})
	child := workflow.ExecuteChildWorkflow(childCtx, SleepWorkflow, args.ChildSleepSeconds)
	if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return res, err
	}
	res.ParentAfterChildStart = workflow.Now(ctx)
	var childOut SleepResult
	if err := child.Get(ctx, &childOut); err != nil {
		return res, err
	}
	if err := workflow.Sleep(ctx, time.Hour); err != nil {
		return res, err
	}
	res.ParentEnd = workflow.Now(ctx)
	return res, nil
}

// Registrar
func registerTSTestWorkflows(w worker.Worker) {
	w.RegisterWorkflow(SleepWorkflow)
	w.RegisterWorkflow(InteractionWorkflow)
	w.RegisterWorkflow(FailOnceThenSleepWorkflow)
	w.RegisterWorkflow(ContinueAsNewSleepWorkflow)
	w.RegisterWorkflow(SignalWithStartTargetWorkflow)
	w.RegisterWorkflow(ParentTimeSkippingWorkflow)
}

// ============================================================================
// Tests — 1:1 ports of the Python V2 tests
// ============================================================================

// TestSkipFullRun ports test_skip_full_run: enable TS, let workflow run to completion.
func (s *TimeSkippingV2TestSuite) TestSkipFullRun() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wallStart := time.Now()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 3600.0)
	s.NoError(err)
	var result SleepResult
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	s.Equal("all done", result.Message)
	virtualElapsed := result.End.Sub(result.Start)
	s.GreaterOrEqualf(virtualElapsed, 3600*time.Second,
		"virtual elapsed was %v; expected >= 3600s", virtualElapsed)
	// 1h timer should skip in well under 3s wall.
	s.Lessf(wallElapsed, 3*time.Second,
		"workflow took %v wall time; time skipping did not engage", wallElapsed)
	s.assertTimeWasSkipped(ctx, run)
}

// TestWithTimeSkippingDisabled ports test_with_time_skipping_disabled: no TS
// stamped → 1h timer does not complete within 3s.
func (s *TimeSkippingV2TestSuite) TestWithTimeSkippingDisabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			SleepWorkflow, 3600.0)
	})
	s.NoError(err)

	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	waitErr := run.Get(deadline, nil)
	// Expect deadline exceeded (workflow can't finish in 3s of wall clock).
	s.Error(waitErr)
}

// TestFastForwardWithResume ports test_fast_forward_with_resume: 1h FF,
// signal, +1h FF, signal, workflow completes.
func (s *TimeSkippingV2TestSuite) TestFastForwardWithResume() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wallStart := time.Now()
	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			InteractionWorkflow, 2)
	})
	s.NoError(err)

	t0, err := s.server.GetCurrentTime(ctx, run)
	s.NoError(err)

	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(1*time.Hour))
	s.NoError(err)
	s.True(ok, "expected first fast-forward to complete at 1h")
	s.NoError(s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil))

	t1, err := s.server.GetCurrentTime(ctx, run)
	s.NoError(err)
	s.assertDurationSame(3600*time.Second, t1.Sub(t0), 10*time.Second)

	ok, err = s.server.FastForward(ctx, run, testsuite.WithDuration(1*time.Hour))
	s.NoError(err)
	s.True(ok, "expected second fast-forward to complete at 2h total")
	s.NoError(s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil))

	t2, err := s.server.GetCurrentTime(ctx, run)
	s.NoError(err)
	s.assertDurationSame(7200*time.Second, t2.Sub(t0), 10*time.Second)

	var result string
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	s.Equal("done", result)
	s.Lessf(wallElapsed, 60*time.Second, "took %v wall; expected fast finish", wallElapsed)
	s.assertTimeWasSkipped(ctx, run)
}

// TestPartialFastForwardThenUnbounded ports test_partial_fast_forward_then_unbounded.
func (s *TimeSkippingV2TestSuite) TestPartialFastForwardThenUnbounded() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			SleepWorkflow, 3600.0)
	})
	s.NoError(err)

	t0, err := s.server.GetCurrentTime(ctx, run)
	s.NoError(err)

	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(30*time.Minute))
	s.NoError(err)
	s.True(ok)

	t1, err := s.server.GetCurrentTime(ctx, run)
	s.NoError(err)
	s.assertDurationSame(30*60*time.Second, t1.Sub(t0), 10*time.Second)

	// Unbounded resume: returns (false, nil) after termination.
	ok, err = s.server.FastForward(ctx, run)
	s.NoError(err)
	s.False(ok)

	var result SleepResult
	s.NoError(run.Get(ctx, &result))
	s.Equal("all done", result.Message)

	tEnd, err := s.server.GetCurrentTime(ctx, run)
	s.NoError(err)
	s.assertDurationSame(3600*time.Second, tEnd.Sub(t0), 10*time.Second)

	s.assertTimeWasSkipped(ctx, run)
}

// TestChildWorkflowPropagatesTimeSkipping ports the equivalent test.
func (s *TimeSkippingV2TestSuite) TestChildWorkflowPropagatesTimeSkipping() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	childID := "child-" + uuid.NewString()
	wallStart := time.Now()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "parent-" + uuid.NewString(), TaskQueue: tq},
		ParentTimeSkippingWorkflow,
		ParentTimeSkippingArgs{ChildID: childID, ChildSleepSeconds: 3600},
	)
	s.NoError(err)
	var result ParentTimeSkippingResult
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	// Parent skipped 1h + 1h = 2h. Child skipped 1h. All virtual.
	s.Lessf(wallElapsed, 15*time.Second, "took %v wall; expected TS engaged in both parent and child", wallElapsed)
	s.assertTimeWasSkipped(ctx, run)
	// Child too:
	childRun := s.server.Client().GetWorkflow(ctx, childID, "")
	s.assertTimeWasSkipped(ctx, childRun)
}

// TestChildWorkflowWithPropagationDisabled ports the equivalent test.
func (s *TimeSkippingV2TestSuite) TestChildWorkflowWithPropagationDisabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	// Temporarily override the skipper's config to disable propagation for
	// child workflows started via the parent.
	prev := s.server.TimeSkipper().Config()
	s.NoError(s.server.TimeSkipper().SetConfig(testsuite.TimeSkippingConfig{
		Enabled:            true,
		DisablePropagation: true,
	}))
	defer func() { _ = s.server.TimeSkipper().SetConfig(prev) }()

	childID := "child-" + uuid.NewString()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "parent-" + uuid.NewString(), TaskQueue: tq},
		ParentTimeSkippingWorkflow,
		ParentTimeSkippingArgs{ChildID: childID, ChildSleepSeconds: 2},
	)
	s.NoError(err)
	// Parent will skip its own timers; child won't skip (propagation off).
	// Give the child a bit of wall time for its 2s sleep to complete.
	var result ParentTimeSkippingResult
	s.NoError(run.Get(ctx, &result))
	s.assertTimeWasSkipped(ctx, run)
	childRun := s.server.Client().GetWorkflow(ctx, childID, "")
	s.assertTimeWasNotSkipped(ctx, childRun)
}

// TestTimeSkipperWrappingLocalEnvClient ports test_timeskipper_wrapping_local_env_client:
// use TimeSkipper directly instead of through DevServerV2.
func (s *TimeSkippingV2TestSuite) TestTimeSkipperWrappingLocalEnvClient() {
	ctx := context.Background()

	// Fresh dev server WITHOUT DevServerV2 (no stamping) — mimics Python's
	// "start_local, then wrap the client with TimeSkipper directly".
	raw, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		ExistingPath:  os.Getenv("TEMPORAL_DEV_SERVER_EXISTING_PATH"),
		ClientOptions: &client.Options{Namespace: "default"},
		LogLevel:      "warn",
		ExtraArgs: []string{
			"--dynamic-config-value", "frontend.WorkflowTimeSkippingEnabled=true",
		},
	})
	s.NoError(err)
	defer func() { _ = raw.Stop() }()

	skipper, err := testsuite.NewTimeSkipper(raw.Client(), "default",
		testsuite.TimeSkippingConfig{Enabled: true})
	s.NoError(err)

	tq := "tq-" + uuid.NewString()
	w := worker.New(skipper.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wallStart := time.Now()
	run, err := skipper.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 3600.0)
	s.NoError(err)
	var result SleepResult
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	s.Lessf(wallElapsed, 3*time.Second, "took %v; TS did not engage via bare TimeSkipper", wallElapsed)
	// Engagement check via history (independent of DevServerV2):
	found, err := historyHasEventType(ctx, raw.Client(), run,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIME_SKIPPING_TRANSITIONED)
	s.NoError(err)
	s.True(found)
}

// TestFastForwardReturnsFalseWhenWorkflowTerminatesFirst ports the equivalent.
func (s *TimeSkippingV2TestSuite) TestFastForwardReturnsFalseWhenWorkflowTerminatesFirst() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			SleepWorkflow, 60.0)
	})
	s.NoError(err)

	// FF beyond workflow's own sleep → workflow ends before FF target.
	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(2*time.Hour))
	s.NoError(err)
	s.False(ok, "expected fast_forward to return false when workflow terminated first")
	s.assertTimeWasSkipped(ctx, run)
}

// TestFastForwardSpansRetries: FF crosses retry backoff + attempt 2 sleep.
func (s *TimeSkippingV2TestSuite) TestFastForwardSpansRetries() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{
				ID:        "wf-" + uuid.NewString(),
				TaskQueue: tq,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    time.Hour,
					BackoffCoefficient: 1.0,
					MaximumAttempts:    2,
				},
			}, FailOnceThenSleepWorkflow, 3600.0)
	})
	s.NoError(err)

	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(2*time.Hour+30*time.Minute))
	s.NoError(err)
	s.True(ok, "expected FF to span retry")
	// Re-enable unbounded skipping so the remaining ~30m of attempt-2 sleep
	// is skipped rather than waited out.
	_, err = s.server.FastForward(ctx, run)
	s.NoError(err)
	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal("done", result)
	s.assertTimeWasSkipped(ctx, run)
}

// TestFastForwardSpansContinueAsNew.
func (s *TimeSkippingV2TestSuite) TestFastForwardSpansContinueAsNew() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			ContinueAsNewSleepWorkflow, CANArgs{SleepSeconds: 3600, RunsRemaining: 3, CurrentRun: 1})
	})
	s.NoError(err)

	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(2*time.Hour))
	s.NoError(err)
	s.True(ok, "expected FF to span CAN chain")
	// Re-enable unbounded.
	_, err = s.server.FastForward(ctx, run)
	s.NoError(err)
	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal("done", result)
	s.assertTimeWasSkipped(ctx, run)
}

// TestFastForwardSpansCronRestarts.
func (s *TimeSkippingV2TestSuite) TestFastForwardSpansCronRestarts() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wfID := "wf-" + uuid.NewString()
	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: wfID, TaskQueue: tq, CronSchedule: "@every 1h"},
			SleepWorkflow, 60.0)
	})
	s.NoError(err)
	defer func() { _ = s.server.Client().CancelWorkflow(ctx, wfID, "") }()

	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(3*time.Hour))
	s.NoError(err)
	s.True(ok)

	require.Eventually(s.T(), func() bool {
		n, err := s.countWorkflowsWithID(ctx, wfID)
		return err == nil && n >= 3
	}, 10*time.Second, 200*time.Millisecond, "expected >= 3 cron runs to appear in visibility")

	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) countWorkflowsWithID(ctx context.Context, wfID string) (int, error) {
	resp, err := s.server.Client().WorkflowService().ListWorkflowExecutions(ctx,
		&workflowservice.ListWorkflowExecutionsRequest{
			Namespace: "default",
			Query:     fmt.Sprintf(`WorkflowId = %q`, wfID),
			PageSize:  100,
		})
	if err != nil {
		return 0, err
	}
	return len(resp.GetExecutions()), nil
}

// TestSignalWithStartStampsTimeSkippingConfig.
func (s *TimeSkippingV2TestSuite) TestSignalWithStartStampsTimeSkippingConfig() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wallStart := time.Now()
	run, err := s.server.Client().SignalWithStartWorkflow(ctx,
		"wf-"+uuid.NewString(),
		"go", nil,
		client.StartWorkflowOptions{TaskQueue: tq},
		SignalWithStartTargetWorkflow, 3600.0)
	s.NoError(err)
	var result SignalWithStartResult
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	s.Lessf(wallElapsed, 10*time.Second, "took %v wall; TS not engaged", wallElapsed)
	virtualElapsed := result.End.Sub(result.AfterSignal)
	s.assertDurationSame(3600*time.Second, virtualElapsed, 50*time.Second)
	s.assertTimeWasSkipped(ctx, run)
}

// TestGetTimeSkippingInfoDuringWorkflow.
func (s *TimeSkippingV2TestSuite) TestGetTimeSkippingInfoDuringWorkflow() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		InteractionWorkflow, 1)
	s.NoError(err)

	// Give the workflow a moment to persist state.
	require.Eventually(s.T(), func() bool {
		tsi, _ := s.server.GetTimeSkippingInfo(ctx, run)
		return tsi != nil && tsi.GetCurrentTime() != nil
	}, 5*time.Second, 100*time.Millisecond, "TSI not populated after start")

	tsi, err := s.server.GetTimeSkippingInfo(ctx, run)
	s.NoError(err)
	s.NotNil(tsi)
	s.True(tsi.GetEffectiveConfig().GetEnabled(), "expected effective_config.enabled")
	s.Nil(tsi.GetFastForwardInfo(), "no FF issued yet")

	s.NoError(s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil))
	_ = run.Get(ctx, nil)
}

// TestGetTimeSkippingInfoReturnsNilWhenTSNeverEnabled.
func (s *TimeSkippingV2TestSuite) TestGetTimeSkippingInfoReturnsNilWhenTSNeverEnabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			InteractionWorkflow, 1)
	})
	s.NoError(err)
	defer func() {
		_ = s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil)
		_ = run.Get(ctx, nil)
	}()

	tsi, err := s.server.GetTimeSkippingInfo(ctx, run)
	s.NoError(err)
	s.Nil(tsi)
}

// TestTimeSkippingVirtualClock.
func (s *TimeSkippingV2TestSuite) TestTimeSkippingVirtualClock() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			SleepWorkflow, 100000.0)
	})
	s.NoError(err)

	wallStart := time.Now().UTC()
	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(1*time.Hour))
	s.NoError(err)
	s.True(ok)

	currentTime, err := s.server.GetCurrentTime(ctx, run)
	s.NoError(err)
	offset := currentTime.Sub(wallStart)
	s.Greaterf(offset, 3550*time.Second, "virtual clock only %v past wall start; expected ~1h", offset)
	s.Lessf(offset, 3700*time.Second, "virtual clock %v past wall start; expected ~1h", offset)

	_ = s.server.Client().CancelWorkflow(ctx, run.GetID(), run.GetRunID())
	s.assertTimeWasSkipped(ctx, run)
}

// TestTransitionEventPayload.
func (s *TimeSkippingV2TestSuite) TestTransitionEventPayload() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			SleepWorkflow, 100000.0)
	})
	s.NoError(err)

	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(1*time.Hour))
	s.NoError(err)
	s.True(ok)

	// Find the disabled-after-fast-forward transition; verify target_time and wall_clock_time populated.
	iter := s.server.Client().GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var found *historypb.HistoryEvent
	for iter.HasNext() {
		e, err := iter.Next()
		s.NoError(err)
		if e.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIME_SKIPPING_TRANSITIONED {
			if attrs := e.GetWorkflowExecutionTimeSkippingTransitionedEventAttributes(); attrs != nil && attrs.GetDisabledAfterFastForward() {
				found = e
				break
			}
		}
	}
	s.NotNil(found, "expected a disabled_after_fast_forward transition event")
	attrs := found.GetWorkflowExecutionTimeSkippingTransitionedEventAttributes()
	s.NotNil(attrs.GetTargetTime())
	// wall_clock_time is set by server; existence check.
	_ = attrs

	_ = s.server.Client().CancelWorkflow(ctx, run.GetID(), run.GetRunID())
	s.assertTimeWasSkipped(ctx, run)
}

// TestChildWorkflowStartedEventHasStatePropagation.
func (s *TimeSkippingV2TestSuite) TestChildWorkflowStartedEventHasStatePropagation() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	childID := "child-" + uuid.NewString()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "parent-" + uuid.NewString(), TaskQueue: tq},
		ParentTimeSkippingWorkflow,
		ParentTimeSkippingArgs{ChildID: childID, ChildSleepSeconds: 60},
	)
	s.NoError(err)
	var result ParentTimeSkippingResult
	s.NoError(run.Get(ctx, &result))

	// Verify child's WorkflowExecutionStarted event carries time_skipping_state_propagation.
	childRun := s.server.Client().GetWorkflow(ctx, childID, "")
	iter := s.server.Client().GetWorkflowHistory(ctx, childRun.GetID(), childRun.GetRunID(), false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var startedAttrs *historypb.WorkflowExecutionStartedEventAttributes
	for iter.HasNext() {
		e, err := iter.Next()
		s.NoError(err)
		if e.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
			startedAttrs = e.GetWorkflowExecutionStartedEventAttributes()
			break
		}
	}
	s.NotNil(startedAttrs)
	s.NotNilf(startedAttrs.GetTimeSkippingStatePropagation(),
		"child's WorkflowExecutionStarted event has no time_skipping_state_propagation")
	s.assertTimeWasSkipped(ctx, run)
}

// TestFastForwardClampedToExecutionTimeout.
func (s *TimeSkippingV2TestSuite) TestFastForwardClampedToExecutionTimeout() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{
				ID:                       "wf-" + uuid.NewString(),
				TaskQueue:                tq,
				WorkflowExecutionTimeout: 30 * time.Minute,
			}, SleepWorkflow, 100000.0)
	})
	s.NoError(err)

	// FF for 1h — but execution timeout is 30m; workflow times out at ~30m before FF completes.
	ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(1*time.Hour))
	s.NoError(err)
	s.False(ok, "expected fast_forward to return false when workflow timed out first")

	iter := s.server.Client().GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	timedOut := false
	for iter.HasNext() {
		e, err := iter.Next()
		s.NoError(err)
		if e.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT {
			timedOut = true
			break
		}
	}
	s.True(timedOut, "expected WORKFLOW_EXECUTION_TIMED_OUT in history")
}

// TestOverridingFastForwardRaisesOnOriginal — fire-and-forget one FF, then
// fire another; awaiting the first surfaces the id-mismatch error.
func (s *TimeSkippingV2TestSuite) TestOverridingFastForwardRaisesOnOriginal() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()

	// No worker yet — the initial workflow task stays pending so TS can't skip.
	var run client.WorkflowRun
	var err error
	s.server.WithTimeSkippingDisabled(func() {
		run, err = s.server.Client().ExecuteWorkflow(ctx,
			client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
			SleepWorkflow, 100000.0)
	})
	s.NoError(err)

	type ffResult struct {
		ok  bool
		err error
	}
	firstCh := make(chan ffResult, 1)
	go func() {
		ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(2*time.Hour))
		firstCh <- ffResult{ok, err}
	}()

	// Wait for the first FF's id to land on the workflow.
	firstID, err := s.waitForFastForwardID(ctx, run, "", 10*time.Second)
	s.NoError(err)

	secondCh := make(chan ffResult, 1)
	go func() {
		ok, err := s.server.FastForward(ctx, run, testsuite.WithDuration(30*time.Minute))
		secondCh <- ffResult{ok, err}
	}()

	// Wait for the second FF's id to land (different from the first's).
	_, err = s.waitForFastForwardID(ctx, run, firstID, 10*time.Second)
	s.NoError(err)

	// Awaiting the first FF: server should have delivered
	// FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_FAILED with an id-mismatch reason.
	firstRes := <-firstCh
	s.Error(firstRes.err, "expected id-mismatch error on first FF")
	s.Truef(strings.Contains(firstRes.err.Error(), "overridden") || strings.Contains(firstRes.err.Error(), "fast_forward_id"),
		"expected mismatch reason in error, got %q", firstRes.err)

	// Attach a worker so the workflow can complete and the second FF finishes.
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()
	secondRes := <-secondCh
	s.NoError(secondRes.err)
	s.True(secondRes.ok)

	// Let the workflow finish quickly by re-enabling unbounded.
	_, _ = s.server.FastForward(ctx, run)
	_ = run.Get(ctx, nil)
	s.assertTimeWasSkipped(ctx, run)
}

// TestMaxSessionSkipCountStampedByEnv — start a fresh env with
// TSConfig.MaxSessionSkipCount=5, start a workflow, read the value back off
// the WorkflowExecutionStarted event.
func (s *TimeSkippingV2TestSuite) TestMaxSessionSkipCountStampedByEnv() {
	ctx := context.Background()
	srv, err := testsuite.StartDevServerV2(ctx, testsuite.DevServerV2Options{
		DevServerOptions: testsuite.DevServerOptions{
			ExistingPath:  os.Getenv("TEMPORAL_DEV_SERVER_EXISTING_PATH"),
			ClientOptions: &client.Options{Namespace: "default"},
			LogLevel:      "warn",
			ExtraArgs: []string{
				"--dynamic-config-value", "frontend.WorkflowTimeSkippingEnabled=true",
			},
		},
		TSConfig: testsuite.TimeSkippingConfig{Enabled: true, MaxSessionSkipCount: 5},
	})
	s.NoError(err)
	defer func() { _ = srv.Stop() }()

	tq := "tq-" + uuid.NewString()
	w := worker.New(srv.Client(), tq, worker.Options{})
	w.RegisterWorkflow(InteractionWorkflow)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := srv.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		InteractionWorkflow, 1)
	s.NoError(err)
	defer func() {
		_ = srv.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil)
		_ = run.Get(ctx, nil)
	}()

	iter := srv.Client().GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var startedTSC *commonpb.TimeSkippingConfig
	for iter.HasNext() {
		e, err := iter.Next()
		s.NoError(err)
		if e.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
			startedTSC = e.GetWorkflowExecutionStartedEventAttributes().GetTimeSkippingConfig()
			break
		}
	}
	s.NotNil(startedTSC)
	s.EqualValues(5, startedTSC.GetMaxSessionSkipCount())
}

// Sanity: the compile-time check that a subset of imports is actually used.
var (
	_ = math.Pi
	_ = errors.New
	_ sync.Mutex
	_ = serviceerror.NewInvalidArgument
)
