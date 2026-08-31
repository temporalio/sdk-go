package internal

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

type (
	PollerAutoscalerSuite struct {
		suite.Suite
	}
)

func TestPollerAutoscalerSuite(t *testing.T) {
	suite.Run(t, new(PollerAutoscalerSuite))
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

	blockingPoller := newBlockingProbeTaskPoller()
	poller := newScalableTaskPoller(
		blockingPoller,
		ilog.NewNopLogger(),
		behavior,
		metrics.PollerTypeWorkflowStickyTask,
		&atomic.Bool{},
		nil,
	)

	s.Equal(metrics.PollerTypeWorkflowStickyTask, poller.taskPollerType)
}

func (s *ScalableTaskPollerSuite) TestNewScalableTaskPollerUsesDynamicRunnerOnlyForAutoscaling() {
	autoscalingPoller := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		&pollerBehaviorAutoscaling{
			initialNumberOfPollers: 1,
			maximumNumberOfPollers: 2,
			minimumNumberOfPollers: 1,
		},
		metrics.PollerTypeWorkflowTask,
		&atomic.Bool{},
		nil,
	)
	s.NotNil(autoscalingPoller.autoscalingRunner)
	s.Equal(0, autoscalingPoller.pollerCount)

	simpleMaximumPoller := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		&pollerBehaviorSimpleMaximum{maximumNumberOfPollers: 2},
		metrics.PollerTypeWorkflowTask,
		&atomic.Bool{},
		nil,
	)
	s.Nil(simpleMaximumPoller.autoscalingRunner)
	s.Equal(2, simpleMaximumPoller.pollerCount)
}

func (s *ScalableTaskPollerSuite) TestSlotReservationDataUsesKnownTaskQueueKind() {
	autoscalingBehavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: 1,
		maximumNumberOfPollers: 2,
		minimumNumberOfPollers: 1,
	}
	bw := &baseWorker{
		options: baseWorkerOptions{
			slotReservationData: slotReservationData{
				taskQueue:     "test-task-queue",
				taskQueueKind: enumspb.TASK_QUEUE_KIND_UNSPECIFIED,
			},
		},
	}

	nonStickyPoller := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		autoscalingBehavior,
		metrics.PollerTypeWorkflowTask,
		&atomic.Bool{},
		nil,
	)
	s.Equal(enumspb.TASK_QUEUE_KIND_NORMAL, bw.slotReservationData(nonStickyPoller).taskQueueKind)

	stickyPoller := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		autoscalingBehavior,
		metrics.PollerTypeWorkflowStickyTask,
		&atomic.Bool{},
		nil,
	)
	s.Equal(enumspb.TASK_QUEUE_KIND_STICKY, bw.slotReservationData(stickyPoller).taskQueueKind)

	mixedPoller := newScalableTaskPoller(
		newBlockingProbeTaskPoller(),
		ilog.NewNopLogger(),
		&pollerBehaviorSimpleMaximum{maximumNumberOfPollers: 1},
		metrics.PollerTypeWorkflowTask,
		&atomic.Bool{},
		nil,
	)
	s.Equal(enumspb.TASK_QUEUE_KIND_UNSPECIFIED, bw.slotReservationData(mixedPoller).taskQueueKind)
}

func (s *ScalableTaskPollerSuite) TestTrackingSlotSupplierPassesTaskQueueKind() {
	supplier := &captureReservationInfoSlotSupplier{}
	trackingSupplier := newTrackingSlotSupplier(supplier, trackingSlotSupplierOptions{
		logger:         ilog.NewNopLogger(),
		metricsHandler: metrics.NopHandler,
	})

	permit, err := trackingSupplier.ReserveSlot(context.Background(), &slotReservationData{
		taskQueue:     "test-task-queue",
		taskQueueKind: enumspb.TASK_QUEUE_KIND_STICKY,
	})

	s.NoError(err)
	s.NotNil(permit)
	s.Equal("test-task-queue", supplier.taskQueue)
	s.Equal(enumspb.TASK_QUEUE_KIND_STICKY, supplier.taskQueueKind)
}

func (s *ScalableTaskPollerSuite) TestInitializeTaskPollersCreatesBalancerForMultiplePollers() {
	newPoller := func(pollerType string) scalableTaskPoller {
		return newScalableTaskPoller(
			newBlockingProbeTaskPoller(),
			ilog.NewNopLogger(),
			&pollerBehaviorAutoscaling{initialNumberOfPollers: 1, maximumNumberOfPollers: 2, minimumNumberOfPollers: 1},
			pollerType,
			&atomic.Bool{},
			nil,
		)
	}

	singlePollerWorker := &baseWorker{}
	singlePollerWorker.initializeTaskPollers([]scalableTaskPoller{newPoller(metrics.PollerTypeWorkflowTask)})
	s.Len(singlePollerWorker.options.taskPollers, 1)
	s.Nil(singlePollerWorker.pollerBalancer)

	bw := &baseWorker{}
	bw.initializeTaskPollers([]scalableTaskPoller{
		newPoller(metrics.PollerTypeWorkflowTask),
		newPoller(metrics.PollerTypeWorkflowStickyTask),
	})
	s.Len(bw.options.taskPollers, 2)
	s.NotNil(bw.pollerBalancer)
	// Panic if task pollers are initialized more than once
	s.Panics(func() {
		bw.initializeTaskPollers([]scalableTaskPoller{newPoller(metrics.PollerTypeWorkflowTask)})
	})
}

type blockingGaugeMetricsHandler struct {
	metrics.Handler
	blockMarkUpdates  atomic.Bool
	firstMarkStarted  chan struct{}
	secondMarkStarted chan struct{}
	unblockFirstMark  chan struct{}
	markCount         atomic.Int32
}

