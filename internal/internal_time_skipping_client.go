package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const scheduleFastForwardPollInterval = 100 * time.Millisecond

// Validate checks internal consistency of the config.
//
// NOTE: Experimental
func (c *TimeSkippingConfig) Validate() error {
	if !c.Enabled && c.FastForwardConfig != nil {
		return errors.New("FastForwardConfig cannot be set when Enabled is false")
	}
	return nil
}

// ToProto converts the config to its proto counterpart.
//
// NOTE: Experimental
func (c *TimeSkippingConfig) ToProto() *commonpb.TimeSkippingConfig {
	if c == nil {
		return nil
	}
	return &commonpb.TimeSkippingConfig{
		Enabled:             c.Enabled,
		DisablePropagation:  c.DisablePropagation,
		MaxSessionSkipCount: c.MaxSessionSkipCount,
		FastForwardConfig:   c.FastForwardConfig.ToProto(),
	}
}

// ToProto converts the config to its proto counterpart.
//
// NOTE: Experimental
func (c *FastForwardConfig) ToProto() *commonpb.FastForwardConfig {
	if c == nil {
		return nil
	}
	return &commonpb.FastForwardConfig{
		Id:       c.ID,
		Duration: durationpb.New(c.Duration),
	}
}

func timeSkippingConfigFromProto(p *commonpb.TimeSkippingConfig) *TimeSkippingConfig {
	if p == nil {
		return nil
	}
	return &TimeSkippingConfig{
		Enabled:             p.GetEnabled(),
		DisablePropagation:  p.GetDisablePropagation(),
		MaxSessionSkipCount: p.GetMaxSessionSkipCount(),
		FastForwardConfig:   fastForwardConfigFromProto(p.GetFastForwardConfig()),
	}
}

func fastForwardConfigFromProto(p *commonpb.FastForwardConfig) *FastForwardConfig {
	if p == nil {
		return nil
	}
	return &FastForwardConfig{
		ID:       p.GetId(),
		Duration: p.GetDuration().AsDuration(),
	}
}

func timeSkippingInfoFromProto(p *commonpb.TimeSkippingInfo) *TimeSkippingInfo {
	if p == nil {
		return nil
	}
	return &TimeSkippingInfo{
		CurrentTime:             timeFromProto(p.GetCurrentTime()),
		EffectiveConfig:         timeSkippingConfigFromProto(p.GetEffectiveConfig()),
		FastForwardInfo:         timeSkippingFastForwardInfoFromProto(p.GetFastForwardInfo()),
		CurrentSessionSkipCount: p.GetCurrentSessionSkipCount(),
	}
}

func timeSkippingFastForwardInfoFromProto(p *commonpb.TimeSkippingFastForwardInfo) *TimeSkippingFastForwardInfo {
	if p == nil {
		return nil
	}
	return &TimeSkippingFastForwardInfo{
		ID:           p.GetFastForwardId(),
		Duration:     p.GetFastForwardDuration().AsDuration(),
		TargetTime:   timeFromProto(p.GetTargetTime()),
		HasCompleted: p.GetHasCompleted(),
	}
}

