package internal

import (
	"context"
	"errors"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/internal/common/metrics"
)

const (
	minMeaningfulStickyBacklog  int64 = 2
	invalidAdmissionKindMessage       = "workflow slot admission requires a normal or sticky queue kind"
)

// workflowSlotAdmission arbitrates normal and sticky polls before slot reservation.
type workflowSlotAdmission struct {
	maxSlots      int
	normalActive  int
	stickyActive  int
	stickyBacklog int64
	wakeCh        chan struct{}
	mu            sync.Mutex
}

type slotAdmissionPoller interface {
	setSlotAdmission(*workflowSlotAdmission)
}

func newWorkflowSlotAdmission(maxSlots int) *workflowSlotAdmission {
	// Zero means the supplier has no well-defined capacity. Invalid negative
	// values also disable capacity-aware admission.
	if maxSlots <= 0 {
		return nil
	}

	return &workflowSlotAdmission{
		maxSlots: maxSlots,
		wakeCh:   make(chan struct{}),
	}
}

// attachPollers configures exactly one autoscaling normal poller and one autoscaling sticky poller.
// It reports whether the input contained that pair.
func (a *workflowSlotAdmission) attachPollers(taskPollers []scalableTaskPoller) bool {
	if len(taskPollers) != 2 {
		return false
	}

	var normalWorker, stickyWorker *scalableTaskPoller
	var stickyPoller slotAdmissionPoller
	for i := range taskPollers {
		candidate := &taskPollers[i]
		if candidate.autoscalingRunner == nil {
			return false
		}

		switch candidate.taskPollerType {
		case metrics.PollerTypeWorkflowTask:
			if normalWorker != nil {
				return false
			}
			normalWorker = candidate
		case metrics.PollerTypeWorkflowStickyTask:
			if stickyWorker != nil {
				return false
			}
			poller, ok := candidate.taskPoller.(slotAdmissionPoller)
			if !ok {
				return false
			}
			stickyWorker = candidate
			stickyPoller = poller
		default:
			return false
		}
	}
	if normalWorker == nil || stickyWorker == nil {
		return false
	}

	normalWorker.admissionKind = enumspb.TASK_QUEUE_KIND_NORMAL
	stickyWorker.admissionKind = enumspb.TASK_QUEUE_KIND_STICKY
	stickyPoller.setSlotAdmission(a)
	return true
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
		if a.normalActive == 0 {
			return true
		}
		if a.stickyActive == 0 && a.normalActive+1 >= a.maxSlots {
			return false
		}
		if a.hasStickyBacklog() {
			return false
		}
	case enumspb.TASK_QUEUE_KIND_STICKY:
		if a.stickyActive == 0 {
			return true
		}
		if a.normalActive == 0 && a.stickyActive+1 >= a.maxSlots {
			return false
		}
		if a.hasStickyBacklog() {
			return true
		}
	default:
		panic(invalidAdmissionKindMessage)
	}

	return a.normalActive+a.stickyActive < a.maxSlots
}

func (a *workflowSlotAdmission) hasStickyBacklog() bool {
	return a.stickyBacklog >= minMeaningfulStickyBacklog && a.stickyBacklog > int64(a.stickyActive)
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
		panic(invalidAdmissionKindMessage)
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
