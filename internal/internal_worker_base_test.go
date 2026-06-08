package internal

import (
	"context"
	"sync"
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

func (s *ScalableTaskPollerSuite) TestNewScalableTaskPollerSetsTaskPollerType() {
	behavior := NewPollerBehaviorSimpleMaximum(
		PollerBehaviorSimpleMaximumOptions{
			MaximumNumberOfPollers: 1,
		},
	)

	blockingPoller := newSemaphoreProbeTaskPoller()
	poller := newScalableTaskPoller(
		blockingPoller,
		ilog.NewNopLogger(),
		behavior,
		metrics.PollerTypeWorkflowStickyTask,
		&atomic.Bool{},
	)

	s.Equal(metrics.PollerTypeWorkflowStickyTask, poller.taskPollerType)
}
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
	poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior, "", nil)
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
	poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior, "", nil)

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

// TestAutoscalingHalvesOnResourceExhausted verifies that a ResourceExhausted poll
// error HALVES the poller target (t/2) - as opposed to the -1 decrement applied to
// other errors - and that the halved value reaches the live pollerSemaphore via the
// scaleCallback wiring. Asserting the exact step sequence (16 -> 8 -> 4, then 4 -> 3
// for a non-RE error, then 3 -> 1) is what distinguishes halving from a plain
// scale-down: a -1 decrement would yield 15, 7, etc., failing these assertions.
//
// PollScalerReportHandleSuite.TestErrorScaleDown covers the same arithmetic against
// a mock scaleCallback; this test additionally proves the real semaphore is resized.
func (s *ScalableTaskPollerSuite) TestAutoscalingHalvesOnResourceExhausted() {
	const initialPollers = 16
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: initialPollers,
		maximumNumberOfPollers: 100,
		minimumNumberOfPollers: 1,
	}

	// The server advertising poller autoscaling is what enables error-driven
	// scale-down (the everSawScalingDecision || serverSupportsAutoscaling guard
	// in handleError). Simulate that here.
	serverSupportsAutoscaling := &atomic.Bool{}
	serverSupportsAutoscaling.Store(true)

	poller := newScalableTaskPoller(&semaphoreProbeTaskPoller{}, ilog.NewNopLogger(), behavior, serverSupportsAutoscaling)
	handle := poller.pollerAutoscalerReportHandle

	resourceExhausted := serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, "injected resource exhausted")
	otherError := serviceerror.NewInternal("some other failure")

	assertMaxPermits := func(want int) {
		_, max := readSemaphoreState(poller.pollerSemaphore)
		require.Equal(s.T(), want, max)
	}

	assertMaxPermits(initialPollers) // 16, before any error

	handle.handleError(resourceExhausted)
	assertMaxPermits(8) // 16 / 2 (halve)

	handle.handleError(resourceExhausted)
	assertMaxPermits(4) // 8 / 2 (halve)

	// A non-ResourceExhausted error only decrements: 4 -> 3, NOT 4 / 2 == 2.
	// This is the assertion that proves ResourceExhausted specifically halves.
	handle.handleError(otherError)
	assertMaxPermits(3)

	handle.handleError(resourceExhausted)
	assertMaxPermits(1) // 3 / 2 == 1, clamped at the configured minimum

	handle.handleError(resourceExhausted)
	assertMaxPermits(1) // already at minimum, stays clamped
}

type semaphoreProbeTaskPoller struct {
	signals chan struct{}
	done    chan struct{}
	closed  atomic.Bool
}

func newSemaphoreProbeTaskPoller() *semaphoreProbeTaskPoller {
	return &semaphoreProbeTaskPoller{
		signals: make(chan struct{}, 32),
		done:    make(chan struct{}),
	}
}

// PollTask implements taskPoller and blocks until a signal is provided so the semaphore permits stay acquired.
func (p *semaphoreProbeTaskPoller) PollTask() (taskForWorker, error) {
	select {
	case <-p.signals:
		return nil, nil
	case <-p.done:
		return nil, nil
	}
}

func (p *semaphoreProbeTaskPoller) Allow(n int) {
	for range n {
		select {
		case p.signals <- struct{}{}:
		case <-p.done:
			return
		}
	}
}

func (p *semaphoreProbeTaskPoller) Close() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.done)
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

