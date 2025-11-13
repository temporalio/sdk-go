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
