// Package timeskipping drives Temporal's per-workflow and per-schedule time
// skipping (v2) against a real server.
//
// Time skipping lets a test advance an execution's virtual clock instead of
// waiting out its timers, so an hour-long sleep or a week of scheduled runs
// completes in moments. It is a testing facility and is not intended for
// production workloads, but it is driven through an ordinary client rather than
// a test framework, so it lives here rather than in testsuite.
//
// Time skipping is switched on per execution, by passing a
// [go.temporal.io/sdk/client.TimeSkippingConfig] when the workflow is started
// or the schedule created. Nothing is enabled for you and there is no ambient
// mode: an execution skips time only if its own configuration says so. A
// workflow's configuration does propagate to the children and scheduled
// workflows it starts, unless DisablePropagation is set.
//
//	cfg := client.TimeSkippingConfig{Enabled: true}
//	run, _ := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
//		ID:                 "wf",
//		TaskQueue:          tq,
//		TimeSkippingConfig: &cfg,
//	}, MyWorkflow)
//
//	// Advance an hour and wait for it to land.
//	ok, _ := timeskipping.FastForwardWorkflow(ctx, c, run,
//		timeskipping.WorkflowFastForwardOptions{Duration: time.Hour})
//
// # Shape of this API
//
// Every function names the resource it acts on — Workflow or Schedule — and
// takes that resource as an argument rather than as a field of its options.
// The options carry parameters only.
//
// The two halves are otherwise parallel, and differ in one way: a workflow
// takes both a client and a [go.temporal.io/sdk/client.WorkflowRun], because a
// WorkflowRun holds no client of its own, while a
// [go.temporal.io/sdk/client.ScheduleHandle] does and so travels alone.
//
// A workflow operation takes only the workflow ID from the run it is given,
// and by default targets the chain's current run, so it follows along across
// retry, continue-as-new and cron. Pinning to one run is therefore always
// explicit, via RunID on [WorkflowFastForwardOptions].
//
// [PollWorkflowFastForward] has no such field, because the server keys the wait
// on the workflow ID and it spans the chain: a fast-forward issued on one run
// can complete on a later one, and the poll is woken either way.
//
// The server must have time skipping enabled; [StartDevServer] starts a dev
// server with the dynamic config it needs.
package timeskipping

import (
	"context"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
)

// The configuration and info types time skipping shares with the rest of the
// client API — TimeSkippingConfig, FastForwardConfig, TimeSkippingInfo and
// TimeSkippingFastForwardInfo — live in [go.temporal.io/sdk/client] and are
// used from there rather than re-exported here.
type (
	// WorkflowFastForwardOptions configures a single fast-forward on a workflow.
	//
	// NOTE: Experimental
	WorkflowFastForwardOptions = internal.WorkflowFastForwardOptions

	// ScheduleFastForwardOptions configures a single fast-forward on a schedule.
	//
	// NOTE: Experimental
	ScheduleFastForwardOptions = internal.ScheduleFastForwardOptions

	// WorkflowPollFastForwardOptions identifies the workflow fast-forward to wait
	// for.
	//
	// NOTE: Experimental
	WorkflowPollFastForwardOptions = internal.WorkflowPollFastForwardOptions

	// SchedulePollFastForwardOptions identifies the schedule fast-forward to wait
	// for.
	//
	// NOTE: Experimental
	SchedulePollFastForwardOptions = internal.SchedulePollFastForwardOptions

	// FastForwardPollResult is the outcome of waiting for a fast-forward. A
	// fast-forward that stops without completing is reported in FailedReason
	// rather than as an error; errors mean the call itself failed.
	//
	// NOTE: Experimental
	FastForwardPollResult = internal.FastForwardPollResult
)

