package internal

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

type (
	PollScalerReportHandleSuite struct {
		suite.Suite
	}
)

func TestPollScalerReportHandleSuite(t *testing.T) {
	suite.Run(t, new(PollScalerReportHandleSuite))
}

type (
	ScalableTaskPollerSuite struct {
		suite.Suite
	}
)

func TestScalableTaskPollerSuite(t *testing.T) {
	suite.Run(t, new(ScalableTaskPollerSuite))
}

type testTask struct {
	psd pollerScaleDecision
}

// isEmpty implements taskForWorker.
func (t *testTask) isEmpty() bool {
	return false
}

// scaleDecision implements taskForWorker.
func (t *testTask) scaleDecision() (pollerScaleDecision, bool) {
	return t.psd, true
}

func newTestTask(delta int) *testTask {
	return &testTask{
		psd: pollerScaleDecision{
			pollRequestDeltaSuggestion: delta,
		},
	}
}

type emptyTask struct{}

func newEmptyTask() *emptyTask {
	return &emptyTask{}
}

// isEmpty implements taskForWorker.
func (t *emptyTask) isEmpty() bool {
	return true
}

// scaleDecision implements taskForWorker.
func (t *emptyTask) scaleDecision() (pollerScaleDecision, bool) {
	return pollerScaleDecision{}, false
}

func (s *PollScalerReportHandleSuite) TestErrorScaleDown() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.handleTask(newTestTask(0))
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), 4, targetSuggestion, "should suggest scaling down on resource exhausted error")
	// Non resource exhausted errors should scale down by 1
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), 3, targetSuggestion)
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), 2, targetSuggestion)
	// We should not scale down below minPollerCount
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), 2, targetSuggestion)
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), 2, targetSuggestion)
}

func (s *PollScalerReportHandleSuite) TestScaleDownOnEmptyTask() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.handleTask(newTestTask(0))
	ps.handleTask(newEmptyTask())
	assert.Equal(s.T(), 7, targetSuggestion)
}

func (s *PollScalerReportHandleSuite) TestScaleUpOnDelay() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.handleTask(newTestTask(10))
	assert.Equal(s.T(), 0, targetSuggestion)
	ps.newPeriod()
	ps.handleTask(newTestTask(100))
	// We should scale up to but not past the max poller count
	assert.Equal(s.T(), 10, targetSuggestion)

}

// TestSaturationScaleUp_FlatThroughput verifies that a saturated poller can
// scale up via the budget probe even when throughput is flat. Throughput growth
// from the new poller refills the budget for further scaling.
func (s *PollScalerReportHandleSuite) TestSaturationScaleUp_FlatThroughput() {
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 1,
		maxPollerCount:     10,
		minPollerCount:     1,
		scaleCallback:      func(int) {},
	})

	// Period 1: ingest 20 tasks → establishes baseline.
	for range 20 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // ingested=20, last=0 → throughputGrew=true → budget refilled

	// Period 2: flat throughput, saturated (no empty polls).
	for range 20 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // ingested=20, last=20 → throughputGrew=false, newly saturated → budget=1

	// Server sends +1. Throughput gate closed, but saturation budget allows it.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 2, int(ps.target.Load()),
		"saturation budget should allow +1 when poller is saturated")

	// Simulate the new poller helping: throughput grows next period.
	for range 25 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // ingested=25, last=20 → throughputGrew=true → budget refilled

	// Flat again with 2 pollers.
	for range 25 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // ingested=25, last=25 → flat, sustained saturation → budget kept

	// Another +1 allowed from the refilled budget.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 3, int(ps.target.Load()),
		"budget should be refilled after throughput growth confirms probe helped")
}

// TestSaturationScaleUp_ServerBottleneck verifies that when adding pollers
// doesn't help (server bottleneck), the budget is exhausted and further
// scale-ups are blocked.
func (s *PollScalerReportHandleSuite) TestSaturationScaleUp_ServerBottleneck() {
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 5,
		maxPollerCount:     20,
		minPollerCount:     1,
		scaleCallback:      func(int) {},
	})

	// Period 1: establish baseline.
	for range 50 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // throughputGrew (from 0) → budget = max(1, 5*0.2) = 1

	// Period 2: flat throughput, saturated → newly saturated, budget already set.
	for range 50 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod()

	// Use the budget: +1 → target=6.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 6, int(ps.target.Load()))

	// Period 3: throughput still flat (server bottleneck, new poller didn't help).
	for range 50 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // flat, sustained saturation, budget not refilled

	// Budget exhausted. +1 blocked.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 6, int(ps.target.Load()),
		"scale-up should be blocked after budget exhausted without throughput growth")
}

