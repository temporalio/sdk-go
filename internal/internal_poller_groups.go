package internal

import (
	"math/rand"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

type (
	// pollerGroupManager gives concrete pollers a lease-based API for assigning
	// poll requests to groups. This gives autoscaling runners access to the
	// MCN-required minimum, and notifies registered listeners when poller group membership changes. The
	// underlying tracker still owns the synchronized group state and selection
	// algorithm.
	pollerGroupManager struct {
		tracker *pollerGroupTracker

		listenersMu sync.Mutex
		listeners   []func()
	}

	// pollerGroupLease represents the SDK-local group reservation for one
	// poll request RPC. The lease tracks the request group ID only; response group IDs are
	// handled separately because the server may return a different group ID for
	// follow-up routing or sticky backlog attribution.
	pollerGroupLease struct {
		manager   *pollerGroupManager
		groupID   string
		queueKind enumspb.TaskQueueKind
	}

	pollerGroupTracker struct {
		workflow bool
		mu       sync.Mutex
		groups   map[string]*pollerGroupState
	}

	pollerGroupState struct {
		weight float32

		// Activity/Nexus use total pending
		pendingPollCount int

		// Workflow uses queue-kind-specific pending counts
		workflowPendingNormal int
		workflowPendingSticky int

		// Workflow-only. Updated from sticky poll responses using the response
		// group ID, which may differ from the request group ID.
		workflowStickyBacklog int64
	}
)

func newPollerGroupManager(workflow bool) *pollerGroupManager {
	return &pollerGroupManager{
		tracker: newPollerGroupTracker(workflow),
	}
}

func (m *pollerGroupManager) requiredMin(queueKind enumspb.TaskQueueKind) int {
	if m == nil || m.tracker == nil {
		return 0
	}
	return m.tracker.requiredMin(queueKind)
}

func (m *pollerGroupManager) reserve(queueKind enumspb.TaskQueueKind) pollerGroupLease {
	if m == nil || m.tracker == nil {
		return pollerGroupLease{}
	}
	groupID := m.tracker.reserve(queueKind)
	return pollerGroupLease{
		manager:   m,
		groupID:   groupID,
		queueKind: queueKind,
	}
}

func (m *pollerGroupManager) reserveWorkflowPoll(preferredQueueKind enumspb.TaskQueueKind, stickyEnabled bool) pollerGroupLease {
	if m == nil || m.tracker == nil {
		return pollerGroupLease{queueKind: preferredQueueKind}
	}
	groupID, queueKind := m.tracker.reserveWorkflowPoll(preferredQueueKind, stickyEnabled)
	return pollerGroupLease{
		manager:   m,
		groupID:   groupID,
		queueKind: queueKind,
	}
}

func (m *pollerGroupManager) updateGroups(groups []*taskqueuepb.PollerGroupInfo) {
	if m == nil || m.tracker == nil || len(groups) == 0 {
		return
	}
	if m.tracker.updateGroups(groups) {
		m.signal()
	}
}

func (m *pollerGroupManager) updateStickyBacklog(groupID string, backlogCountHint int64) {
	if m == nil || m.tracker == nil || groupID == "" {
		return
	}
	m.tracker.updateStickyBacklog(groupID, backlogCountHint)
}

func (m *pollerGroupManager) addListener(fn func()) {
	if m == nil || fn == nil {
		return
	}
	m.listenersMu.Lock()
	defer m.listenersMu.Unlock()
	m.listeners = append(m.listeners, fn)
}

func (m *pollerGroupManager) signal() {
	m.listenersMu.Lock()
	listeners := append([]func(){}, m.listeners...)
	m.listenersMu.Unlock()

	for _, fn := range listeners {
		fn()
	}
}

func (l pollerGroupLease) groupIDOrEmpty() string {
	return l.groupID
}

func (l pollerGroupLease) queueKindOr(fallback enumspb.TaskQueueKind) enumspb.TaskQueueKind {
	if l.queueKind == enumspb.TASK_QUEUE_KIND_UNSPECIFIED {
		return fallback
	}
	return l.queueKind
}

func (l pollerGroupLease) release() {
	if l.manager == nil || l.manager.tracker == nil || l.groupID == "" {
		return
	}
	l.manager.tracker.release(l.groupID, l.queueKind)
}

func newPollerGroupTracker(workflow bool) *pollerGroupTracker {
	return &pollerGroupTracker{
		workflow: workflow,
		mu:       sync.Mutex{},
		groups:   make(map[string]*pollerGroupState),
	}
}

func (t *pollerGroupTracker) requiredMin(queueKind enumspb.TaskQueueKind) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.workflow {
		return len(t.groups)
	}

	switch queueKind {
	case enumspb.TASK_QUEUE_KIND_NORMAL, enumspb.TASK_QUEUE_KIND_STICKY:
		return len(t.groups)
	default:
		return 0
	}
}

