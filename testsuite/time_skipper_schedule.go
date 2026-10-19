package testsuite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/common/retry"
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
	patchCtx := context.WithValue(ctx, retry.ConfigKey, retry.NewGrpcRetryConfig(retryInitialInterval))
	_, err := t.client.WorkflowService().PatchSchedule(patchCtx,
		&workflowservice.PatchScheduleRequest{
			Namespace:  t.namespace,
			ScheduleId: scheduleID,
			Patch: &schedulepb.SchedulePatch{
				TimeSkippingConfig: config.ToProto(),
			},
			Identity:  t.identity,
			RequestId: uuid.NewString(),
		})
	return err
}

// pollScheduleFastForwardCompletion polls the schedule until the fast-forward
// identified by ffID reports completion.
func (t *TimeSkipper) pollScheduleFastForwardCompletion(
	ctx context.Context, handle client.ScheduleHandle, ffID string,
) (bool, error) {
	ticker := time.NewTicker(scheduleFastForwardPollInterval)
	defer ticker.Stop()

	for {
		tsi, err := t.GetScheduleTimeSkippingInfo(ctx, handle)
		if err != nil {
			return false, err
		}
		ffi := tsi.GetFastForwardInfo()
		// The server leaves fast_forward_info in place after completion, so a
		// different id means another caller replaced ours. An empty id is the
		// window before the patch has been applied.
		switch id := ffi.GetFastForwardId(); {
		case id == ffID:
			if ffi.GetHasCompleted() {
				return true, nil
			}
		case id != "":
			return false, fmt.Errorf(
				"fast_forward %q was overridden by another fast_forward call (current id %q)",
				ffID, id)
		}

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}
