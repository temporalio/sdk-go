package test_test

// Schedule time-skipping (v2) integration tests.

import (
	"context"
	"fmt"
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

	server *testsuite.DevServer
}

func TestTimeSkippingScheduleTestSuite(t *testing.T) {
	suite.Run(t, new(TimeSkippingScheduleTestSuite))
}

func (s *TimeSkippingScheduleTestSuite) SetupTest() {
	s.Assertions = require.New(s.T())
}

func (s *TimeSkippingScheduleTestSuite) SetupSuite() {
	s.Assertions = require.New(s.T())

	srv, err := timeskipping.StartDevServer(context.Background(), testsuite.DevServerOptions{
		ExistingPath:   os.Getenv("TEMPORAL_DEV_SERVER_EXISTING_PATH"),
		CachedDownload: testsuite.CachedDownload{Version: devServerVersion},
		ClientOptions:  &client.Options{Namespace: "default"},
		LogLevel:       "warn",
	})
	s.NoError(err)
	s.server = srv
}

func (s *TimeSkippingScheduleTestSuite) TearDownSuite() {
	if s.server != nil {
		s.NoError(s.server.Stop())
	}
}

func ScheduleTargetWorkflow(ctx workflow.Context) (string, error) {
	return "done", nil
}

func ScheduleSleepTargetWorkflow(ctx workflow.Context) (string, error) {
	if err := workflow.Sleep(ctx, time.Hour); err != nil {
		return "", err
	}
	return "done", nil
}

func (s *TimeSkippingScheduleTestSuite) startScheduleWorker(tq string) worker.Worker {
	w := worker.New(s.server.Client(), tq, worker.Options{})
	w.RegisterWorkflow(ScheduleTargetWorkflow)
	w.RegisterWorkflow(ScheduleSleepTargetWorkflow)
	s.NoError(w.Start())
	return w
}

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
			Workflow:                 wf,
			ID:                       "swf-" + uuid.NewString(),
			TaskQueue:                tq,
			WorkflowExecutionTimeout: 24 * time.Hour,
			WorkflowTaskTimeout:      time.Second,
		},
	})
	s.NoError(err)
	return handle
}

// scheduleBookkeepingWait bounds how long a test waits for the scheduler to
// finish recording what a fast-forward did.
const scheduleBookkeepingWait = 10 * time.Second

// waitForScheduleActions waits until cond holds on the schedule's description.
func (s *TimeSkippingScheduleTestSuite) waitForScheduleActions(
	ctx context.Context,
	handle client.ScheduleHandle,
	msg string,
	cond func(*client.ScheduleDescription) bool,
) {
	require.Eventually(s.T(), func() bool {
		desc, err := handle.Describe(ctx)
		return err == nil && cond(desc)
	}, scheduleBookkeepingWait, 100*time.Millisecond, msg)
}

// hasStartedAction reports whether the schedule has recorded an action whose
// workflow actually started, which is what the propagation tests go on to read.
func hasStartedAction(d *client.ScheduleDescription) bool {
	return len(d.Info.RecentActions) > 0 && d.Info.RecentActions[0].StartWorkflowResult != nil
}

func (s *TimeSkippingScheduleTestSuite) TestFastForwardScheduleRequiresPositiveDuration() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ok, err := timeskipping.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{})
	s.Error(err)
	s.False(ok)
	s.Contains(err.Error(), "requires a Duration")

	ok, err = timeskipping.FastForwardSchedule(ctx, handle,
		timeskipping.ScheduleFastForwardOptions{Duration: -time.Hour})
	s.Error(err)
	s.False(ok)
	s.Contains(err.Error(), "greater than zero")
}

func (s *TimeSkippingScheduleTestSuite) TestScheduleTimeSkippingInfoNilWhenNeverEnabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	tsi, err := timeskipping.GetScheduleTimeSkippingInfo(ctx, handle)
	s.NoError(err)
	s.Nil(tsi)
}

func (s *TimeSkippingScheduleTestSuite) TestFastForwardScheduleFiresActions() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	wallStart := time.Now()
	ok, err := timeskipping.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 3 * time.Hour})
	s.NoError(err)
	s.True(ok)

	s.waitForScheduleActions(ctx, handle, "expected >= 3 actions after a 3h fast-forward",
		func(d *client.ScheduleDescription) bool { return d.Info.NumActions >= 3 })
	s.Lessf(time.Since(wallStart), 30*time.Second,
		"3h of schedule fires took %v wall time; time skipping did not engage",
		time.Since(wallStart))
}

func (s *TimeSkippingScheduleTestSuite) TestSequentialFastForwardsOnOneSchedule() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	var prevActions int
	for i := 1; i <= 3; i++ {
		ok, err := timeskipping.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
		s.NoErrorf(err, "fast-forward #%d failed", i)
		s.Truef(ok, "fast-forward #%d did not report completion", i)

		prev := prevActions
		s.waitForScheduleActions(ctx, handle,
			fmt.Sprintf("fast-forward #%d fired no new actions (still %d)", i, prev),
			func(d *client.ScheduleDescription) bool { return d.Info.NumActions > prev })
		desc, err := handle.Describe(ctx)
		s.NoError(err)
		prevActions = desc.Info.NumActions
	}
}

