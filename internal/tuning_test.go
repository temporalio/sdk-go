package internal

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"

	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

func TestSlotReserveInfoImpl_IsSticky(t *testing.T) {
	t.Run("sticky true", func(t *testing.T) {
		info := slotReserveInfoImpl{
			taskQueue:      "test-queue",
			workerBuildId:  "build1",
			workerIdentity: "worker1",
			issuedSlots:    &atomic.Int32{},
			logger:         ilog.NewDefaultLogger(),
			metrics:        metrics.NopHandler,
			sticky:         true,
		}
		assert.True(t, info.IsSticky())
	})

	t.Run("sticky false", func(t *testing.T) {
		info := slotReserveInfoImpl{
			taskQueue:      "test-queue",
			workerBuildId:  "build1",
			workerIdentity: "worker1",
			issuedSlots:    &atomic.Int32{},
			logger:         ilog.NewDefaultLogger(),
			metrics:        metrics.NopHandler,
			sticky:         false,
		}
		assert.False(t, info.IsSticky())
	})
}

// capturingSlotSupplier records the IsSticky() value from the most recent reservation call.
type capturingSlotSupplier struct {
	lastReserveIsSticky    atomic.Bool
	lastTryReserveIsSticky atomic.Bool
	reserveCalled          atomic.Bool
	tryReserveCalled       atomic.Bool
}

func (c *capturingSlotSupplier) ReserveSlot(_ context.Context, info SlotReservationInfo) (*SlotPermit, error) {
	c.lastReserveIsSticky.Store(info.IsSticky())
	c.reserveCalled.Store(true)
	return &SlotPermit{}, nil
}

func (c *capturingSlotSupplier) TryReserveSlot(info SlotReservationInfo) *SlotPermit {
	c.lastTryReserveIsSticky.Store(info.IsSticky())
	c.tryReserveCalled.Store(true)
	return &SlotPermit{}
}

func (c *capturingSlotSupplier) MarkSlotUsed(SlotMarkUsedInfo) {}
func (c *capturingSlotSupplier) ReleaseSlot(SlotReleaseInfo)   {}
func (c *capturingSlotSupplier) MaxSlots() int                 { return 100 }

func TestTrackingSlotSupplier_PropagatesIsSticky(t *testing.T) {
	capturing := &capturingSlotSupplier{}
	tss := newTrackingSlotSupplier(capturing, trackingSlotSupplierOptions{
		logger:         ilog.NewDefaultLogger(),
		metricsHandler: metrics.NopHandler,
	})

	t.Run("ReserveSlot with sticky true", func(t *testing.T) {
		data := &slotReservationData{taskQueue: "test-queue", isSticky: true}
		permit, err := tss.ReserveSlot(context.Background(), data)
		require.NoError(t, err)
		require.NotNil(t, permit)
		assert.True(t, capturing.reserveCalled.Load())
		assert.True(t, capturing.lastReserveIsSticky.Load(),
			"inner supplier should see IsSticky()=true")
	})

	t.Run("ReserveSlot with sticky false", func(t *testing.T) {
		data := &slotReservationData{taskQueue: "test-queue", isSticky: false}
		permit, err := tss.ReserveSlot(context.Background(), data)
		require.NoError(t, err)
		require.NotNil(t, permit)
		assert.False(t, capturing.lastReserveIsSticky.Load(),
			"inner supplier should see IsSticky()=false")
	})

	t.Run("TryReserveSlot with sticky true", func(t *testing.T) {
		data := &slotReservationData{taskQueue: "test-queue", isSticky: true}
		permit := tss.TryReserveSlot(data)
		require.NotNil(t, permit)
		assert.True(t, capturing.tryReserveCalled.Load())
		assert.True(t, capturing.lastTryReserveIsSticky.Load(),
			"inner supplier should see IsSticky()=true on TryReserveSlot")
	})

	t.Run("TryReserveSlot with sticky false", func(t *testing.T) {
		data := &slotReservationData{taskQueue: "test-queue", isSticky: false}
		permit := tss.TryReserveSlot(data)
		require.NotNil(t, permit)
		assert.False(t, capturing.lastTryReserveIsSticky.Load(),
			"inner supplier should see IsSticky()=false on TryReserveSlot")
	})
}

func TestSlotReservationData_IsStickyDefault(t *testing.T) {
	// By default, slotReservationData should have isSticky=false
	data := slotReservationData{taskQueue: "test-queue"}
	assert.False(t, data.isSticky, "default isSticky should be false")
}

