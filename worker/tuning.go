package worker

import (
	"go.temporal.io/sdk/internal"
)

// WorkerTuner allows for the dynamic customization of some aspects of worker behavior.
type WorkerTuner = internal.WorkerTuner

// SlotPermit is a permit to use a slot.
type SlotPermit = internal.SlotPermit

// SlotSupplier controls how slots are handed out for workflow and activity tasks as well as
// local activities when used in conjunction with a WorkerTuner.
type SlotSupplier = internal.SlotSupplier

// SlotReservationInfo contains information that SlotSupplier instances can use during
// reservation calls.
type SlotReservationInfo = internal.SlotReservationInfo

// SlotMarkUsedInfo contains information that SlotSupplier instances can use during
// SlotSupplier.MarkSlotUsed calls.
type SlotMarkUsedInfo = internal.SlotMarkUsedInfo

// SlotReleaseInfo contains information that SlotSupplier instances can use during
// SlotSupplier.ReleaseSlot calls.
type SlotReleaseInfo = internal.SlotReleaseInfo

// FixedSizeTunerOptions are the options used by NewFixedSizeTuner.
type FixedSizeTunerOptions = internal.FixedSizeTunerOptions

// CompositeTunerOptions are the options used by NewCompositeTuner.
type CompositeTunerOptions = internal.CompositeTunerOptions

// NewFixedSizeTuner creates a WorkerTuner that uses fixed size slot suppliers.
func NewFixedSizeTuner(options FixedSizeTunerOptions) (WorkerTuner, error) {
	return internal.NewFixedSizeTuner(options)
}

// NewCompositeTuner creates a WorkerTuner that uses a combination of slot suppliers.
func NewCompositeTuner(options CompositeTunerOptions) (WorkerTuner, error) {
	return internal.NewCompositeTuner(options)
}

// NewFixedSizeSlotSupplier creates a new FixedSizeSlotSupplier with the given number of slots.
func NewFixedSizeSlotSupplier(numSlots int) (SlotSupplier, error) {
	return internal.NewFixedSizeSlotSupplier(numSlots)
}
