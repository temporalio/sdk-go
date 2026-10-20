package testsuite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
)

// scheduleFastForwardPollInterval is how often FastForwardSchedule re-reads the
// schedule while waiting for a fast-forward to finish. Unlike the workflow
// path there is no PollWorkflowExecutionTimeSkipping equivalent for schedules,
// so completion has to be polled rather than long-polled.
const scheduleFastForwardPollInterval = 100 * time.Millisecond

// FastForwardSchedule advances a schedule's virtual clock by the given
// duration and waits for the fast-forward to finish.
//
// While a schedule is idle its clock jumps to its next fire time, so a
// fast-forward runs the actions that fall inside the requested window
// back-to-back. The schedule's clock does not advance while it has work
// outstanding — it is paused or closed, has pending backfills, or has
// in-flight starts, cancels or terminates — so actions settle before time moves
// again.
//
// WithDuration is required. A workflow runs to a terminal state, so for
// workflows an absent duration means "skip to the end"; a schedule has no such
// end, and the server rejects time skipping on a schedule without a bounded
// fast-forward.
//
// Returns (true, nil) once the fast-forward completes, and an error if another
// caller's fast-forward supersedes it.
//
// NOTE: Experimental
func (t *TimeSkipper) FastForwardSchedule(
	ctx context.Context, handle client.ScheduleHandle, opts ...FastForwardOption,
) (bool, error) {
	cfg := &fastForwardConfig{}
	for _, o := range opts {
		o.applyFastForward(cfg)
	}
	if cfg.duration == nil {
		return false, errors.New(
			"FastForwardSchedule requires WithDuration: a schedule has no terminal " +
				"state to skip to, so the server rejects unbounded time skipping on it")
	}

	ffID := uuid.NewString()
	current := t.Config()
	tsc := TimeSkippingConfig{
		Enabled:             true,
		FastForwardConfig:   &FastForwardConfig{ID: ffID, Duration: *cfg.duration},
		DisablePropagation:  current.DisablePropagation,
		MaxSessionSkipCount: current.MaxSessionSkipCount,
	}
	if err := t.patchScheduleTimeSkippingConfig(ctx, handle.GetID(), tsc); err != nil {
		return false, err
	}
	return t.pollScheduleFastForwardCompletion(ctx, handle, ffID)
}

// GetScheduleTimeSkippingInfo returns the schedule's server-side
// TimeSkippingInfo, or nil if it has never had time skipping enabled.
//
// The server keeps the last fast-forward's info after it finishes, so a
// non-nil FastForwardInfo does not mean one is in flight — check its
// HasCompleted. Likewise EffectiveConfig.Enabled goes false once a bounded
// fast-forward reaches its target, which is ordinary completion rather than
// anything having gone wrong.
//
// NOTE: Experimental
func (t *TimeSkipper) GetScheduleTimeSkippingInfo(
	ctx context.Context, handle client.ScheduleHandle,
) (*commonpb.TimeSkippingInfo, error) {
	desc, err := handle.Describe(ctx)
	if err != nil {
		return nil, err
	}
	return desc.Info.TimeSkippingInfo, nil
}

// SetScheduleTimeSkippingConfig applies a time-skipping configuration to a
// schedule, replacing any existing one. FastForwardSchedule is the usual way
// in; this is for callers that need to set propagation or session bounds
// without driving a fast-forward.
//
// NOTE: Experimental
func (t *TimeSkipper) SetScheduleTimeSkippingConfig(
	ctx context.Context, handle client.ScheduleHandle, config TimeSkippingConfig,
) error {
	if err := config.Validate(); err != nil {
		return err
	}
	return t.patchScheduleTimeSkippingConfig(ctx, handle.GetID(), config)
}

// patchScheduleTimeSkippingConfig sets the schedule's time-skipping config.
// Time skipping is configured through SchedulePatch — the Schedule message has
// no field for it, so UpdateSchedule cannot carry it (and equally cannot clear
// it).
func (t *TimeSkipper) patchScheduleTimeSkippingConfig(
	ctx context.Context, scheduleID string, config TimeSkippingConfig,
) error {
	return internal.PatchScheduleTimeSkippingConfig(
		ctx, t.client.WorkflowService(), t.namespace, scheduleID, t.identity, config.ToProto())
}

