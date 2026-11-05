package test_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/timeskipping"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const devServerVersion = internal.PinnedDevServerVersion

type TimeSkippingV2TestSuite struct {
	*require.Assertions
	suite.Suite

	server *testsuite.DevServer
}

func TestTimeSkippingV2TestSuite(t *testing.T) {
	suite.Run(t, new(TimeSkippingV2TestSuite))
}

func (s *TimeSkippingV2TestSuite) SetupTest() {
	s.Assertions = require.New(s.T())
}

func (s *TimeSkippingV2TestSuite) SetupSuite() {
	s.Assertions = require.New(s.T())

	// Start our own server because the timeskipping server CHASM flags will not
	// work for the entire suite.
	srv, err := timeskipping.StartDevServer(context.Background(), testsuite.DevServerOptions{
		ExistingPath:   os.Getenv("TEMPORAL_DEV_SERVER_EXISTING_PATH"),
		CachedDownload: testsuite.CachedDownload{Version: devServerVersion},
		ClientOptions:  &client.Options{Namespace: "default"},
		LogLevel:       "warn",
	})
	s.NoError(err)
	s.server = srv
}

func (s *TimeSkippingV2TestSuite) TearDownSuite() {
	if s.server != nil {
		s.NoError(s.server.Stop())
	}
}

func (s *TimeSkippingV2TestSuite) getWorkflowRun(ctx context.Context, run client.WorkflowRun) client.WorkflowRun {
	return s.server.Client().GetWorkflow(ctx, run.GetID(), run.GetRunID())
}

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

// findHistoryEvent returns the first event in the run's history that match
// accepts, or nil when none does.
func findHistoryEvent(
	ctx context.Context, c client.Client, run client.WorkflowRun,
	match func(*historypb.HistoryEvent) bool,
) (*historypb.HistoryEvent, error) {
	iter := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		e, err := iter.Next()
		if err != nil {
			return nil, err
		}
		if match(e) {
			return e, nil
		}
	}
	return nil, nil
}

