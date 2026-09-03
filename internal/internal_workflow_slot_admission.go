package internal

import (
	"context"
	"errors"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
)

const (
	// One queued sticky task does not justify suppressing normal polls.
	minMeaningfulStickyBacklog           int64 = 2
	invalidAdmissionKindMessage                = "workflow slot admission requires a normal or sticky queue kind"
	inconsistentWorkflowAdmissionMessage       = "workflow pollers must share one slot admission coordinator"
)

type pollerTarget func() int64

// workflowSlotAdmission arbitrates normal and sticky polls before slot reservation.
type workflowSlotAdmission struct {
	maxSlots      int
	normalActive  int
	stickyActive  int
	stickyBacklog int64
	stickyTarget  pollerTarget
	wakeCh        chan struct{}
	mu            sync.Mutex
}

func newWorkflowSlotAdmission(maxSlots int, stickyTarget pollerTarget) *workflowSlotAdmission {
	// Zero means the supplier does not expose a finite upper bound, as permitted
	// for suppliers with dynamic capacity. Negative values are invalid.
	if maxSlots <= 0 {
		return nil
	}

	return &workflowSlotAdmission{
		maxSlots:     maxSlots,
		stickyTarget: stickyTarget,
		wakeCh:       make(chan struct{}),
	}
}

// waitForAdmission blocks until kind may compete for a slot or ctx ends.
func (a *workflowSlotAdmission) waitForAdmission(ctx context.Context, kind enumspb.TaskQueueKind) error {
	if kind != enumspb.TASK_QUEUE_KIND_NORMAL && kind != enumspb.TASK_QUEUE_KIND_STICKY {
		return errors.New(invalidAdmissionKindMessage)
	}

	for {
		a.mu.Lock()
		if a.canAdmit(kind) {
			a.mu.Unlock()
			return nil
		}
		wakeCh := a.wakeCh
		a.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wakeCh:
		}
	}
}

// canAdmit applies the queue-kind policy. The caller must hold mu.
func (a *workflowSlotAdmission) canAdmit(kind enumspb.TaskQueueKind) bool {
	switch kind {
	case enumspb.TASK_QUEUE_KIND_NORMAL:
		// Always allow a first normal poll.
		if a.normalActive == 0 {
			return true
		}
		// Preserve capacity for the first sticky poll.
		if a.stickyActive == 0 && a.normalActive+1 >= a.maxSlots {
			return false
		}
		// Prefer sticky if there is a sticky backlog
		if a.needsMoreStickyPolls() {
			return false
		}
	case enumspb.TASK_QUEUE_KIND_STICKY:
		// Always allow a first sticky poll.
		if a.stickyActive == 0 {
			return true
		}
		// Preserve capacity for the first normal poll.
		if a.normalActive == 0 && a.stickyActive+1 >= a.maxSlots {
			return false
		}
		// Let sticky polls catch up with their backlog.
		if a.needsMoreStickyPolls() {
			return true
		}
	default:
		return false
	}

	return a.normalActive+a.stickyActive < a.maxSlots
}

func (a *workflowSlotAdmission) needsMoreStickyPolls() bool {
	// Sticky priority only helps while the scaler can start another sticky poll.
	return a.stickyBacklog >= minMeaningfulStickyBacklog &&
		a.stickyBacklog > int64(a.stickyActive) &&
		int64(a.stickyActive) < a.stickyTarget()
}

func (a *workflowSlotAdmission) start(kind enumspb.TaskQueueKind) {
	a.changeActive(kind, 1)
}

func (a *workflowSlotAdmission) finish(kind enumspb.TaskQueueKind) {
	a.changeActive(kind, -1)
}

func (a *workflowSlotAdmission) changeActive(kind enumspb.TaskQueueKind, change int) {
	a.mu.Lock()
	switch kind {
	case enumspb.TASK_QUEUE_KIND_NORMAL:
		a.normalActive += change
	case enumspb.TASK_QUEUE_KIND_STICKY:
		a.stickyActive += change
	default:
		a.mu.Unlock()
		return
	}
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowSlotAdmission) setStickyBacklog(backlog int64) {
	a.mu.Lock()
	if backlog == a.stickyBacklog {
		a.mu.Unlock()
		return
	}

	a.stickyBacklog = backlog
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowSlotAdmission) wakeWaiters() {
	close(a.wakeCh)
	a.wakeCh = make(chan struct{})
}