func (h *blockingGaugeMetricsHandler) Gauge(name string) metrics.Gauge {
	gauge := h.Handler.Gauge(name)
	if name != metrics.WorkerTaskSlotsUsed {
		return gauge
	}
	return metrics.GaugeFunc(func(value float64) {
		if h.blockMarkUpdates.Load() {
			switch h.markCount.Add(1) {
			case 1:
				close(h.firstMarkStarted)
				<-h.unblockFirstMark
			case 2:
				close(h.secondMarkStarted)
			}
		}
		gauge.Update(value)
	})
}

func (s *ScalableTaskPollerSuite) TestTrackingSlotSupplierPublishesLatestConcurrentState() {
	inner, err := NewFixedSizeSlotSupplier(2)
	s.Require().NoError(err)
	capturingHandler := metrics.NewCapturingHandler()
	metricsHandler := &blockingGaugeMetricsHandler{
		Handler:           capturingHandler,
		firstMarkStarted:  make(chan struct{}),
		secondMarkStarted: make(chan struct{}),
		unblockFirstMark:  make(chan struct{}),
	}
	trackingSupplier := newTrackingSlotSupplier(inner, trackingSlotSupplierOptions{
		logger:         ilog.NewNopLogger(),
		metricsHandler: metricsHandler,
	})

	permit1, err := trackingSupplier.ReserveSlot(context.Background(), &slotReservationData{})
	s.Require().NoError(err)
	permit2, err := trackingSupplier.ReserveSlot(context.Background(), &slotReservationData{})
	s.Require().NoError(err)
	metricsHandler.blockMarkUpdates.Store(true)

	firstMarkDone := make(chan struct{})
	go func() {
		trackingSupplier.MarkSlotUsed(permit1)
		close(firstMarkDone)
	}()
	<-metricsHandler.firstMarkStarted
	secondMarkDone := make(chan struct{})
	go func() {
		trackingSupplier.MarkSlotUsed(permit2)
		close(secondMarkDone)
	}()
	select {
	case <-metricsHandler.secondMarkStarted:
	case <-time.After(time.Second):
		close(metricsHandler.unblockFirstMark)
		s.FailNow("second slot state change blocked behind the first metric update")
	}
	close(metricsHandler.unblockFirstMark)
	<-firstMarkDone
	<-secondMarkDone

	var usedSlots float64
	for _, gauge := range capturingHandler.Gauges() {
		if gauge.Name == metrics.WorkerTaskSlotsUsed {
			usedSlots = gauge.Value()
		}
	}
	s.Equal(float64(2), usedSlots)
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

func (s *PollerAutoscalerSuite) TestErrorScaleDown() {
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
	})
	ps.handleTask(newTestTask(0))
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), int64(4), ps.target.Load(), "should suggest scaling down on resource exhausted error")
	// Non resource exhausted errors should scale down by 1
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), int64(3), ps.target.Load())
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), int64(2), ps.target.Load())
	// We should not scale down below minPollerCount
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), int64(2), ps.target.Load())
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), int64(2), ps.target.Load())
}

func (s *PollerAutoscalerSuite) TestScaleDownOnEmptyTask() {
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
	})
	ps.handleTask(newTestTask(0))
	ps.handleTask(newEmptyTask())
	assert.Equal(s.T(), int64(7), ps.target.Load())
}

func (s *PollerAutoscalerSuite) TestScaleUpOnDelay() {
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
	})
	ps.handleTask(newTestTask(10))
	assert.Equal(s.T(), int64(8), ps.target.Load())
	ps.newPeriod()
	ps.handleTask(newTestTask(100))
	// We should scale up to but not past the max poller count
	assert.Equal(s.T(), int64(10), ps.target.Load())

}

func (s *ScalableTaskPollerSuite) TestAutoscalingConcurrencyScalesUpToMaximum() {
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: 2,
		maximumNumberOfPollers: 3,
		minimumNumberOfPollers: 1,
	}

	blockingPoller := newBlockingProbeTaskPoller()
	poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior, "", nil, nil)
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
		blockingPoller.Allow(readAutoscalingPollerState(poller.autoscalingRunner))
		blockingPoller.Close()
		bw.Stop()
	}()

	eventuallyAutoscalingPollerState(s.T(), poller.autoscalingRunner, 2, "expected initial pollers to start")

	require.Never(s.T(), func() bool {
		blockingPoller.Allow(readAutoscalingPollerState(poller.autoscalingRunner))
		return readAutoscalingPollerState(poller.autoscalingRunner) > 2
	}, 200*time.Millisecond, 10*time.Millisecond, "should not exceed initial concurrency")

	poller.pollerAutoscaler.updateTarget(func(int64) int64 { return 3 })

	eventuallyAutoscalingPollerState(s.T(), poller.autoscalingRunner, 3, "expected concurrency to scale up to maximum")

	require.Never(s.T(), func() bool {
		blockingPoller.Allow(readAutoscalingPollerState(poller.autoscalingRunner))
		return readAutoscalingPollerState(poller.autoscalingRunner) > 3
	}, 200*time.Millisecond, 10*time.Millisecond, "should not exceed maximum concurrency")
}

