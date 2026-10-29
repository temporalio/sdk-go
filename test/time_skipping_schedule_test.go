package test_test

// Schedule time-skipping (v2) integration tests.
//
// Schedules differ from workflows in three ways that shape these tests:
//   - Configuration arrives through SchedulePatch; the Schedule message has no
//     field for it, so UpdateSchedule cannot carry (or clear) it.
//   - A bounded fast-forward is required. A workflow has a terminal state to
//     skip to; a schedule does not.
//   - There is no long-poll RPC, so completion is polled off Describe.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/timeskipping"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type TimeSkippingScheduleTestSuite struct {
	*require.Assertions
	suite.Suite

	server *timeskipping.DevServer
}

func TestTimeSkippingScheduleTestSuite(t *testing.T) {
	suite.Run(t, new(TimeSkippingScheduleTestSuite))
}

func (s *TimeSkippingScheduleTestSuite) SetupTest() {
	s.Assertions = require.New(s.T())
}

func (s *TimeSkippingScheduleTestSuite) SetupSuite() {
	s.Assertions = require.New(s.T())

	srv, err := timeskipping.StartDevServer(context.Background(), timeskipping.DevServerOptions{
		DevServerOptions: testsuite.DevServerOptions{
			// The time-skipping dynamic config uses the post-rename key names, which
			// no published CLI release carries yet; point this at a newer binary to
			// run these suites. See timeskipping.StartDevServer.
			ExistingPath:   os.Getenv("TEMPORAL_DEV_SERVER_EXISTING_PATH"),
			CachedDownload: testsuite.CachedDownload{Version: devServerVersion},
			ClientOptions:  &client.Options{Namespace: "default"},
			LogLevel:       "warn",
		},
		Config: timeskipping.TimeSkippingConfig{Enabled: true},
	})
	s.NoError(err)
	s.server = srv
}

func (s *TimeSkippingScheduleTestSuite) TearDownSuite() {
	if s.server != nil {
		s.NoError(s.server.Stop())
	}
}

// ScheduleTargetWorkflow returns immediately; used where only the fact that an
// action fired matters.
func ScheduleTargetWorkflow(ctx workflow.Context) (string, error) {
	return "done", nil
}

// ScheduleSleepTargetWorkflow sleeps an hour. It only finishes promptly if the
// schedule propagated time skipping to it.
func ScheduleSleepTargetWorkflow(ctx workflow.Context) (string, error) {
	if err := workflow.Sleep(ctx, time.Hour); err != nil {
		return "", err
	}
	return "done", nil
}

// startScheduleWorker runs a worker for the schedule's target workflows. A
// schedule's clock will not advance while it has in-flight starts, so the
// actions need somewhere to run.
func (s *TimeSkippingScheduleTestSuite) startScheduleWorker(tq string) worker.Worker {
	w := worker.New(s.server.Client(), tq, worker.Options{})
	w.RegisterWorkflow(ScheduleTargetWorkflow)
	w.RegisterWorkflow(ScheduleSleepTargetWorkflow)
	s.NoError(w.Start())
	return w
}

// createHourlySchedule makes an unpaused schedule that fires wf every hour.
func (s *TimeSkippingScheduleTestSuite) createHourlySchedule(
	ctx context.Context, tq string, wf any,
) client.ScheduleHandle {
	id := "sched-" + uuid.NewString()
	handle, err := s.server.Client().ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: id,
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{{Every: time.Hour}},
		},
		Action: &client.ScheduleWorkflowAction{
			Workflow:  wf,
			ID:        "swf-" + uuid.NewString(),
			TaskQueue: tq,
			// Execution timeouts are measured in the workflow's own (virtual)
			// time, so this has to exceed anything the action sleeps through
			// or skipping will time the action out instead of completing it.
			WorkflowExecutionTimeout: 24 * time.Hour,
			WorkflowTaskTimeout:      time.Second,
		},
	})
	s.NoError(err)
	return handle
}