// timeFromProto maps an absent timestamp to the zero time rather than to the
// Unix epoch, so callers can tell "not reported" from a real instant.
func timeFromProto(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

// FastForwardWorkflow issues a fast-forward on the workflow and waits for it
// to complete.
//
// With a Duration: bounded — sends a TimeSkippingConfig carrying a fresh
// FastForwardConfig and long-polls PollWorkflowExecutionTimeSkipping for
// completion. Returns (true, nil) on FF completion; (false, nil) when the
// workflow chain terminates or the fast-forward is superseded/reset/TS
// disabled before completion; (false, err) when the server reports that the
// fast_forward_id no longer matches (typically means another caller issued
// a superseding fast-forward).
//
// Without a Duration: unbounded — enables time skipping (no bounded FF) and
// waits for the execution to end, following the chain across retry,
// continue-as-new and cron. Returns (false, nil) once it ends.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.FastForwardWorkflow]
//
// NOTE: Experimental
func FastForwardWorkflow(
	ctx context.Context, c Client, run WorkflowRun, options WorkflowFastForwardOptions,
) (bool, error) {
	wc, err := workflowClientFrom(c)
	if err != nil {
		return false, err
	}
	if options.Duration == 0 {
		return enableUnboundedAndWait(ctx, wc, run)
	}
	return doBoundedFastForward(ctx, wc, run, options.Duration)
}

// GetWorkflowTimeSkippingInfo fetches the workflow's TimeSkippingInfo via
// DescribeWorkflowExecution. Returns (nil, nil) if the workflow has never had
// time skipping enabled.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.GetWorkflowTimeSkippingInfo]
//
// NOTE: Experimental
func GetWorkflowTimeSkippingInfo(
	ctx context.Context, c Client, run WorkflowRun,
) (*TimeSkippingInfo, error) {
	// Empty run ID: TimeSkippingInfo is per-run state that propagates along the
	// chain, so reading the current run is what reports where time skipping now is.
	// The run's own ID would pin to whichever run it was built from.
	desc, err := c.DescribeWorkflowExecution(ctx, run.GetID(), "")
	if err != nil {
		return nil, err
	}
	return timeSkippingInfoFromProto(desc.GetWorkflowExtendedInfo().GetTimeSkippingInfo()), nil
}

// GetWorkflowCurrentTime returns the workflow's current virtual clock, read
// from TimeSkippingInfo.CurrentTime. If the workflow has never had time
// skipping enabled, returns the wall clock (virtual == wall in that case).
//
// Exposed as: [go.temporal.io/sdk/timeskipping.GetWorkflowCurrentTime]
//
// NOTE: Experimental
func GetWorkflowCurrentTime(
	ctx context.Context, c Client, run WorkflowRun,
) (time.Time, error) {
	tsi, err := GetWorkflowTimeSkippingInfo(ctx, c, run)
	if err != nil {
		return time.Time{}, err
	}
	return currentTimeFrom(tsi), nil
}

// currentTimeFrom reads the virtual clock out of a TimeSkippingInfo, falling
// back to the wall clock when the resource has never skipped any time — the
// two are the same thing in that case.
func currentTimeFrom(tsi *TimeSkippingInfo) time.Time {
	if tsi == nil || tsi.CurrentTime.IsZero() {
		return time.Now().UTC()
	}
	return tsi.CurrentTime
}

// workflowClientFrom resolves the concrete client the time-skipping calls need.
func workflowClientFrom(c Client) (*WorkflowClient, error) {
	wc, ok := c.(*WorkflowClient)
	if !ok {
		return nil, fmt.Errorf(
			"time skipping needs the SDK's own client implementation, got %T", c)
	}
	return wc, nil
}

// Copy in the fields that are needed from the existing config, because
// TimeSkippingConfig is always overwritten on the server as a unit.
func carryOverFrom(current *TimeSkippingConfig, tsc *TimeSkippingConfig) {
	if current == nil {
		return
	}
	tsc.DisablePropagation = current.DisablePropagation
	tsc.MaxSessionSkipCount = current.MaxSessionSkipCount
}

// fastForwardConfigFor builds the config for a fast-forward on a workflow,
// carrying over the fields an update would otherwise reset. A nil duration
// means unbounded — enable skipping with no bounded fast-forward.
func fastForwardConfigFor(
	ctx context.Context, c Client, run WorkflowRun, ffID string, d *time.Duration,
) (TimeSkippingConfig, error) {
	tsc := TimeSkippingConfig{Enabled: true}
	cur, err := GetWorkflowTimeSkippingInfo(ctx, c, run)
	if err != nil {
		return TimeSkippingConfig{}, err
	}
	if cur != nil {
		carryOverFrom(cur.EffectiveConfig, &tsc)
	}
	if d != nil {
		tsc.FastForwardConfig = &FastForwardConfig{ID: ffID, Duration: *d}
	}
	return tsc, nil
}
func (wc *WorkflowClient) pollWorkflowTimeSkipping(
	ctx context.Context, request pollWorkflowTimeSkippingRequest,
) (FastForwardPollResult, error) {
	if err := wc.ensureInitialized(ctx); err != nil {
		return FastForwardPollResult{}, err
	}
	if request.WorkflowID == "" {
		return FastForwardPollResult{}, errors.New("missing workflow id argument")
	}
	if request.FastForwardID == "" {
		return FastForwardPollResult{}, errors.New("missing fast forward id argument")
	}

	req := &workflowservice.PollWorkflowExecutionTimeSkippingRequest{
		Namespace: wc.namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: request.WorkflowID,
		},
		FastForwardId: request.FastForwardID,
	}
	for {
		grpcCtx, cancel := newGRPCContext(ctx,
			grpcLongPoll(true), grpcTimeout(pollTimeSkippingTimeout), defaultGrpcRetryParameters(ctx))
		resp, err := wc.workflowService.PollWorkflowExecutionTimeSkipping(grpcCtx, req)
		lapsed := grpcCtx.Err() != nil
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return FastForwardPollResult{}, ctx.Err()
			}
			// Our own poll window closed, as a context error or as the gRPC
			// status it can surface as, so try again.
			if code := status.Code(err); lapsed || code == codes.DeadlineExceeded || code == codes.Canceled {
				continue
			}
			return FastForwardPollResult{}, err
		}

		switch resp.GetFastForwardPollingResult() {
		case enumspb.FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_COMPLETED:
			return FastForwardPollResult{Completed: true}, nil
		case enumspb.FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_FAILED:
			// The server's own reason is handed back rather than interpreted. It
			// distinguishes an id mismatch — a later fast-forward replaced this
			// one, so it can never complete — from benign endings such as the
			// execution finishing first, and callers differ on which of those
			// deserve to be errors.
			reason := resp.GetFailedReason()
			if reason == "" {
				reason = "the server reported the fast-forward as failed without a reason"
			}
			return FastForwardPollResult{FailedReason: reason}, nil
		case enumspb.FAST_FORWARD_POLLING_RESULT_POLL_TIMEOUT:
			continue
		default:
			return FastForwardPollResult{}, fmt.Errorf(
				"PollWorkflowExecutionTimeSkipping returned unknown result %v",
				resp.GetFastForwardPollingResult())
		}
	}
}