func (s *ScalableTaskPollerSuite) TestAutoscalingScalesDownToMinimum() {
	synctest.Test(s.T(), func(t *testing.T) {
		behavior := &pollerBehaviorAutoscaling{
			initialNumberOfPollers: 2,
			maximumNumberOfPollers: 3,
			minimumNumberOfPollers: 1,
		}

		blockingPoller := newBlockingProbeTaskPoller()
		poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior, "", nil, nil)

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
			blockingPoller.Allow(readAutoscalingPollerState(poller.autoscalingRunner))
			blockingPoller.Close()
			bw.Stop()
		}()

		assertAutoscalingPollerState(t, poller.autoscalingRunner, 2, "expected initial concurrency")

		poller.pollerAutoscaler.updateTarget(func(target int64) int64 { return 1 })
		blockingPoller.Allow(2)

		assertAutoscalingPollerState(t, poller.autoscalingRunner, 1, "expected concurrency to reduce to minimum")

		for range 5 {
			assert.Equal(t, int64(1), poller.pollerAutoscaler.target.Load(), "should not scale target below minimum")
			blockingPoller.Allow(1)
			assertAutoscalingPollerState(t, poller.autoscalingRunner, 1, "expected concurrency to recover to minimum")
		}
	})
}

func (s *ScalableTaskPollerSuite) TestAutoscalingPollerGroupAddRaisesRequiredMinimumAfterPollCompletion() {
	pollerGroups := newPollerGroupManager(pollerGroupModeWorkflow, nil)
	pollerGroups.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))
	autoscaler := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount: 1,
		maxPollerCount:     1,
		minPollerCount:     1,
	})
	runner := newAutoscalingTaskPollerRunner(
		autoscaler,
		pollerGroups,
		enumspb.TASK_QUEUE_KIND_NORMAL,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, release, err := runner.acquire(ctx)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, runner.activePolls(), "expected one active poll for one group")

	assert.Equal(s.T(), 1, runner.effectiveTarget(), "expected one effective poller before group add")
	pollerGroups.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 1},
	}))

	assert.Equal(s.T(), 2, runner.effectiveTarget(), "expected group add to raise required minimum")
	release()

	_, firstRelease, err := runner.acquire(ctx)
	require.NoError(s.T(), err)
	defer firstRelease()
	_, secondRelease, err := runner.acquire(ctx)
	require.NoError(s.T(), err)
	defer secondRelease()
	assert.Equal(s.T(), 2, runner.activePolls(), "expected group add to raise required minimum")
	assert.Equal(s.T(), int64(1), autoscaler.target.Load(), "group add should not mutate autoscaler target")
}

func (s *ScalableTaskPollerSuite) TestAutoscalingPollerGroupUpdateWakesRunnerSharingStore() {
	groupInfos := newPollerGroupInfoStore()
	publisher := newPollerGroupManager(pollerGroupModeNonWorkflow, groupInfos)
	subscriber := newPollerGroupManager(pollerGroupModeWorkflow, groupInfos)
	publisher.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))
	runner := newAutoscalingTaskPollerRunner(
		newPollerAutoscaler(pollerAutoscalerOptions{
			initialPollerCount: 1,
			maxPollerCount:     1,
			minPollerCount:     1,
		}),
		subscriber,
		enumspb.TASK_QUEUE_KIND_NORMAL,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstLease, firstRelease, err := runner.acquire(ctx)
	require.NoError(s.T(), err)
	defer firstRelease()
	defer firstLease.release()

	type acquireResult struct {
		lease   pollerGroupLease
		release func()
		err     error
	}
	resultCh := make(chan acquireResult, 1)
	go func() {
		lease, release, err := runner.acquire(ctx)
		resultCh <- acquireResult{lease: lease, release: release, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.release != nil {
			result.release()
		}
		result.lease.release()
		s.T().Fatal("second poll acquired before another group was added")
	case <-time.After(20 * time.Millisecond):
	}

	publisher.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 1},
	}))

	select {
	case result := <-resultCh:
		require.NoError(s.T(), result.err)
		defer result.release()
		defer result.lease.release()
		require.Equal(s.T(), "group-b", result.lease.groupIDOrEmpty())
	case <-time.After(time.Second):
		s.T().Fatal("shared store update did not wake runner")
	}
}

func (s *ScalableTaskPollerSuite) TestAutoscalingCurrentGroupCoverageDoesNotWaitForStalePolls() {
	for _, test := range []struct {
		name          string
		mode          pollerGroupMode
		initialGroups []*taskqueuepb.PollerGroupInfo
	}{
		{
			name: "workflow replaced groups",
			mode: pollerGroupModeWorkflow,
			initialGroups: []*taskqueuepb.PollerGroupInfo{
				{Id: "old-a", Weight: 1},
				{Id: "old-b", Weight: 1},
			},
		},
		{
			name: "non-workflow replaced groups",
			mode: pollerGroupModeNonWorkflow,
			initialGroups: []*taskqueuepb.PollerGroupInfo{
				{Id: "old-a", Weight: 1},
				{Id: "old-b", Weight: 1},
			},
		},
		{name: "non-workflow ungrouped polls", mode: pollerGroupModeNonWorkflow},
	} {
		s.Run(test.name, func() {
			pollerGroups := newPollerGroupManager(test.mode, nil)
			pollerGroups.updateGroups(testPollerGroupsInfo(1, test.initialGroups))
			runner := newAutoscalingTaskPollerRunner(
				newPollerAutoscaler(pollerAutoscalerOptions{
					initialPollerCount: 2,
					maxPollerCount:     2,
					minPollerCount:     2,
				}),
				pollerGroups,
				enumspb.TASK_QUEUE_KIND_NORMAL,
			)

			type admission struct {
				lease   pollerGroupLease
				release func()
			}
			acquire := func() []admission {
				admissions := make([]admission, 0, 2)
				for range 2 {
					lease, release, err := runner.acquire(s.T().Context())
					require.NoError(s.T(), err)
					admissions = append(admissions, admission{lease: lease, release: release})
				}
				return admissions
			}
			release := func(admissions []admission) {
				for _, admission := range admissions {
					admission.lease.release()
					admission.release()
				}
			}

			oldAdmissions := acquire()

			pollerGroups.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
				{Id: "new-a", Weight: 1},
				{Id: "new-b", Weight: 1},
			}))

			newAdmissions := acquire()
			newGroups := make(map[string]struct{}, len(newAdmissions))
			for _, admission := range newAdmissions {
				newGroups[admission.lease.groupIDOrEmpty()] = struct{}{}
			}
			require.Equal(s.T(), map[string]struct{}{"new-a": {}, "new-b": {}}, newGroups)
			require.Equal(s.T(), 4, runner.activePolls(), "current coverage may temporarily exceed the target while stale polls drain")

			release(oldAdmissions)
			require.Equal(s.T(), 2, runner.activePolls())
			_, ok := pollerGroups.tryReserveRequired(enumspb.TASK_QUEUE_KIND_NORMAL)
			require.False(s.T(), ok, "releasing stale polls must not remove current coverage")
			release(newAdmissions)
		})
	}
}

