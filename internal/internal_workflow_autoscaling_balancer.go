package internal

import (
	"context"
	"errors"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
)

const (
	// One queued sticky task does not justify suppressing normal polls.
	minMeaningfulStickyBacklog        int64 = 2
	invalidAdmissionKindMessage             = "workflow poll admission requires a normal or sticky queue kind"
	missingPollerBalancerMessage            = "workers with multiple task pollers require a poll balancer"
	inconsistentPollerBalancerMessage       = "task pollers must share the same poll balancer"
)

// workflowAutoscalingBalancer coordinates autoscaling Workflow polls across
// queue kinds and groups. Other grouped polls use pollerGroupManager.
type workflowAutoscalingBalancer struct {
	maxSlots int
	// Aggregate reservations include removed incarnations until their leases end.
	reservations workflowReservations
	// ungroupedStickyBacklog holds the backlog hint when no groups are known.
	ungroupedStickyBacklog int64
	groupStore             *pollerGroupSnapshotStore
	// groups holds reservations and backlog hints for known poller groups.
	// When groups is non-empty, ungroupedStickyBacklog is zero.
	groups       map[string]*workflowGroupState
	stickyTarget int64
	wakeCh       chan struct{}
	mu           sync.Mutex
}

type workflowGroupState struct {
	key           pollerGroupKey
	reservations  workflowReservations
	stickyBacklog int64
}

type workflowReservations struct {
	normal int
	sticky int
}

func newWorkflowAutoscalingBalancer(
	maxSlots int,
	stickyTarget int64,
	groupStore *pollerGroupSnapshotStore,
) *workflowAutoscalingBalancer {
	return &workflowAutoscalingBalancer{
		maxSlots:     maxSlots,
		groupStore:   groupStore,
		groups:       make(map[string]*workflowGroupState),
		stickyTarget: stickyTarget,
		wakeCh:       make(chan struct{}),
	}
}

func (a *workflowAutoscalingBalancer) hasFiniteCapacity() bool {
	return a.maxSlots > 0
}

// waitForKind waits without consuming the runner's poll target.
func (a *workflowAutoscalingBalancer) waitForKind(
	ctx context.Context,
	kind enumspb.TaskQueueKind,
) error {
	if !validAdmissionKind(kind) {
		return errors.New(invalidAdmissionKindMessage)
	}

	for {
		var snapshot pollerGroupSnapshot
		var groupsChanged <-chan struct{}
		if a.groupStore != nil {
			snapshot, groupsChanged = a.groupStore.observe()
		}

		a.mu.Lock()
		if a.groupStore != nil {
			a.syncGroups(snapshot)
		}
		_, ok := a.eligibleGroups(kind, snapshot.groups)
		if ok {
			a.mu.Unlock()
			return nil
		}
		wakeCh := a.wakeCh
		a.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wakeCh:
		case <-groupsChanged:
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
			snapshot, groupsChanged = a.groupStore.observe()
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

func (a *workflowAutoscalingBalancer) tryAcquireLocked(
	kind enumspb.TaskQueueKind,
	snapshot pollerGroupSnapshot,
) (pollerGroupLease, bool) {
	if a.groupStore != nil {
		a.syncGroups(snapshot)
	}
	candidates, ok := a.eligibleGroups(kind, snapshot.groups)
	if !ok {
		return pollerGroupLease{}, false
	}
	if len(snapshot.groups) == 0 {
		a.reservations.change(kind, 1)
		a.wakeWaiters()
		return pollerGroupLease{owner: a, kind: kind}, true
	}

	return a.reserveCandidate(kind, candidates), true
}

// eligibleGroups returns the weighted groups kind may poll and whether it may
// poll now. Required queue-kind coverage precedes sticky backlog and weights.
func (a *workflowAutoscalingBalancer) eligibleGroups(
	kind enumspb.TaskQueueKind,
	groups map[string]pollerGroupSnapshotEntry,
) (map[string]pollerGroupSnapshotEntry, bool) {
	if len(groups) == 0 {
		return nil, a.canAdmitUngrouped(kind)
	}

	other := otherQueueKind(kind)
	missing := a.coverageCandidates(kind, groups)
	otherMissing := a.coverageCandidates(other, groups)
	if len(missing) > 0 {
		if len(otherMissing) > 0 && a.reservations.forKind(kind) > a.reservations.forKind(other) {
			return nil, false
		}

		return missing, true
	}
	if len(otherMissing) > 0 {
		return nil, false
	}
	if a.hasFiniteCapacity() && a.reservations.total() >= a.maxSlots {
		return nil, false
	}

	sticky := a.stickyCandidates(groups)
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		if len(sticky) == 0 || !a.stickyCanGrow() {
			return nil, false
		}

		return sticky, true
	}
	if len(sticky) > 0 && a.stickyCanGrow() {
		return nil, false
	}

	return groups, true
}

// canAdmitUngrouped applies the queue-kind policy. The caller holds mu.
func (a *workflowAutoscalingBalancer) canAdmitUngrouped(kind enumspb.TaskQueueKind) bool {
	switch kind {
	case enumspb.TASK_QUEUE_KIND_NORMAL:
		// Always allow a first normal poll.
		if a.reservations.normal == 0 {
			return true
		}
		// Preserve capacity for the first sticky poll.
		if a.reservations.sticky == 0 && (!a.hasFiniteCapacity() || a.reservations.normal+1 >= a.maxSlots) {
			return false
		}
		// Prefer sticky when it can help drain the backlog.
		if a.needsMoreStickyPolls() {
			return false
		}
	case enumspb.TASK_QUEUE_KIND_STICKY:
		// Always allow a first sticky poll.
		if a.reservations.sticky == 0 {
			return true
		}
		// Preserve capacity for the first normal poll.
		if a.reservations.normal == 0 && (!a.hasFiniteCapacity() || a.reservations.sticky+1 >= a.maxSlots) {
			return false
		}
		if int64(a.reservations.sticky) >= a.stickyTarget {
			return false
		}
		// Let sticky polls catch up with their backlog.
		if a.needsMoreStickyPolls() {
			return true
		}
	default:
		return false
	}

	return !a.hasFiniteCapacity() || a.reservations.total() < a.maxSlots
}

func (a *workflowAutoscalingBalancer) needsMoreStickyPolls() bool {
	// Sticky priority only helps while the scaler can start another sticky poll.
	return a.ungroupedStickyBacklog >= minMeaningfulStickyBacklog &&
		a.ungroupedStickyBacklog > int64(a.reservations.sticky) &&
		int64(a.reservations.sticky) < a.stickyTarget
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
	missing := len(a.coverageCandidates(kind, snapshot.groups)) > 0
	a.mu.Unlock()
	return missing
}

func otherQueueKind(kind enumspb.TaskQueueKind) enumspb.TaskQueueKind {
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		return enumspb.TASK_QUEUE_KIND_NORMAL
	}

	return enumspb.TASK_QUEUE_KIND_STICKY
}

