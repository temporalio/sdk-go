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

// workflowAutoscalingBalancer assigns queue kinds, groups, and shared workflow slots.
type workflowAutoscalingBalancer struct {
	maxSlots int
	// Aggregate reservations include removed generations until their leases end.
	reservations workflowReservations
	// stickyBacklog holds the backlog hint when no poller groups are known.
	stickyBacklog int64
	groupStore    *pollerGroupSnapshotStore
	// groups holds reservations and backlog hints for known poller groups.
	// When groups is non-empty, stickyBacklog is zero.
	groups       map[string]*workflowBalancerGroup
	stickyTarget pollerTarget
	wakeCh       chan struct{}
	mu           sync.Mutex
}

type workflowBalancerGroup struct {
	generation    int64
	reservations  workflowReservations
	stickyBacklog int64
}

type workflowReservations struct {
	normal int
	sticky int
}

func newWorkflowAutoscalingBalancer(
	maxSlots int,
	stickyTarget pollerTarget,
	groupStore *pollerGroupSnapshotStore,
) *workflowAutoscalingBalancer {
	return &workflowAutoscalingBalancer{
		maxSlots:     maxSlots,
		groupStore:   groupStore,
		groups:       make(map[string]*workflowBalancerGroup),
		stickyTarget: stickyTarget,
		wakeCh:       make(chan struct{}),
	}
}

func (a *workflowAutoscalingBalancer) hasFiniteCapacity() bool {
	return a.maxSlots > 0
}

