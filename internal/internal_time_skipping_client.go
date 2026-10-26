package internal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// scheduleFastForwardPollInterval is how often FastForwardSchedule re-reads the
// schedule while waiting for a fast-forward to finish. Unlike the workflow
// path there is no PollWorkflowExecutionTimeSkipping equivalent for schedules,
// so completion has to be polled rather than long-polled.
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

// ToProto converts the config to its proto counterpart. It performs no defaulting or ID
// generation — the fields are copied 1:1.
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

// ToProto maps to the proto counterpart.
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

// timeSkippingConfigFromProto converts a server-supplied config back to the typed form,
// for read-back on update responses.
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
// TimeSkipper.Client() are stamped with config. The namespace is required
// because it isn't exposed on the Client interface but is needed for our
// PollWorkflowExecutionTimeSkipping and UpdateWorkflowExecutionOptions calls.
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
	// Time skipping reaches past the Client interface to PollWorkflowExecutionTimeSkipping,
	// which has no interface entry, so the concrete type is resolved once here rather
	// than asserted on every fast-forward. NewClientFromExisting always returns a
	// *WorkflowClient — it rejects any other implementation of c itself, and plugins
	// cannot substitute the instance — so this cannot fail.
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
// DisablePropagation and MaxSessionSkipCount over from the current config. The
// latter matters: dropping it silently replaces the caller's session cap with
// the server default, which is the bug the merged Python SDK shipped with.
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
	return t.client.pollWorkflowTimeSkipping(ctx, pollWorkflowTimeSkippingRequest{
		WorkflowID:    run.GetID(),
		FastForwardID: ffID,
	})
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
	// The typed API derives the field mask from which change fields are set,
	// so a partial update does not need one built by hand. RunId is left empty
	// so the server resolves the current run, letting a fast-forward follow a
	// chain across retry / continue-as-new / cron.
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
// While a schedule is idle its clock jumps to its next fire time, so a
// fast-forward runs the actions that fall inside the requested window
// back-to-back. The schedule's clock does not advance while it has work
// outstanding — it is paused or closed, has pending backfills, or has
// in-flight starts, cancels or terminates — so actions settle before time moves
// again.
//
// Duration is required. A workflow runs to a terminal state, so for workflows
// an absent duration means "skip to the end"; a schedule has no such end, and
// the server rejects time skipping on a schedule without a bounded
// fast-forward.
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
	ctx context.Context, handle ScheduleHandle,
) (*TimeSkippingInfo, error) {
	desc, err := handle.Describe(ctx)
	if err != nil {
		return nil, err
	}
	return desc.Info.TimeSkippingInfo, nil
}

// patchScheduleTimeSkippingConfig sets the schedule's time-skipping config.
// Time skipping is configured through SchedulePatch — the Schedule message has
// no field for it, so UpdateSchedule cannot carry it (and equally cannot clear
// it).
func (t *TimeSkipper) patchScheduleTimeSkippingConfig(
	ctx context.Context, scheduleID string, config TimeSkippingConfig,
) error {
	return t.client.ScheduleClient().GetHandle(ctx, scheduleID).
		SetTimeSkippingConfig(ctx, ScheduleSetTimeSkippingConfigOptions{Config: &config})
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
	ctx context.Context, handle ScheduleHandle, ffID string,
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

		// The server leaves FastForwardInfo in place after completion, so an
		// id alone proves nothing; it has to be matched against ours. An empty
		// id is the window before the patch has been applied.
		//
		// A differing id means a later fast-forward replaced ours, which only
		// happens if the caller drives two at once — unlikely for a test helper,
		// and their own bug if so. It is handled because it is the one reading
		// under which ours provably can never complete, not because it is
		// expected.
		var ffi *TimeSkippingFastForwardInfo
		if desc.Info.TimeSkippingInfo != nil {
			ffi = desc.Info.TimeSkippingInfo.FastForwardInfo
		}
		if ffi != nil {
			switch {
			case ffi.ID == ffID:
				if ffi.HasCompleted {
					return true, nil
				}
			case ffi.ID != "":
				return false, fmt.Errorf(
					"fast_forward %q can never complete: the schedule's current fast_forward is %q, "+
						"so this one was replaced by a later FastForwardSchedule call",
					ffID, ffi.ID)
			}
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