func TestDecideNextPollKind_StickyMode(t *testing.T) {
	wtp := &workflowTaskPoller{mode: Sticky, stickyCacheSize: 10}
	assert.True(t, wtp.decideNextPollKind(), "Sticky mode should always return true")
	// Counters should not be touched for non-Mixed modes.
	assert.Equal(t, 0, wtp.pendingStickyPollCount)
	assert.Equal(t, 0, wtp.pendingRegularPollCount)
}

func TestDecideNextPollKind_NonStickyMode(t *testing.T) {
	wtp := &workflowTaskPoller{mode: NonSticky, stickyCacheSize: 10}
	assert.False(t, wtp.decideNextPollKind(), "NonSticky mode should always return false")
	assert.Equal(t, 0, wtp.pendingStickyPollCount)
	assert.Equal(t, 0, wtp.pendingRegularPollCount)
}

func TestDecideNextPollKind_MixedMode_Balancing(t *testing.T) {
	wtp := &workflowTaskPoller{mode: Mixed, stickyCacheSize: 10}

	// First decision: both counts are 0, sticky preferred when equal.
	isSticky := wtp.decideNextPollKind()
	assert.True(t, isSticky, "first decision should be sticky (counts equal)")
	assert.Equal(t, 1, wtp.pendingStickyPollCount)
	assert.Equal(t, 0, wtp.pendingRegularPollCount)

	// Second decision: sticky=1, regular=0, should pick regular to balance.
	isSticky = wtp.decideNextPollKind()
	assert.False(t, isSticky, "second decision should be regular (sticky > regular)")
	assert.Equal(t, 1, wtp.pendingStickyPollCount)
	assert.Equal(t, 1, wtp.pendingRegularPollCount)

	// Third decision: counts equal again, prefer sticky.
	isSticky = wtp.decideNextPollKind()
	assert.True(t, isSticky, "third decision should be sticky (counts equal)")
	assert.Equal(t, 2, wtp.pendingStickyPollCount)
	assert.Equal(t, 1, wtp.pendingRegularPollCount)
}

func TestDecideNextPollKind_MixedMode_StickyBacklog(t *testing.T) {
	wtp := &workflowTaskPoller{
		mode:            Mixed,
		stickyCacheSize: 10,
		stickyBacklog:   5,
		// Start with sticky > regular so that without backlog, regular would be preferred.
		pendingStickyPollCount:  3,
		pendingRegularPollCount: 1,
	}

	// Even though sticky count > regular count, backlog forces sticky.
	isSticky := wtp.decideNextPollKind()
	assert.True(t, isSticky, "should choose sticky when backlog > 0")
	assert.Equal(t, 4, wtp.pendingStickyPollCount)
	assert.Equal(t, 1, wtp.pendingRegularPollCount)
}

func TestDecideNextPollKind_MixedMode_DisabledStickyCache(t *testing.T) {
	wtp := &workflowTaskPoller{mode: Mixed, stickyCacheSize: 0}
	isSticky := wtp.decideNextPollKind()
	assert.False(t, isSticky, "should return false when sticky cache is disabled")
	assert.Equal(t, 0, wtp.pendingStickyPollCount)
	assert.Equal(t, 0, wtp.pendingRegularPollCount)
}

func TestUndoPollDecision_Mixed(t *testing.T) {
	wtp := &workflowTaskPoller{mode: Mixed, stickyCacheSize: 10}

	// Simulate decideNextPollKind then undo.
	isSticky := wtp.decideNextPollKind()
	assert.True(t, isSticky)
	assert.Equal(t, 1, wtp.pendingStickyPollCount)

	wtp.undoPollDecision(isSticky)
	assert.Equal(t, 0, wtp.pendingStickyPollCount, "counter should be back to 0 after undo")

	// Do the same for a regular decision.
	wtp.pendingStickyPollCount = 2
	wtp.pendingRegularPollCount = 0
	isSticky = wtp.decideNextPollKind()
	assert.False(t, isSticky, "should pick regular since sticky count is higher")
	assert.Equal(t, 1, wtp.pendingRegularPollCount)

	wtp.undoPollDecision(isSticky)
	assert.Equal(t, 0, wtp.pendingRegularPollCount, "regular counter should be back to 0 after undo")
}