// waitForKind keeps an unknown-capacity runner's blocked turn out of its target.
func (a *workflowAutoscalingBalancer) waitForKind(
	ctx context.Context,
	kind enumspb.TaskQueueKind,
) error {
	if !validAdmissionKind(kind) {
		return errors.New(invalidAdmissionKindMessage)
	}

	for {
		a.mu.Lock()
		active := a.reservations.forKind(kind)
		otherActive := a.reservations.forKind(otherQueueKind(kind))
		if active == 0 || otherActive > 0 {
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

// acquire reserves one queue kind and poller group.
func (a *workflowAutoscalingBalancer) acquire(
	ctx context.Context,
	kind enumspb.TaskQueueKind,
) (pollerGroupLease, error) {
	if !validAdmissionKind(kind) {
		return pollerGroupLease{}, errors.New(invalidAdmissionKindMessage)
	}

	for {
		var snapshot pollerGroupSnapshot
		var groupsChanged <-chan struct{}
		if a.groupStore != nil {
			groupsChanged = a.groupStore.changed()
			snapshot = a.groupStore.snapshot()
		}

		a.mu.Lock()
		lease, ok := a.tryAcquireLocked(kind, snapshot)
		if ok {
			a.mu.Unlock()
			return lease, nil
		}
		wakeCh := a.wakeCh
		a.mu.Unlock()

		select {
		case <-ctx.Done():
			return pollerGroupLease{}, ctx.Err()
		case <-wakeCh:
		case <-groupsChanged:
		}
	}
}

func validAdmissionKind(kind enumspb.TaskQueueKind) bool {
	return kind == enumspb.TASK_QUEUE_KIND_NORMAL || kind == enumspb.TASK_QUEUE_KIND_STICKY
}

// tryAcquireLocked expects a.mu to be held.
func (a *workflowAutoscalingBalancer) tryAcquireLocked(
	kind enumspb.TaskQueueKind,
	snapshot pollerGroupSnapshot,
) (pollerGroupLease, bool) {
	if a.groupStore != nil {
		a.syncGroups(snapshot)
	}
	if len(snapshot.weights) == 0 {
		if !a.canAdmitUngrouped(kind) {
			return pollerGroupLease{}, false
		}

		a.reservations.change(kind, 1)
		a.wakeWaiters()
		return pollerGroupLease{owner: a, kind: kind}, true
	}

	if a.hasFiniteCapacity() && !a.hasSlotFor(kind) {
		return pollerGroupLease{}, false
	}

	other := otherQueueKind(kind)
	missing := a.coverageCandidates(kind, snapshot.weights)
	otherMissing := a.coverageCandidates(other, snapshot.weights)
	if len(missing) > 0 {
		if len(otherMissing) > 0 && a.reservations.forKind(kind) > a.reservations.forKind(other) {
			return pollerGroupLease{}, false
		}

		return a.reserveCandidate(kind, missing), true
	}
	if len(otherMissing) > 0 {
		return pollerGroupLease{}, false
	}

	sticky := a.stickyCandidates(snapshot.weights)
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		if len(sticky) == 0 || !a.stickyCanGrow() {
			return pollerGroupLease{}, false
		}

		return a.reserveCandidate(kind, sticky), true
	}
	if a.hasFiniteCapacity() && len(sticky) > 0 && a.stickyCanGrow() {
		return pollerGroupLease{}, false
	}

	return a.reserveCandidate(kind, snapshot.weights), true
}

// canAdmitUngrouped applies the queue-kind policy. The caller holds mu.
func (a *workflowAutoscalingBalancer) canAdmitUngrouped(kind enumspb.TaskQueueKind) bool {
	// Without a finite limit, only prevent either kind from starting a second
	// poll before the other kind starts its first.
	if a.maxSlots <= 0 {
		switch kind {
		case enumspb.TASK_QUEUE_KIND_NORMAL:
			return a.reservations.normal == 0 || a.reservations.sticky > 0
		case enumspb.TASK_QUEUE_KIND_STICKY:
			return a.reservations.sticky == 0 || a.reservations.normal > 0
		default:
			return false
		}
	}

	switch kind {
	case enumspb.TASK_QUEUE_KIND_NORMAL:
		// Always allow a first normal poll.
		if a.reservations.normal == 0 {
			return true
		}
		// Preserve capacity for the first sticky poll.
		if a.reservations.sticky == 0 && a.reservations.normal+1 >= a.maxSlots {
			return false
		}
		// Prefer sticky if there is a sticky backlog
		if a.needsMoreStickyPolls() {
			return false
		}
	case enumspb.TASK_QUEUE_KIND_STICKY:
		// Always allow a first sticky poll.
		if a.reservations.sticky == 0 {
			return true
		}
		// Preserve capacity for the first normal poll.
		if a.reservations.normal == 0 && a.reservations.sticky+1 >= a.maxSlots {
			return false
		}
		if int64(a.reservations.sticky) >= a.stickyTarget() {
			return false
		}
		// Let sticky polls catch up with their backlog.
		if a.needsMoreStickyPolls() {
			return true
		}
	default:
		return false
	}

	return a.reservations.total() < a.maxSlots
}

func (a *workflowAutoscalingBalancer) needsMoreStickyPolls() bool {
	// Sticky priority only helps while the scaler can start another sticky poll.
	return a.stickyBacklog >= minMeaningfulStickyBacklog &&
		a.stickyBacklog > int64(a.reservations.sticky) &&
		int64(a.reservations.sticky) < a.stickyTarget()
}

func (a *workflowAutoscalingBalancer) setStickyBacklog(backlog int64) {
	a.mu.Lock()
	a.setStickyBacklogLocked(max(backlog, 0))
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) requiredMin() int {
	if a.groupStore == nil {
		return 0
	}

	return a.groupStore.len()
}

// hasCoverageGap reports whether kind may exceed its target to restore coverage.
func (a *workflowAutoscalingBalancer) hasCoverageGap(kind enumspb.TaskQueueKind) bool {
	if a.groupStore == nil {
		return false
	}

	snapshot := a.groupStore.snapshot()
	a.mu.Lock()
	a.syncGroups(snapshot)
	missing := len(a.coverageCandidates(kind, snapshot.weights)) > 0
	a.mu.Unlock()
	return missing
}

func (a *workflowAutoscalingBalancer) hasSlotFor(kind enumspb.TaskQueueKind) bool {
	total := a.reservations.total()
	if total >= a.maxSlots {
		return false
	}
	if total == 0 {
		return true
	}

	active := a.reservations.forKind(kind)
	otherActive := a.reservations.forKind(otherQueueKind(kind))
	if active == 0 {
		return true
	}

	return otherActive > 0 || total+1 < a.maxSlots
}

func otherQueueKind(kind enumspb.TaskQueueKind) enumspb.TaskQueueKind {
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		return enumspb.TASK_QUEUE_KIND_NORMAL
	}

	return enumspb.TASK_QUEUE_KIND_STICKY
}

func (a *workflowAutoscalingBalancer) coverageCandidates(
	kind enumspb.TaskQueueKind,
	weights map[string]float32,
) map[string]float32 {
	candidates := make(map[string]float32)
	for groupID, group := range a.groups {
		if group.reservations.forKind(kind) == 0 {
			candidates[groupID] = weights[groupID]
		}
	}
	return candidates
}

func (a *workflowAutoscalingBalancer) stickyCandidates(weights map[string]float32) map[string]float32 {
	candidates := make(map[string]float32)
	for groupID, group := range a.groups {
		if group.stickyBacklog >= minMeaningfulStickyBacklog &&
			group.stickyBacklog > int64(group.reservations.sticky) {
			candidates[groupID] = weights[groupID]
		}
	}
	return candidates
}

func (a *workflowAutoscalingBalancer) stickyCanGrow() bool {
	return int64(a.reservations.sticky) < a.stickyTarget()
}

func (a *workflowAutoscalingBalancer) reserveCandidate(
	kind enumspb.TaskQueueKind,
	candidates map[string]float32,
) pollerGroupLease {
	groupID := choosePollerGroup(candidates)
	group := a.groups[groupID]
	group.reservations.change(kind, 1)
	a.reservations.change(kind, 1)
	a.wakeWaiters()
	return pollerGroupLease{
		owner:      a,
		groupID:    groupID,
		generation: group.generation,
		kind:       kind,
	}
}

func (a *workflowAutoscalingBalancer) syncGroups(snapshot pollerGroupSnapshot) {
	if len(snapshot.weights) > 0 {
		a.stickyBacklog = 0
	}

	for groupID := range snapshot.weights {
		group := a.groups[groupID]
		if group == nil || group.generation != snapshot.generations[groupID] {
			a.groups[groupID] = &workflowBalancerGroup{
				generation: snapshot.generations[groupID],
			}
		}
	}
	for groupID := range a.groups {
		if _, ok := snapshot.weights[groupID]; !ok {
			delete(a.groups, groupID)
		}
	}
}

func (r *workflowReservations) forKind(kind enumspb.TaskQueueKind) int {
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		return r.sticky
	}

	return r.normal
}

