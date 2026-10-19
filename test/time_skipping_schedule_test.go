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
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type TimeSkippingScheduleTestSuite struct {
	*require.Assertions
	suite.Suite

	server *testsuite.DevServerV2
}

func TestTimeSkippingScheduleTestSuite(t *testing.T) {
	suite.Run(t, new(TimeSkippingScheduleTestSuite))
}

func (s *TimeSkippingScheduleTestSuite) SetupTest() {
	s.Assertions = require.New(s.T())
}

func (s *TimeSkippingScheduleTestSuite) SetupSuite() {
	s.Assertions = require.New(s.T())

	srv, err := testsuite.StartDevServerV2(context.Background(), testsuite.DevServerV2Options{
		DevServerOptions: testsuite.DevServerOptions{
			ExistingPath:   os.Getenv("TEMPORAL_DEV_SERVER_EXISTING_PATH"),
			CachedDownload: testsuite.CachedDownload{Version: devServerVersion},
			ClientOptions:  &client.Options{Namespace: "default"},
			LogLevel:       "warn",
		},
		TSConfig: testsuite.TimeSkippingConfig{Enabled: true},
	})
	s.NoError(err)
	s.server = srv
	s.requireScheduleTimeSkippingSupport()
}

// requireScheduleTimeSkippingSupport skips the suite unless the dev server
// actually implements schedule time skipping. A server that predates it accepts
// the patch and silently ignores it, which would otherwise leave every
// fast-forward in this suite polling until the test binary times out.
func (s *TimeSkippingScheduleTestSuite) requireScheduleTimeSkippingSupport() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tq := "tq-preflight-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(context.Background()) }()

	err := s.server.TimeSkipper().SetScheduleTimeSkippingConfig(ctx, handle,
		testsuite.TimeSkippingConfig{
			Enabled: true,
			FastForwardConfig: &testsuite.FastForwardConfig{
				ID:       uuid.NewString(),
				Duration: time.Minute,
			},
		})
	if err != nil {
		w.Stop()
		s.skipSuite("schedule time skipping unavailable on this dev server: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tsi, tsiErr := s.server.GetScheduleTimeSkippingInfo(ctx, handle)
		s.NoError(tsiErr)
		if tsi != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	w.Stop()
	s.skipSuite("dev server accepted a schedule time-skipping patch but never reported " +
		"TimeSkippingInfo, so it predates schedule time skipping; point " +
		"TEMPORAL_DEV_SERVER_EXISTING_PATH at a newer server binary")
}

// skipSuite shuts the dev server down before skipping. Skipping from
// SetupSuite bypasses TearDownSuite, which would otherwise leave the server
// process alive holding the test binary's stdio open.
func (s *TimeSkippingScheduleTestSuite) skipSuite(format string, args ...any) {
	if s.server != nil {
		_ = s.server.Stop()
		s.server = nil
	}
	s.T().Skipf(format, args...)
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
// that unbounded skipping is rejected. For workflows an absent WithDuration
// means "skip to the end"; a schedule has no end.
func (s *TimeSkippingScheduleTestSuite) TestFastForwardScheduleRequiresDuration() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ok, err := s.server.FastForwardSchedule(ctx, handle)
	s.Error(err)
	s.False(ok)
	s.Contains(err.Error(), "WithDuration")
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
	ok, err := s.server.FastForwardSchedule(ctx, handle, testsuite.WithDuration(3*time.Hour))
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

// TestScheduleDescribeSurfacesTimeSkippingInfo asserts the config round-trips
// back through Describe, which is the only way to observe schedule TS state.
func (s *TimeSkippingScheduleTestSuite) TestScheduleDescribeSurfacesTimeSkippingInfo() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ok, err := s.server.FastForwardSchedule(ctx, handle, testsuite.WithDuration(2*time.Hour))
	s.NoError(err)
	s.True(ok)

	tsi, err := s.server.GetScheduleTimeSkippingInfo(ctx, handle)
	s.NoError(err)
	s.NotNil(tsi)
	s.NotNil(tsi.GetCurrentTime(), "expected the schedule's virtual clock to be reported")

	// Reaching the target auto-disables skipping, so the terminal state is a
	// completed fast-forward with the config switched back off.
	s.True(tsi.GetFastForwardInfo().GetHasCompleted(),
		"expected the fast-forward to be recorded as completed")
	s.False(tsi.GetEffectiveConfig().GetEnabled(),
		"expected time skipping to auto-disable once the target time was reached")

	// The virtual clock should have moved roughly the requested span ahead.
	s.Greaterf(tsi.GetCurrentTime().AsTime().Sub(time.Now()), 90*time.Minute,
		"expected the schedule clock to be ~2h ahead, got %v",
		tsi.GetCurrentTime().AsTime())
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
	ok, err := s.server.FastForwardSchedule(ctx, handle, testsuite.WithDuration(2*time.Hour))
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