// TestTaskNotDroppedDuringShutdown verifies the two-stage shutdown: when a
// poller receives a task during shutdown, the task is still dispatched and
// processed rather than silently dropped.
func TestTaskNotDroppedDuringShutdown(t *testing.T) {
	taskProcessed := make(chan struct{}, 1)
	pollStarted := make(chan struct{})

	// A poller that blocks until returnTask is closed, then returns a task
	// exactly once. Subsequent polls return nil so the poller can exit.
	tp := &shutdownTaskPoller{
		pollStarted: pollStarted,
		returnTask:  make(chan struct{}),
		task:        &testTask{},
	}

	processor := &recordingTaskProcessor{
		processed: taskProcessed,
	}
	workerPollCompleteOnShutdown := &atomic.Bool{}
	workerPollCompleteOnShutdown.Store(true)

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:     &testSlotSupplier{},
		maxTaskPerSecond: 1000,
		taskPollers: []scalableTaskPoller{
			{taskPollerType: "test", pollerCount: 1, taskPoller: tp},
		},
		taskProcessor:                processor,
		workerType:                   "ShutdownTest",
		logger:                       ilog.NewNopLogger(),
		stopTimeout:                  5 * time.Second,
		metricsHandler:               metrics.NopHandler,
		workerPollCompleteOnShutdown: workerPollCompleteOnShutdown,
	})

	bw.Start()

	// Wait for the poller to be actively polling.
	<-pollStarted

	// AggregatedWorker.Stop sets noRepoll before stopping base workers.
	bw.noRepoll.Store(true)

	// Stop exercises the base worker shutdown path: close(stopCh),
	// poll-side limiter cancellation, and awaitWaitGroup.
	stopDone := make(chan struct{})
	go func() {
		bw.Stop()
		close(stopDone)
	}()

	<-bw.stopCh

	// Release the poller after shutdown has started so the task is polled
	// during shutdown. The poller returns a task and then nil on subsequent
	// polls, allowing it to exit via noRepoll/stopCh during Stop().
	close(tp.returnTask)

	select {
	case <-taskProcessed:
		// Success: the task was dispatched and processed during shutdown
	case <-time.After(5 * time.Second):
		t.Fatal("task polled during shutdown was not processed (dropped)")
	}

	select {
	case <-stopDone:
		// Stop completed cleanly
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return in time")
	}
}

// effectivelyBlockedDispatchRate lets the first task consume the limiter's
// initial token while making the next task wait until the test cancels the
// limiter context.
const effectivelyBlockedDispatchRate = 0.001

func TestTaskDrainDuringShutdownRespectsDispatchRate(t *testing.T) {
	taskProcessed := make(chan struct{}, 2)
	workerPollCompleteOnShutdown := &atomic.Bool{}
	workerPollCompleteOnShutdown.Store(true)

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:                 &testSlotSupplier{},
		maxTaskPerSecond:             effectivelyBlockedDispatchRate,
		taskPollers:                  []scalableTaskPoller{},
		taskProcessor:                &recordingTaskProcessor{processed: taskProcessed},
		workerType:                   "ShutdownRateLimitTest",
		logger:                       ilog.NewNopLogger(),
		stopTimeout:                  5 * time.Second,
		metricsHandler:               metrics.NopHandler,
		workerPollCompleteOnShutdown: workerPollCompleteOnShutdown,
	})

	bw.stopWG.Add(1)
	go bw.runTaskDispatcher()
	bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}

	select {
	case <-taskProcessed:
	case <-time.After(time.Second):
		t.Fatal("first task polled during shutdown was not processed")
	}

	secondSent := make(chan struct{})
	go func() {
		bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}
		close(secondSent)
	}()
	select {
	case <-secondSent:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not receive the second task")
	}

	select {
	case <-taskProcessed:
		t.Fatal("second task should not bypass the dispatch rate during shutdown drain")
	case <-time.After(200 * time.Millisecond):
	}

	bw.taskLimiterContextCancel()
	close(bw.taskQueueCh)

	require.True(t, awaitWaitGroup(&bw.stopWG, time.Second),
		"dispatcher and processed tasks should finish after taskQueueCh closes")
}