func (s *ScalableTaskPollerSuite) TestAutoscalingPollerGroupRemovalLowersRequiredMinimum() {
	pollerGroups := newPollerGroupManager(pollerGroupModeWorkflow, nil)
	pollerGroups.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 100},
	}))
	autoscaler := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount: 1,
		maxPollerCount:     1,
		minPollerCount:     1,
	})
	runner := newAutoscalingTaskPollerRunner(
		autoscaler,
		pollerGroups,
		enumspb.TASK_QUEUE_KIND_NORMAL,
	)

	assert.Equal(s.T(), 2, runner.effectiveTarget(), "expected two effective pollers for two groups")
	pollerGroups.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))

	assert.Equal(s.T(), 1, runner.effectiveTarget(), "expected group removal to lower required minimum")
	lease, ok := pollerGroups.tryReserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL)
	require.True(s.T(), ok)
	defer lease.release()
	assert.Equal(s.T(), "group-a", lease.groupIDOrEmpty(), "future reservations should not use removed groups")
	assert.Equal(s.T(), int64(1), autoscaler.target.Load(), "group removal should not mutate autoscaler target")
}

func (s *ScalableTaskPollerSuite) TestAutoscalingWorkflowRunnerBlocksExtraNormalUntilStickyCoverage() {
	pollerGroups := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	pollerGroups.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))
	normalRunner := newAutoscalingTaskPollerRunner(
		newPollerAutoscaler(pollerAutoscalerOptions{
			initialPollerCount: 2,
			maxPollerCount:     2,
			minPollerCount:     1,
		}),
		pollerGroups,
		enumspb.TASK_QUEUE_KIND_NORMAL,
	)
	stickyRunner := newAutoscalingTaskPollerRunner(
		newPollerAutoscaler(pollerAutoscalerOptions{
			initialPollerCount: 1,
			maxPollerCount:     1,
			minPollerCount:     1,
		}),
		pollerGroups,
		enumspb.TASK_QUEUE_KIND_STICKY,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstNormalLease, firstNormalRelease, err := normalRunner.acquire(ctx)
	require.NoError(s.T(), err)
	defer firstNormalRelease()
	defer firstNormalLease.release()

	type acquisition struct {
		lease   pollerGroupLease
		release func()
		err     error
	}
	secondNormal := make(chan acquisition, 1)
	go func() {
		lease, release, err := normalRunner.acquire(ctx)
		secondNormal <- acquisition{lease: lease, release: release, err: err}
	}()

	select {
	case result := <-secondNormal:
		if result.release != nil {
			result.release()
		}
		result.lease.release()
		s.T().Fatal("extra normal poll acquired before sticky coverage", result.err)
	case <-time.After(50 * time.Millisecond):
	}

	stickyLease, stickyRelease, err := stickyRunner.acquire(ctx)
	require.NoError(s.T(), err)
	defer stickyRelease()
	defer stickyLease.release()

	select {
	case result := <-secondNormal:
		require.NoError(s.T(), result.err)
		require.Equal(s.T(), enumspb.TASK_QUEUE_KIND_NORMAL, result.lease.queueKind)
		result.lease.release()
		result.release()
	case <-time.After(time.Second):
		s.T().Fatal("extra normal poll did not acquire after sticky coverage completed")
	}
}

func (s *ScalableTaskPollerSuite) TestAutoscalingEffectiveTargetUsesMCNRequiredMinimumAsFloor() {
	tests := []struct {
		name          string
		current       int
		configuredMin int
		configuredMax int
		requiredMin   int
		expected      int
	}{
		{
			name:          "configured min below required minimum",
			current:       1,
			configuredMin: 1,
			configuredMax: 10,
			requiredMin:   3,
			expected:      3,
		},
		{
			name:          "configured max below required minimum",
			current:       1,
			configuredMin: 1,
			configuredMax: 2,
			requiredMin:   4,
			expected:      4,
		},
		{
			name:          "current target respected within effective bounds",
			current:       3,
			configuredMin: 1,
			configuredMax: 5,
			requiredMin:   2,
			expected:      3,
		},
		{
			name:          "current target capped at configured max when above effective bounds",
			current:       6,
			configuredMin: 1,
			configuredMax: 5,
			requiredMin:   2,
			expected:      5,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.Equal(tt.expected, effectivePollerTarget(
				tt.current,
				tt.configuredMin,
				tt.configuredMax,
				tt.requiredMin,
			))
		})
	}
}

