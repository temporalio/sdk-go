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
	// Reservations include polls waiting for slots. A poll remains counted until
	// its lease ends, even if its group disappears from the latest snapshot.
	reservations workflowKindCounts
	// Active polls have acquired slots and count toward queue-kind fairness.
	active workflowKindCounts
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
	reservations  workflowKindCounts
	stickyBacklog int64
}

// workflowKindCounts stores separate normal and sticky totals. The balancer
// uses it both for reservation and active-poll accounting.
type workflowKindCounts struct {
	normal int
	sticky int
}

type workflowGroupCandidates struct {
	groups map[string]pollerGroupSnapshotEntry
	// coverageRequired means this poll kind must restore minimum per-group coverage.
	coverageRequired bool
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

// waitForPollTurn waits until group coverage, sticky backlog, and queue-kind
// fairness allow this poll kind to proceed.
func (a *workflowAutoscalingBalancer) waitForPollTurn(
	ctx context.Context,
	kind enumspb.TaskQueueKind,
) error {
	if !validAdmissionKind(kind) {
		return errors.New(invalidAdmissionKindMessage)
	}

	for {
		a.mu.Lock()
		var snapshot pollerGroupSnapshot
		var groupsChanged <-chan struct{}
		if a.groupStore != nil {
			snapshot, groupsChanged = a.syncGroupsLocked()
		}
		if a.canTakeTurn(kind, snapshot.groups) {
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
		a.mu.Lock()
		var snapshot pollerGroupSnapshot
		var groupsChanged <-chan struct{}
		if a.groupStore != nil {
			snapshot, groupsChanged = a.syncGroupsLocked()
		}
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
	candidates, ok := a.eligibleGroups(kind, snapshot.groups)
	if !ok {
		return pollerGroupLease{}, false
	}
	if len(snapshot.groups) == 0 {
		a.reservations.change(kind, 1)
		a.wakeWaiters()
		return pollerGroupLease{owner: a, kind: kind}, true
	}

	return a.reserveCandidate(kind, candidates.groups), true
}

// canTakeTurn applies queue-kind fairness before runner capacity is consumed.
func (a *workflowAutoscalingBalancer) canTakeTurn(
	kind enumspb.TaskQueueKind,
	groups map[string]pollerGroupSnapshotEntry,
) bool {
	candidates, ok := a.eligibleGroups(kind, groups)
	if !ok {
		return false
	}

	// Required coverage takes precedence over queue-kind fairness.
	if candidates.coverageRequired {
		return true
	}

	return a.canPollKind(kind)
}

// eligibleGroups returns groups this kind may target under coverage and backlog
// policy. It returns false when this kind must wait. Queue-kind fairness is
// outside this function.
func (a *workflowAutoscalingBalancer) eligibleGroups(
	kind enumspb.TaskQueueKind,
	groups map[string]pollerGroupSnapshotEntry,
) (workflowGroupCandidates, bool) {
	if len(groups) == 0 {
		return workflowGroupCandidates{}, true
	}

	other := otherQueueKind(kind)
	missing := a.coverageCandidates(kind, groups)
	otherMissing := a.coverageCandidates(other, groups)
	if len(missing) > 0 {
		if len(otherMissing) > 0 && a.reservations.forKind(kind) > a.reservations.forKind(other) {
			return workflowGroupCandidates{}, false
		}

		return workflowGroupCandidates{
			groups:           missing,
			coverageRequired: true,
		}, true
	}
	if len(otherMissing) > 0 {
		return workflowGroupCandidates{}, false
	}

	sticky := a.stickyCandidates(groups)
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		if len(sticky) == 0 || !a.stickyCanGrow() {
			return workflowGroupCandidates{}, false
		}

		return workflowGroupCandidates{groups: sticky}, true
	}
	if len(sticky) > 0 && a.stickyCanGrow() {
		return workflowGroupCandidates{}, false
	}

	return workflowGroupCandidates{groups: groups}, true
}

// canPollKind applies slot-backed queue-kind fairness. The caller holds mu.
func (a *workflowAutoscalingBalancer) canPollKind(kind enumspb.TaskQueueKind) bool {
	switch kind {
	case enumspb.TASK_QUEUE_KIND_NORMAL:
		// Always allow a first normal poll.
		if a.active.normal == 0 {
			return true
		}
		// Preserve capacity for the first sticky poll.
		if a.active.sticky == 0 && (!a.hasFiniteCapacity() || a.active.normal+1 >= a.maxSlots) {
			return false
		}
		// Prefer sticky when it can help drain the backlog.
		if a.needsMoreStickyPolls() {
			return false
		}
	case enumspb.TASK_QUEUE_KIND_STICKY:
		// Always allow a first sticky poll.
		if a.active.sticky == 0 {
			return true
		}
		// Preserve capacity for the first normal poll.
		if a.active.normal == 0 && (!a.hasFiniteCapacity() || a.active.sticky+1 >= a.maxSlots) {
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

	return !a.hasFiniteCapacity() || a.active.total() < a.maxSlots
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

	a.mu.Lock()
	snapshot, _ := a.syncGroupsLocked()
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

// syncGroupsLocked reconciles local groups with the current store snapshot.
// The caller must hold a.mu so snapshots cannot be applied out of order.
func (a *workflowAutoscalingBalancer) syncGroupsLocked() (pollerGroupSnapshot, <-chan struct{}) {
	snapshot, changed := a.groupStore.observe()
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

	return snapshot, changed
}

func (r *workflowKindCounts) forKind(kind enumspb.TaskQueueKind) int {
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		return r.sticky
	}

	return r.normal
}

func (r *workflowKindCounts) change(kind enumspb.TaskQueueKind, change int) {
	if kind == enumspb.TASK_QUEUE_KIND_STICKY {
		r.sticky += change
		return
	}
	if kind != enumspb.TASK_QUEUE_KIND_NORMAL {
		panic(invalidAdmissionKindMessage)
	}

	r.normal += change
}

func (r *workflowKindCounts) total() int {
	return r.normal + r.sticky
}

// start marks a reservation active after it acquires a slot.
func (a *workflowAutoscalingBalancer) start(kind enumspb.TaskQueueKind) {
	a.mu.Lock()
	a.active.change(kind, 1)
	a.wakeWaiters()
	a.mu.Unlock()
}

// releaseActivePoll atomically releases an active poll and its reservation.
func (a *workflowAutoscalingBalancer) releaseActivePoll(lease pollerGroupLease) {
	a.mu.Lock()
	a.active.change(lease.kind, -1)
	a.releaseReservationLocked(lease)
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) releaseReservation(lease pollerGroupLease) {
	a.mu.Lock()
	a.releaseReservationLocked(lease)
	a.wakeWaiters()
	a.mu.Unlock()
}

func (a *workflowAutoscalingBalancer) releaseReservationLocked(lease pollerGroupLease) {
	if lease.group.id != "" {
		group := a.groups[lease.group.id]
		if group != nil && group.key == lease.group && group.reservations.forKind(lease.kind) > 0 {
			group.reservations.change(lease.kind, -1)
		}
	}
	if a.reservations.forKind(lease.kind) > 0 {
		a.reservations.change(lease.kind, -1)
	}
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
	snapshot, _ := a.syncGroupsLocked()
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
