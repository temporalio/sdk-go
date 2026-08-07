package internal

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
)

// countingReserveInfo counts NumIssuedSlots calls so a test can observe that ReserveSlot has begun
// without resorting to a fixed sleep.
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

// TestTryReserveSlotDoesNotBlockDuringRampWait covers the regression this fix addresses: ReserveSlot
// used to hold lastIssuedMu across its ramp wait, so the eager, non-blocking TryReserveSlot could
// block on that mutex for up to RampThrottle.
func TestTryReserveSlotDoesNotBlockDuringRampWait(t *testing.T) {
	const ramp = 2 * time.Second

	rcOpts := DefaultResourceControllerOptions()
	rcOpts.MemTargetPercent = 0.8
	rcOpts.CpuTargetPercent = 0.9
	// Usage well under target, so the controller always grants and RampThrottle is the only gate.
	rcOpts.InfoSupplier = &FakeSystemInfoSupplier{memUse: 0.1, cpuUse: 0.1}

	supplier, err := NewResourceBasedSlotSupplier(NewResourceController(rcOpts),
		ResourceBasedSlotSupplierOptions{MinSlots: 0, MaxSlots: 1000, RampThrottle: ramp})
	require.NoError(t, err)

	info := &countingReserveInfo{issued: 10} // past MinSlots, so the throttle applies

	// Stamps lastSlotIssuedAt, so the ReserveSlot below has a full ramp to wait out.
	require.NotNil(t, supplier.TryReserveSlot(info), "first eager reserve should succeed")
	callsBeforeReserve := info.numIssuedCalls.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reserveReturned := make(chan struct{})
	go func() {
		_, _ = supplier.ReserveSlot(ctx, info)
		close(reserveReturned)
	}()

	// ReserveSlot calls NumIssuedSlots once, just before entering the ramp wait. Syncing on that
	// beats sleeping a fixed duration.
	require.Eventually(t, func() bool {
		return info.numIssuedCalls.Load() > callsBeforeReserve
	}, ramp, time.Millisecond, "ReserveSlot did not start")

	start := time.Now()
	supplier.TryReserveSlot(info)
	elapsed := time.Since(start)
	require.Less(t, elapsed, ramp/4,
		"eager TryReserveSlot blocked for %v; it should not wait on ReserveSlot's ramp throttle", elapsed)

	cancel()
	<-reserveReturned
}

// TestRampThrottleBoundsIssuanceAcrossConcurrentReserveSlot covers what the sequential test below
// cannot: several ReserveSlot callers contending at once, each having independently decided it need
// not wait. Issuance must still be spaced by RampThrottle.
func TestRampThrottleBoundsIssuanceAcrossConcurrentReserveSlot(t *testing.T) {
	const ramp = 100 * time.Millisecond
	const reservers = 4

	rcOpts := DefaultResourceControllerOptions()
	rcOpts.MemTargetPercent = 0.8
	rcOpts.CpuTargetPercent = 0.9
	rcOpts.InfoSupplier = &FakeSystemInfoSupplier{memUse: 0.1, cpuUse: 0.1}

	supplier, err := NewResourceBasedSlotSupplier(NewResourceController(rcOpts),
		ResourceBasedSlotSupplierOptions{MinSlots: 0, MaxSlots: 1000, RampThrottle: ramp})
	require.NoError(t, err)

	// Past MinSlots so the throttle applies, and lastSlotIssuedAt is still the zero time, so every
	// goroutine computes a non-positive wait and goes straight for a slot.
	info := &countingReserveInfo{issued: 10}

	var mu sync.Mutex
	issuedAt := make([]time.Time, 0, reservers)
	var reserveErrs []error

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < reservers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			permit, err := supplier.ReserveSlot(context.Background(), info)
			at := time.Now()

			mu.Lock()
			defer mu.Unlock()
			if err != nil || permit == nil {
				reserveErrs = append(reserveErrs, err)
				return
			}
			issuedAt = append(issuedAt, at)
		}()
	}
	close(start)
	wg.Wait()

	require.Empty(t, reserveErrs)
	require.Len(t, issuedAt, reservers)

	sort.Slice(issuedAt, func(i, j int) bool { return issuedAt[i].Before(issuedAt[j]) })
	for i := 1; i < len(issuedAt); i++ {
		gap := issuedAt[i].Sub(issuedAt[i-1])
		t.Logf("slots %d and %d issued %v apart", i-1, i, gap)
		// Half a ramp absorbs scheduling noise and still catches the failure mode, which issues
		// slots microseconds apart.
		require.Greater(t, gap, ramp/2, "RampThrottle of %v was not applied between slots %d and %d",
			ramp, i-1, i)
	}
}

// TestRampThrottleStillBoundsIssuance guards the invariant the fix must preserve: dropping
// lastIssuedMu before the ramp wait must not let issuance past MinSlots exceed one slot per
// RampThrottle.
func TestRampThrottleStillBoundsIssuance(t *testing.T) {
	const ramp = 100 * time.Millisecond

	rcOpts := DefaultResourceControllerOptions()
	rcOpts.MemTargetPercent = 0.8
	rcOpts.CpuTargetPercent = 0.9
	rcOpts.InfoSupplier = &FakeSystemInfoSupplier{memUse: 0.1, cpuUse: 0.1}

	supplier, err := NewResourceBasedSlotSupplier(NewResourceController(rcOpts),
		ResourceBasedSlotSupplierOptions{MinSlots: 0, MaxSlots: 1000, RampThrottle: ramp})
	require.NoError(t, err)

	info := &countingReserveInfo{issued: 10} // past MinSlots, so the throttle applies

	require.NotNil(t, supplier.TryReserveSlot(info), "first slot should issue")
	require.Nil(t, supplier.TryReserveSlot(info), "second slot within RampThrottle must be throttled")

	require.Eventually(t, func() bool {
		return supplier.TryReserveSlot(info) != nil
	}, ramp*5, ramp/10, "slot should issue again after RampThrottle elapses")
}