// pollScheduleFastForwardCompletion waits for the fast-forward identified by
// ffID to complete.
//
// "Poll" here is client-side: unlike the workflow path, which hands the wait to
// the server via PollWorkflowExecutionTimeSkipping, schedules have no such RPC,
// so this re-reads DescribeSchedule on a ticker and watches
// TimeSkippingFastForwardInfo.HasCompleted. One fast-forward therefore costs a
// Describe every scheduleFastForwardPollInterval rather than a single blocking
// call. Replace this with the schedules polling API once it exists.
func (t *TimeSkipper) pollScheduleFastForwardCompletion(
	ctx context.Context, handle client.ScheduleHandle, ffID string,
) (bool, error) {
	ticker := time.NewTicker(scheduleFastForwardPollInterval)
	defer ticker.Stop()

	// Kept for the timeout message. A schedule's clock only advances while it
	// is idle, and the server decides that from state the SDK cannot see
	// (paused, closed, migrating, backfilling, starts in flight). Rather than
	// reproduce those rules here and have them rot, report what was last
	// observed and let the caller read the schedule.
	var lastPaused bool
	var lastActions int

	for {
		desc, err := handle.Describe(ctx)
		if err != nil {
			// The deadline can land inside Describe just as easily as in the
			// select below, and that error arrives gRPC-wrapped with the cause
			// no longer in the chain. Normalize both paths to the same report.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return false, scheduleFastForwardTimeout(handle, ffID, lastPaused, lastActions, ctxErr)
			}
			// Describe retries transient codes itself, so anything surfacing
			// here is terminal (the schedule is gone, or the caller cannot read
			// it) or has already exhausted those retries. Either way polling
			// again will not help — abort, but say what the read was for.
			return false, fmt.Errorf(
				"reading schedule %q while waiting for fast_forward %q: %w",
				handle.GetID(), ffID, err)
		}
		lastPaused = desc.Schedule.State != nil && desc.Schedule.State.Paused
		lastActions = desc.Info.NumActions

		ffi := desc.Info.TimeSkippingInfo.GetFastForwardInfo()
		// The server leaves fast_forward_info in place after completion, so an
		// id alone proves nothing; it has to be matched against ours. An empty
		// id is the window before the patch has been applied.
		//
		// A differing id means a later fast-forward replaced ours, which only
		// happens if the caller drives two at once — unlikely for a test helper,
		// and their own bug if so. It is handled because it is the one reading
		// under which ours provably can never complete, not because it is
		// expected.
		switch id := ffi.GetFastForwardId(); {
		case id == ffID:
			if ffi.GetHasCompleted() {
				return true, nil
			}
		case id != "":
			return false, fmt.Errorf(
				"fast_forward %q can never complete: the schedule's current fast_forward is %q, "+
					"so this one was replaced by a later FastForwardSchedule call",
				ffID, id)
		}

		select {
		case <-ctx.Done():
			return false, scheduleFastForwardTimeout(handle, ffID, lastPaused, lastActions, ctx.Err())
		case <-ticker.C:
		}
	}
}

// scheduleFastForwardTimeout reports a fast-forward that outlived the caller's
// context, with the last state seen. Whether a schedule can skip is decided by
// server state the SDK cannot see, so this says what was observed rather than
// naming a cause.
func scheduleFastForwardTimeout(
	handle client.ScheduleHandle, ffID string, paused bool, actions int, cause error,
) error {
	return fmt.Errorf(
		"gave up waiting for fast_forward %q on schedule %q (paused=%v, actions so far=%d): "+
			"%w. A schedule's clock only advances while it is idle, so check whether it is "+
			"paused, closed, migrating, backfilling, or has starts still in flight",
		ffID, handle.GetID(), paused, actions, cause)
}
