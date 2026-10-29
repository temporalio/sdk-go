package lambdaworker

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/worker"
)

// Only Permit is implemented; the slot suppliers under test don't use the other methods.
type fakeMarkUsedInfo struct {
	worker.SlotMarkUsedInfo
	permit *worker.SlotPermit
}

func (i fakeMarkUsedInfo) Permit() *worker.SlotPermit { return i.permit }

type fakeReleaseInfo struct {
	worker.SlotReleaseInfo
	permit *worker.SlotPermit
}

func (i fakeReleaseInfo) Permit() *worker.SlotPermit { return i.permit }

func reserveSlot(t *testing.T, s worker.SlotSupplier) *worker.SlotPermit {
	t.Helper()
	permit, err := s.ReserveSlot(context.Background(), nil)
	require.NoError(t, err)
	return permit
}

func markSlotUsed(s worker.SlotSupplier, permit *worker.SlotPermit) {
	s.MarkSlotUsed(fakeMarkUsedInfo{permit: permit})
}

func releaseSlot(s worker.SlotSupplier, permit *worker.SlotPermit) {
	s.ReleaseSlot(fakeReleaseInfo{permit: permit})
}

func newTestCountingTuner(t *testing.T) (worker.WorkerTuner, *taskCounter) {
	t.Helper()
	base, err := fixedSizeTunerFromLimits(worker.Options{})
	require.NoError(t, err)
	tuner, counter, err := newCountingTuner(base)
	require.NoError(t, err)
	return tuner, counter
}

func TestCountingSlotSupplier_CountsOnlyMarkedPermits(t *testing.T) {
	tuner, counter := newTestCountingTuner(t)
	s := tuner.GetActivityTaskSlotSupplier()

	unused := reserveSlot(t, s)
	used := reserveSlot(t, s)
	markSlotUsed(s, used)

	inHand, total := counter.snapshot()
	assert.Equal(t, 1, inHand)
	assert.Equal(t, uint64(1), total)

	releaseSlot(s, unused)
	inHand, total = counter.snapshot()
	assert.Equal(t, 1, inHand, "releasing an unused permit must not change the count")
	assert.Equal(t, uint64(1), total)

	releaseSlot(s, used)
	inHand, total = counter.snapshot()
	assert.Equal(t, 0, inHand)
	assert.Equal(t, uint64(1), total, "total tasks started never decreases")
}

func TestCountingTuner_CountsAllTaskKinds(t *testing.T) {
	tuner, counter := newTestCountingTuner(t)
	suppliers := []worker.SlotSupplier{
		tuner.GetWorkflowTaskSlotSupplier(),
		tuner.GetActivityTaskSlotSupplier(),
		tuner.GetLocalActivitySlotSupplier(),
		tuner.GetNexusSlotSupplier(),
		tuner.GetSessionActivitySlotSupplier(),
	}
	for _, s := range suppliers {
		require.NotNil(t, s)
		markSlotUsed(s, reserveSlot(t, s))
	}

	inHand, total := counter.snapshot()
	assert.Equal(t, len(suppliers), inHand)
	assert.Equal(t, uint64(len(suppliers)), total)
}

func TestCountingTuner_PreservesLimits(t *testing.T) {
	opts := worker.Options{}
	applyLambdaWorkerDefaults(&opts)
	base, err := fixedSizeTunerFromLimits(opts)
	require.NoError(t, err)
	tuner, _, err := newCountingTuner(base)
	require.NoError(t, err)

	assert.Equal(t, defaultMaxConcurrentWorkflowTaskExecutionSize, tuner.GetWorkflowTaskSlotSupplier().MaxSlots())
	assert.Equal(t, defaultMaxConcurrentActivityExecutionSize, tuner.GetActivityTaskSlotSupplier().MaxSlots())
	assert.Equal(t, defaultMaxConcurrentLocalActivityExecutionSize, tuner.GetLocalActivitySlotSupplier().MaxSlots())
	assert.Equal(t, defaultMaxConcurrentNexusTaskExecutionSize, tuner.GetNexusSlotSupplier().MaxSlots())
	assert.Equal(t, defaultMaxConcurrentActivityExecutionSize, tuner.GetSessionActivitySlotSupplier().MaxSlots())

	s := tuner.GetActivityTaskSlotSupplier()
	for range defaultMaxConcurrentActivityExecutionSize {
		require.NotNil(t, s.TryReserveSlot(nil))
	}
	assert.Nil(t, s.TryReserveSlot(nil), "the wrapper must not issue more slots than the inner supplier")
}

