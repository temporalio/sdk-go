package internal

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