func TestUndoPollDecision_NonMixed_Noop(t *testing.T) {
	wtp := &workflowTaskPoller{mode: Sticky, stickyCacheSize: 10}
	// Should not panic or change anything.
	wtp.undoPollDecision(true)
	assert.Equal(t, 0, wtp.pendingStickyPollCount)
	assert.Equal(t, 0, wtp.pendingRegularPollCount)
}

func TestGetNextPollRequest_WithHint_Sticky(t *testing.T) {
	wtp := &workflowTaskPoller{
		mode:            Mixed,
		stickyCacheSize: 10,
		taskQueueName:   "test-queue",
		stickyUUID:      "sticky-uuid-123",
		namespace:       "test-ns",
		identity:        "test-identity",
	}

	hint := &pollHint{isSticky: true}
	req := wtp.getNextPollRequest(hint)

	assert.Equal(t, getWorkerTaskQueue("sticky-uuid-123"), req.TaskQueue.GetName())
	assert.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, req.TaskQueue.GetKind())
	assert.Equal(t, "test-queue", req.TaskQueue.GetNormalName())

	// Counters should NOT be incremented by getNextPollRequest when a hint is provided.
	assert.Equal(t, 0, wtp.pendingStickyPollCount)
	assert.Equal(t, 0, wtp.pendingRegularPollCount)
}

func TestGetNextPollRequest_WithHint_Normal(t *testing.T) {
	wtp := &workflowTaskPoller{
		mode:            Mixed,
		stickyCacheSize: 10,
		taskQueueName:   "test-queue",
		stickyUUID:      "sticky-uuid-123",
		namespace:       "test-ns",
		identity:        "test-identity",
	}

	hint := &pollHint{isSticky: false}
	req := wtp.getNextPollRequest(hint)

	assert.Equal(t, "test-queue", req.TaskQueue.GetName())
	assert.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, req.TaskQueue.GetKind())

	// Counters should NOT be incremented by getNextPollRequest when a hint is provided.
	assert.Equal(t, 0, wtp.pendingStickyPollCount)
	assert.Equal(t, 0, wtp.pendingRegularPollCount)
}

func TestGetNextPollRequest_WithoutHint_FallsBackToInlineDecision(t *testing.T) {
	wtp := &workflowTaskPoller{
		mode:            Mixed,
		stickyCacheSize: 10,
		taskQueueName:   "test-queue",
		stickyUUID:      "sticky-uuid-123",
		namespace:       "test-ns",
		identity:        "test-identity",
	}

	// No hint: should use inline decision logic and increment counter.
	req := wtp.getNextPollRequest(nil)
	// First call with equal counts should pick sticky.
	assert.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, req.TaskQueue.GetKind())
	assert.Equal(t, 1, wtp.pendingStickyPollCount, "counter should be incremented inline")
}

func TestDecideAndPollHint_CounterLifecycle(t *testing.T) {
	// Simulate the full lifecycle: decide -> (reserve succeeds) -> poll -> release.
	wtp := &workflowTaskPoller{
		mode:            Mixed,
		stickyCacheSize: 10,
		taskQueueName:   "test-queue",
		stickyUUID:      "sticky-uuid-123",
		namespace:       "test-ns",
		identity:        "test-identity",
	}

	// Step 1: Pre-decide (increments counter).
	isSticky := wtp.decideNextPollKind()
	assert.True(t, isSticky)
	assert.Equal(t, 1, wtp.pendingStickyPollCount)

	// Step 2: getNextPollRequest with hint (does NOT increment counter).
	hint := &pollHint{isSticky: isSticky}
	req := wtp.getNextPollRequest(hint)
	assert.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, req.TaskQueue.GetKind())
	assert.Equal(t, 1, wtp.pendingStickyPollCount, "counter unchanged by getNextPollRequest with hint")

	// Step 3: release (decrements counter, as happens after poll).
	wtp.release(req.TaskQueue.GetKind())
	assert.Equal(t, 0, wtp.pendingStickyPollCount, "counter back to 0 after release")
}

func TestDecideAndCancel_CounterLifecycle(t *testing.T) {
	// Simulate: decide -> reservation fails -> undo.
	wtp := &workflowTaskPoller{
		mode:            Mixed,
		stickyCacheSize: 10,
		taskQueueName:   "test-queue",
	}

	isSticky := wtp.decideNextPollKind()
	assert.True(t, isSticky)
	assert.Equal(t, 1, wtp.pendingStickyPollCount)

	// Reservation failed, undo the decision.
	wtp.undoPollDecision(isSticky)
	assert.Equal(t, 0, wtp.pendingStickyPollCount, "counter should be back to 0 after undo")
}