// TestFastForwardScheduleRequiresDuration asserts the schedule-specific rule
// that unbounded skipping is rejected. For workflows a zero Duration
// means "skip to the end"; a schedule has no end.
func (s *TimeSkippingScheduleTestSuite) TestFastForwardScheduleRequiresDuration() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ok, err := s.server.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{})
	s.Error(err)
	s.False(ok)
	s.Contains(err.Error(), "requires a Duration")
}

// TestScheduleTimeSkippingInfoNilWhenNeverEnabled asserts Describe reports no
// time-skipping info for a schedule that never had it enabled.
func (s *TimeSkippingScheduleTestSuite) TestScheduleTimeSkippingInfoNilWhenNeverEnabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	tsi, err := s.server.GetScheduleTimeSkippingInfo(ctx, handle)
	s.NoError(err)
	s.Nil(tsi)
}

// TestFastForwardScheduleFiresActions is the core case: advancing an hourly
// schedule by three hours runs the actions in that window back-to-back.
func (s *TimeSkippingScheduleTestSuite) TestFastForwardScheduleFiresActions() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	wallStart := time.Now()
	ok, err := s.server.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 3 * time.Hour})
	s.NoError(err)
	s.True(ok)

	desc, err := handle.Describe(ctx)
	s.NoError(err)
	s.GreaterOrEqualf(desc.Info.NumActions, 3,
		"expected >= 3 actions after a 3h fast-forward, got %d", desc.Info.NumActions)
	s.Lessf(time.Since(wallStart), 30*time.Second,
		"3h of schedule fires took %v wall time; time skipping did not engage",
		time.Since(wallStart))
}

// TestSequentialFastForwardsOnOneSchedule checks that a schedule can be
// fast-forwarded repeatedly, each call completing and firing its own actions.
//
// It deliberately asserts nothing about fast-forward ids. Whether a call
// observes its own fast-forward rather than a previous completed one is not
// observable through the public API today — see go-tsk-phases.md, "restore when
// schedules get a polling API".
func (s *TimeSkippingScheduleTestSuite) TestSequentialFastForwardsOnOneSchedule() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	var prevActions int
	for i := 1; i <= 3; i++ {
		ok, err := s.server.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
		s.NoErrorf(err, "fast-forward #%d failed", i)
		s.Truef(ok, "fast-forward #%d did not report completion", i)

		desc, err := handle.Describe(ctx)
		s.NoError(err)
		s.Greaterf(desc.Info.NumActions, prevActions,
			"fast-forward #%d fired no new actions (still %d)", i, desc.Info.NumActions)
		prevActions = desc.Info.NumActions
	}
}

// TestPollScheduleFastForwardWithoutTimeSkipper covers the CLI's shape: the
// fast-forward is issued through the plain schedule handle, and the wait is a
// separate call holding no TimeSkipper. This is the path temporalio/cli#1246
// hand-rolled because it was not reachable.
func (s *TimeSkippingScheduleTestSuite) TestPollScheduleFastForwardWithoutTimeSkipper() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ffID := uuid.NewString()
	s.NoError(handle.SetTimeSkippingConfig(ctx, client.ScheduleSetTimeSkippingConfigOptions{
		Config: &client.TimeSkippingConfig{
			Enabled:           true,
			FastForwardConfig: &client.FastForwardConfig{ID: ffID, Duration: 3 * time.Hour},
		},
	}))

	res, err := timeskipping.PollScheduleFastForward(ctx, handle,
		timeskipping.PollScheduleFastForwardOptions{FastForwardID: ffID})
	s.NoError(err)
	s.True(res.Completed)
	s.Empty(res.FailedReason)

	desc, err := handle.Describe(ctx)
	s.NoError(err)
	s.Greater(desc.Info.NumActions, 0, "expected the fast-forward to have fired actions")
}

