package worker_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"
)

type workerWithoutInfo struct {
	worker.Worker
}

type workerWithInfo struct {
	worker.Worker
	info worker.Info
}

func (w workerWithInfo) Info() worker.Info {
	return w.info
}

func TestGetInfo(t *testing.T) {
	_, err := worker.GetInfo(workerWithoutInfo{})
	require.ErrorContains(t, err, "does not implement worker.InfoProvider")

	_, err = worker.GetInfo(nil)
	require.ErrorContains(t, err, "does not implement worker.InfoProvider")

	want := worker.Info{SlotUsage: worker.SlotUsage{
		Activity: worker.SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: time.Unix(1, 0)},
	}}
	got, err := worker.GetInfo(workerWithInfo{info: want})
	require.NoError(t, err)
	require.Equal(t, want, got)
}
