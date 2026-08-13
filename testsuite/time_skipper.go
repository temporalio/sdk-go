package testsuite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	workflowpb "go.temporal.io/api/workflow/v1"
)

// TimeSkippingConfig is the per-workflow time-skipping (v2) configuration.
// Mirrors temporal.api.common.v1.TimeSkippingConfig.
//
// WARNING: Per-workflow time skipping (v2) is currently experimental.
type TimeSkippingConfig struct {
	// Enabled controls whether time skipping is on for this workflow.
	Enabled bool

	// FastForwardConfig is the one-shot fast-forward, if any. Nil means no
	// bounded FF (either unbounded skipping when Enabled or no skipping at all).
	FastForwardConfig *FastForwardConfig

	// DisablePropagation stops the enabled flag from propagating to child
	// workflow executions. Virtual start time still propagates.
	DisablePropagation bool

	// MaxSessionSkipCount caps the number of skips within one continuous
	// time-skipping session. Zero (proto3 default) means "use server default".
	MaxSessionSkipCount int32
}

// Validate checks internal consistency of the config.
func (c *TimeSkippingConfig) Validate() error {
	if !c.Enabled && c.FastForwardConfig != nil {
		return fmt.Errorf("FastForwardConfig cannot be set when Enabled is false")
	}
	return nil
}

// ToProto converts the SDK-level config to its proto counterpart. It performs
// no defaulting or ID generation — the fields are copied 1:1.
func (c *TimeSkippingConfig) ToProto() *commonpb.TimeSkippingConfig {
	if c == nil {
		return nil
	}
	proto := &commonpb.TimeSkippingConfig{
		Enabled:             c.Enabled,
		DisablePropagation:  c.DisablePropagation,
		MaxSessionSkipCount: c.MaxSessionSkipCount,
	}
	if c.FastForwardConfig != nil {
		proto.FastForwardConfig = c.FastForwardConfig.ToProto()
	}
	return proto
}

// FastForwardConfig is a one-shot fast-forward inside a TimeSkippingConfig.
// Both fields are required whenever a fast-forward is configured.
type FastForwardConfig struct {
	// ID identifies this fast-forward for PollWorkflowExecutionTimeSkipping.
	ID string

	// Duration advances the workflow's virtual time by this amount. Time
	// skipping auto-disables when the target is reached.
	Duration time.Duration
}

// ToProto maps to the proto counterpart.
func (c *FastForwardConfig) ToProto() *commonpb.FastForwardConfig {
	if c == nil {
		return nil
	}
	return &commonpb.FastForwardConfig{
		Id:       c.ID,
		Duration: durationpb.New(c.Duration),
	}
}

// FastForwardOption is a functional option passed to FastForward. The current
// only option is WithDuration; more may be added.
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
// WARNING: Per-workflow time skipping (v2) is currently experimental.
type TimeSkipper struct {
	config       TimeSkippingConfig
	client       client.Client
	namespace    string
	stampEnabled bool
}

// NewTimeSkipper wraps client so that all workflows started via the returned
// TimeSkipper.Client() are stamped with config. The namespace is required
// because it isn't exposed on the Client interface but is needed for our
// PollWorkflowExecutionTimeSkipping and UpdateWorkflowExecutionOptions calls.
func NewTimeSkipper(c client.Client, namespace string, config TimeSkippingConfig) (*TimeSkipper, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	ts := &TimeSkipper{config: config, namespace: namespace, stampEnabled: true}
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
func (t *TimeSkipper) Client() client.Client { return t.client }

// Config returns the configuration currently applied to future workflow starts.
func (t *TimeSkipper) Config() TimeSkippingConfig { return t.config }

// SetConfig replaces the configuration applied to future workflow starts.
// Does not affect workflows already running.
func (t *TimeSkipper) SetConfig(config TimeSkippingConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	t.config = config
	return nil
}

// WithTimeSkippingDisabled runs f with TimeSkippingConfig stamping suspended.
// Workflows started via TimeSkipper.Client during f do not receive a
// TimeSkippingConfig on their StartWorkflow request. Existing workflows are
// unaffected.
func (t *TimeSkipper) WithTimeSkippingDisabled(f func()) {
	prev := t.stampEnabled
	t.stampEnabled = false
	defer func() { t.stampEnabled = prev }()
	f()
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
	tsc := TimeSkippingConfig{
		Enabled:             true,
		FastForwardConfig:   &FastForwardConfig{ID: ffID, Duration: d},
		DisablePropagation:  t.config.DisablePropagation,
		MaxSessionSkipCount: t.config.MaxSessionSkipCount,
	}
	if _, err := t.updateTimeSkippingConfig(ctx, run, tsc); err != nil {
		return false, err
	}
	return t.pollFastForwardCompletion(ctx, run, ffID)
}

func (t *TimeSkipper) enableUnboundedAndWait(
	ctx context.Context, run client.WorkflowRun,
) (bool, error) {
	tsc := TimeSkippingConfig{
		Enabled:             true,
		DisablePropagation:  t.config.DisablePropagation,
		MaxSessionSkipCount: t.config.MaxSessionSkipCount,
	}
	if _, err := t.updateTimeSkippingConfig(ctx, run, tsc); err != nil {
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
	req := &workflowservice.PollWorkflowExecutionTimeSkippingRequest{
		Namespace:         t.namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: run.GetID()},
		FastForwardId:     ffID,
	}
	for {
		resp, err := t.client.WorkflowService().PollWorkflowExecutionTimeSkipping(ctx, req)
		if err != nil {
			return false, err
		}
		switch resp.GetFastForwardPollingResult() {
		case enumspb.FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_COMPLETED:
			return true, nil
		case enumspb.FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_FAILED:
			// The server explains the specific cause in failed_reason
			// (id mismatch, execution ended, config reset, TS disabled).
			// In this SDK, id-mismatch means another caller (or an SDK
			// bug) overrode the fast_forward; surface loudly. Other
			// causes are benign "couldn't complete" outcomes — return
			// (false, nil).
			if strings.Contains(resp.GetFailedReason(), "fast_forward_id") {
				return false, fmt.Errorf(
					"fast_forward %q was overridden by another fast_forward call (server: %s)",
					ffID, resp.GetFailedReason())
			}
			return false, nil
		case enumspb.FAST_FORWARD_POLLING_RESULT_POLL_TIMEOUT:
			// Server-side long-poll expiry; re-poll.
			continue
		default:
			return false, fmt.Errorf(
				"PollWorkflowExecutionTimeSkipping returned unknown result %v",
				resp.GetFastForwardPollingResult())
		}
	}
}

func (t *TimeSkipper) updateTimeSkippingConfig(
	ctx context.Context, run client.WorkflowRun, config TimeSkippingConfig,
) (*workflowservice.UpdateWorkflowExecutionOptionsResponse, error) {
	return t.client.WorkflowService().UpdateWorkflowExecutionOptions(ctx,
		&workflowservice.UpdateWorkflowExecutionOptionsRequest{
			Namespace: t.namespace,
			WorkflowExecution: &commonpb.WorkflowExecution{
				WorkflowId: run.GetID(),
			},
			WorkflowExecutionOptions: &workflowpb.WorkflowExecutionOptions{
				TimeSkippingConfig: config.ToProto(),
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"time_skipping_config"},
			},
			Identity: "",
		})
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
	if o.skipper.stampEnabled && in.Options != nil && in.Options.TimeSkippingConfig == nil {
		in.Options.TimeSkippingConfig = o.skipper.config.ToProto()
	}
	return o.Next.ExecuteWorkflow(ctx, in)
}
