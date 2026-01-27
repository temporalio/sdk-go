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

// SystemInfoSupplier implementations provide information about system resources.
// Use contrib/hostinfo.NewSystemInfoSupplier() for a gopsutil-based implementation,
// or provide your own.
type SystemInfoSupplier = internal.SystemInfoSupplier

// SystemInfoContext provides context for SystemInfoSupplier calls.
type SystemInfoContext = internal.SystemInfoContext

// HasSystemInfoSupplier is an optional interface that SlotSupplier implementations can implement
// to expose their SystemInfoSupplier.
type HasSystemInfoSupplier = internal.HasSystemInfoSupplier

// ResourceBasedTunerOptions configures a resource-based tuner.
type ResourceBasedTunerOptions = internal.ResourceBasedTunerOptions

// NewResourceBasedTuner creates a WorkerTuner that dynamically adjusts the number of slots based
// on system resources. Specify the target CPU and memory usage as a value between 0 and 1.
//
// InfoSupplier is required - use contrib/hostinfo.NewSystemInfoSupplier() for a gopsutil-based
// implementation, or provide your own.
func NewResourceBasedTuner(opts ResourceBasedTunerOptions) (WorkerTuner, error) {
	return internal.NewResourceBasedTuner(opts)
}

// ResourceBasedSlotSupplierOptions configures a particular ResourceBasedSlotSupplier.
type ResourceBasedSlotSupplierOptions = internal.ResourceBasedSlotSupplierOptions

// ResourceBasedSlotSupplier is a SlotSupplier that issues slots based on system resource usage.
type ResourceBasedSlotSupplier = internal.ResourceBasedSlotSupplier

// NewResourceBasedSlotSupplier creates a ResourceBasedSlotSupplier given the provided
// ResourceController and ResourceBasedSlotSupplierOptions. All ResourceBasedSlotSupplier instances
// must use the same ResourceController.
func NewResourceBasedSlotSupplier(
	controller *ResourceController,
	options ResourceBasedSlotSupplierOptions,
) (*ResourceBasedSlotSupplier, error) {
	return internal.NewResourceBasedSlotSupplier(controller, options)
}

// ResourceControllerOptions contains configurable parameters for a ResourceController.
// It is recommended to use DefaultResourceControllerOptions to create a ResourceControllerOptions
// and only modify the mem/cpu target percent fields.
type ResourceControllerOptions = internal.ResourceControllerOptions

// ResourceController is used by ResourceBasedSlotSupplier to make decisions about whether slots
// should be issued based on system resource usage.
type ResourceController = internal.ResourceController

// NewResourceController creates a new ResourceController with the provided options.
// WARNING: It is important that you do not create multiple ResourceController instances. Since
// the controller looks at overall system resources, multiple instances with different configs can
// only conflict with one another.
func NewResourceController(options ResourceControllerOptions) *ResourceController {
	return internal.NewResourceController(options)
}

// DefaultResourceControllerOptions returns a ResourceControllerOptions with default values.
func DefaultResourceControllerOptions() ResourceControllerOptions {
	return internal.DefaultResourceControllerOptions()
}

// DefaultWorkflowResourceBasedSlotSupplierOptions returns default options for workflow slot suppliers.
func DefaultWorkflowResourceBasedSlotSupplierOptions() ResourceBasedSlotSupplierOptions {
	return internal.DefaultWorkflowResourceBasedSlotSupplierOptions()
}

// DefaultActivityResourceBasedSlotSupplierOptions returns default options for activity slot suppliers.
func DefaultActivityResourceBasedSlotSupplierOptions() ResourceBasedSlotSupplierOptions {
	return internal.DefaultActivityResourceBasedSlotSupplierOptions()
}