// TestSaturationScaleUp_LargeScaleUp verifies that the budget scales with
// poller count (20%) and resets on throughput growth, enabling gradual
// convergence for large scale-ups.
func (s *PollScalerReportHandleSuite) TestSaturationScaleUp_LargeScaleUp() {
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 10,
		maxPollerCount:     100,
		minPollerCount:     1,
		scaleCallback:      func(int) {},
	})

	// Period 1: establish baseline.
	for range 100 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // throughputGrew → budget = max(1, 10*0.2) = 2

	// Period 2: flat, newly saturated.
	for range 100 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod()

	// Budget = 2: can add 2 pollers.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 11, int(ps.target.Load()))
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 12, int(ps.target.Load()))

	// Budget exhausted.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 12, int(ps.target.Load()), "budget of 2 should be exhausted")

	// Throughput grows (the 2 new pollers helped).
	for range 120 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // ingested=120, last=100 → throughputGrew → budget = max(1, 12*0.2) = 2

	// Flat again.
	for range 120 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod()

	// Budget refilled: can add 2 more.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 13, int(ps.target.Load()))
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 14, int(ps.target.Load()))
}

// TestSaturationScaleUp_IdlePollers verifies that the saturation budget is
// cleared when pollers have spare capacity (empty polls), even if the budget
// was previously set.
func (s *PollScalerReportHandleSuite) TestSaturationScaleUp_IdlePollers() {
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 1,
		maxPollerCount:     10,
		minPollerCount:     1,
		scaleCallback:      func(int) {},
	})

	// Period 1: establish baseline (sets budget via throughputGrew).
	for range 20 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod()

	// Period 2: lower throughput, budget reduced from unlimited to bounded.
	for range 15 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod()

	// +1 arrives: budget is 1 (max(1, 1*0.2)), allows one probe.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 2, int(ps.target.Load()),
		"bounded budget should allow one probe scale-up")

	// Budget exhausted: next +1 blocked.
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 2, int(ps.target.Load()),
		"scale-up should be blocked when budget is exhausted")
}

// TestSaturationScaleUp_ThroughputPathUnchanged verifies that the existing
// throughput growth path is completely unmodified: when throughput grows,
// all +1 hints are applied without consuming any budget.
func (s *PollScalerReportHandleSuite) TestSaturationScaleUp_ThroughputPathUnchanged() {
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 5,
		maxPollerCount:     20,
		minPollerCount:     1,
		scaleCallback:      func(int) {},
	})

	// Period 1: baseline.
	for range 50 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod()

	// Period 2: throughput grew 20%.
	for range 60 {
		ps.handleTask(newTestTask(0))
	}
	ps.newPeriod() // 60 >= 50*1.1=55 → throughputGrew=true

	// Multiple +1 hints should all be applied (existing behavior, no consume).
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 6, int(ps.target.Load()))
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 7, int(ps.target.Load()))
	ps.handleTask(newTestTask(1))
	require.Equal(s.T(), 8, int(ps.target.Load()),
		"throughput growth path should apply all +1 hints without limit")
}

func (s *ScalableTaskPollerSuite) TestAutoscalingConcurrencyScalesUpToMaximum() {
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: 2,
		maximumNumberOfPollers: 3,
		minimumNumberOfPollers: 1,
	}

	blockingPoller := newSemaphoreProbeTaskPoller()
	poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior)
	poller.taskPollerType = "test"

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:     &testSlotSupplier{},
		maxTaskPerSecond: 1000,
		taskPollers:      []scalableTaskPoller{poller},
		taskProcessor:    noopTaskProcessor{},
		workerType:       "AutoscalingTest",
		logger:           ilog.NewNopLogger(),
		stopTimeout:      time.Second,
		metricsHandler:   metrics.NopHandler,
	})

	bw.Start()
	defer func() {
		allowBlockedPollers(blockingPoller, poller.pollerSemaphore)
		blockingPoller.Close()
		bw.Stop()
	}()

	eventuallySemaphoreState(s.T(), blockingPoller, poller.pollerSemaphore, 2, 2, "expected initial poller to start")

	require.Never(s.T(), func() bool {
		allowBlockedPollers(blockingPoller, poller.pollerSemaphore)
		permits, _ := readSemaphoreState(poller.pollerSemaphore)
		return permits > 2
	}, 200*time.Millisecond, 10*time.Millisecond, "should not exceed initial concurrency")

	poller.pollerAutoscalerReportHandle.updateTarget(func(int64) int64 { return 3 })

	eventuallySemaphoreState(s.T(), blockingPoller, poller.pollerSemaphore, 3, 3, "expected concurrency to scale up to maximum")

	require.Never(s.T(), func() bool {
		allowBlockedPollers(blockingPoller, poller.pollerSemaphore)
		permits, _ := readSemaphoreState(poller.pollerSemaphore)
		return permits > 3
	}, 200*time.Millisecond, 10*time.Millisecond, "should not exceed maximum concurrency")
}