func (r *workflowReservations) change(kind enumspb.TaskQueueKind, change int) {
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		r.sticky += change
		return
	}
	if kind != enumspb.TASK_QUEUE_KIND_NORMAL {
		panic(invalidAdmissionKindMessage)
	}

	r.normal += change
}

func (r *workflowReservations) total() int {
	return r.normal + r.sticky
}

func (a *workflowAutoscalingBalancer) releaseLease(lease pollerGroupLease) {
	a.mu.Lock()
	if lease.groupID != "" {
		group := a.groups[lease.groupID]
		if group != nil && group.generation == lease.generation && group.reservations.forKind(lease.kind) > 0 {
			group.reservations.change(lease.kind, -1)
		}
	}
	if a.reservations.forKind(lease.kind) > 0 {
		a.reservations.change(lease.kind, -1)
	}
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) setStickyGroupBacklog(
	groupID string,
	backlog int64,
	snapshot pollerGroupSnapshot,
) {
	backlog = max(backlog, 0)
	a.mu.Lock()
	a.syncGroups(snapshot)
	if len(snapshot.weights) == 0 {
		if groupID == "" {
			a.setStickyBacklogLocked(backlog)
		}
		a.mu.Unlock()
		return
	}

	group := a.groups[groupID]
	if group != nil && group.stickyBacklog != backlog {
		group.stickyBacklog = backlog
		a.wakeWaiters()
	}
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) setStickyBacklogLocked(backlog int64) {
	if backlog == a.stickyBacklog {
		return
	}

	a.stickyBacklog = backlog
	a.wakeWaiters()
}

func (a *workflowAutoscalingBalancer) signal() {
	a.mu.Lock()
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) wakeWaiters() {
	close(a.wakeCh)
	a.wakeCh = make(chan struct{})
}