type sysInfoSlotSupplier struct {
	worker.SlotSupplier
	provider worker.SysInfoProvider
}

func (s sysInfoSlotSupplier) SysInfoProvider() worker.SysInfoProvider { return s.provider }

type fakeSysInfoProvider struct{ worker.SysInfoProvider }

func TestCountingTuner_PreservesSysInfoProvider(t *testing.T) {
	fixed, err := worker.NewFixedSizeSlotSupplier(1)
	require.NoError(t, err)
	provider := &fakeSysInfoProvider{}
	base, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier: sysInfoSlotSupplier{SlotSupplier: fixed, provider: provider},
		ActivitySlotSupplier: fixed,
	})
	require.NoError(t, err)

	tuner, _, err := newCountingTuner(base)
	require.NoError(t, err)

	sip, ok := tuner.GetWorkflowTaskSlotSupplier().(worker.HasSysInfoProvider)
	require.True(t, ok)
	assert.Same(t, provider, sip.SysInfoProvider())
	_, ok = tuner.GetActivityTaskSlotSupplier().(worker.HasSysInfoProvider)
	assert.False(t, ok)
	assert.Nil(t, tuner.GetNexusSlotSupplier(), "nil suppliers stay nil")
}

func TestUsesResourceBasedSupplier(t *testing.T) {
	fixedTuner, err := fixedSizeTunerFromLimits(worker.Options{})
	require.NoError(t, err)
	assert.False(t, usesResourceBasedSupplier(fixedTuner))

	fixed, err := worker.NewFixedSizeSlotSupplier(1)
	require.NoError(t, err)
	mixed, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier:        fixed,
		ActivitySlotSupplier:        fixed,
		LocalActivitySlotSupplier:   fixed,
		NexusSlotSupplier:           fixed,
		SessionActivitySlotSupplier: &worker.ResourceBasedSlotSupplier{},
	})
	require.NoError(t, err)
	assert.True(t, usesResourceBasedSupplier(mixed))
}

func TestWaitForIdle_ReturnsAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, counter := newTestCountingTuner(t)
		start := time.Now()
		assert.True(t, waitForIdle(context.Background(), counter, 10*time.Second))
		elapsed := time.Since(start)
		assert.GreaterOrEqual(t, elapsed, 10*time.Second)
		assert.Less(t, elapsed, 10*time.Second+2*idleCheckInterval)
	})
}

func TestWaitForIdle_WaitsForTaskInHand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tuner, counter := newTestCountingTuner(t)
		s := tuner.GetWorkflowTaskSlotSupplier()
		permit := reserveSlot(t, s)
		markSlotUsed(s, permit)
		go func() {
			time.Sleep(30 * time.Second)
			releaseSlot(s, permit)
		}()

		start := time.Now()
		assert.True(t, waitForIdle(context.Background(), counter, 10*time.Second))
		// The idle clock starts from the last check that saw the task in hand, which can be up to
		// one check interval before the release.
		assert.GreaterOrEqual(t, time.Since(start), 40*time.Second-idleCheckInterval)
	})
}

func TestWaitForIdle_TaskBetweenChecksRestartsClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tuner, counter := newTestCountingTuner(t)
		s := tuner.GetActivityTaskSlotSupplier()
		go func() {
			// Start and finish a task between two checks, so it is never seen in hand.
			time.Sleep(5*time.Second + idleCheckInterval/2)
			permit := reserveSlot(t, s)
			markSlotUsed(s, permit)
			releaseSlot(s, permit)
		}()

		start := time.Now()
		assert.True(t, waitForIdle(context.Background(), counter, 10*time.Second))
		assert.GreaterOrEqual(t, time.Since(start), 15*time.Second)
	})
}

func TestWaitForIdle_ReturnsFalseWhenContextDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, counter := newTestCountingTuner(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.False(t, waitForIdle(ctx, counter, 10*time.Second))
	})
}

func TestIdleThreshold(t *testing.T) {
	noJitter := func(time.Duration) time.Duration { return 0 }
	assert.Equal(t, 10*time.Second+idleCheckInterval, idleThreshold(10*time.Second, noJitter))

	var maxJitter time.Duration
	maxedJitter := func(n time.Duration) time.Duration {
		maxJitter = n
		return n - 1
	}
	threshold := idleThreshold(10*time.Second, maxedJitter)
	assert.Equal(t, 2*time.Second, maxJitter)
	assert.Equal(t, 10*time.Second+idleCheckInterval+maxJitter-1, threshold)
}