func (t *pollerGroupTracker) reserve(queueKind enumspb.TaskQueueKind) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.groups) == 0 {
		return ""
	}

	candidates := make(map[string]*pollerGroupState, len(t.groups))
	for groupID, group := range t.groups {
		if t.pendingCount(group, queueKind) == 0 {
			candidates[groupID] = group
		}
	}
	if len(candidates) == 0 {
		candidates = t.groups
	}

	groupID := choosePollerGroup(candidates)
	if groupID == "" {
		return ""
	}
	t.incrementPending(t.groups[groupID], queueKind)
	return groupID
}

// reserveWorkflowPoll first satisfies per-group MCN coverage by selecting a
// group that is missing normal or sticky coverage. For that selected group, it
// uses preferredQueueKind first when that kind's coverage is missing, then
// fills whichever required kind is still missing. Once coverage is met, it
// chooses a group by server-provided weight and then chooses that group's
// workflow queue kind from its sticky backlog state.
func (t *pollerGroupTracker) reserveWorkflowPoll(preferredQueueKind enumspb.TaskQueueKind, stickyEnabled bool) (string, enumspb.TaskQueueKind) {
	if t == nil {
		return "", preferredQueueKind
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.groups) == 0 {
		return "", preferredQueueKind
	}

	if groupID := choosePollerGroup(t.workflowCoverageCandidates(stickyEnabled)); groupID != "" {
		queueKind := t.workflowCoverageQueueKind(t.groups[groupID], preferredQueueKind, stickyEnabled)
		t.incrementPending(t.groups[groupID], queueKind)
		return groupID, queueKind
	}

	groupID := choosePollerGroup(t.groups)
	if groupID == "" {
		return "", preferredQueueKind
	}
	queueKind := t.workflowFloatingQueueKind(t.groups[groupID], stickyEnabled)
	t.incrementPending(t.groups[groupID], queueKind)
	return groupID, queueKind
}

func (t *pollerGroupTracker) release(groupID string, queueKind enumspb.TaskQueueKind) {
	if t == nil || groupID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	group, ok := t.groups[groupID]
	if !ok {
		return
	}
	t.decrementPending(group, queueKind)
}

func (t *pollerGroupTracker) updateGroups(groups []*taskqueuepb.PollerGroupInfo) bool {
	if t == nil || len(groups) == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.groups == nil {
		t.groups = make(map[string]*pollerGroupState)
	}

	seen := make(map[string]struct{}, len(groups))
	membershipChanged := false
	for _, incoming := range groups {
		groupID := incoming.GetId()
		if groupID == "" {
			continue
		}
		seen[groupID] = struct{}{}

		if existing, ok := t.groups[groupID]; ok {
			existing.weight = incoming.GetWeight()
			continue
		}
		t.groups[groupID] = &pollerGroupState{weight: incoming.GetWeight()}
		membershipChanged = true
	}

	for groupID := range t.groups {
		if _, ok := seen[groupID]; !ok {
			delete(t.groups, groupID)
			membershipChanged = true
		}
	}

	return membershipChanged
}

func (t *pollerGroupTracker) updateStickyBacklog(groupID string, backlogCountHint int64) {
	if t == nil || !t.workflow || groupID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	group, ok := t.groups[groupID]
	if !ok {
		return
	}
	group.workflowStickyBacklog = backlogCountHint
}

