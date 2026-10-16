package worker_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"
)

type workerWithoutSlotUsage struct {
	worker.Worker
}

type workerWithSlotUsage struct {
	worker.Worker
	usage worker.SlotUsage
}

func (w workerWithSlotUsage) SlotUsage() worker.SlotUsage {
	return w.usage
}

func TestGetSlotUsage(t *testing.T) {
	_, err := worker.GetSlotUsage(workerWithoutSlotUsage{})
	require.ErrorContains(t, err, "does not implement worker.SlotUsageProvider")

	_, err = worker.GetSlotUsage(nil)
	require.ErrorContains(t, err, "does not implement worker.SlotUsageProvider")

	want := worker.SlotUsage{Activity: worker.SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: time.Unix(1, 0)}}
	got, err := worker.GetSlotUsage(workerWithSlotUsage{usage: want})
	require.NoError(t, err)
	require.Equal(t, want, got)
}