func (s *ScalableTaskPollerSuite) TestAutoscalingMCNRequiredFloorStillGatedBySlots() {
	behavior := &pollerBehaviorAutoscaling{
		initialNumberOfPollers: 1,
		maximumNumberOfPollers: 1,
		minimumNumberOfPollers: 1,
	}
	pollerGroups := newPollerGroupManager(pollerGroupModeWorkflow, nil)
	pollerGroups.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 1},
	}))

	blockingPoller := newBlockingProbeTaskPoller()
	poller := newScalableTaskPoller(
		blockingPoller,
		ilog.NewNopLogger(),
		behavior,
		metrics.PollerTypeWorkflowTask,
		&atomic.Bool{},
		pollerGroups,
	)
	slotSupplier := newLimitedSlotSupplier(1)

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:     slotSupplier,
		maxTaskPerSecond: 1000,
		taskPollers:      []scalableTaskPoller{poller},
		taskProcessor:    noopTaskProcessor{},
		workerType:       "AutoscalingMCNSlotCapacityTest",
		logger:           ilog.NewNopLogger(),
		stopTimeout:      time.Second,
		metricsHandler:   metrics.NopHandler,
	})

	bw.Start()
	defer func() {
		blockingPoller.Allow(readAutoscalingPollerState(poller.autoscalingRunner))
		blockingPoller.Close()
		bw.Stop()
	}()

	assert.Equal(s.T(), 2, poller.autoscalingRunner.effectiveTarget(),
		"expected MCN required floor to raise the effective target")
	require.Eventually(s.T(), func() bool {
		return blockingPoller.startedPolls() == 1
	}, time.Second, 10*time.Millisecond, "expected first poll to start")
	require.Equal(s.T(), int32(1), slotSupplier.reserves.Load(), "expected only one slot to be reserved")
	require.Nil(s.T(), slotSupplier.TryReserveSlot(nil), "expected no spare slot while first poll is running")

	require.Never(s.T(), func() bool {
		return blockingPoller.startedPolls() > 1
	}, 200*time.Millisecond, 10*time.Millisecond, "MCN required floor should not bypass slot capacity")

	blockingPoller.Allow(1)

	require.Eventually(s.T(), func() bool {
		return blockingPoller.startedPolls() == 2
	}, time.Second, 10*time.Millisecond, "expected second poll to start after a slot is released")
}

func (s *ScalableTaskPollerSuite) TestAutoscalingDoesNotHoldSlotWhileWaitingForPollCapacity() {
	synctest.Test(s.T(), func(t *testing.T) {
		behavior := &pollerBehaviorAutoscaling{
			initialNumberOfPollers: 1,
			maximumNumberOfPollers: 2,
			minimumNumberOfPollers: 1,
		}

		blockingPoller := newBlockingProbeTaskPoller()
		poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior, "", nil, nil)
		slotSupplier := newLimitedSlotSupplier(2)

		bw := newBaseWorker(baseWorkerOptions{
			slotSupplier:     slotSupplier,
			maxTaskPerSecond: 1000,
			taskPollers:      []scalableTaskPoller{poller},
			taskProcessor:    noopTaskProcessor{},
			workerType:       "AutoscalingSlotCapacityTest",
			logger:           ilog.NewNopLogger(),
			stopTimeout:      time.Second,
			metricsHandler:   metrics.NopHandler,
		})

		bw.Start()
		defer func() {
			blockingPoller.Allow(readAutoscalingPollerState(poller.autoscalingRunner))
			blockingPoller.Close()
			bw.Stop()
		}()

		assertAutoscalingPollerState(t, poller.autoscalingRunner, 1, "expected initial poller to start")
		require.Equal(t, int32(1), slotSupplier.reserves.Load(),
			"autoscaling poller should not reserve another slot while blocked by its target")

		permit := slotSupplier.TryReserveSlot(nil)
		require.NotNil(t, permit, "unused slot should remain available while autoscaling target is full")
		slotSupplier.ReleaseSlot(nil)
	})
}

func (s *ScalableTaskPollerSuite) TestAutoscalingBalancerDoesNotHoldSlotsWhileBlocked() {
	synctest.Test(s.T(), func(t *testing.T) {
		behavior := &pollerBehaviorAutoscaling{
			initialNumberOfPollers: 2,
			maximumNumberOfPollers: 2,
			minimumNumberOfPollers: 1,
		}

		blockingPoller := newBlockingProbeTaskPoller()
		poller := newScalableTaskPoller(blockingPoller, ilog.NewNopLogger(), behavior, "a", nil, nil)
		slotSupplier := newLimitedSlotSupplier(2)

		bw := newBaseWorker(baseWorkerOptions{
			slotSupplier:     slotSupplier,
			maxTaskPerSecond: 1000,
			taskPollers: []scalableTaskPoller{
				poller,
				{taskPollerType: "b"},
			},
			taskProcessor:  noopTaskProcessor{},
			workerType:     "AutoscalingBalancerTest",
			logger:         ilog.NewNopLogger(),
			stopTimeout:    time.Second,
			metricsHandler: metrics.NopHandler,
		})

		bw.Start()
		defer func() {
			blockingPoller.Allow(readAutoscalingPollerState(poller.autoscalingRunner))
			blockingPoller.Close()
			bw.Stop()
		}()

		assertAutoscalingPollerState(t, poller.autoscalingRunner, 1, "expected first poller to start")
		require.Equal(t, int32(1), slotSupplier.reserves.Load(),
			"autoscaling poller should not reserve another slot while blocked by poller balancer")
	})
}

type blockingProbeTaskPoller struct {
	signals chan struct{}
	done    chan struct{}
	closed  atomic.Bool
	started atomic.Int32
}

func newBlockingProbeTaskPoller() *blockingProbeTaskPoller {
	return &blockingProbeTaskPoller{
		signals: make(chan struct{}, 32),
		done:    make(chan struct{}),
	}
}