// PollWorkflowFastForward waits for the identified fast-forward on a workflow
// execution to complete.
//
// Returns (true, nil) on completion; (false, nil) when the workflow chain
// terminates or the fast-forward is superseded, reset, or time skipping is
// disabled before it completes; (false, err) when the server reports that the
// fast-forward ID no longer matches.
//
// The wait is a server long poll, bounded by the caller's context.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.PollWorkflowFastForward]
//
// NOTE: Experimental
func PollWorkflowFastForward(
	ctx context.Context, c Client, run WorkflowRun, options WorkflowPollFastForwardOptions,
) (FastForwardPollResult, error) {
	wc, err := workflowClientFrom(c)
	if err != nil {
		return FastForwardPollResult{}, err
	}
	return wc.pollWorkflowTimeSkipping(ctx, pollWorkflowTimeSkippingRequest{
		WorkflowID:    run.GetID(),
		FastForwardID: options.FastForwardID,
	})
}

func doBoundedFastForward(
	ctx context.Context, wc *WorkflowClient, run WorkflowRun, d time.Duration,
) (bool, error) {
	ffID := uuid.NewString()
	tsc, err := fastForwardConfigFor(ctx, wc, run, ffID, &d)
	if err != nil {
		return false, err
	}
	if err := updateTimeSkippingConfig(ctx, wc, run, tsc); err != nil {
		return false, err
	}
	res, err := wc.pollWorkflowTimeSkipping(ctx, pollWorkflowTimeSkippingRequest{
		WorkflowID:    run.GetID(),
		FastForwardID: ffID,
	})
	if err != nil {
		return false, err
	}
	// An id mismatch means another caller superseded this fast-forward, so return an error.
	if strings.Contains(res.FailedReason, "fast_forward_id does not match") {
		return false, fmt.Errorf(
			"fast_forward %q was replaced by a later fast_forward call (server: %s)",
			ffID, res.FailedReason)
	}
	return res.Completed, nil
}

