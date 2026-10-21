package internal

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
)

// FastForwardOptions configures a single fast-forward.
//
// Exposed as: [go.temporal.io/sdk/timeskipping.FastForwardOptions]
//
// NOTE: Experimental
type FastForwardOptions struct {
	// Duration to advance virtual time by.
	//
	// For a workflow, zero means unbounded: enable time skipping and wait for the
	// execution to end. For a schedule it is required — a schedule has no terminal
	// state to skip to, so the server rejects unbounded time skipping on one.
	Duration time.Duration
}

// TimeSkipper wraps a client with an interceptor that stamps a
// TimeSkippingConfig on every workflow started through TimeSkipper.Client.
// Callers can also FastForward a running workflow, read its virtual clock, or
// suspend stamping for a block.
//
// TimeSkipper is normally accessed through a v2 dev-server environment; see
// StartDevServerV2. It can be used directly to drive time skipping on an
// existing
//
// NOTE: Experimental
//
// Exposed as: [go.temporal.io/sdk/timeskipping.TimeSkipper]
type TimeSkipper struct {
	client    Client
	namespace string

	// mu guards config and stampEnabled, which callers may mutate from one
	// goroutine while workflows are being started from another.
	mu           sync.RWMutex
	config       TimeSkippingConfig
	stampEnabled bool
}

// NewTimeSkipper wraps client so that all workflows started via the returned
// TimeSkipper.Client() are stamped with config. The namespace is required
// because it isn't exposed on the Client interface but is needed for our
// PollWorkflowExecutionTimeSkipping and UpdateWorkflowExecutionOptions calls.
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
	ts.client = wrapped
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
// With WithDuration(d): bounded — sends a TimeSkippingConfig carrying a fresh
// FastForwardConfig and long-polls PollWorkflowExecutionTimeSkipping for
// completion. Returns (true, nil) on FF completion; (false, nil) when the
// workflow chain terminates or the fast-forward is superseded/reset/TS
// disabled before completion; (false, err) when the server reports that the
// fast_forward_id no longer matches (typically means another caller issued
// a superseding FastForward).
//
// Without WithDuration: unbounded — enables time skipping (no bounded FF) and
// waits for a terminal history event on the current run. Always returns
// (false, nil) after the workflow terminates.
//
// NOTE: Experimental
func (t *TimeSkipper) FastForward(
	ctx context.Context, run WorkflowRun, options FastForwardOptions,
) (bool, error) {
	if options.Duration == 0 {
		return t.enableUnboundedAndWait(ctx, run)
	}
	return t.doBoundedFastForward(ctx, run, options.Duration)
}

// GetTimeSkippingInfo fetches the workflow's TimeSkippingInfo via
// DescribeWorkflowExecution. Returns (nil, nil) if the workflow has never had
// time skipping enabled (proto convention).
//
// NOTE: Experimental
func (t *TimeSkipper) GetTimeSkippingInfo(
	ctx context.Context, run WorkflowRun,
) (*commonpb.TimeSkippingInfo, error) {
	desc, err := t.client.DescribeWorkflowExecution(ctx, run.GetID(), run.GetRunID())
	if err != nil {
		return nil, err
	}
	ext := desc.GetWorkflowExtendedInfo()
	if ext == nil {
		return nil, nil
	}
	return ext.GetTimeSkippingInfo(), nil
}

// GetCurrentTime returns the workflow's current virtual clock, read from
// TimeSkippingInfo.current_time via GetTimeSkippingInfo. If the workflow has
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
	if tsi == nil || tsi.GetCurrentTime() == nil {
		return time.Now().UTC(), nil
	}
	return tsi.GetCurrentTime().AsTime(), nil
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
	return t.pollFastForwardCompletion(ctx, run, ffID)
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

func (t *TimeSkipper) pollFastForwardCompletion(
	ctx context.Context, run WorkflowRun, ffID string,
) (bool, error) {
	// RunId is left unset so the server resolves the current run, letting a
	// fast-forward follow a chain across retry / continue-as-new / cron.
	wc, ok := t.client.(*WorkflowClient)
	if !ok {
		return false, errors.New("time skipping requires a client created by this SDK")
	}
	return wc.pollWorkflowTimeSkipping(ctx, pollWorkflowTimeSkippingRequest{
		WorkflowID:    run.GetID(),
		FastForwardID: ffID,
	})
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