func historyHasEventType(
	ctx context.Context, c client.Client, run client.WorkflowRun, et enumspb.EventType,
) (bool, error) {
	e, err := findHistoryEvent(ctx, c, run, func(e *historypb.HistoryEvent) bool {
		return e.GetEventType() == et
	})
	return e != nil, err
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
// it.
func (s *TimeSkippingV2TestSuite) waitForFastForwardID(
	ctx context.Context, run client.WorkflowRun, expectChangeFrom string, deadline time.Duration,
) (string, error) {
	giveUp := time.Now().Add(deadline)
	for time.Now().Before(giveUp) {
		tsi, err := timeskipping.GetWorkflowTimeSkippingInfo(ctx, s.server.Client(), run)
		if err != nil {
			return "", err
		}
		if tsi != nil {
			id := tsi.FastForwardInfo.ID
			if id != "" && id != expectChangeFrom {
				return id, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", errors.New("timed out waiting for expected fast_forward_id")
}

type SleepResult struct {
	Start   time.Time
	End     time.Time
	Message string
}

func SleepWorkflow(ctx workflow.Context, seconds float64) (SleepResult, error) {
	tStart := workflow.Now(ctx)
	tickCh := workflow.GetSignalChannel(ctx, "tick")
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

func InteractionWorkflow(ctx workflow.Context, numRequiredSignals int) (string, error) {
	ch := workflow.GetSignalChannel(ctx, "proceed")
	timer := workflow.NewTimer(ctx, 10*time.Hour)
	got := 0
	done := false
	for !done && got < numRequiredSignals {
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

func FailOnceThenSleepWorkflow(ctx workflow.Context, seconds float64) (string, error) {
	if err := workflow.Sleep(ctx, time.Duration(seconds*float64(time.Second))); err != nil {
		return "", err
	}
	if workflow.GetInfo(ctx).Attempt < 2 {
		return "", temporal.NewApplicationError("first attempt fails on purpose", "PlannedFail")
	}
	return "done", nil
}

type CANArgs struct {
	SleepSeconds  float64
	RunsRemaining int
	CurrentRun    int
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

type ParentTimeSkippingArgs struct {
	ChildID           string
	ChildSleepSeconds float64
}

type ParentTimeSkippingResult struct {
	ParentStart           time.Time
	ParentAfterWait1      time.Time
	ParentAfterChildStart time.Time
	ParentEnd             time.Time
}

// ParentTimeSkippingWorkflow: parent sleeps 1h, starts a child, parent sleeps 1h.
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

func registerTSTestWorkflows(w worker.Worker) {
	w.RegisterWorkflow(SleepWorkflow)
	w.RegisterWorkflow(InteractionWorkflow)
	w.RegisterWorkflow(FailOnceThenSleepWorkflow)
	w.RegisterWorkflow(ContinueAsNewSleepWorkflow)
	w.RegisterWorkflow(SignalWithStartTargetWorkflow)
	w.RegisterWorkflow(ParentTimeSkippingWorkflow)
}

func (s *TimeSkippingV2TestSuite) TestPollFastForwardIssuedSeparately() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	// Start without time skipping.
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 3600.0)
	s.NoError(err)

	cl := s.server.Client()
	ffID := uuid.NewString()
	_, err = cl.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: run.GetID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			TimeSkippingConfig: &client.TimeSkippingConfigChange{
				Value: &client.TimeSkippingConfig{
					Enabled:           true,
					FastForwardConfig: &client.FastForwardConfig{ID: ffID, Duration: time.Hour},
				},
			},
		},
	})
	s.NoError(err)

	res, err := timeskipping.PollWorkflowFastForward(ctx, cl, run,
		timeskipping.WorkflowPollFastForwardOptions{FastForwardID: ffID})
	s.NoError(err)
	s.True(res.Completed, "expected the fast-forward to complete")
	s.Empty(res.FailedReason)

	var result SleepResult
	s.NoError(run.Get(ctx, &result))
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestPollFastForwardSpansContinueAsNew() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	// Three runs of an hour each, with the fast-forward targeting two hours in:
	// its target can only be reached after at least one continue-as-new.
	ffID := uuid.NewString()
	cfg := client.TimeSkippingConfig{
		Enabled:           true,
		FastForwardConfig: &client.FastForwardConfig{ID: ffID, Duration: 2 * time.Hour},
	}
	wfID := "wf-" + uuid.NewString()
	cl := s.server.Client()
	run, err := cl.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: wfID, TaskQueue: tq, TimeSkippingConfig: &cfg},
		ContinueAsNewSleepWorkflow, CANArgs{SleepSeconds: 3600, RunsRemaining: 3, CurrentRun: 1})
	s.NoError(err)
	firstRunID := run.GetRunID()
	defer func() { _ = cl.CancelWorkflow(ctx, wfID, "") }()

	res, err := timeskipping.PollWorkflowFastForward(ctx, cl, run,
		timeskipping.WorkflowPollFastForwardOptions{FastForwardID: ffID})
	s.NoError(err)
	s.True(res.Completed, "expected the fast-forward to complete on a later run")
	s.Empty(res.FailedReason)

	desc, err := cl.DescribeWorkflowExecution(ctx, wfID, "")
	s.NoError(err)
	s.NotEqual(firstRunID, desc.GetWorkflowExecutionInfo().GetExecution().GetRunId(),
		"fast-forward completed without the chain advancing")

	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestPollFastForwardValidatesArgs() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	cl := s.server.Client()

	_, err := timeskipping.PollWorkflowFastForward(ctx, cl, cl.GetWorkflow(ctx, "", ""),
		timeskipping.WorkflowPollFastForwardOptions{FastForwardID: uuid.NewString()})
	s.Error(err)
	s.Contains(err.Error(), "workflow id")

	_, err = timeskipping.PollWorkflowFastForward(ctx,
		cl, cl.GetWorkflow(ctx, "wf-"+uuid.NewString(), ""),
		timeskipping.WorkflowPollFastForwardOptions{})
	s.Error(err)
	s.Contains(err.Error(), "fast forward id")
}

func (s *TimeSkippingV2TestSuite) TestFastForwardWorkflowRejectsNegativeDuration() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	cl := s.server.Client()

	_, err := timeskipping.FastForwardWorkflow(ctx, cl,
		cl.GetWorkflow(ctx, "wf-"+uuid.NewString(), ""),
		timeskipping.WorkflowFastForwardOptions{Duration: -time.Hour})
	s.Error(err)
	s.Contains(err.Error(), "zero (unbounded) or more")
}