func (s *TimeSkippingScheduleTestSuite) TestPollScheduleFastForwardIssuedSeparately() {
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
		timeskipping.SchedulePollFastForwardOptions{FastForwardID: ffID})
	s.NoError(err)
	s.True(res.Completed)
	s.Empty(res.FailedReason)

	s.waitForScheduleActions(ctx, handle, "expected the fast-forward to have fired actions",
		func(d *client.ScheduleDescription) bool { return d.Info.NumActions > 0 })
}

func (s *TimeSkippingScheduleTestSuite) TestPollScheduleFastForwardRequiresID() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	res, err := timeskipping.PollScheduleFastForward(ctx, handle,
		timeskipping.SchedulePollFastForwardOptions{})
	s.Error(err, "a missing argument is a failure of the call, not an outcome")
	s.False(res.Completed)
	s.Contains(err.Error(), "FastForwardID")
}

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
		timeskipping.SchedulePollFastForwardOptions{FastForwardID: ffID})
	s.NoError(err, "stopping without completing is an outcome, not a call failure")
	s.False(res.Completed)
	s.Contains(res.FailedReason, "no longer enabled",
		"should diagnose, not wait out the deadline")
}

func (s *TimeSkippingScheduleTestSuite) TestScheduleDescribeReturnsTimeSkippingInfo() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	ok, err := timeskipping.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.True(ok)

	tsi, err := timeskipping.GetScheduleTimeSkippingInfo(ctx, handle)
	s.NoError(err)
	s.NotNil(tsi)
	s.False(tsi.CurrentTime.IsZero(), "expected the schedule's virtual clock to be reported")

	s.True(tsi.FastForwardInfo.HasCompleted,
		"expected the fast-forward to be recorded as completed")
	s.False(tsi.EffectiveConfig.Enabled,
		"expected time skipping to auto-disable once the target time was reached")

	s.Greaterf(tsi.CurrentTime.Sub(time.Now()), 90*time.Minute,
		"expected the schedule clock to be ~2h ahead, got %v",
		tsi.CurrentTime)
}

func (s *TimeSkippingScheduleTestSuite) TestSchedulePropagatesTimeSkippingToWorkflows() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleSleepTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	wallStart := time.Now()
	ok, err := timeskipping.FastForwardSchedule(ctx, handle, timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.True(ok)

	s.waitForScheduleActions(ctx, handle,
		"expected the schedule to have started a workflow", hasStartedAction)
	desc, err := handle.Describe(ctx)
	s.NoError(err)
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

func (s *TimeSkippingScheduleTestSuite) TestScheduleWithPropagationDisabled() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	s.NoError(handle.SetTimeSkippingConfig(ctx, client.ScheduleSetTimeSkippingConfigOptions{
		Config: &client.TimeSkippingConfig{DisablePropagation: true},
	}))
	ok, err := timeskipping.FastForwardSchedule(ctx, handle,
		timeskipping.ScheduleFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.True(ok, "the schedule's own clock should still fast-forward")

	s.waitForScheduleActions(ctx, handle,
		"expected the schedule to have started a workflow", hasStartedAction)
	desc, err := handle.Describe(ctx)
	s.NoError(err)

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

	tsi, err := timeskipping.GetWorkflowTimeSkippingInfo(ctx, s.server.Client(), run)
	s.NoError(err)
	if tsi != nil && tsi.EffectiveConfig != nil {
		s.False(tsi.EffectiveConfig.Enabled,
			"DisablePropagation was set, but the action has time skipping enabled")
	}
}

// TestScheduleCurrentTimeAdvances is the schedule half of
// TestGetCurrentTimeAdvances: the virtual clock a fast-forward moves is
// readable afterwards, and it has moved by roughly the duration asked for.
//
// A schedule that has never skipped any time reports the wall clock, so the
// before-reading doubles as a check on that fallback.
func (s *TimeSkippingScheduleTestSuite) TestScheduleCurrentTimeAdvances() {
	ctx := context.Background()
	tq := "tq-" + uuid.NewString()
	w := s.startScheduleWorker(tq)
	defer w.Stop()

	handle := s.createHourlySchedule(ctx, tq, ScheduleTargetWorkflow)
	defer func() { _ = handle.Delete(ctx) }()

	before, err := timeskipping.GetScheduleCurrentTime(ctx, handle)
	s.NoError(err)
	s.WithinDuration(time.Now(), before, time.Minute,
		"a schedule that has never skipped time should report the wall clock")

	ok, err := timeskipping.FastForwardSchedule(ctx, handle,
		timeskipping.ScheduleFastForwardOptions{Duration: 3 * time.Hour})
	s.NoError(err)
	s.True(ok)

	after, err := timeskipping.GetScheduleCurrentTime(ctx, handle)
	s.NoError(err)
	s.WithinDurationf(before.Add(3*time.Hour), after, 5*time.Minute,
		"expected the virtual clock to advance ~3h, went from %v to %v", before, after)
}