func enableUnboundedAndWait(
	ctx context.Context, wc *WorkflowClient, run WorkflowRun,
) (bool, error) {
	tsc, err := fastForwardConfigFor(ctx, wc, run, "", nil)
	if err != nil {
		return false, err
	}
	if err := updateTimeSkippingConfig(ctx, wc, run, tsc); err != nil {
		return false, err
	}
	// Watch history until the full execution chain ends.
	watchRunID := run.GetRunID()
	for {
		iter := wc.GetWorkflowHistory(ctx, run.GetID(), watchRunID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		var nextRunID string
		var closed bool
		for iter.HasNext() {
			event, err := iter.Next()
			if err != nil {
				return false, err
			}
			if nextRunID, closed = runClosedBy(event); closed {
				break
			}
		}
		if !closed || nextRunID == "" {
			return false, nil
		}
		watchRunID = nextRunID
	}
}

// updateTimeSkippingConfig sets the config on the current workflow execution
// run.
func updateTimeSkippingConfig(
	ctx context.Context, c Client, run WorkflowRun, config TimeSkippingConfig,
) error {
	_, err := c.UpdateWorkflowExecutionOptions(ctx, UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: run.GetID(),
		WorkflowExecutionOptionsChanges: WorkflowExecutionOptionsChanges{
			TimeSkippingConfig: &TimeSkippingConfigChange{Value: &config},
		},
	})
	return err
}

// runClosedBy reports whether e closes a run, and if not, returns the next run ID.
func runClosedBy(e *historypb.HistoryEvent) (nextRunID string, closed bool) {
	switch e.GetEventType() {
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		return e.GetWorkflowExecutionCompletedEventAttributes().GetNewExecutionRunId(), true
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		return e.GetWorkflowExecutionFailedEventAttributes().GetNewExecutionRunId(), true
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
		return e.GetWorkflowExecutionTimedOutEventAttributes().GetNewExecutionRunId(), true
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
		return e.GetWorkflowExecutionContinuedAsNewEventAttributes().GetNewExecutionRunId(), true
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
		return "", true
	}
	return "", false
}

// FastForwardSchedule advances a schedule's virtual clock by the given
// duration and waits for the fast-forward to finish.
//
// Duration is required, since they don't have an end like workflows do.
//
// Returns (true, nil) once the fast-forward completes, and an error if another
// caller's fast-forward supersedes it.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.FastForwardSchedule]
//
// NOTE: Experimental
func FastForwardSchedule(
	ctx context.Context, handle ScheduleHandle, options ScheduleFastForwardOptions,
) (bool, error) {
	if options.Duration == 0 {
		return false, errors.New(
			"FastForwardSchedule requires a Duration: a schedule has no terminal " +
				"state to skip to, so the server rejects unbounded time skipping on it")
	}

	ffID := uuid.NewString()
	tsc := TimeSkippingConfig{
		Enabled:           true,
		FastForwardConfig: &FastForwardConfig{ID: ffID, Duration: options.Duration},
	}
	cur, err := GetScheduleTimeSkippingInfo(ctx, handle)
	if err != nil {
		return false, err
	}
	if cur != nil {
		carryOverFrom(cur.EffectiveConfig, &tsc)
	}
	if err := handle.SetTimeSkippingConfig(ctx,
		ScheduleSetTimeSkippingConfigOptions{Config: &tsc}); err != nil {
		return false, err
	}
	res, err := PollScheduleFastForward(ctx, handle, SchedulePollFastForwardOptions{FastForwardID: ffID})
	if err != nil {
		return false, err
	}

	if res.FailedReason != "" {
		return false, fmt.Errorf("fast_forward %q did not complete: %s", ffID, res.FailedReason)
	}
	return res.Completed, nil
}

// GetScheduleTimeSkippingInfo returns the schedule's server-side
// TimeSkippingInfo, or nil if it has never had time skipping enabled.
//
// The server always returns the most recent info, so check HasCompleted to
// check for completion.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.GetScheduleTimeSkippingInfo]
//
// NOTE: Experimental
func GetScheduleTimeSkippingInfo(
	ctx context.Context, handle ScheduleHandle,
) (*TimeSkippingInfo, error) {
	desc, err := handle.Describe(ctx)
	if err != nil {
		return nil, err
	}
	return desc.Info.TimeSkippingInfo, nil
}

