package worker

import (
	"fmt"

	"go.temporal.io/sdk/internal"
)

// Info is a point-in-time snapshot of a worker's state. Each field is read independently, so the
// snapshot is not atomic across fields.
//
// NOTE: Experimental
type Info = internal.WorkerInfo

// InfoProvider is implemented by workers that can report their state. Workers created by [New]
// implement it. A type that wraps a worker can implement it by forwarding to the wrapped worker.
//
// NOTE: Experimental
type InfoProvider interface {
	// Info returns a snapshot of the worker's state. It must be safe to call concurrently with any
	// other worker method.
	Info() Info
}

var _ InfoProvider = (*internal.AggregatedWorker)(nil)

// GetInfo returns a snapshot of w's state. It returns an error if w does not implement
// [InfoProvider].
//
// NOTE: Experimental
func GetInfo(w Worker) (Info, error) {
	provider, ok := w.(InfoProvider)
	if !ok {
		return Info{}, fmt.Errorf(
			"worker of type %T does not implement worker.InfoProvider; use a worker created by worker.New", w)
	}
	return provider.Info(), nil
}

// SlotUsage is a point-in-time snapshot of a worker's task slot usage, broken down by task kind.
// Each task kind is read independently, so the snapshot is not atomic across task kinds.
//
// NOTE: Experimental
type SlotUsage = internal.SlotUsage

// SlotKindUsage is a snapshot of the task slot usage for one task kind.
//
// NOTE: Experimental
type SlotKindUsage = internal.SlotKindUsage