func (s *TimeSkippingV2TestSuite) TestSkipFullRun() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wallStart := time.Now()
	cfg := client.TimeSkippingConfig{Enabled: true}
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq, TimeSkippingConfig: &cfg},
		SleepWorkflow, 3600.0)
	s.NoError(err)
	var result SleepResult
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	s.Equal("all done", result.Message)
	virtualElapsed := result.End.Sub(result.Start)
	s.GreaterOrEqualf(virtualElapsed, 3600*time.Second,
		"virtual elapsed was %v; expected >= 3600s", virtualElapsed)
	s.Lessf(wallElapsed, 3*time.Second,
		"workflow took %v wall time; time skipping did not engage", wallElapsed)
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestNoTimeSkippingConfig() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 3600.0)
	s.NoError(err)

	deadline, cancelDeadline := context.WithTimeout(ctx, 3*time.Second)
	defer cancelDeadline()
	waitErr := run.Get(deadline, nil)
	s.Error(waitErr)
}

func (s *TimeSkippingV2TestSuite) TestFastForwardWithResume() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wallStart := time.Now()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		InteractionWorkflow, 2)
	s.NoError(err)

	t0, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 1 * time.Hour})
	s.NoError(err)
	s.True(ok, "expected the first fast-forward to reach its target")
	s.NoError(s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil))

	t1, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.assertDurationSame(3600*time.Second, t1.Sub(t0), 10*time.Second)

	ok, err = timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 1 * time.Hour})
	s.NoError(err)
	s.True(ok, "expected the second fast-forward to reach its target")
	s.NoError(s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil))

	t2, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.assertDurationSame(7200*time.Second, t2.Sub(t0), 10*time.Second)

	var result string
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	s.Equal("done", result)
	s.Lessf(wallElapsed, 60*time.Second, "took %v wall; expected fast finish", wallElapsed)
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestPartialFastForwardThenUnbounded() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 3600.0)
	s.NoError(err)

	t0, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 30 * time.Minute})
	s.NoError(err)
	s.True(ok)

	t1, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.assertDurationSame(30*60*time.Second, t1.Sub(t0), 10*time.Second)

	ok, err = timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{})
	s.NoError(err)
	s.False(ok)

	var result SleepResult
	s.NoError(run.Get(ctx, &result))
	s.Equal("all done", result.Message)

	tEnd, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.assertDurationSame(3600*time.Second, tEnd.Sub(t0), 10*time.Second)

	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestChildWorkflowPropagatesTimeSkipping() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	childID := "child-" + uuid.NewString()
	wallStart := time.Now()
	cfg := client.TimeSkippingConfig{Enabled: true}
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "parent-" + uuid.NewString(), TaskQueue: tq, TimeSkippingConfig: &cfg},
		ParentTimeSkippingWorkflow,
		ParentTimeSkippingArgs{ChildID: childID, ChildSleepSeconds: 3600},
	)
	s.NoError(err)
	var result ParentTimeSkippingResult
	s.NoError(run.Get(ctx, &result))
	wallElapsed := time.Since(wallStart)

	s.Lessf(wallElapsed, 15*time.Second, "took %v wall; expected TS engaged in both parent and child", wallElapsed)
	s.assertTimeWasSkipped(ctx, run)
	childRun := s.server.Client().GetWorkflow(ctx, childID, "")
	s.assertTimeWasSkipped(ctx, childRun)
}

func (s *TimeSkippingV2TestSuite) TestChildWorkflowWithPropagationDisabled() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	cfg := client.TimeSkippingConfig{Enabled: true, DisablePropagation: true}

	childID := "child-" + uuid.NewString()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			ID:                 "parent-" + uuid.NewString(),
			TaskQueue:          tq,
			TimeSkippingConfig: &cfg,
		},
		ParentTimeSkippingWorkflow,
		ParentTimeSkippingArgs{ChildID: childID, ChildSleepSeconds: 2},
	)
	s.NoError(err)
	var result ParentTimeSkippingResult
	s.NoError(run.Get(ctx, &result))
	s.assertTimeWasSkipped(ctx, run)
	childRun := s.server.Client().GetWorkflow(ctx, childID, "")
	s.assertTimeWasNotSkipped(ctx, childRun)
}