// TestPollScheduleFastForwardRequiresID guards the one argument the standalone
// wait cannot default.
func (s *TimeSkippingScheduleTestSuite) TestPollScheduleFastForwardRequiresID() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	res, err := timeskipping.PollScheduleFastForward(ctx, handle,
		timeskipping.PollScheduleFastForwardOptions{})
	s.Error(err, "a missing argument is a failure of the call, not an outcome")
	s.False(res.Completed)
	s.Contains(err.Error(), "FastForwardID")
}

// TestPollScheduleFastForwardReportsTimeSkippingDisabled asserts the wait gives
// up with a diagnosis, rather than spinning to the caller's deadline, when time
// skipping is switched off before the fast-forward finishes.
func (s *TimeSkippingScheduleTestSuite) TestPollScheduleFastForwardReportsTimeSkippingDisabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	// Pause first so the clock cannot advance and complete the fast-forward
	// before the disable lands.
	s.NoError(handle.Pause(ctx, client.SchedulePauseOptions{Note: "hold for disable test"}))

	ffID := uuid.NewString()
	s.NoError(handle.SetTimeSkippingConfig(ctx, client.ScheduleSetTimeSkippingConfigOptions{
		Config: &client.TimeSkippingConfig{
			Enabled:           true,
			FastForwardConfig: &client.FastForwardConfig{ID: ffID, Duration: 3 * time.Hour},
		},
	}))
	s.NoError(handle.SetTimeSkippingConfig(ctx, client.ScheduleSetTimeSkippingConfigOptions{
		Config: &client.TimeSkippingConfig{Enabled: false},
	}))

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := timeskipping.PollScheduleFastForward(waitCtx, handle,
		timeskipping.PollScheduleFastForwardOptions{FastForwardID: ffID})
	s.NoError(err, "stopping without completing is an outcome, not a call failure")
	s.False(res.Completed)
	s.Contains(res.FailedReason, "no longer enabled",
		"should diagnose, not wait out the deadline")
}

// TestFastForwardPausedScheduleReportsWhyItGaveUp covers the realistic way a
// schedule fast-forward fails to finish: a paused schedule's clock does not
// advance, so the call waits out the caller's context. The SDK deliberately
// does not try to detect that itself — whether a schedule can skip is decided
// by server state it cannot see — so what is asserted here is that the timeout
// says enough to diagnose it.
func (s *TimeSkippingScheduleTestSuite) TestFastForwardPausedScheduleReportsWhyItGaveUp() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()
	s.NoError(handle.Pause(ctx, client.SchedulePauseOptions{Note: "clock must not advance"}))

	ffCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ok, err := s.server.FastForwardSchedule(ffCtx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 3 * time.Hour})
	s.False(ok)
	s.Error(err)
	s.ErrorIs(err, context.DeadlineExceeded, "the caller's deadline is what ends the wait")
	// The message has to be enough to work out why, without the SDK claiming
	// to know which of the server's blocking conditions applied.
	s.Contains(err.Error(), "paused=true")
	s.Contains(err.Error(), "only advances while it is idle")

	desc, err := handle.Describe(ctx)
	s.NoError(err)
	s.Zero(desc.Info.NumActions, "a paused schedule must not fire actions")
}

// TestScheduleDescribeSurfacesTimeSkippingInfo asserts the config round-trips
// back through Describe, which is the only way to observe schedule TS state.
func (s *TimeSkippingScheduleTestSuite) TestScheduleDescribeSurfacesTimeSkippingInfo() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ok, err := s.server.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.True(ok)

	tsi, err := s.server.GetScheduleTimeSkippingInfo(ctx, handle)
	s.NoError(err)
	s.NotNil(tsi)
	s.False(tsi.CurrentTime.IsZero(), "expected the schedule's virtual clock to be reported")

	// Reaching the target auto-disables skipping, so the terminal state is a
	// completed fast-forward with the config switched back off.
	s.True(tsi.FastForwardInfo.HasCompleted,
		"expected the fast-forward to be recorded as completed")
	s.False(tsi.EffectiveConfig.Enabled,
		"expected time skipping to auto-disable once the target time was reached")

	// The virtual clock should have moved roughly the requested span ahead.
	s.Greaterf(tsi.CurrentTime.Sub(time.Now()), 90*time.Minute,
		"expected the schedule clock to be ~2h ahead, got %v",
		tsi.CurrentTime)
}