func (a *workflowAutoscalingBalancer) coverageCandidates(
	kind enumspb.TaskQueueKind,
	groups map[string]pollerGroupSnapshotEntry,
) map[string]pollerGroupSnapshotEntry {
	candidates := make(map[string]pollerGroupSnapshotEntry)
	for groupID, group := range a.groups {
		if group.reservations.forKind(kind) == 0 {
			candidates[groupID] = groups[groupID]
		}
	}
	return candidates
}

func (a *workflowAutoscalingBalancer) stickyCandidates(
	groups map[string]pollerGroupSnapshotEntry,
) map[string]pollerGroupSnapshotEntry {
	candidates := make(map[string]pollerGroupSnapshotEntry)
	for groupID, group := range a.groups {
		if group.stickyBacklog >= minMeaningfulStickyBacklog &&
			group.stickyBacklog > int64(group.reservations.sticky) {
			candidates[groupID] = groups[groupID]
		}
	}
	return candidates
}

func (a *workflowAutoscalingBalancer) stickyCanGrow() bool {
	return int64(a.reservations.sticky) < a.stickyTarget
}

func (a *workflowAutoscalingBalancer) reserveCandidate(
	kind enumspb.TaskQueueKind,
	candidates map[string]pollerGroupSnapshotEntry,
) pollerGroupLease {
	groupID := choosePollerGroup(candidates)
	group := a.groups[groupID]
	group.reservations.change(kind, 1)
	a.reservations.change(kind, 1)
	a.wakeWaiters()
	return pollerGroupLease{
		owner: a,
		group: group.key,
		kind:  kind,
	}
}

func (a *workflowAutoscalingBalancer) syncGroups(snapshot pollerGroupSnapshot) {
	if len(snapshot.groups) > 0 {
		a.ungroupedStickyBacklog = 0
	}

	for groupID, entry := range snapshot.groups {
		group := a.groups[groupID]
		if group == nil || group.key != entry.key {
			a.groups[groupID] = &workflowGroupState{key: entry.key}
		}
	}
	for groupID := range a.groups {
		if _, ok := snapshot.groups[groupID]; !ok {
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

func (a *workflowAutoscalingBalancer) releaseReservation(lease pollerGroupLease) {
	a.mu.Lock()
	if lease.group.id != "" {
		group := a.groups[lease.group.id]
		if group != nil && group.key == lease.group && group.reservations.forKind(lease.kind) > 0 {
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
) {
	backlog = max(backlog, 0)
	a.mu.Lock()
	if a.groupStore == nil {
		a.mu.Unlock()
		return
	}
	snapshot := a.groupStore.snapshot()
	a.syncGroups(snapshot)
	if len(snapshot.groups) == 0 {
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
	if backlog == a.ungroupedStickyBacklog {
		return
	}

	a.ungroupedStickyBacklog = backlog
	a.wakeWaiters()
}

func (a *workflowAutoscalingBalancer) signal() {
	a.mu.Lock()
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) setStickyTarget(target int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if target == a.stickyTarget {
		return
	}

	a.stickyTarget = target
	a.wakeWaiters()
}

func (a *workflowAutoscalingBalancer) wakeWaiters() {
	close(a.wakeCh)
	a.wakeCh = make(chan struct{})
}