func (s *TimeSkippingV2TestSuite) TestFastForwardReturnsFalseWhenWorkflowTerminatesFirst() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 60.0)
	s.NoError(err)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.False(ok, "expected fast_forward to return false when workflow terminated first")
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestFastForwardSpansRetries() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			ID:        "wf-" + uuid.NewString(),
			TaskQueue: tq,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    time.Hour,
				BackoffCoefficient: 1.0,
				MaximumAttempts:    2,
			},
		}, FailOnceThenSleepWorkflow, 3600.0)
	s.NoError(err)
	firstRun := s.getWorkflowRun(ctx, run)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 2*time.Hour + 30*time.Minute})
	s.NoError(err)
	s.True(ok, "expected the fast-forward to reach its target, not be cut short by the retry")
	_, err = timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{})
	s.NoError(err)
	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal("done", result)

	require.Eventually(s.T(), func() bool {
		n, err := s.countWorkflowsWithID(ctx, run.GetID())
		return err == nil && n == 2
	}, 10*time.Second, 200*time.Millisecond,
		"expected two runs: the failed attempt and its retry")

	s.assertTimeWasSkipped(ctx, firstRun)
}

func (s *TimeSkippingV2TestSuite) TestFastForwardSpansContinueAsNew() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		ContinueAsNewSleepWorkflow, CANArgs{SleepSeconds: 3600, RunsRemaining: 3, CurrentRun: 1})
	s.NoError(err)
	firstRun := s.getWorkflowRun(ctx, run)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 2 * time.Hour})
	s.NoError(err)
	s.True(ok, "expected the fast-forward to reach its target, not be cut short by the continue-as-new")
	_, err = timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{})
	s.NoError(err)
	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal("done", result)

	require.Eventually(s.T(), func() bool {
		n, err := s.countWorkflowsWithID(ctx, run.GetID())
		return err == nil && n == 3
	}, 10*time.Second, 200*time.Millisecond, "expected all three runs of the chain")

	s.assertTimeWasSkipped(ctx, firstRun)
}

func (s *TimeSkippingV2TestSuite) TestUnboundedWaitSpansContinueAsNew() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wfID := "wf-" + uuid.NewString()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: wfID, TaskQueue: tq},
		ContinueAsNewSleepWorkflow, CANArgs{SleepSeconds: 3600, RunsRemaining: 3, CurrentRun: 1})
	s.NoError(err)
	firstRun := s.getWorkflowRun(ctx, run)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run,
		timeskipping.WorkflowFastForwardOptions{})
	s.NoError(err)
	s.False(ok)

	desc, err := s.server.Client().DescribeWorkflowExecution(ctx, wfID, "")
	s.NoError(err)
	s.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		desc.GetWorkflowExecutionInfo().GetStatus(),
		"unbounded wait returned before the chain finished")

	n, err := s.countWorkflowsWithID(ctx, wfID)
	s.NoError(err)
	s.Equal(3, n, "expected all three runs of the chain")

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal("done", result)
	s.assertTimeWasSkipped(ctx, firstRun)
}

func (s *TimeSkippingV2TestSuite) TestFastForwardSpansCronRestarts() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wfID := "wf-" + uuid.NewString()
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: wfID, TaskQueue: tq, CronSchedule: "@every 1h"},
		SleepWorkflow, 60.0)
	s.NoError(err)
	defer func() { _ = s.server.Client().CancelWorkflow(ctx, wfID, "") }()

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 3 * time.Hour})
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

func (s *TimeSkippingV2TestSuite) TestSignalWithStartSkipsTime() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	wallStart := time.Now()
	cfg := client.TimeSkippingConfig{Enabled: true}
	run, err := s.server.Client().SignalWithStartWorkflow(ctx,
		"wf-"+uuid.NewString(),
		"go", nil,
		client.StartWorkflowOptions{TaskQueue: tq, TimeSkippingConfig: &cfg},
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

func (s *TimeSkippingV2TestSuite) TestGetTimeSkippingInfoDuringWorkflow() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	cfg := client.TimeSkippingConfig{Enabled: true}
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq, TimeSkippingConfig: &cfg},
		InteractionWorkflow, 1)
	s.NoError(err)

	// Give the workflow a moment to persist state.
	require.Eventually(s.T(), func() bool {
		tsi, _ := timeskipping.GetWorkflowTimeSkippingInfo(ctx, s.server.Client(), run)
		return tsi != nil && !tsi.CurrentTime.IsZero()
	}, 5*time.Second, 100*time.Millisecond, "TSI not populated after start")

	tsi, err := timeskipping.GetWorkflowTimeSkippingInfo(ctx, s.server.Client(), run)
	s.NoError(err)
	s.NotNil(tsi)
	s.True(tsi.EffectiveConfig.Enabled, "expected effective_config.enabled")
	s.Nil(tsi.FastForwardInfo, "no FF issued yet")

	s.NoError(s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil))
	_ = run.Get(ctx, nil)
}

