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

// NewTimeSkipper wraps client so that all workflows started via the returned
// TimeSkipper.Client() are stamped with config.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.NewTimeSkipper]
//
// NOTE: Experimental
func NewTimeSkipper(c Client, namespace string, config TimeSkippingConfig) (*TimeSkipper, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	ts := &TimeSkipper{
		namespace:    namespace,
		config:       config,
		stampEnabled: true,
	}
	wrapped, err := NewClientFromExisting(context.Background(), c, ClientOptions{
		Interceptors: []ClientInterceptor{&timeSkippingClientInterceptor{skipper: ts}},
	})
	if err != nil {
		return nil, err
	}
	// NewClientFromExisting always returns a *WorkflowClient.
	ts.client = wrapped.(*WorkflowClient)
	return ts, nil
}

// Client returns the wrapped client that stamps a TimeSkippingConfig on each
// workflow start.
//
// NOTE: Experimental
func (t *TimeSkipper) Client() Client { return t.client }

// Config returns the configuration currently applied to future workflow starts.
//
// NOTE: Experimental
func (t *TimeSkipper) Config() TimeSkippingConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config
}

// SetConfig replaces the configuration applied to future workflow starts.
// Does not affect workflows already running.
//
// NOTE: Experimental
func (t *TimeSkipper) SetConfig(config TimeSkippingConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.config = config
	return nil
}

// WithTimeSkippingDisabled runs f with TimeSkippingConfig stamping suspended.
// Workflows started via TimeSkipper.Client during f do not receive a
// TimeSkippingConfig on their StartWorkflow request. Existing workflows are
// unaffected.
//
// Suspension applies process-wide for the duration of f, not just to the
// calling goroutine.
//
// NOTE: Experimental
func (t *TimeSkipper) WithTimeSkippingDisabled(f func()) {
	t.mu.Lock()
	prev := t.stampEnabled
	t.stampEnabled = false
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.stampEnabled = prev
		t.mu.Unlock()
	}()
	f()
}

// stampConfig returns the config to stamp on a starting workflow, or nil when
// stamping is currently suspended.
func (t *TimeSkipper) stampConfig() *TimeSkippingConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.stampEnabled {
		return nil
	}
	cfg := t.config
	return &cfg
}

// FastForward issues a fast-forward on the workflow and waits for it to
// complete.
//
// With a Duration: bounded — sends a TimeSkippingConfig carrying a fresh
// FastForwardConfig and long-polls PollWorkflowExecutionTimeSkipping for
// completion. Returns (true, nil) on FF completion; (false, nil) when the
// workflow chain terminates or the fast-forward is superseded/reset/TS
// disabled before completion; (false, err) when the server reports that the
// fast_forward_id no longer matches (typically means another caller issued
// a superseding FastForward).
//
// Without a Duration: unbounded — enables time skipping (no bounded FF) and
// waits for a terminal history event on the current run. Always returns
// (false, nil) after the workflow terminates.
//
// NOTE: Experimental
func (t *TimeSkipper) FastForward(
	ctx context.Context, run WorkflowRun, options TimeSkippingFastForwardOptions,
) (bool, error) {
	if options.Duration == 0 {
		return t.enableUnboundedAndWait(ctx, run)
	}
	return t.doBoundedFastForward(ctx, run, options.Duration)
}

// GetTimeSkippingInfo fetches the workflow's TimeSkippingInfo via
// DescribeWorkflowExecution. Returns (nil, nil) if the workflow has never had
// time skipping enabled.
//
// NOTE: Experimental
func (t *TimeSkipper) GetTimeSkippingInfo(
	ctx context.Context, run WorkflowRun,
) (*TimeSkippingInfo, error) {
	desc, err := t.client.DescribeWorkflowExecution(ctx, run.GetID(), run.GetRunID())
	if err != nil {
		return nil, err
	}
	return timeSkippingInfoFromProto(desc.GetWorkflowExtendedInfo().GetTimeSkippingInfo()), nil
}

// GetCurrentTime returns the workflow's current virtual clock, read from
// TimeSkippingInfo.CurrentTime via GetTimeSkippingInfo. If the workflow has
// never had time skipping enabled, returns the wall clock (virtual == wall in
// that case).
//
// NOTE: Experimental
func (t *TimeSkipper) GetCurrentTime(
	ctx context.Context, run WorkflowRun,
) (time.Time, error) {
	tsi, err := t.GetTimeSkippingInfo(ctx, run)
	if err != nil {
		return time.Time{}, err
	}
	if tsi == nil || tsi.CurrentTime.IsZero() {
		return time.Now().UTC(), nil
	}
	return tsi.CurrentTime, nil
}