func TestTaskDrainAfterDispatchLimiterCancelReleasesUnprocessedTasks(t *testing.T) {
	taskProcessed := make(chan struct{}, 3)
	workerPollCompleteOnShutdown := &atomic.Bool{}
	workerPollCompleteOnShutdown.Store(true)
	slotSupplier := &CountingSlotSupplier{}

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:                 slotSupplier,
		maxTaskPerSecond:             effectivelyBlockedDispatchRate,
		taskPollers:                  []scalableTaskPoller{},
		taskProcessor:                &recordingTaskProcessor{processed: taskProcessed},
		workerType:                   "ShutdownTimeoutDrainTest",
		logger:                       ilog.NewNopLogger(),
		stopTimeout:                  5 * time.Second,
		metricsHandler:               metrics.NopHandler,
		workerPollCompleteOnShutdown: workerPollCompleteOnShutdown,
	})

	bw.stopWG.Add(1)
	go bw.runTaskDispatcher()
	bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}

	select {
	case <-taskProcessed:
	case <-time.After(time.Second):
		t.Fatal("first task polled during shutdown was not processed")
	}

	secondSent := make(chan struct{})
	go func() {
		bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}
		close(secondSent)
	}()
	select {
	case <-secondSent:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not receive the second task")
	}

	thirdSent := make(chan struct{})
	go func() {
		bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}
		close(thirdSent)
	}()

	bw.taskLimiterContextCancel()
	select {
	case <-thirdSent:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not keep receiving after dispatch limiter cancellation")
	}
	close(bw.taskQueueCh)

	require.True(t, awaitWaitGroup(&bw.stopWG, time.Second),
		"dispatcher should keep receiving and releasing tasks after dispatch limiter cancellation so pollers can exit")
	require.Empty(t, taskProcessed,
		"only the task dispatched before dispatch limiter cancellation should be processed")
	require.Equal(t, int32(1), slotSupplier.uses.Load(),
		"only the processed task should mark its slot used")
	require.Equal(t, int32(3), slotSupplier.releases.Load(),
		"processed and discarded tasks should all release their slots")
}

func TestTaskNotProcessedDuringLegacyShutdown(t *testing.T) {
	tests := []struct {
		name                         string
		workerPollCompleteOnShutdown *atomic.Bool
	}{
		{
			name: "nil capability",
		},
		{
			name:                         "false capability",
			workerPollCompleteOnShutdown: &atomic.Bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskProcessed := make(chan struct{}, 1)
			pollStarted := make(chan struct{})

			// This poller simulates a poll returning a task after shutdown has
			// already started. Legacy shutdown should not dispatch that task.
			tp := &shutdownTaskPoller{
				pollStarted: pollStarted,
				returnTask:  make(chan struct{}),
				task:        &testTask{},
			}

			bw := newBaseWorker(baseWorkerOptions{
				slotSupplier:     &testSlotSupplier{},
				maxTaskPerSecond: 1000,
				taskPollers: []scalableTaskPoller{
					{taskPollerType: "test", pollerCount: 1, taskPoller: tp},
				},
				taskProcessor:                &recordingTaskProcessor{processed: taskProcessed},
				workerType:                   "LegacyShutdownTest",
				logger:                       ilog.NewNopLogger(),
				stopTimeout:                  5 * time.Second,
				metricsHandler:               metrics.NopHandler,
				workerPollCompleteOnShutdown: tt.workerPollCompleteOnShutdown,
			})

			bw.Start()
			<-pollStarted

			// AggregatedWorker.Stop sets noRepoll before stopping base workers.
			bw.noRepoll.Store(true)

			stopDone := make(chan struct{})
			go func() {
				bw.Stop()
				close(stopDone)
			}()

			<-bw.stopCh
			close(tp.returnTask)

			select {
			case <-stopDone:
			case <-time.After(5 * time.Second):
				t.Fatal("Stop() did not return in time")
			}

			select {
			case <-taskProcessed:
				t.Fatal("task polled during legacy shutdown was processed")
			default:
			}
		})
	}
}

// shutdownTaskPoller blocks until returnTask is closed, then returns a task
// exactly once. Subsequent polls return nil.
type shutdownTaskPoller struct {
	pollStarted chan struct{}
	returnTask  chan struct{}
	task        taskForWorker
	returned    atomic.Bool
}

func (p *shutdownTaskPoller) PollTask() (taskForWorker, error) {
	select {
	case p.pollStarted <- struct{}{}:
	default:
	}
	<-p.returnTask
	if p.returned.CompareAndSwap(false, true) {
		return p.task, nil
	}
	return nil, nil
}

type recordingTaskProcessor struct {
	processed chan struct{}
}

func (p *recordingTaskProcessor) ProcessTask(any) error {
	select {
	case p.processed <- struct{}{}:
	default:
	}
	return nil
}

