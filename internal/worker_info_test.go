package internal

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

func newTestTrackingSlotSupplier(t *testing.T, numSlots int) *trackingSlotSupplier {
	t.Helper()
	inner, err := NewFixedSizeSlotSupplier(numSlots)
	require.NoError(t, err)
	return newTrackingSlotSupplier(inner, trackingSlotSupplierOptions{
		logger:         ilog.NewNopLogger(),
		metricsHandler: metrics.NopHandler,
	})
}

func reserveTestSlot(t *testing.T, tss *trackingSlotSupplier) *SlotPermit {
	t.Helper()
	permit, err := tss.ReserveSlot(t.Context(), &slotReservationData{taskQueue: "test-task-queue"})
	require.NoError(t, err)
	return permit
}

func TestTrackingSlotSupplierUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tss := newTestTrackingSlotSupplier(t, 3)

		// Reserved slots, like those held by idle pollers, are not in use.
		used := reserveTestSlot(t, tss)
		unused := reserveTestSlot(t, tss)
		require.Equal(t, SlotKindUsage{}, tss.usage())

		time.Sleep(time.Second)
		startedAt := time.Now()
		tss.MarkSlotUsed(used)
		require.Equal(t, SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: startedAt}, tss.usage())

		// Empty polls are released without being marked used, and must not count as a change.
		time.Sleep(time.Second)
		tss.ReleaseSlot(unused, SlotReleaseReasonTaskProcessed)
		require.Equal(t, SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: startedAt}, tss.usage())

		time.Sleep(time.Second)
		finishedAt := time.Now()
		tss.ReleaseSlot(used, SlotReleaseReasonTaskProcessed)
		require.Equal(t, SlotKindUsage{UsedSlots: 0, UsedSlotsLastChanged: finishedAt}, tss.usage())
	})
}

func TestAggregatedWorkerSlotUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		require.Equal(t, WorkerInfo{}, (&AggregatedWorker{}).Info())

		workflowSlots := newTestTrackingSlotSupplier(t, 2)
		localActivitySlots := newTestTrackingSlotSupplier(t, 2)
		activitySlots := newTestTrackingSlotSupplier(t, 2)
		sessionActivitySlots := newTestTrackingSlotSupplier(t, 2)
		sessionCreationSlots := newTestTrackingSlotSupplier(t, 2)
		nexusSlots := newTestTrackingSlotSupplier(t, 2)

		aw := &AggregatedWorker{
			workflowWorker: &workflowWorker{
				worker:              &baseWorker{slotSupplier: workflowSlots},
				localActivityWorker: &baseWorker{slotSupplier: localActivitySlots},
			},
			activityWorker: &activityWorker{worker: &baseWorker{slotSupplier: activitySlots}},
			sessionWorker: &sessionWorker{
				activityWorker: &activityWorker{worker: &baseWorker{slotSupplier: sessionActivitySlots}},
				creationWorker: &activityWorker{worker: &baseWorker{slotSupplier: sessionCreationSlots}},
			},
		}

		start := time.Now()
		for _, tss := range []*trackingSlotSupplier{
			workflowSlots, localActivitySlots, activitySlots, sessionActivitySlots, sessionCreationSlots, nexusSlots,
		} {
			tss.MarkSlotUsed(reserveTestSlot(t, tss))
		}
		// The latest change across the activity workers wins.
		time.Sleep(time.Second)
		finished := reserveTestSlot(t, activitySlots)
		activitySlots.MarkSlotUsed(finished)
		time.Sleep(time.Second)
		activitySlots.ReleaseSlot(finished, SlotReleaseReasonTaskProcessed)

		// The Nexus worker isn't created until start.
		require.Equal(t, SlotUsage{
			Workflow:      SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: start},
			Activity:      SlotKindUsage{UsedSlots: 3, UsedSlotsLastChanged: start.Add(2 * time.Second)},
			LocalActivity: SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: start},
		}, aw.Info().SlotUsage)

		aw.nexusSlotSupplier.Store(nexusSlots)
		require.Equal(t, SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: start}, aw.Info().SlotUsage.Nexus)
	})
}
