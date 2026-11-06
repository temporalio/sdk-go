package worker

import (
	"fmt"

	"go.temporal.io/sdk/internal"
)

// SlotUsage is a point-in-time snapshot of a worker's task slot usage, broken down by task kind.
// Each task kind is read independently, so the snapshot is not atomic across task kinds.
//
// NOTE: Experimental
type SlotUsage = internal.SlotUsage

// SlotKindUsage is a snapshot of the task slot usage for one task kind.
//
// NOTE: Experimental
type SlotKindUsage = internal.SlotKindUsage

// SlotUsageProvider is implemented by workers that can report their task slot usage. Workers
// created by [New] implement it. A type that wraps a worker can implement it by forwarding to the
// wrapped worker.
//
// NOTE: Experimental
type SlotUsageProvider interface {
	// SlotUsage returns a snapshot of the worker's task slot usage. It must be safe to call
	// concurrently with any other worker method.
	SlotUsage() SlotUsage
}

var _ SlotUsageProvider = (*internal.AggregatedWorker)(nil)

// GetSlotUsage returns a snapshot of w's task slot usage. It returns an error if w does not
// implement [SlotUsageProvider].
//
// NOTE: Experimental
func GetSlotUsage(w Worker) (SlotUsage, error) {
	provider, ok := w.(SlotUsageProvider)
	if !ok {
		return SlotUsage{}, fmt.Errorf(
			"worker of type %T does not implement worker.SlotUsageProvider; use a worker created by "+
				"worker.New", w)
	}
	return provider.SlotUsage(), nil
}
