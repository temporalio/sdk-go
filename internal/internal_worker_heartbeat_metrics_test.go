package internal

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workerpb "go.temporal.io/api/worker/v1"

	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

// TestHeartbeatCapturesPollerTarget verifies the poller target gauge is captured per
// poller type and lands in the matching WorkerPollerInfo block of the heartbeat.
func TestHeartbeatCapturesPollerTarget(t *testing.T) {
	h := newHeartbeatMetricsHandler(metrics.NewCapturingHandler())

	setPoller := func(pollerType string, current, target float64) {
		ph := h.forPoller(pollerType)
		ph.Gauge(metrics.NumPoller).Update(current)
		ph.Gauge(metrics.PollerTarget).Update(target)
	}
	setPoller(metrics.PollerTypeWorkflowTask, 4, 10)
	setPoller(metrics.PollerTypeWorkflowStickyTask, 6, 12)
	setPoller(metrics.PollerTypeActivityTask, 23, 30)
	// Nexus is intentionally left unset to cover the "no autoscaler" case.

	hb := &workerpb.WorkerHeartbeat{}
	h.PopulateHeartbeat(hb, &populateHeartbeatOptions{pollTimeTracker: &pollTimeTracker{}})

	require.Equal(t, int32(4), hb.GetWorkflowPollerInfo().GetCurrentPollers())
	require.Equal(t, int32(10), hb.GetWorkflowPollerInfo().GetTargetPollers())
	require.Equal(t, int32(6), hb.GetWorkflowStickyPollerInfo().GetCurrentPollers())
	require.Equal(t, int32(12), hb.GetWorkflowStickyPollerInfo().GetTargetPollers())
	require.Equal(t, int32(23), hb.GetActivityPollerInfo().GetCurrentPollers())
	require.Equal(t, int32(30), hb.GetActivityPollerInfo().GetTargetPollers())
	require.Zero(t, hb.GetNexusPollerInfo().GetTargetPollers())
}

// TestHeartbeatPollerTargetFromAutoscaler verifies the real autoscaler wiring: the
// target it publishes is routed through the heartbeat handler and reported in
// subsequent heartbeats as it changes.
func TestHeartbeatPollerTargetFromAutoscaler(t *testing.T) {
	const initialPollers = 12
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: initialPollers,
		maximumNumberOfPollers: 100,
		minimumNumberOfPollers: 1,
	}
	serverSupportsAutoscaling := &atomic.Bool{}
	serverSupportsAutoscaling.Store(true)

	h := newHeartbeatMetricsHandler(metrics.NewCapturingHandler())
	poller := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		h,
		behavior,
		metrics.PollerTypeActivityTask,
		serverSupportsAutoscaling,
	)

	heartbeat := func() *workerpb.WorkerPollerInfo {
		hb := &workerpb.WorkerHeartbeat{}
		h.PopulateHeartbeat(hb, &populateHeartbeatOptions{
			activityPollerBehavior: behavior,
			pollTimeTracker:        &pollTimeTracker{},
		})
		return hb.GetActivityPollerInfo()
	}

	info := heartbeat()
	require.True(t, info.GetIsAutoscaling())
	require.Equal(t, int32(initialPollers), info.GetTargetPollers())

	// The heartbeat tracks the target as it halves on ResourceExhausted errors.
	resourceExhausted := serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, "")
	poller.pollerAutoscaler.handleError(resourceExhausted)
	require.Equal(t, int32(6), heartbeat().GetTargetPollers())
	poller.pollerAutoscaler.handleError(resourceExhausted)
	require.Equal(t, int32(3), heartbeat().GetTargetPollers())
}
