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
// WorkerPollerInfo block and follows the target as it changes, while a fixed-size pool
// reports the count it was configured with.
//
// Registration goes through trackPollerTargets rather than being injected, so this also
// covers the path the running worker actually uses.
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
		nil,
	)
	const fixedPollers = 2
	fixed := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		NewPollerBehaviorSimpleMaximum(PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: fixedPollers}),
		metrics.PollerTypeNexusTask,
		&atomic.Bool{},
		nil,
	)
	bw := &baseWorker{pollerTargets: &pollerTargetTracker{}}
	bw.trackPollerTargets([]scalableTaskPoller{autoscaled, fixed})

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
	require.Equal(t, int32(fixedPollers), hb.GetNexusPollerInfo().GetTargetPollers(),
		"a fixed-size pool reports the count it was configured with")

	// The heartbeat follows the target as it halves on ResourceExhausted errors, and the
	// fixed pool stays put.
	resourceExhausted := serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, "")
	autoscaled.pollerAutoscaler.handleError(resourceExhausted)
	require.Equal(t, int32(6), heartbeat().GetActivityPollerInfo().GetTargetPollers())
	autoscaled.pollerAutoscaler.handleError(resourceExhausted)
	hb = heartbeat()
	require.Equal(t, int32(3), hb.GetActivityPollerInfo().GetTargetPollers())
	require.Equal(t, int32(fixedPollers), hb.GetNexusPollerInfo().GetTargetPollers())
}

// The local activity pool is fixed-size but has no poller type, and has no server-side
// poller to report: it must stay out of the heartbeat rather than claim one of the real
// poller types.
func TestHeartbeatOmitsLocalActivityPollerTarget(t *testing.T) {
	localActivity := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		NewPollerBehaviorSimpleMaximum(PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: 2}),
		"",
		nil,
		nil,
	)
	bw := &baseWorker{pollerTargets: &pollerTargetTracker{}}
	bw.trackPollerTargets([]scalableTaskPoller{localActivity})

	targets := make(map[string]int32)
	bw.collectPollerTargets(targets)
	require.Empty(t, targets)
}

// A fixed-size pool reports through the same tracker the autoscaling pools use, so a
// worker that never autoscales still shows up in a fleet-wide poller count.
func TestHeartbeatReportsFixedPollerTargets(t *testing.T) {
	fixed := func(pollerType string, maxPollers int) scalableTaskPoller {
		return newScalableTaskPoller(
			newBlockingProbeTaskPoller(),
			ilog.NewNopLogger(),
			NewPollerBehaviorSimpleMaximum(PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: maxPollers}),
			pollerType,
			&atomic.Bool{},
			nil,
		)
	}
	bw := &baseWorker{pollerTargets: &pollerTargetTracker{}}
	bw.trackPollerTargets([]scalableTaskPoller{
		fixed(metrics.PollerTypeWorkflowTask, 4),
		fixed(metrics.PollerTypeWorkflowStickyTask, 5),
		fixed(metrics.PollerTypeActivityTask, 6),
		fixed(metrics.PollerTypeNexusTask, 7),
	})

	targets := make(map[string]int32)
	bw.collectPollerTargets(targets)

	h := newHeartbeatMetricsHandler(metrics.NewCapturingHandler())
	hb := &workerpb.WorkerHeartbeat{}
	h.PopulateHeartbeat(hb, &populateHeartbeatOptions{
		pollTimeTracker: &pollTimeTracker{},
		pollerTargets:   targets,
	})

	require.Equal(t, int32(4), hb.GetWorkflowPollerInfo().GetTargetPollers())
	require.Equal(t, int32(5), hb.GetWorkflowStickyPollerInfo().GetTargetPollers())
	require.Equal(t, int32(6), hb.GetActivityPollerInfo().GetTargetPollers())
	require.Equal(t, int32(7), hb.GetNexusPollerInfo().GetTargetPollers())
}

// An autoscaling nexus pool reports a target even though the nexus poll request has no
// field to carry one -- the heartbeat covers every autoscaling pool, not only the pools
// fairness acts on.
func TestHeartbeatReportsNexusPollerTarget(t *testing.T) {
	serverSupportsAutoscaling := &atomic.Bool{}
	serverSupportsAutoscaling.Store(true)
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: 7,
		maximumNumberOfPollers: 100,
		minimumNumberOfPollers: 1,
	}
	nexus := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		behavior,
		metrics.PollerTypeNexusTask,
		serverSupportsAutoscaling,
		nil,
	)
	bw := &baseWorker{pollerTargets: &pollerTargetTracker{}}
	bw.trackPollerTargets([]scalableTaskPoller{nexus})

	targets := make(map[string]int32)
	bw.collectPollerTargets(targets)
	require.Equal(t, int32(7), targets[metrics.PollerTypeNexusTask])
}

// Session workers poll derived task queues with the activity poller type, so counting
// them would overwrite the worker's own activity target. A fixed-size session pool now has
// a target to contribute, so the exclusion has to keep holding.
func TestPollerTargetsExcludeSessionWorker(t *testing.T) {
	fixedActivityWorker := func(maxPollers int) *activityWorker {
		bw := &baseWorker{pollerTargets: &pollerTargetTracker{}}
		bw.trackPollerTargets([]scalableTaskPoller{newScalableTaskPoller(
			newBlockingProbeTaskPoller(),
			ilog.NewNopLogger(),
			NewPollerBehaviorSimpleMaximum(PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: maxPollers}),
			metrics.PollerTypeActivityTask,
			&atomic.Bool{},
			nil,
		)})
		return &activityWorker{worker: bw}
	}
	aw := &AggregatedWorker{
		activityWorker: fixedActivityWorker(3),
		sessionWorker: &sessionWorker{
			creationWorker: fixedActivityWorker(9),
			activityWorker: fixedActivityWorker(9),
		},
	}

	require.Equal(t, map[string]int32{metrics.PollerTypeActivityTask: 3}, aw.pollerTargets())
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
	require.Zero(t, hb.GetNexusPollerInfo().GetTargetPollers(),
		"a type absent from the map reports unknown")
}
