package internal

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workerpb "go.temporal.io/api/worker/v1"

	"go.temporal.io/sdk/internal/common/metrics"
)

// TestPollerAutoscalerDrainDecisions verifies the autoscaler counts scale decisions and that
// draining returns the counts since the previous drain (i.e. per-interval, not cumulative).
func TestPollerAutoscalerDrainDecisions(t *testing.T) {
	serverSupportsAutoscaling := &atomic.Bool{}
	serverSupportsAutoscaling.Store(true)
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount:        8,
		maxPollerCount:            100,
		minPollerCount:            1,
		serverSupportsAutoscaling: serverSupportsAutoscaling,
	})

	// scaleUpAllowed starts closed, so server-requested scale-ups are suppressed (dropped).
	ps.handleTask(newTestTask(1))
	ps.handleTask(newTestTask(1))
	// ResourceExhausted halves the target (8 -> 4): re_halve, no clamp.
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))

	// First drain returns everything recorded so far.
	first := ps.drainDecisions()
	assert.Equal(t, int64(2), first[pollerScaleServerUpSuppressed], "two suppressed server scale-ups")
	assert.Equal(t, int64(1), first[pollerScaleReHalve], "one re-halve on ResourceExhausted")
	_, ok := first[pollerScaleServerDown]
	assert.False(t, ok, "server_down should not appear when it never fired")

	// Draining again with no new decisions is empty (counts were reset).
	assert.Empty(t, ps.drainDecisions())

	// A new suppressed scale-up shows as a delta of 1, not the cumulative total.
	ps.handleTask(newTestTask(1))
	third := ps.drainDecisions()
	assert.Equal(t, int64(1), third[pollerScaleServerUpSuppressed])
	_, ok = third[pollerScaleReHalve]
	assert.False(t, ok, "re_halve had no new decisions this interval")
}

// TestScaleDecisionsHeartbeat verifies the per-poller-type decision breakdown is written onto
// the WorkerHeartbeat's poller info.
func TestScaleDecisionsHeartbeat(t *testing.T) {
	hm := newHeartbeatMetricsHandler(metrics.NopHandler)
	hb := &workerpb.WorkerHeartbeat{}
	hm.PopulateHeartbeat(hb, &populateHeartbeatOptions{
		pollTimeTracker: &pollTimeTracker{},
		scaleDecisionsByPollerType: map[string]map[string]int64{
			metrics.PollerTypeActivityTask: {
				pollerScaleServerUpSuppressed: 2,
				pollerScaleReHalve:            1,
			},
		},
	})

	decisions := hb.ActivityPollerInfo.GetScaleDecisions()
	assert.Equal(t, int64(2), decisions[pollerScaleServerUpSuppressed])
	assert.Equal(t, int64(1), decisions[pollerScaleReHalve])
	// Poller types with no decisions have an empty breakdown.
	assert.Empty(t, hb.WorkflowPollerInfo.GetScaleDecisions())
	assert.Empty(t, hb.NexusPollerInfo.GetScaleDecisions())
}
