package internal

import (
	"context"
	"errors"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
)

const (
	// One queued sticky task does not justify suppressing normal polls.
	minMeaningfulStickyBacklog          int64 = 2
	invalidAdmissionKindMessage               = "workflow slot admission requires a normal or sticky queue kind"
	inconsistentWorkflowBalancerMessage       = "workflow pollers must share one poll balancer"
)

type pollerTarget func() int64

// workflowAutoscalingBalancer preserves both queue kinds and prioritizes sticky backlog
// when split autoscaling workflow pollers share finite capacity.
type workflowAutoscalingBalancer struct {
	maxSlots      int
	normalActive  int
	stickyActive  int
	stickyBacklog int64
	stickyTarget  pollerTarget
	wakeCh        chan struct{}
	mu            sync.Mutex
}

func newWorkflowAutoscalingBalancer(maxSlots int, stickyTarget pollerTarget) *workflowAutoscalingBalancer {
	return &workflowAutoscalingBalancer{
		maxSlots:     maxSlots,
		stickyTarget: stickyTarget,
		wakeCh:       make(chan struct{}),
	}
}

func (a *workflowAutoscalingBalancer) hasFiniteCapacity() bool {
	return a.maxSlots > 0
}

// waitForAdmission blocks until kind may compete for a slot or ctx ends.
func (a *workflowAutoscalingBalancer) waitForAdmission(ctx context.Context, kind enumspb.TaskQueueKind) error {
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
func (a *workflowAutoscalingBalancer) canAdmit(kind enumspb.TaskQueueKind) bool {
	// Without a finite limit, only prevent either kind from starting a second
	// poll before the other kind starts its first.
	if a.maxSlots <= 0 {
		switch kind {
		case enumspb.TASK_QUEUE_KIND_NORMAL:
			return a.normalActive == 0 || a.stickyActive > 0
		case enumspb.TASK_QUEUE_KIND_STICKY:
			return a.stickyActive == 0 || a.normalActive > 0
		default:
			return false
		}
	}

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

func (a *workflowAutoscalingBalancer) needsMoreStickyPolls() bool {
	// Sticky priority only helps while the scaler can start another sticky poll.
	return a.stickyBacklog >= minMeaningfulStickyBacklog &&
		a.stickyBacklog > int64(a.stickyActive) &&
		int64(a.stickyActive) < a.stickyTarget()
}

func (a *workflowAutoscalingBalancer) start(kind enumspb.TaskQueueKind) {
	a.changeActive(kind, 1)
}

func (a *workflowAutoscalingBalancer) finish(kind enumspb.TaskQueueKind) {
	a.changeActive(kind, -1)
}

func (a *workflowAutoscalingBalancer) changeActive(kind enumspb.TaskQueueKind, change int) {
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

func (a *workflowAutoscalingBalancer) setStickyBacklog(backlog int64) {
	a.mu.Lock()
	if backlog == a.stickyBacklog {
		a.mu.Unlock()
		return
	}

	a.stickyBacklog = backlog
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) wakeWaiters() {
	close(a.wakeCh)
	a.wakeCh = make(chan struct{})
}
