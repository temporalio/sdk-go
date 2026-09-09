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

// TestHeartbeatTracksAutoscalerTarget verifies the autoscaler target reaches the matching
// WorkerPollerInfo block, and follows the target as it changes. Poller types with a fixed
// count have no autoscaler and report zero.
func TestHeartbeatTracksAutoscalerTarget(t *testing.T) {
	const initialPollers = 12
	serverSupportsAutoscaling := &atomic.Bool{}
	serverSupportsAutoscaling.Store(true)
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: initialPollers,
		maximumNumberOfPollers: 100,
		minimumNumberOfPollers: 1,
	}
	autoscaled := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		behavior,
		metrics.PollerTypeActivityTask,
		serverSupportsAutoscaling,
	)
	fixed := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		NewPollerBehaviorSimpleMaximum(PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: 2}),
		metrics.PollerTypeNexusTask,
		&atomic.Bool{},
	)
	bw := &baseWorker{options: baseWorkerOptions{taskPollers: []scalableTaskPoller{autoscaled, fixed}}}

	h := newHeartbeatMetricsHandler(metrics.NewCapturingHandler())
	heartbeat := func() *workerpb.WorkerHeartbeat {
		targets := make(map[string]int32)
		bw.collectPollerTargets(targets)
		hb := &workerpb.WorkerHeartbeat{}
		h.PopulateHeartbeat(hb, &populateHeartbeatOptions{
			activityPollerBehavior: behavior,
			pollTimeTracker:        &pollTimeTracker{},
			pollerTargets:          targets,
		})
		return hb
	}

	hb := heartbeat()
	require.Equal(t, int32(initialPollers), hb.GetActivityPollerInfo().GetTargetPollers())
	require.Zero(t, hb.GetNexusPollerInfo().GetTargetPollers(),
		"the fixed-size poller has no autoscaler")

	// The heartbeat follows the target as it halves on ResourceExhausted errors.
	resourceExhausted := serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, "")
	autoscaled.pollerAutoscaler.handleError(resourceExhausted)
	require.Equal(t, int32(6), heartbeat().GetActivityPollerInfo().GetTargetPollers())
	autoscaled.pollerAutoscaler.handleError(resourceExhausted)
	require.Equal(t, int32(3), heartbeat().GetActivityPollerInfo().GetTargetPollers())
}

// TestHeartbeatReportsPollerTargetsPerType verifies each poller type reads its own target.
func TestHeartbeatReportsPollerTargetsPerType(t *testing.T) {
	h := newHeartbeatMetricsHandler(metrics.NewCapturingHandler())

	hb := &workerpb.WorkerHeartbeat{}
	h.PopulateHeartbeat(hb, &populateHeartbeatOptions{
		pollTimeTracker: &pollTimeTracker{},
		pollerTargets: map[string]int32{
			metrics.PollerTypeWorkflowTask:       10,
			metrics.PollerTypeWorkflowStickyTask: 12,
			metrics.PollerTypeActivityTask:       30,
		},
	})

	require.Equal(t, int32(10), hb.GetWorkflowPollerInfo().GetTargetPollers())
	require.Equal(t, int32(12), hb.GetWorkflowStickyPollerInfo().GetTargetPollers())
	require.Equal(t, int32(30), hb.GetActivityPollerInfo().GetTargetPollers())
	require.Zero(t, hb.GetNexusPollerInfo().GetTargetPollers())
}
