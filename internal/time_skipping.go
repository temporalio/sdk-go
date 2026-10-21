package internal

import (
	"errors"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

type (
	// TimeSkippingConfig is the time-skipping (v2) configuration for an execution or a
	// schedule. It mirrors temporal.api.common.v1.TimeSkippingConfig.
	//
	// Time skipping is a testing facility and is not intended for production workloads.
	//
	// Exposed as: [go.temporal.io/sdk/client.TimeSkippingConfig], [go.temporal.io/sdk/timeskipping.TimeSkippingConfig]
	//
	// NOTE: Experimental
	TimeSkippingConfig struct {
		// Enabled controls whether time skipping is on.
		Enabled bool

		// FastForwardConfig is the one-shot fast-forward, if any. Nil means no bounded
		// fast-forward: for a workflow that is unbounded skipping when Enabled, or no
		// skipping at all when not. A schedule has no terminal state to skip to, so the
		// server requires a bounded fast-forward on one.
		FastForwardConfig *FastForwardConfig

		// DisablePropagation stops the enabled flag from propagating to executions started
		// by this one — a child workflow, or a workflow started by a schedule. Virtual
		// start time still propagates.
		DisablePropagation bool

		// MaxSessionSkipCount caps the number of skips within one time-skipping session,
		// which runs from one configuration update to the next. Reaching it disables time
		// skipping. Zero means use the server default.
		MaxSessionSkipCount int32
	}

	// FastForwardConfig is a one-shot fast-forward inside a [TimeSkippingConfig]. Both
	// fields are required whenever a fast-forward is configured.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.FastForwardConfig], [go.temporal.io/sdk/client.FastForwardConfig]
	//
	// NOTE: Experimental
	FastForwardConfig struct {
		// ID identifies this fast-forward when waiting for it to complete.
		ID string

		// Duration advances virtual time by this amount. Reaching the resulting target
		// time completes the fast-forward and disables time skipping.
		Duration time.Duration
	}
)

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
