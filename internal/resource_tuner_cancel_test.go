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

// reserveCallCounter counts NumIssuedSlots calls, so a test can observe that ReserveSlot has entered
// its retry loop without resorting to a fixed sleep.
type reserveCallCounter struct {
	calls atomic.Int64
}

func (c *reserveCallCounter) TaskQueue() string { return "test-tq" }
func (c *reserveCallCounter) TaskQueueKind() enumspb.TaskQueueKind {
	return enumspb.TASK_QUEUE_KIND_NORMAL
}
func (c *reserveCallCounter) WorkerBuildId() string           { return "" }
func (c *reserveCallCounter) WorkerIdentity() string          { return "" }
func (c *reserveCallCounter) NumIssuedSlots() int             { c.calls.Add(1); return 10 }
func (c *reserveCallCounter) Logger() log.Logger              { return ilog.NewNopLogger() }
func (c *reserveCallCounter) MetricsHandler() metrics.Handler { return metrics.NopHandler }

// TestReserveSlotRejectsAlreadyCancelledContext covers the MinSlots fast path, which returns a permit
// without consulting the controller. semaphore.Acquire, which FixedSizeSlotSupplier reserves through,
// fails a cancelled context even when it could acquire without blocking; this keeps the two suppliers
// consistent.
func TestReserveSlotRejectsAlreadyCancelledContext(t *testing.T) {
	rcOpts := DefaultResourceControllerOptions()
	rcOpts.InfoSupplier = &FakeSystemInfoSupplier{memUse: 0.1, cpuUse: 0.1}

	supplier, err := NewResourceBasedSlotSupplier(NewResourceController(rcOpts),
		ResourceBasedSlotSupplierOptions{MinSlots: 100, MaxSlots: 1000, RampThrottle: 0})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	permit, err := supplier.ReserveSlot(ctx, &reserveCallCounter{}) // NumIssuedSlots is 10, under MinSlots
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, permit)
}

// TestReserveSlotHonorsContextCancellation covers worker shutdown while the controller is declining
// slots. Both cases below skip the ramp wait, which used to be the only place the context was
// observed, leaving the retry loop with no cancellation point at all.
func TestReserveSlotHonorsContextCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ramp time.Duration
	}{
		// The default for workflow slot suppliers: the ramp block is skipped entirely.
		{"no ramp throttle", 0},
		// A ramp is configured, but the last issuance is old enough that no wait is required.
		{"ramp throttle already elapsed", 50 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rcOpts := DefaultResourceControllerOptions()
			rcOpts.MemTargetPercent = 0.8
			rcOpts.CpuTargetPercent = 0.9
			// Over the memory target, so the controller always declines and TryReserveSlot always
			// returns nil. This is the sustained-pressure case the tuner exists to handle.
			rcOpts.InfoSupplier = &FakeSystemInfoSupplier{memUse: 0.95, cpuUse: 0.95}

			supplier, err := NewResourceBasedSlotSupplier(NewResourceController(rcOpts),
				ResourceBasedSlotSupplierOptions{MinSlots: 0, MaxSlots: 1000, RampThrottle: tc.ramp})
			require.NoError(t, err)

			info := &reserveCallCounter{} // NumIssuedSlots is 10, past MinSlots
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			reserveErr := make(chan error, 1)
			go func() {
				_, err := supplier.ReserveSlot(ctx, info)
				reserveErr <- err
			}()

			// Each retry iteration calls NumIssuedSlots once. Waiting for a second call means the
			// loop is spinning, so cancelling now exercises the retry path rather than entry.
			require.Eventually(t, func() bool {
				return info.calls.Load() > 1
			}, 2*time.Second, time.Millisecond, "ReserveSlot did not enter its retry loop")

			cancel()

			select {
			case err := <-reserveErr:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(2 * time.Second):
				t.Fatal("ReserveSlot did not return after its context was cancelled")
			}
		})
	}
}