func (s *TimeSkippingV2TestSuite) TestGetTimeSkippingInfoReturnsNilWhenTSNeverEnabled() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		InteractionWorkflow, 1)
	s.NoError(err)
	defer func() {
		_ = s.server.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil)
		_ = run.Get(ctx, nil)
	}()

	tsi, err := timeskipping.GetWorkflowTimeSkippingInfo(ctx, s.server.Client(), run)
	s.NoError(err)
	s.Nil(tsi)
}

func (s *TimeSkippingV2TestSuite) TestTimeSkippingVirtualClock() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 100000.0)
	s.NoError(err)

	wallStart := time.Now().UTC()
	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 1 * time.Hour})
	s.NoError(err)
	s.True(ok)

	currentTime, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.assertDurationSame(time.Hour, currentTime.Sub(wallStart), 100*time.Second)

	_ = s.server.Client().CancelWorkflow(ctx, run.GetID(), run.GetRunID())
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestTransitionEventPayload() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 100000.0)
	s.NoError(err)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 1 * time.Hour})
	s.NoError(err)
	s.True(ok)

	found, err := findHistoryEvent(ctx, s.server.Client(), run, func(e *historypb.HistoryEvent) bool {
		attrs := e.GetWorkflowExecutionTimeSkippingTransitionedEventAttributes()
		return attrs != nil && attrs.GetDisabledAfterFastForward()
	})
	s.NoError(err)
	s.NotNil(found, "expected a disabled_after_fast_forward transition event")
	attrs := found.GetWorkflowExecutionTimeSkippingTransitionedEventAttributes()
	s.NotNil(attrs.GetTargetTime())
	s.NotNil(attrs.GetWallClockTime())

	_ = s.server.Client().CancelWorkflow(ctx, run.GetID(), run.GetRunID())
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestChildWorkflowStartedEventHasStatePropagation() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	childID := "child-" + uuid.NewString()
	cfg := client.TimeSkippingConfig{Enabled: true}
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "parent-" + uuid.NewString(), TaskQueue: tq, TimeSkippingConfig: &cfg},
		ParentTimeSkippingWorkflow,
		ParentTimeSkippingArgs{ChildID: childID, ChildSleepSeconds: 60},
	)
	s.NoError(err)
	var result ParentTimeSkippingResult
	s.NoError(run.Get(ctx, &result))

	childRun := s.server.Client().GetWorkflow(ctx, childID, "")
	started, err := findHistoryEvent(ctx, s.server.Client(), childRun, func(e *historypb.HistoryEvent) bool {
		return e.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED
	})
	s.NoError(err)
	s.NotNil(started)
	startedAttrs := started.GetWorkflowExecutionStartedEventAttributes()
	s.NotNilf(startedAttrs.GetTimeSkippingStatePropagation(),
		"child's WorkflowExecutionStarted event has no time_skipping_state_propagation")
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestFastForwardClampedToExecutionTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			ID:                       "wf-" + uuid.NewString(),
			TaskQueue:                tq,
			WorkflowExecutionTimeout: 30 * time.Minute,
		}, SleepWorkflow, 100000.0)
	s.NoError(err)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 1 * time.Hour})
	s.NoError(err)
	s.False(ok, "expected fast_forward to return false when workflow timed out first")

	timedOut, err := historyHasEventType(ctx, s.server.Client(), run,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT)
	s.NoError(err)
	s.True(timedOut, "expected WORKFLOW_EXECUTION_TIMED_OUT in history")
}