// GetScheduleCurrentTime returns the schedule's current virtual clock, read
// from TimeSkippingInfo.CurrentTime. If the schedule has never had time
// skipping enabled, returns the wall clock (virtual == wall in that case).
//
// Exposed as: [go.temporal.io/sdk/timeskipping.GetScheduleCurrentTime]
//
// NOTE: Experimental
func GetScheduleCurrentTime(
	ctx context.Context, handle ScheduleHandle,
) (time.Time, error) {
	tsi, err := GetScheduleTimeSkippingInfo(ctx, handle)
	if err != nil {
		return time.Time{}, err
	}
	return currentTimeFrom(tsi), nil
}

// PollScheduleFastForward waits for the identified fast-forward on a schedule to
// complete, returning (true, nil) when it does.
//
// "Poll" here is client-side, unlike for workflows.
//
// The caller's context bounds the wait; there is no internal timeout.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.PollScheduleFastForward]
//
// NOTE: Experimental
func PollScheduleFastForward(
	ctx context.Context, handle ScheduleHandle, options SchedulePollFastForwardOptions,
) (FastForwardPollResult, error) {
	ffID := options.FastForwardID
	if ffID == "" {
		return FastForwardPollResult{}, errors.New("missing FastForwardID argument")
	}
	ticker := time.NewTicker(scheduleFastForwardPollInterval)
	defer ticker.Stop()

	// Kept for the timeout message.
	var lastPaused bool
	var lastActions int

	for {
		desc, err := handle.Describe(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return FastForwardPollResult{}, scheduleFastForwardTimeout(handle, ffID, lastPaused, lastActions, ctxErr)
			}
			return FastForwardPollResult{}, fmt.Errorf(
				"reading schedule %q while waiting for fast_forward %q: %w",
				handle.GetID(), ffID, err)
		}
		lastPaused = desc.Schedule.State != nil && desc.Schedule.State.Paused
		lastActions = desc.Info.NumActions

		// A differing id means a later fast-forward replaced ours; in this
		// case, querying this ID will never complete. No time-skipping state at
		// all means the schedule has never had it enabled.
		if desc.Info.TimeSkippingInfo == nil {
			return FastForwardPollResult{
				FailedReason: "time skipping has never been enabled on this schedule",
			}, nil
		}

		// FastForwardInfo absent while time skipping is configured means the
		// patch has not been applied yet.
		ffi := desc.Info.TimeSkippingInfo.FastForwardInfo
		if ffi != nil {
			switch {
			case ffi.ID == ffID:
				if ffi.HasCompleted {
					return FastForwardPollResult{Completed: true}, nil
				}
			case ffi.ID != "":
				return FastForwardPollResult{FailedReason: fmt.Sprintf(
					"the schedule's current fast-forward is %q, so this one was replaced "+
						"by a later call", ffi.ID)}, nil
			}
		}

		if tsi := desc.Info.TimeSkippingInfo; tsi != nil &&
			tsi.EffectiveConfig != nil && !tsi.EffectiveConfig.Enabled {
			return FastForwardPollResult{FailedReason: "time skipping is no longer enabled on " +
				"this schedule — it was either disabled by another caller or stopped because " +
				"MaxSessionSkipCount was reached"}, nil
		}

		select {
		case <-ctx.Done():
			return FastForwardPollResult{}, scheduleFastForwardTimeout(handle, ffID, lastPaused, lastActions, ctx.Err())
		case <-ticker.C:
		}
	}
}

// scheduleFastForwardTimeout reports a fast-forward that outlived the caller's
// context, with the last state recorded.
func scheduleFastForwardTimeout(
	handle ScheduleHandle, ffID string, paused bool, actions int, cause error,
) error {
	return fmt.Errorf(
		"gave up waiting for fast_forward %q on schedule %q (paused=%v, actions so far=%d): "+
			"%w. A schedule's clock only advances while it is idle, so check whether it is "+
			"paused, closed, migrating, backfilling, or has starts still in flight",
		ffID, handle.GetID(), paused, actions, cause)
}