// PollTask implements taskPoller and blocks until a signal is provided so active polls stay acquired.
func (p *blockingProbeTaskPoller) PollTask(pollerGroupLease) (taskForWorker, error) {
	p.started.Add(1)
	select {
	case <-p.signals:
		return nil, nil
	case <-p.done:
		return nil, nil
	}
}

func (p *blockingProbeTaskPoller) Allow(n int) {
	for range n {
		select {
		case p.signals <- struct{}{}:
		case <-p.done:
			return
		}
	}
}

func (p *blockingProbeTaskPoller) startedPolls() int32 {
	return p.started.Load()
}

func (p *blockingProbeTaskPoller) Close() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.done)
	}
}

func eventuallyAutoscalingPollerState(t *testing.T, runner *autoscalingTaskPollerRunner, expectedActive int, msg string) {
	require.Eventually(t, func() bool {
		return readAutoscalingPollerState(runner) == expectedActive
	}, time.Second, 10*time.Millisecond, msg)
}

func assertAutoscalingPollerState(t *testing.T, runner *autoscalingTaskPollerRunner, expectedActive int, msg string) {
	synctest.Wait()
	require.Equal(t, expectedActive, readAutoscalingPollerState(runner), msg)
}

func readAutoscalingPollerState(runner *autoscalingTaskPollerRunner) int {
	if runner == nil {
		return 0
	}
	return runner.activePolls()
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

type captureReservationInfoSlotSupplier struct {
	taskQueue     string
	taskQueueKind enumspb.TaskQueueKind
}

func (s *captureReservationInfoSlotSupplier) ReserveSlot(
	ctx context.Context,
	info SlotReservationInfo,
) (*SlotPermit, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	s.taskQueue = info.TaskQueue()
	s.taskQueueKind = info.TaskQueueKind()
	return &SlotPermit{}, nil
}

func (s *captureReservationInfoSlotSupplier) TryReserveSlot(info SlotReservationInfo) *SlotPermit {
	s.taskQueue = info.TaskQueue()
	s.taskQueueKind = info.TaskQueueKind()
	return &SlotPermit{}
}

func (s *captureReservationInfoSlotSupplier) MarkSlotUsed(SlotMarkUsedInfo) {}

func (s *captureReservationInfoSlotSupplier) ReleaseSlot(SlotReleaseInfo) {}

func (s *captureReservationInfoSlotSupplier) MaxSlots() int { return 0 }

type limitedSlotSupplier struct {
	slots    chan struct{}
	reserves atomic.Int32
	releases atomic.Int32
}

func newLimitedSlotSupplier(slots int) *limitedSlotSupplier {
	s := &limitedSlotSupplier{slots: make(chan struct{}, slots)}
	for range slots {
		s.slots <- struct{}{}
	}
	return s
}

func (s *limitedSlotSupplier) ReserveSlot(ctx context.Context, info SlotReservationInfo) (*SlotPermit, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.slots:
		s.reserves.Add(1)
		return &SlotPermit{}, nil
	}
}

func (s *limitedSlotSupplier) TryReserveSlot(SlotReservationInfo) *SlotPermit {
	select {
	case <-s.slots:
		s.reserves.Add(1)
		return &SlotPermit{}
	default:
		return nil
	}
}

func (s *limitedSlotSupplier) MarkSlotUsed(SlotMarkUsedInfo) {}

func (s *limitedSlotSupplier) ReleaseSlot(SlotReleaseInfo) {
	s.releases.Add(1)
	s.slots <- struct{}{}
}

func (s *limitedSlotSupplier) MaxSlots() int { return cap(s.slots) }

type noopTaskProcessor struct{}

func (noopTaskProcessor) ProcessTask(any) error { return nil }

// TestTaskNotDroppedDuringShutdown verifies the two-stage shutdown: when a
// poller receives a task during shutdown, the task is still dispatched and
// processed rather than silently dropped.
func TestTaskNotDroppedDuringShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		taskProcessed := make(chan struct{}, 1)
		pollStarted := make(chan struct{}, 1)

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
		synctest.Wait()

		select {
		case <-taskProcessed:
			// Success: the task was dispatched and processed during shutdown
		default:
			t.Fatal("task polled during shutdown was not processed (dropped)")
		}

		select {
		case <-stopDone:
			// Stop completed cleanly
		default:
			t.Fatal("Stop() did not return after the poller completed")
		}
	})
}

func TestAutoscalingTaskNotDroppedDuringShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		taskProcessed := make(chan struct{}, 1)
		pollStarted := make(chan struct{}, 1)
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
		poller := newScalableTaskPoller(
			tp,
			ilog.NewNopLogger(),
			&pollerBehaviorAutoscaling{
				initialNumberOfPollers: 1,
				maximumNumberOfPollers: 2,
				minimumNumberOfPollers: 1,
			},
			"test",
			&atomic.Bool{},
			nil,
		)

		bw := newBaseWorker(baseWorkerOptions{
			slotSupplier:                 &testSlotSupplier{},
			maxTaskPerSecond:             1000,
			taskPollers:                  []scalableTaskPoller{poller},
			taskProcessor:                processor,
			workerType:                   "AutoscalingShutdownTest",
			logger:                       ilog.NewNopLogger(),
			stopTimeout:                  5 * time.Second,
			metricsHandler:               metrics.NopHandler,
			workerPollCompleteOnShutdown: workerPollCompleteOnShutdown,
		})

		bw.Start()
		<-pollStarted
		bw.noRepoll.Store(true)

		stopDone := make(chan struct{})
		go func() {
			bw.Stop()
			close(stopDone)
		}()

		<-bw.stopCh
		close(tp.returnTask)
		synctest.Wait()

		select {
		case <-taskProcessed:
		default:
			t.Fatal("task polled during autoscaling shutdown was not processed")
		}

		select {
		case <-stopDone:
		default:
			t.Fatal("Stop() did not return after the poller completed")
		}
	})
}