func TestStopTimeoutBoundsPollerDrain(t *testing.T) {
	pollStarted := make(chan struct{})
	releasePoll := make(chan struct{})
	var releasePollOnce sync.Once
	releaseBlockedPoller := func() {
		releasePollOnce.Do(func() {
			close(releasePoll)
		})
	}
	defer releaseBlockedPoller()
	tp := &blockingShutdownPoller{
		pollStarted: pollStarted,
		releasePoll: releasePoll,
	}
	workerPollCompleteOnShutdown := &atomic.Bool{}
	workerPollCompleteOnShutdown.Store(true)

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:     &testSlotSupplier{},
		maxTaskPerSecond: 1000,
		taskPollers: []scalableTaskPoller{
			{taskPollerType: "test", pollerCount: 1, taskPoller: tp},
		},
		taskProcessor:                noopTaskProcessor{},
		workerType:                   "StopTimeoutTest",
		logger:                       ilog.NewNopLogger(),
		stopTimeout:                  50 * time.Millisecond,
		metricsHandler:               metrics.NopHandler,
		workerPollCompleteOnShutdown: workerPollCompleteOnShutdown,
	})

	bw.Start()
	<-pollStarted

	stopDone := make(chan struct{})
	start := time.Now()
	go func() {
		bw.Stop()
		close(stopDone)
	}()

	// Stop() should return after stopTimeout (~50ms), not block for the
	// full pollTaskServiceTimeOut (70s).
	select {
	case <-stopDone:
		elapsed := time.Since(start)
		require.Less(t, elapsed, time.Second,
			"Stop() should return after stopTimeout, not wait for pollTaskServiceTimeOut")
	case <-time.After(time.Second):
		releaseBlockedPoller()
		require.True(t, awaitWaitGroup(&bw.stopWG, time.Second),
			"worker goroutines should finish after blocked poll is released")
		t.Fatal("Stop() should return after stopTimeout, not wait for pollTaskServiceTimeOut")
	}

	releaseBlockedPoller()
	require.True(t, awaitWaitGroup(&bw.stopWG, time.Second),
		"worker goroutines should finish after blocked poll is released")
}

func TestLegacyStopReturnsPromptlyWithBlockedPoller(t *testing.T) {
	pollStarted := make(chan struct{})
	tp := &stopAwareShutdownPoller{
		pollStarted: pollStarted,
	}

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:     &testSlotSupplier{},
		maxTaskPerSecond: 1000,
		taskPollers: []scalableTaskPoller{
			{taskPollerType: "test", pollerCount: 1, taskPoller: tp},
		},
		taskProcessor:                noopTaskProcessor{},
		workerType:                   "LegacyStopTimeoutTest",
		logger:                       ilog.NewNopLogger(),
		stopTimeout:                  5 * time.Second,
		metricsHandler:               metrics.NopHandler,
		workerPollCompleteOnShutdown: &atomic.Bool{},
	})
	tp.stopC = bw.stopCh

	bw.Start()
	<-pollStarted

	start := time.Now()
	bw.Stop()
	elapsed := time.Since(start)

	require.Less(t, elapsed, time.Second,
		"legacy Stop() should return promptly when a blocked poll observes shutdown")
	require.True(t, tp.stopped.Load(), "blocked poller should observe shutdown")
}

type blockingShutdownPoller struct {
	pollStarted chan struct{}
	releasePoll chan struct{}
	started     atomic.Bool
}

func (p *blockingShutdownPoller) PollTask() (taskForWorker, error) {
	if p.started.CompareAndSwap(false, true) {
		close(p.pollStarted)
	}
	<-p.releasePoll
	return nil, nil
}

type stopAwareShutdownPoller struct {
	pollStarted chan struct{}
	stopC       <-chan struct{}
	started     atomic.Bool
	stopped     atomic.Bool
}

func (p *stopAwareShutdownPoller) PollTask() (taskForWorker, error) {
	if p.started.CompareAndSwap(false, true) {
		close(p.pollStarted)
	}
	<-p.stopC
	p.stopped.Store(true)
	return nil, errStop
}

func (s *PollScalerReportHandleSuite) TestAutoscaleDownOnTimeoutWithCapability() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount:        10,
		maxPollerCount:            10,
		minPollerCount:            1,
		serverSupportsAutoscaling: &atomic.Bool{},
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.serverSupportsAutoscaling.Store(true)

	// Send 20 empty polls - should scale all the way down to min (1)
	for i := 0; i < 20; i++ {
		ps.handleTask(newEmptyTask())
	}
	assert.Equal(s.T(), 1, targetSuggestion)
	assert.False(s.T(), ps.everSawScalingDecision.Load())
}

func (s *PollScalerReportHandleSuite) TestAutoscaleDownOnTimeoutWithoutCapability() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 10,
		maxPollerCount:     10,
		minPollerCount:     1,
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})

	// Send 20 empty polls - should NOT scale down because we haven't seen a
	// scaling decision and server doesn't support autoscaling
	for i := 0; i < 20; i++ {
		ps.handleTask(newEmptyTask())
	}
	// target never changed from initial, callback was never called
	assert.Equal(s.T(), 0, targetSuggestion)
	assert.Equal(s.T(), int64(10), ps.target.Load())
}