// TestSchedulePropagatesTimeSkippingToWorkflows asserts the schedule's actions
// inherit time skipping: a started workflow that sleeps an hour completes
// without an hour of wall time, and its history records the transition.
func (s *TimeSkippingScheduleTestSuite) TestSchedulePropagatesTimeSkippingToWorkflows() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleSleepTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	wallStart := time.Now()
	ok, err := s.server.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.True(ok)

	desc, err := handle.Describe(ctx)
	s.NoError(err)
	s.NotEmpty(desc.Info.RecentActions, "expected the schedule to have started a workflow")
	s.Lessf(time.Since(wallStart), 30*time.Second,
		"actions sleeping 1h each took %v wall time; skipping did not propagate",
		time.Since(wallStart))

	// The schedule's action should carry propagated time-skipping state on its
	// WorkflowExecutionStarted event, the same way a child workflow does.
	started := desc.Info.RecentActions[0].StartWorkflowResult
	s.NotNil(started)
	run := s.server.Client().GetWorkflow(ctx, started.WorkflowID, started.FirstExecutionRunID)

	iter := s.server.Client().GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
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
		"schedule-started workflow has no time_skipping_state_propagation on its "+
			"WorkflowExecutionStarted event")

	// And it should actually skip: a 1h sleep must not cost an hour of wall time.
	s.NoError(run.Get(ctx, nil))
	s.Lessf(time.Since(wallStart), 60*time.Second,
		"the schedule's 1h-sleeping action took %v wall time", time.Since(wallStart))
}

// TestScheduleWithPropagationDisabled is the schedule counterpart of
// TestChildWorkflowWithPropagationDisabled: with DisablePropagation set, the
// schedule still skips its own clock, but its actions start without time
// skipping. The target returns immediately so the assertion is structural
// rather than a race against wall time.
func (s *TimeSkippingScheduleTestSuite) TestScheduleWithPropagationDisabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	prev := s.server.TimeSkipper.Config()
	s.NoError(s.server.TimeSkipper.SetConfig(timeskipping.TimeSkippingConfig{
		Enabled:            true,
		DisablePropagation: true,
	}))
	defer func() { _ = s.server.TimeSkipper.SetConfig(prev) }()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ok, err := s.server.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.True(ok, "the schedule's own clock should still fast-forward")

	desc, err := handle.Describe(ctx)
	s.NoError(err)
	s.NotEmpty(desc.Info.RecentActions, "expected the schedule to have started a workflow")

	started := desc.Info.RecentActions[0].StartWorkflowResult
	s.NotNil(started)
	run := s.server.Client().GetWorkflow(ctx, started.WorkflowID, started.FirstExecutionRunID)

	iter := s.server.Client().GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
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

	// Virtual start time propagates regardless: the action is handed the
	// schedule's accumulated skip so its clock starts where the schedule's is.
	// What DisablePropagation suppresses is the enabled flag and the
	// fast-forward, so the action does not go on skipping on its own.
	prop := startedAttrs.GetTimeSkippingStatePropagation()
	s.NotNil(prop, "virtual start time should still propagate")
	s.Nilf(prop.GetFastForwardTargetTime(),
		"DisablePropagation was set, but the action inherited a fast-forward target: %v", prop)

	tsi, err := s.server.GetTimeSkippingInfo(ctx, run)
	s.NoError(err)
	if tsi != nil && tsi.EffectiveConfig != nil {
		s.False(tsi.EffectiveConfig.Enabled,
			"DisablePropagation was set, but the action has time skipping enabled")
	}
}
