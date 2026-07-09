package internal

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
)

// countingReserveInfo is a minimal SlotReservationInfo that records how many times
// NumIssuedSlots is called, so a test can observe that ReserveSlot has begun without a fixed sleep.
type countingReserveInfo struct {
	issued         int
	numIssuedCalls atomic.Int32
}

func (c *countingReserveInfo) TaskQueue() string { return "test-tq" }
func (c *countingReserveInfo) TaskQueueKind() enumspb.TaskQueueKind {
	return enumspb.TASK_QUEUE_KIND_NORMAL
}
func (c *countingReserveInfo) WorkerBuildId() string           { return "" }
func (c *countingReserveInfo) WorkerIdentity() string          { return "" }
func (c *countingReserveInfo) NumIssuedSlots() int             { c.numIssuedCalls.Add(1); return c.issued }
func (c *countingReserveInfo) Logger() log.Logger              { return ilog.NewNopLogger() }
func (c *countingReserveInfo) MetricsHandler() metrics.Handler { return metrics.NopHandler }

// TestTryReserveSlotDoesNotBlockDuringRampWait verifies that the non-blocking eager path
// (TryReserveSlot) is not stalled by a concurrent ReserveSlot that is waiting out the ramp throttle.
//
// Regression: ReserveSlot used to hold lastIssuedMu across its time.After(RampThrottle) wait, so a
// concurrent TryReserveSlot blocked on that mutex for up to RampThrottle.
func TestTryReserveSlotDoesNotBlockDuringRampWait(t *testing.T) {
	const ramp = 2 * time.Second

	rcOpts := DefaultResourceControllerOptions()
	rcOpts.MemTargetPercent = 0.8
	rcOpts.CpuTargetPercent = 0.9
	rcOpts.InfoSupplier = &FakeSystemInfoSupplier{memUse: 0.1, cpuUse: 0.1} // well under target => issue

	supplier, err := NewResourceBasedSlotSupplier(NewResourceController(rcOpts),
		ResourceBasedSlotSupplierOptions{MinSlots: 0, MaxSlots: 1000, RampThrottle: ramp})
	require.NoError(t, err)

	info := &countingReserveInfo{issued: 10} // >= MinSlots, so the throttle path is exercised

	// Seed lastSlotIssuedAt to "now" so the next reservation must wait out the ramp.
	require.NotNil(t, supplier.TryReserveSlot(info), "precondition: first eager reserve should succeed")
	callsBeforeReserve := info.numIssuedCalls.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reserveReturned := make(chan struct{})
	go func() {
		_, _ = supplier.ReserveSlot(ctx, info)
		close(reserveReturned)
	}()

	// Known race: ReserveSlot calls NumIssuedSlots() once, immediately before entering the ramp
	// wait. Wait for that call instead of sleeping a fixed duration.
	require.Eventually(t, func() bool {
		return info.numIssuedCalls.Load() > callsBeforeReserve
	}, ramp, time.Millisecond, "ReserveSlot did not start")

	// The eager, non-blocking check must return promptly, not wait out RampThrottle.
	start := time.Now()
	supplier.TryReserveSlot(info)
	elapsed := time.Since(start)
	require.Less(t, elapsed, ramp/4,
		"TryReserveSlot (eager, non-blocking) blocked for %v — it should not wait on ReserveSlot's ramp throttle", elapsed)

	cancel()
	<-reserveReturned
}

// TestRampThrottleStillBoundsIssuance guards the invariant the fix must preserve: even though
// ReserveSlot no longer holds lastIssuedMu across its wait, issuance beyond MinSlots is still
// gated to at most one slot per RampThrottle (enforced by TryReserveSlot's lastSlotIssuedAt check).
func TestRampThrottleStillBoundsIssuance(t *testing.T) {
	const ramp = 100 * time.Millisecond

	rcOpts := DefaultResourceControllerOptions()
	rcOpts.MemTargetPercent = 0.8
	rcOpts.CpuTargetPercent = 0.9
	rcOpts.InfoSupplier = &FakeSystemInfoSupplier{memUse: 0.1, cpuUse: 0.1}

	supplier, err := NewResourceBasedSlotSupplier(NewResourceController(rcOpts),
		ResourceBasedSlotSupplierOptions{MinSlots: 0, MaxSlots: 1000, RampThrottle: ramp})
	require.NoError(t, err)

	info := &countingReserveInfo{issued: 10} // >= MinSlots, so the throttle gate applies

	require.NotNil(t, supplier.TryReserveSlot(info), "first slot should issue")
	require.Nil(t, supplier.TryReserveSlot(info), "second slot within RampThrottle must be throttled")

	require.Eventually(t, func() bool {
		return supplier.TryReserveSlot(info) != nil
	}, ramp*5, ramp/10, "slot should issue again after RampThrottle elapses")
}