func (s *PollScalerReportHandleSuite) TestAutoscaleDownOnTimeoutClampsToMin() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount:        10,
		maxPollerCount:            10,
		minPollerCount:            3,
		serverSupportsAutoscaling: &atomic.Bool{},
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.serverSupportsAutoscaling.Store(true)

	// Send 20 empty polls - should scale down but clamp at min (3)
	for i := 0; i < 20; i++ {
		ps.handleTask(newEmptyTask())
	}
	assert.Equal(s.T(), 3, targetSuggestion)
	assert.False(s.T(), ps.everSawScalingDecision.Load())
}

func (s *PollScalerReportHandleSuite) TestErrorScaleDownWithCapability() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount:        8,
		maxPollerCount:            10,
		minPollerCount:            2,
		serverSupportsAutoscaling: &atomic.Bool{},
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.serverSupportsAutoscaling.Store(true)

	// Should scale down on errors even without having seen a scaling decision
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), 4, targetSuggestion)
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), 3, targetSuggestion)
}

// TestPollerBalancerReturnsNilWhenOwnCountZero is a regression test for
// https://github.com/temporalio/sdk-go/issues/2236
// It verifies that balance() returns nil immediately when the calling poller
// type's count has dropped to <= 0, even if another type has count == 0.
func TestPollerBalancerReturnsNilWhenOwnCountZero(t *testing.T) {
	pb := &pollerBalancer{
		pollerCount:   make(map[string]int),
		pollerBarrier: make(map[string]barrier),
	}
	pb.registerPollerType("sticky")
	pb.registerPollerType("non-sticky")

	ctx := context.Background()

	// Both types have count 0 — balance should return nil immediately for either type.
	err := pb.balance(ctx, "sticky")
	require.NoError(t, err, "balance should return nil when own count is 0")

	err = pb.balance(ctx, "non-sticky")
	require.NoError(t, err, "balance should return nil when own count is 0")

	// Simulate: sticky has 1 poller, non-sticky has 0.
	// balance("sticky") should block waiting for non-sticky. But if sticky's count
	// drops to 0 before non-sticky starts, balance should return nil.
	pb.incrementPoller("sticky")
	pb.decrementPoller("sticky") // sticky count is back to 0

	// Even though non-sticky count is 0, we should NOT block because our own count is 0.
	// Run with a timeout to catch the bug where it would block indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = pb.balance(ctx, "sticky")
	require.NoError(t, err, "balance should return nil when own count is 0, not block on other type's barrier")
}

// TestPollerBalancerBlocksWhenOtherTypeHasNoPollers verifies the normal blocking
// behavior: balance() blocks when another poller type has no active pollers, and
// unblocks once that type starts a poller.
func TestPollerBalancerBlocksWhenOtherTypeHasNoPollers(t *testing.T) {
	pb := &pollerBalancer{
		pollerCount:   make(map[string]int),
		pollerBarrier: make(map[string]barrier),
	}
	pb.registerPollerType("sticky")
	pb.registerPollerType("non-sticky")

	// sticky has 1 poller, non-sticky has 0 — balance("sticky") should block.
	pb.incrementPoller("sticky")

	done := make(chan error, 1)
	go func() {
		done <- pb.balance(context.Background(), "sticky")
	}()

	// Verify it's still blocked.
	select {
	case <-done:
		t.Fatal("balance should be blocking")
	case <-time.After(50 * time.Millisecond):
	}

	// Start a non-sticky poller — this should unblock balance.
	pb.incrementPoller("non-sticky")

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("balance should have returned after non-sticky poller started")
	}
}

func (s *ScalableTaskPollerSuite) TestNewScalableTaskPollerAllTypes() {
	behavior := NewPollerBehaviorSimpleMaximum(
		PollerBehaviorSimpleMaximumOptions{
			MaximumNumberOfPollers: 1,
		},
	)

	cases := []struct {
		name  string
		ptype string
	}{
		{"workflow", metrics.PollerTypeWorkflowTask},
		{"workflow-sticky", metrics.PollerTypeWorkflowStickyTask},
		{"activity", metrics.PollerTypeActivityTask},
		{"nexus", metrics.PollerTypeNexusTask},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			poller := newScalableTaskPoller(
				newSemaphoreProbeTaskPoller(),
				ilog.NewNopLogger(),
				behavior,
				tc.ptype,
				&atomic.Bool{},
			)
			s.Equal(tc.ptype, poller.taskPollerType)
		})
	}
}