// effectivelyBlockedDispatchRate lets the first task consume the limiter's
// initial token while making the next task wait until the test cancels the
// limiter context.
const effectivelyBlockedDispatchRate = 0.001

func TestTaskDrainDuringShutdownRespectsDispatchRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		synctest.Wait()

		select {
		case <-taskProcessed:
		default:
			t.Fatal("first task polled during shutdown was not processed")
		}

		secondSent := make(chan struct{})
		go func() {
			bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}
			close(secondSent)
		}()
		synctest.Wait()
		select {
		case <-secondSent:
		default:
			t.Fatal("dispatcher did not receive the second task")
		}

		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		select {
		case <-taskProcessed:
			t.Fatal("second task should not bypass the dispatch rate during shutdown drain")
		default:
		}

		bw.taskLimiterContextCancel()
		close(bw.taskQueueCh)
		synctest.Wait()
		bw.stopWG.Wait()
	})
}

func TestTaskDrainAfterDispatchLimiterCancelReleasesUnprocessedTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		synctest.Wait()

		select {
		case <-taskProcessed:
		default:
			t.Fatal("first task polled during shutdown was not processed")
		}

		secondSent := make(chan struct{})
		go func() {
			bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}
			close(secondSent)
		}()
		synctest.Wait()
		select {
		case <-secondSent:
		default:
			t.Fatal("dispatcher did not receive the second task")
		}

		thirdSent := make(chan struct{})
		go func() {
			bw.taskQueueCh <- &polledTask{task: &testTask{}, permit: &SlotPermit{}}
			close(thirdSent)
		}()

		bw.taskLimiterContextCancel()
		synctest.Wait()
		select {
		case <-thirdSent:
		default:
			t.Fatal("dispatcher did not keep receiving after dispatch limiter cancellation")
		}
		close(bw.taskQueueCh)
		synctest.Wait()
		bw.stopWG.Wait()
		require.Empty(t, taskProcessed,
			"only the task dispatched before dispatch limiter cancellation should be processed")
		require.Equal(t, int32(1), slotSupplier.uses.Load(),
			"only the processed task should mark its slot used")
		require.Equal(t, int32(3), slotSupplier.releases.Load(),
			"processed and discarded tasks should all release their slots")
	})
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
			synctest.Test(t, func(t *testing.T) {
				taskProcessed := make(chan struct{}, 1)
				pollStarted := make(chan struct{}, 1)

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
				synctest.Wait()
				select {
				case <-pollStarted:
				default:
					t.Fatal("poller did not start before the system became quiescent")
				}

				// AggregatedWorker.Stop sets noRepoll before stopping base workers.
				bw.noRepoll.Store(true)

				stopDone := make(chan struct{})
				go func() {
					bw.Stop()
					close(stopDone)
				}()

				synctest.Wait()
				select {
				case <-bw.stopCh:
				default:
					t.Fatal("shutdown did not start")
				}
				close(tp.returnTask)
				synctest.Wait()

				select {
				case <-stopDone:
				default:
					t.Fatal("Stop() did not return after the poller completed")
				}

				select {
				case <-taskProcessed:
					t.Fatal("task polled during legacy shutdown was processed")
				default:
				}
			})
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

func (p *shutdownTaskPoller) PollTask(pollerGroupLease) (taskForWorker, error) {
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
	synctest.Test(t, func(t *testing.T) {
		pollStarted := make(chan struct{}, 1)
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

		start := time.Now()
		bw.Stop()
		require.Equal(t, 50*time.Millisecond, time.Since(start),
			"Stop() should return after stopTimeout, not wait for pollTaskServiceTimeOut")

		releaseBlockedPoller()
		require.True(t, awaitWaitGroup(&bw.stopWG, time.Second),
			"worker goroutines should finish after blocked poll is released")
	})
}

func TestLegacyStopReturnsPromptlyWithBlockedPoller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pollStarted := make(chan struct{}, 1)
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

		require.Zero(t, time.Since(start),
			"legacy Stop() should return promptly when a blocked poll observes shutdown")
		require.True(t, tp.stopped.Load(), "blocked poller should observe shutdown")
	})
}

type blockingShutdownPoller struct {
	pollStarted chan struct{}
	releasePoll chan struct{}
	started     atomic.Bool
}

func (p *blockingShutdownPoller) PollTask(pollerGroupLease) (taskForWorker, error) {
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

func (p *stopAwareShutdownPoller) PollTask(pollerGroupLease) (taskForWorker, error) {
	if p.started.CompareAndSwap(false, true) {
		close(p.pollStarted)
	}
	<-p.stopC
	p.stopped.Store(true)
	return nil, errStop
}

func (s *PollerAutoscalerSuite) TestAutoscaleDownOnTimeoutWithCapability() {
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount:        10,
		maxPollerCount:            10,
		minPollerCount:            1,
		serverSupportsAutoscaling: &atomic.Bool{},
	})
	ps.serverSupportsAutoscaling.Store(true)

	// Send 20 empty polls - should scale all the way down to min (1)
	for range 20 {
		ps.handleTask(newEmptyTask())
	}
	assert.Equal(s.T(), int64(1), ps.target.Load())
	assert.False(s.T(), ps.everSawScalingDecision.Load())
}

func (s *PollerAutoscalerSuite) TestAutoscaleDownOnTimeoutWithoutCapability() {
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount: 10,
		maxPollerCount:     10,
		minPollerCount:     1,
	})

	// Send 20 empty polls - should NOT scale down because we haven't seen a
	// scaling decision and server doesn't support autoscaling
	for range 20 {
		ps.handleTask(newEmptyTask())
	}
	// target never changed from initial
	assert.Equal(s.T(), int64(10), ps.target.Load())
}