func (s *TimeSkippingV2TestSuite) TestOverridingFastForwardRaisesOnOriginal() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()

	// No worker yet so skipping doesn't start.
	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 100000.0)
	s.NoError(err)

	type ffResult struct {
		ok  bool
		err error
	}
	firstCh := make(chan ffResult, 1)
	go func() {
		ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 2 * time.Hour})
		firstCh <- ffResult{ok, err}
	}()

	// Wait for the first FF's id to land on the workflow.
	firstID, err := s.waitForFastForwardID(ctx, run, "", 10*time.Second)
	s.NoError(err)

	secondCh := make(chan ffResult, 1)
	go func() {
		ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 30 * time.Minute})
		secondCh <- ffResult{ok, err}
	}()

	// Wait for the second FF's id to land (different from the first's).
	_, err = s.waitForFastForwardID(ctx, run, firstID, 10*time.Second)
	s.NoError(err)

	// Awaiting the first FF: server should have delivered
	// FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_FAILED with an id-mismatch reason.
	firstRes := <-firstCh
	s.Error(firstRes.err, "expected id-mismatch error on first FF")
	// The server's own wording, so this fails if the reason changes shape
	// rather than passing on any error that mentions a fast-forward.
	s.ErrorContains(firstRes.err, "does not match the execution's current fast-forward")

	// Attach a worker so the workflow can complete and the second FF finishes.
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()
	secondRes := <-secondCh
	s.NoError(secondRes.err)
	s.True(secondRes.ok)

	// Let the workflow finish quickly by re-enabling unbounded.
	_, _ = timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{})
	_ = run.Get(ctx, nil)
	s.assertTimeWasSkipped(ctx, run)
}

func (s *TimeSkippingV2TestSuite) TestMaxSessionSkipCountOnStartOptions() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	cfg := client.TimeSkippingConfig{Enabled: true, MaxSessionSkipCount: 5}
	srv := s.server

	tq := "tq-" + uuid.NewString()
	w := worker.New(srv.Client(), tq, worker.Options{})
	w.RegisterWorkflow(InteractionWorkflow)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := srv.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			ID:                 "wf-" + uuid.NewString(),
			TaskQueue:          tq,
			TimeSkippingConfig: &cfg,
		},
		InteractionWorkflow, 1)
	s.NoError(err)
	defer func() {
		_ = srv.Client().SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "proceed", nil)
		_ = run.Get(ctx, nil)
	}()

	started, err := findHistoryEvent(ctx, srv.Client(), run, func(e *historypb.HistoryEvent) bool {
		return e.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED
	})
	s.NoError(err)
	s.NotNil(started)
	startedTSC := started.GetWorkflowExecutionStartedEventAttributes().GetTimeSkippingConfig()
	s.NotNil(startedTSC)
	s.EqualValues(5, startedTSC.GetMaxSessionSkipCount())
}

func (s *TimeSkippingV2TestSuite) TestBoundedFastForwardAdvancesClockByItsDuration() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 100000.0)
	s.NoError(err)

	now, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.WithinDuration(time.Now(), now, 5*time.Second)

	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{Duration: 1000 * time.Second})
	s.NoError(err)
	s.True(ok)

	now, err = timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.WithinDuration(time.Now().Add(1000*time.Second), now, 30*time.Second)
	s.assertTimeWasSkipped(ctx, run)

	s.NoError(s.server.Client().CancelWorkflow(ctx, run.GetID(), run.GetRunID()))
}

// TestUnboundedFastForwardEnablesSkippingMidRun starts with no config, then
// enables unbounded skipping on the running workflow and runs it to completion.
func (s *TimeSkippingV2TestSuite) TestUnboundedFastForwardEnablesSkippingMidRun() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	tq := "tq-" + uuid.NewString()
	w := worker.New(s.server.Client(), tq, worker.Options{})
	registerTSTestWorkflows(w)
	s.NoError(w.Start())
	defer w.Stop()

	run, err := s.server.Client().ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: "wf-" + uuid.NewString(), TaskQueue: tq},
		SleepWorkflow, 100000.0)
	s.NoError(err)

	// Unbounded: enables skipping and waits for the terminal event, always
	// reporting false since there is no fast-forward completion to observe.
	ok, err := timeskipping.FastForwardWorkflow(ctx, s.server.Client(), run, timeskipping.WorkflowFastForwardOptions{})
	s.NoError(err)
	s.False(ok)

	var result SleepResult
	s.NoError(run.Get(ctx, &result))
	s.Equal("all done", result.Message)

	now, err := timeskipping.GetWorkflowCurrentTime(ctx, s.server.Client(), run)
	s.NoError(err)
	s.WithinDuration(time.Now().Add(100000*time.Second), now, 30*time.Second)
	s.assertTimeWasSkipped(ctx, run)
}