func (t *pollerGroupTracker) workflowCoverageCandidates(stickyEnabled bool) map[string]*pollerGroupState {
	candidates := make(map[string]*pollerGroupState)
	for groupID, group := range t.groups {
		if group.workflowPendingNormal == 0 || stickyEnabled && group.workflowPendingSticky == 0 {
			candidates[groupID] = group
		}
	}
	return candidates
}

func (t *pollerGroupTracker) workflowCoverageQueueKind(
	group *pollerGroupState,
	preferredQueueKind enumspb.TaskQueueKind,
	stickyEnabled bool,
) enumspb.TaskQueueKind {
	if stickyEnabled && preferredQueueKind == enumspb.TASK_QUEUE_KIND_STICKY && group.workflowPendingSticky == 0 {
		return enumspb.TASK_QUEUE_KIND_STICKY
	}
	if group.workflowPendingNormal == 0 {
		return enumspb.TASK_QUEUE_KIND_NORMAL
	}
	if stickyEnabled && group.workflowPendingSticky == 0 {
		return enumspb.TASK_QUEUE_KIND_STICKY
	}
	return preferredQueueKind
}

func (t *pollerGroupTracker) workflowFloatingQueueKind(group *pollerGroupState, stickyEnabled bool) enumspb.TaskQueueKind {
	if stickyEnabled && group.workflowStickyBacklog > int64(group.workflowPendingSticky) {
		return enumspb.TASK_QUEUE_KIND_STICKY
	}
	return enumspb.TASK_QUEUE_KIND_NORMAL
}

func (t *pollerGroupTracker) pendingCount(group *pollerGroupState, queueKind enumspb.TaskQueueKind) int {
	if group == nil {
		return 0
	}
	if !t.workflow {
		return group.pendingPollCount
	}
	if queueKind == enumspb.TASK_QUEUE_KIND_STICKY {
		return group.workflowPendingSticky
	}
	return group.workflowPendingNormal
}

func (t *pollerGroupTracker) incrementPending(group *pollerGroupState, queueKind enumspb.TaskQueueKind) {
	if group == nil {
		return
	}
	if !t.workflow {
		group.pendingPollCount++
		return
	}
	if queueKind == enumspb.TASK_QUEUE_KIND_STICKY {
		group.workflowPendingSticky++
	} else {
		group.workflowPendingNormal++
	}
}

func (t *pollerGroupTracker) decrementPending(group *pollerGroupState, queueKind enumspb.TaskQueueKind) {
	if group == nil {
		return
	}
	if !t.workflow {
		if group.pendingPollCount > 0 {
			group.pendingPollCount--
		}
		return
	}
	if queueKind == enumspb.TASK_QUEUE_KIND_STICKY {
		if group.workflowPendingSticky > 0 {
			group.workflowPendingSticky--
		}
	} else if group.workflowPendingNormal > 0 {
		group.workflowPendingNormal--
	}
}

// choosePollerGroup picks a random group using the configured weights.
// If all weights are zero or negative, it picks uniformly from all groups.
// If floating-point rounding prevents the weighted walk from selecting a group,
// it falls back to the last positive-weight candidate encountered.
func choosePollerGroup(groups map[string]*pollerGroupState) string {
	if len(groups) == 0 {
		return ""
	}

	totalWeight := float32(0)
	for _, group := range groups {
		if group.weight > 0 {
			totalWeight += group.weight
		}
	}

	// if all weights are 0, pick randomly
	if totalWeight <= 0 {
		selected := rand.Intn(len(groups))
		for groupID := range groups {
			if selected == 0 {
				return groupID
			}
			selected--
		}
		return ""
	}

	point := rand.Float32() * totalWeight
	var lastCandidate string
	for groupID, group := range groups {
		if group.weight <= 0 {
			continue
		}
		lastCandidate = groupID
		if point < group.weight {
			return groupID
		}
		point -= group.weight
	}

	// Floating-point rounding fallback.
	return lastCandidate
}