func (s *ScalableTaskPollerSuite) TestAutoscalingScalesDownToMinimum() {
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: 2,
		maximumNumberOfPollers: 3,
		minimumNumberOfPollers: 1,
	}

	blockingPoller := newSemaphoreProbeTaskPoller()
	poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior)
	poller.taskPollerType = "test"

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:     &testSlotSupplier{},
		maxTaskPerSecond: 1000,
		taskPollers:      []scalableTaskPoller{poller},
		taskProcessor:    noopTaskProcessor{},
		workerType:       "AutoscalingTest",
		logger:           ilog.NewNopLogger(),
		stopTimeout:      time.Second,
		metricsHandler:   metrics.NopHandler,
	})

	bw.Start()
	defer func() {
		allowBlockedPollers(blockingPoller, poller.pollerSemaphore)
		blockingPoller.Close()
		bw.Stop()
	}()

	eventuallySemaphoreState(s.T(), blockingPoller, poller.pollerSemaphore, 2, 2, "expected initial concurrency")

	poller.pollerAutoscalerReportHandle.updateTarget(func(target int64) int64 { return 1 })

	eventuallySemaphoreState(s.T(), blockingPoller, poller.pollerSemaphore, 1, 1, "expected concurrency to reduce to minimum")

	require.Never(s.T(), func() bool {
		allowBlockedPollers(blockingPoller, poller.pollerSemaphore)
		permits, _ := readSemaphoreState(poller.pollerSemaphore)
		return permits == 0
	}, 200*time.Millisecond, 10*time.Millisecond, "should not scale below minimum")
}

type semaphoreProbeTaskPoller struct {
	signals chan struct{}
	closed  atomic.Bool
}

func newSemaphoreProbeTaskPoller() *semaphoreProbeTaskPoller {
	return &semaphoreProbeTaskPoller{
		signals: make(chan struct{}, 32),
	}
}

// PollTask implements taskPoller and blocks until a signal is provided so the semaphore permits stay acquired.
func (p *semaphoreProbeTaskPoller) PollTask() (taskForWorker, error) {
	_, ok := <-p.signals
	if !ok {
		return nil, nil
	}
	return nil, nil
}

// Cleanup implements taskPoller.
func (p *semaphoreProbeTaskPoller) Cleanup() error {
	p.Close()
	return nil
}

func (p *semaphoreProbeTaskPoller) Allow(n int) {
	for range n {
		for {
			if p.closed.Load() {
				return
			}
			select {
			case p.signals <- struct{}{}:
				goto next
			default:
				time.Sleep(1 * time.Millisecond)
			}
		}
	next:
	}
}

func (p *semaphoreProbeTaskPoller) Close() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.signals)
	}
}

func allowBlockedPollers(p *semaphoreProbeTaskPoller, sem *pollerSemaphore) {
	if p == nil || sem == nil {
		return
	}
	permits, _ := readSemaphoreState(sem)
	if permits > 0 {
		p.Allow(permits)
	}
}

func eventuallySemaphoreState(t *testing.T, blockingPoller *semaphoreProbeTaskPoller, sem *pollerSemaphore, expectedPermits, expectedMax int, msg string) {
	require.Eventually(t, func() bool {
		allowBlockedPollers(blockingPoller, sem)
		permits, max := readSemaphoreState(sem)
		return permits == expectedPermits && max == expectedMax
	}, time.Second, 10*time.Millisecond, msg)
}

func readSemaphoreState(ps *pollerSemaphore) (permits int, max int) {
	if ps == nil {
		return 0, 0
	}
	barrier := <-ps.bs
	permits = ps.permits
	max = ps.maxPermits
	ps.bs <- barrier
	return
}

type testSlotSupplier struct{}

func (s *testSlotSupplier) ReserveSlot(ctx context.Context, info SlotReservationInfo) (*SlotPermit, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return &SlotPermit{}, nil
}

func (s *testSlotSupplier) TryReserveSlot(SlotReservationInfo) *SlotPermit {
	return &SlotPermit{}
}

func (s *testSlotSupplier) MarkSlotUsed(SlotMarkUsedInfo) {}

func (s *testSlotSupplier) ReleaseSlot(SlotReleaseInfo) {}

func (s *testSlotSupplier) MaxSlots() int { return 0 }

type noopTaskProcessor struct{}

func (noopTaskProcessor) ProcessTask(any) error { return nil }