// fastForwardConfigFrom builds the config to stamp for a fast-forward, carrying
// DisablePropagation and MaxSessionSkipCount over from the current config.
// A nil duration means unbounded — enable skipping with no bounded fast-forward.
func (t *TimeSkipper) fastForwardConfigFrom(ffID string, d *time.Duration) TimeSkippingConfig {
	current := t.Config()
	tsc := TimeSkippingConfig{
		Enabled:             true,
		DisablePropagation:  current.DisablePropagation,
		MaxSessionSkipCount: current.MaxSessionSkipCount,
	}
	if d != nil {
		tsc.FastForwardConfig = &FastForwardConfig{ID: ffID, Duration: *d}
	}
	return tsc
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
			RunId:      request.RunID,
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

// PollFastForward waits for the identified fast-forward on a workflow execution
// to complete.
//
// Returns (true, nil) on completion; (false, nil) when the workflow chain
// terminates or the fast-forward is superseded, reset, or time skipping is
// disabled before it completes; (false, err) when the server reports that the
// fast-forward ID no longer matches.
//
// The wait is a server long poll, bounded by the caller's context.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.PollFastForward]
//
// NOTE: Experimental
func PollFastForward(
	ctx context.Context, c Client, options TimeSkippingPollFastForwardOptions,
) (FastForwardPollResult, error) {
	wc, ok := c.(*WorkflowClient)
	if !ok {
		return FastForwardPollResult{}, fmt.Errorf(
			"polling a fast-forward needs the SDK's own client implementation, got %T", c)
	}
	return wc.pollWorkflowTimeSkipping(ctx, pollWorkflowTimeSkippingRequest(options))
}

func (t *TimeSkipper) doBoundedFastForward(
	ctx context.Context, run WorkflowRun, d time.Duration,
) (bool, error) {
	ffID := uuid.NewString()
	tsc := t.fastForwardConfigFrom(ffID, &d)
	if err := t.updateTimeSkippingConfig(ctx, run, tsc); err != nil {
		return false, err
	}
	// RunId is left unset so the server resolves the current run, letting a
	// fast-forward follow a chain across retry / continue-as-new / cron.
	res, err := t.client.pollWorkflowTimeSkipping(ctx, pollWorkflowTimeSkippingRequest{
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

func (t *TimeSkipper) enableUnboundedAndWait(
	ctx context.Context, run WorkflowRun,
) (bool, error) {
	tsc := t.fastForwardConfigFrom("", nil)
	if err := t.updateTimeSkippingConfig(ctx, run, tsc); err != nil {
		return false, err
	}
	// Watch history until a terminal event fires on the current run.
	iter := t.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return false, err
		}
		if isTerminalEvent(event) {
			return false, nil
		}
	}
	return false, nil
}

func (t *TimeSkipper) updateTimeSkippingConfig(
	ctx context.Context, run WorkflowRun, config TimeSkippingConfig,
) error {
	// RunId is left empty so the server resolves the current run, letting a
	// fast-forward follow a chain across retry / continue-as-new / cron.
	_, err := t.client.UpdateWorkflowExecutionOptions(ctx, UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: run.GetID(),
		WorkflowExecutionOptionsChanges: WorkflowExecutionOptionsChanges{
			TimeSkippingConfig: &TimeSkippingConfigChange{Value: &config},
		},
	})
	return err
}

func isTerminalEvent(e *historypb.HistoryEvent) bool {
	switch e.GetEventType() {
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
		enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
		return true
	}
	return false
}

// FastForwardSchedule advances a schedule's virtual clock by the given
// duration and waits for the fast-forward to finish.
//
// Duration is required, since they don't have an end like workflows do.
//
// Returns (true, nil) once the fast-forward completes, and an error if another
// caller's fast-forward supersedes it.
//
// NOTE: Experimental
func (t *TimeSkipper) FastForwardSchedule(
	ctx context.Context, handle ScheduleHandle, options ScheduleFastForwardOptions,
) (bool, error) {
	if options.Duration == 0 {
		return false, errors.New(
			"FastForwardSchedule requires a Duration: a schedule has no terminal " +
				"state to skip to, so the server rejects unbounded time skipping on it")
	}

	ffID := uuid.NewString()
	tsc := t.fastForwardConfigFrom(ffID, &options.Duration)
	if err := t.patchScheduleTimeSkippingConfig(ctx, handle.GetID(), tsc); err != nil {
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
// NOTE: Experimental
func (t *TimeSkipper) GetScheduleTimeSkippingInfo(
	ctx context.Context, handle ScheduleHandle,
) (*TimeSkippingInfo, error) {
	desc, err := handle.Describe(ctx)
	if err != nil {
		return nil, err
	}
	return desc.Info.TimeSkippingInfo, nil
}

// patchScheduleTimeSkippingConfig sets the schedule's time-skipping config.
func (t *TimeSkipper) patchScheduleTimeSkippingConfig(
	ctx context.Context, scheduleID string, config TimeSkippingConfig,
) error {
	return t.client.ScheduleClient().GetHandle(ctx, scheduleID).
		SetTimeSkippingConfig(ctx, ScheduleSetTimeSkippingConfigOptions{Config: &config})
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

// timeSkippingClientInterceptor is the outbound interceptor that stamps
// TimeSkippingConfig on ExecuteWorkflow calls when stamping is enabled.
type timeSkippingClientInterceptor struct {
	InterceptorBase
	skipper *TimeSkipper
}

func (i *timeSkippingClientInterceptor) InterceptClient(
	next ClientOutboundInterceptor,
) ClientOutboundInterceptor {
	return &timeSkippingClientOutboundInterceptor{
		ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next},
		skipper:                       i.skipper,
	}
}

type timeSkippingClientOutboundInterceptor struct {
	ClientOutboundInterceptorBase
	skipper *TimeSkipper
}

func (o *timeSkippingClientOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context, in *ClientExecuteWorkflowInput,
) (WorkflowRun, error) {
	if in.Options != nil && in.Options.TimeSkippingConfig == nil {
		in.Options.TimeSkippingConfig = o.skipper.stampConfig()
	}
	return o.Next.ExecuteWorkflow(ctx, in)
}

func (o *timeSkippingClientOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context, in *ClientSignalWithStartWorkflowInput,
) (WorkflowRun, error) {
	if in.Options != nil && in.Options.TimeSkippingConfig == nil {
		in.Options.TimeSkippingConfig = o.skipper.stampConfig()
	}
	return o.Next.SignalWithStartWorkflow(ctx, in)
}