func (s *PollerAutoscalerSuite) TestAutoscaleDownOnTimeoutClampsToMin() {
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount:        10,
		maxPollerCount:            10,
		minPollerCount:            3,
		serverSupportsAutoscaling: &atomic.Bool{},
	})
	ps.serverSupportsAutoscaling.Store(true)

	// Send 20 empty polls - should scale down but clamp at min (3)
	for range 20 {
		ps.handleTask(newEmptyTask())
	}
	assert.Equal(s.T(), int64(3), ps.target.Load())
	assert.False(s.T(), ps.everSawScalingDecision.Load())
}

func (s *PollerAutoscalerSuite) TestErrorScaleDownWithCapability() {
	ps := newPollerAutoscaler(pollerAutoscalerOptions{
		initialPollerCount:        8,
		maxPollerCount:            10,
		minPollerCount:            2,
		serverSupportsAutoscaling: &atomic.Bool{},
	})
	ps.serverSupportsAutoscaling.Store(true)

	// Should scale down on errors even without having seen a scaling decision
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), int64(4), ps.target.Load())
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), int64(3), ps.target.Load())
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

	ctx := t.Context()

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
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = pb.balance(ctx, "sticky")
	require.NoError(t, err, "balance should return nil when own count is 0, not block on other type's barrier")
}

// TestPollerBalancerBlocksWhenOtherTypeHasNoPollers verifies the normal blocking
// behavior: balance() blocks when another poller type has no active pollers, and
// unblocks once that type starts a poller.
func TestPollerBalancerBlocksWhenOtherTypeHasNoPollers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
			done <- pb.balance(t.Context(), "sticky")
		}()

		synctest.Wait()
		select {
		case <-done:
			t.Fatal("balance should be blocking")
		default:
		}

		// Start a non-sticky poller — this should unblock balance.
		pb.incrementPoller("non-sticky")
		synctest.Wait()
		require.NoError(t, <-done)
	})
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
				newBlockingProbeTaskPoller(),
				ilog.NewNopLogger(),
				behavior,
				tc.ptype,
				&atomic.Bool{},
				nil,
			)
			s.Equal(tc.ptype, poller.taskPollerType)
		})
	}
}

// sessionTokenWaitProbe is a token bucket lock that reports the first entry into
// waitForAvailableToken. Entry is the signal a test needs: the poller holds this lock from there
// until Cond.Wait parks it, and close() takes the same lock, so a Stop() beginning after the signal
// cannot overtake the waiter.
type sessionTokenWaitProbe struct {
	sync.Mutex
	entered chan struct{}
	once    sync.Once
}

func (p *sessionTokenWaitProbe) Lock() {
	p.Mutex.Lock()
	p.once.Do(func() { close(p.entered) })
}

// releaseRecordingSlotSupplier records the reason each permit is released with.
type releaseRecordingSlotSupplier struct{ released chan SlotReleaseReason }

func (s *releaseRecordingSlotSupplier) ReserveSlot(ctx context.Context, _ SlotReservationInfo) (*SlotPermit, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return &SlotPermit{}, nil
}
func (s *releaseRecordingSlotSupplier) TryReserveSlot(SlotReservationInfo) *SlotPermit {
	return &SlotPermit{}
}
func (s *releaseRecordingSlotSupplier) MarkSlotUsed(SlotMarkUsedInfo) {}
func (s *releaseRecordingSlotSupplier) ReleaseSlot(info SlotReleaseInfo) {
	select {
	case s.released <- info.Reason():
	default:
	}
}
func (s *releaseRecordingSlotSupplier) MaxSlots() int { return 0 }

// TestStopReturnsPromptlyWithPollerAwaitingSessionToken covers a session worker at its maximum session
// count: the creation poller parks in sessionTokenBucket.waitForAvailableToken. Stop() must wake it and
// the poller must give its slot back, rather than blocking for the whole stopTimeout while the poller
// goroutine and its permit leak.
func TestStopReturnsPromptlyWithPollerAwaitingSessionToken(t *testing.T) {
	probe := &sessionTokenWaitProbe{entered: make(chan struct{})}
	// No available token is the at-capacity state: every session slot is in use.
	bucket := &sessionTokenBucket{Cond: sync.NewCond(probe)}

	supplier := &releaseRecordingSlotSupplier{released: make(chan SlotReleaseReason, 1)}
	tp := newBlockingProbeTaskPoller()
	defer tp.Close()

	bw := newBaseWorker(baseWorkerOptions{
		slotSupplier:     supplier,
		maxTaskPerSecond: 1000,
		taskPollers: []scalableTaskPoller{
			{taskPollerType: "test", pollerCount: 1, taskPoller: tp},
		},
		taskProcessor:      noopTaskProcessor{},
		workerType:         "SessionTokenStopTest",
		logger:             ilog.NewNopLogger(),
		stopTimeout:        5 * time.Second,
		metricsHandler:     metrics.NopHandler,
		sessionTokenBucket: bucket,
	})

	bw.Start()
	<-probe.entered // the poller holds a permit and is inside the token wait

	start := time.Now()
	bw.Stop()
	require.Less(t, time.Since(start), time.Second,
		"Stop() should wake a poller waiting for a session token, not block for the full stopTimeout")

	// The permit is released before the poller's stopWG.Done, so Stop() returning orders this.
	select {
	case reason := <-supplier.released:
		require.Equal(t, SlotReleaseReasonUnused, reason)
	default:
		t.Fatal("poller left the session token wait without releasing its slot permit")
	}
}