// FastForwardWorkflow advances a running workflow's virtual clock and waits
// for the result.
//
// With a Duration the fast-forward is bounded: it completes once virtual time
// reaches the target, and time skipping switches itself off there. Without one
// it is unbounded — skipping stays on and the call waits for the workflow to
// end, which is why that case returns false.
//
// The workflow must already have time skipping configured; starting it with a
// client.TimeSkippingConfig is what does that.
//
// NOTE: Experimental
func FastForwardWorkflow(
	ctx context.Context, c client.Client, run client.WorkflowRun,
	options WorkflowFastForwardOptions,
) (bool, error) {
	return internal.FastForwardWorkflow(ctx, c, run, options)
}

// FastForwardSchedule advances a schedule's virtual clock by the given duration
// and waits for the fast-forward to finish.
//
// Duration is required: a schedule has no terminal state to skip to, so the
// server rejects unbounded time skipping on one.
//
// NOTE: Experimental
func FastForwardSchedule(
	ctx context.Context, handle client.ScheduleHandle, options ScheduleFastForwardOptions,
) (bool, error) {
	return internal.FastForwardSchedule(ctx, handle, options)
}

// PollWorkflowFastForward waits for the identified fast-forward on a workflow
// execution to complete. Use it when the fast-forward was issued elsewhere;
// FastForwardWorkflow already waits for its own.
//
// The wait is a server long poll, reissued until the caller's context ends, so
// the context is what bounds it.
//
// NOTE: Experimental
func PollWorkflowFastForward(
	ctx context.Context, c client.Client, run client.WorkflowRun,
	options WorkflowPollFastForwardOptions,
) (FastForwardPollResult, error) {
	return internal.PollWorkflowFastForward(ctx, c, run, options)
}

// PollScheduleFastForward waits for the identified fast-forward on a schedule
// to complete. Use it when the fast-forward was issued elsewhere;
// FastForwardSchedule already waits for its own.
//
// Schedules have no polling RPC, so this re-reads the schedule until the
// fast-forward resolves; the caller's context bounds the wait.
//
// NOTE: Experimental
func PollScheduleFastForward(
	ctx context.Context, handle client.ScheduleHandle, options SchedulePollFastForwardOptions,
) (FastForwardPollResult, error) {
	return internal.PollScheduleFastForward(ctx, handle, options)
}

// GetWorkflowTimeSkippingInfo returns the server's view of time skipping on a
// workflow, or nil if the workflow has never had it enabled.
//
// NOTE: Experimental
func GetWorkflowTimeSkippingInfo(
	ctx context.Context, c client.Client, run client.WorkflowRun,
) (*client.TimeSkippingInfo, error) {
	return internal.GetWorkflowTimeSkippingInfo(ctx, c, run)
}

// GetScheduleTimeSkippingInfo returns the server's view of time skipping on a
// schedule, or nil if the schedule has never had it enabled.
//
// The server leaves the last fast-forward's info in place after it finishes, so
// check HasCompleted rather than reading a non-nil FastForwardInfo as one still
// in flight.
//
// NOTE: Experimental
func GetScheduleTimeSkippingInfo(
	ctx context.Context, handle client.ScheduleHandle,
) (*client.TimeSkippingInfo, error) {
	return internal.GetScheduleTimeSkippingInfo(ctx, handle)
}

// GetWorkflowCurrentTime returns a workflow's current virtual clock. A workflow
// that has not skipped any time yet reports wall-clock time.
//
// NOTE: Experimental
func GetWorkflowCurrentTime(
	ctx context.Context, c client.Client, run client.WorkflowRun,
) (time.Time, error) {
	return internal.GetWorkflowCurrentTime(ctx, c, run)
}

// GetScheduleCurrentTime returns a schedule's current virtual clock. A schedule
// that has not skipped any time yet reports wall-clock time.
//
// NOTE: Experimental
func GetScheduleCurrentTime(
	ctx context.Context, handle client.ScheduleHandle,
) (time.Time, error) {
	return internal.GetScheduleCurrentTime(ctx, handle)
}
