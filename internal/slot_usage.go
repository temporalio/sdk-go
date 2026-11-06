package internal

import "time"

// SlotUsage is a point-in-time snapshot of a worker's task slot usage, broken down by task kind.
// Each task kind is read independently, so the snapshot is not atomic across task kinds.
//
// NOTE: Experimental
//
// Exposed as: [go.temporal.io/sdk/worker.SlotUsage]
type SlotUsage struct {
	// Workflow is the usage of workflow task slots.
	Workflow SlotKindUsage
	// Activity is the usage of activity task slots. It includes session activity and session
	// creation tasks, if sessions are enabled. A session creation task holds its slot for the life
	// of the session.
	Activity SlotKindUsage
	// LocalActivity is the usage of local activity slots.
	LocalActivity SlotKindUsage
	// Nexus is the usage of Nexus task slots.
	Nexus SlotKindUsage
}

// SlotKindUsage is a snapshot of the task slot usage for one task kind.
//
// NOTE: Experimental
//
// Exposed as: [go.temporal.io/sdk/worker.SlotKindUsage]
type SlotKindUsage struct {
	// UsedSlots is the number of tasks currently being processed. Slots reserved by pollers that
	// have not received a task are not counted.
	UsedSlots int
	// UsedSlotsLastChanged is when a task last started or finished, or the zero time if no task has
	// started since the worker was created. A task that starts and finishes between two snapshots
	// still changes it.
	UsedSlotsLastChanged time.Time
}

func (u SlotKindUsage) add(other SlotKindUsage) SlotKindUsage {
	lastChanged := u.UsedSlotsLastChanged
	if other.UsedSlotsLastChanged.After(lastChanged) {
		lastChanged = other.UsedSlotsLastChanged
	}
	return SlotKindUsage{
		UsedSlots:            u.UsedSlots + other.UsedSlots,
		UsedSlotsLastChanged: lastChanged,
	}
}

// SlotUsage returns a snapshot of the worker's task slot usage. It is safe to call concurrently
// with any other worker method, including before Start and after Stop. Task kinds whose workers
// have not been created yet, such as Nexus before Start, report zero usage.
func (aw *AggregatedWorker) SlotUsage() SlotUsage {
	var u SlotUsage
	if aw.workflowWorker != nil {
		u.Workflow = aw.workflowWorker.worker.slotSupplier.usage()
		u.LocalActivity = aw.workflowWorker.localActivityWorker.slotSupplier.usage()
	}
	if aw.activityWorker != nil {
		u.Activity = u.Activity.add(aw.activityWorker.worker.slotSupplier.usage())
	}
	if aw.sessionWorker != nil {
		u.Activity = u.Activity.add(aw.sessionWorker.activityWorker.worker.slotSupplier.usage())
		u.Activity = u.Activity.add(aw.sessionWorker.creationWorker.worker.slotSupplier.usage())
	}
	if nexusSlots := aw.nexusSlotSupplier.Load(); nexusSlots != nil {
		u.Nexus = nexusSlots.usage()
	}
	return u
}
