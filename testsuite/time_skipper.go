package testsuite

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal"
)

// Time-skipping configuration types. These are the same types the client
// package exposes — defined once in internal so the client and testsuite APIs
// cannot drift, and so a config built for StartWorkflowOptions or
// ScheduleOptions can be handed straight to a TimeSkipper.
type (
	// TimeSkippingConfig is the time-skipping (v2) configuration for an
	// execution or a schedule.
	//
	// NOTE: Experimental
	TimeSkippingConfig = internal.TimeSkippingConfig

	// FastForwardConfig is a one-shot fast-forward inside a TimeSkippingConfig.
	//
	// NOTE: Experimental
	FastForwardConfig = internal.FastForwardConfig
)

// FastForwardOption is a functional option passed to FastForward. The current
// only option is WithDuration; more may be added.
//
// NOTE: Experimental
type FastForwardOption interface {
	applyFastForward(*fastForwardConfig)
}

// fastForwardConfig accumulates the options a caller passed. Internal.
type fastForwardConfig struct {
	duration *time.Duration
}

type fastForwardOptionFunc func(*fastForwardConfig)

func (f fastForwardOptionFunc) applyFastForward(c *fastForwardConfig) { f(c) }

// WithDuration makes a FastForward bounded to the given duration. Without it,
// FastForward enables unbounded time skipping and waits for the workflow to
// terminate.
//
// NOTE: Experimental
func WithDuration(d time.Duration) FastForwardOption {
	return fastForwardOptionFunc(func(c *fastForwardConfig) { c.duration = &d })
}

// TimeSkipper wraps a client with an interceptor that stamps a
// TimeSkippingConfig on every workflow started through TimeSkipper.Client.
// Callers can also FastForward a running workflow, read its virtual clock, or
// suspend stamping for a block.
//
// TimeSkipper is normally accessed through a v2 dev-server environment; see
// StartDevServerV2. It can be used directly to drive time skipping on an
// existing client.
//
// NOTE: Experimental
type TimeSkipper struct {
	client    client.Client
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
func NewTimeSkipper(c client.Client, namespace string, config TimeSkippingConfig) (*TimeSkipper, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	ts := &TimeSkipper{
		namespace:    namespace,
		config:       config,
		stampEnabled: true,
	}
	wrapped, err := client.NewClientFromExistingWithContext(context.Background(), c, client.Options{
		Interceptors: []interceptor.ClientInterceptor{&timeSkippingClientInterceptor{skipper: ts}},
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
func (t *TimeSkipper) Client() client.Client { return t.client }

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
	ctx context.Context, run client.WorkflowRun, opts ...FastForwardOption,
) (bool, error) {
	cfg := &fastForwardConfig{}
	for _, o := range opts {
		o.applyFastForward(cfg)
	}
	if cfg.duration == nil {
		return t.enableUnboundedAndWait(ctx, run)
	}
	return t.doBoundedFastForward(ctx, run, *cfg.duration)
}

// GetTimeSkippingInfo fetches the workflow's TimeSkippingInfo via
// DescribeWorkflowExecution. Returns (nil, nil) if the workflow has never had
// time skipping enabled (proto convention).
//
// NOTE: Experimental
func (t *TimeSkipper) GetTimeSkippingInfo(
	ctx context.Context, run client.WorkflowRun,
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
	ctx context.Context, run client.WorkflowRun,
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

func (t *TimeSkipper) doBoundedFastForward(
	ctx context.Context, run client.WorkflowRun, d time.Duration,
) (bool, error) {
	ffID := uuid.NewString()
	current := t.Config()
	tsc := TimeSkippingConfig{
		Enabled:             true,
		FastForwardConfig:   &FastForwardConfig{ID: ffID, Duration: d},
		DisablePropagation:  current.DisablePropagation,
		MaxSessionSkipCount: current.MaxSessionSkipCount,
	}
	if err := t.updateTimeSkippingConfig(ctx, run, tsc); err != nil {
		return false, err
	}
	return t.pollFastForwardCompletion(ctx, run, ffID)
}

func (t *TimeSkipper) enableUnboundedAndWait(
	ctx context.Context, run client.WorkflowRun,
) (bool, error) {
	current := t.Config()
	tsc := TimeSkippingConfig{
		Enabled:             true,
		DisablePropagation:  current.DisablePropagation,
		MaxSessionSkipCount: current.MaxSessionSkipCount,
	}
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
	ctx context.Context, run client.WorkflowRun, ffID string,
) (bool, error) {
	// RunId is left unset so the server resolves the current run, letting a
	// fast-forward follow a chain across retry / continue-as-new / cron.
	return t.client.PollWorkflowTimeSkipping(ctx, client.PollWorkflowTimeSkippingRequest{
		WorkflowID:    run.GetID(),
		FastForwardID: ffID,
	})
}

func (t *TimeSkipper) updateTimeSkippingConfig(
	ctx context.Context, run client.WorkflowRun, config TimeSkippingConfig,
) error {
	// The typed API derives the field mask from which change fields are set,
	// so a partial update does not need one built by hand. RunId is left empty
	// so the server resolves the current run, letting a fast-forward follow a
	// chain across retry / continue-as-new / cron.
	_, err := t.client.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: run.GetID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			TimeSkippingConfig: &client.TimeSkippingConfigChange{Value: &config},
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
	interceptor.InterceptorBase
	skipper *TimeSkipper
}

func (i *timeSkippingClientInterceptor) InterceptClient(
	next interceptor.ClientOutboundInterceptor,
) interceptor.ClientOutboundInterceptor {
	return &timeSkippingClientOutboundInterceptor{
		ClientOutboundInterceptorBase: interceptor.ClientOutboundInterceptorBase{Next: next},
		skipper:                       i.skipper,
	}
}

type timeSkippingClientOutboundInterceptor struct {
	interceptor.ClientOutboundInterceptorBase
	skipper *TimeSkipper
}

func (o *timeSkippingClientOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context, in *interceptor.ClientExecuteWorkflowInput,
) (client.WorkflowRun, error) {
	if in.Options != nil && in.Options.TimeSkippingConfig == nil {
		in.Options.TimeSkippingConfig = o.skipper.stampConfig()
	}
	return o.Next.ExecuteWorkflow(ctx, in)
}

func (o *timeSkippingClientOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context, in *interceptor.ClientSignalWithStartWorkflowInput,
) (client.WorkflowRun, error) {
	if in.Options != nil && in.Options.TimeSkippingConfig == nil {
		in.Options.TimeSkippingConfig = o.skipper.stampConfig()
	}
	return o.Next.SignalWithStartWorkflow(ctx, in)
}
