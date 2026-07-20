package internal

import (
	"math/rand"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

type (
	pollerGroupInfoStore struct {
		mu         sync.RWMutex
		weights    map[string]float32
		version    int64
		versionSet bool
	}

	// pollerGroupManager gives concrete pollers a lease-based API for assigning
	// poll requests to groups. Group membership and weights are shared across
	// managers, while each tracker owns its poll coverage and sticky backlog.
	pollerGroupManager struct {
		groupInfos *pollerGroupInfoStore
		tracker    *pollerGroupTracker
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

func newPollerGroupInfoStore() *pollerGroupInfoStore {
	return &pollerGroupInfoStore{weights: make(map[string]float32)}
}

func newPollerGroupManager(workflow bool, groupInfos *pollerGroupInfoStore) *pollerGroupManager {
	if groupInfos == nil {
		groupInfos = newPollerGroupInfoStore()
	}
	return &pollerGroupManager{
		groupInfos: groupInfos,
		tracker:    newPollerGroupTracker(workflow),
	}
}

func (m *pollerGroupManager) requiredMin(queueKind enumspb.TaskQueueKind) int {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return 0
	}
	if m.tracker.workflow && queueKind != enumspb.TASK_QUEUE_KIND_NORMAL && queueKind != enumspb.TASK_QUEUE_KIND_STICKY {
		return 0
	}
	return m.groupInfos.len()
}

func (m *pollerGroupManager) reserve() pollerGroupLease {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return pollerGroupLease{}
	}
	groupID := m.tracker.reserve(m.groupInfos.snapshot())
	return pollerGroupLease{
		manager: m,
		groupID: groupID,
	}
}

func (m *pollerGroupManager) reserveWorkflowPoll(preferredQueueKind enumspb.TaskQueueKind, stickyEnabled bool) pollerGroupLease {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return pollerGroupLease{queueKind: preferredQueueKind}
	}
	groupID, queueKind := m.tracker.reserveWorkflowPoll(m.groupInfos.snapshot(), preferredQueueKind, stickyEnabled)
	return pollerGroupLease{
		manager:   m,
		groupID:   groupID,
		queueKind: queueKind,
	}
}

func (m *pollerGroupManager) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	if m == nil || m.groupInfos == nil {
		return
	}
	m.groupInfos.updateGroups(info)
}

func (m *pollerGroupManager) updateStickyBacklog(groupID string, backlogCountHint int64) {
	if m == nil || m.groupInfos == nil || m.tracker == nil || groupID == "" {
		return
	}
	m.tracker.updateStickyBacklog(m.groupInfos.snapshot(), groupID, backlogCountHint)
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

func (t *pollerGroupTracker) reserve(weights map[string]float32) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncGroups(weights)

	if len(t.groups) == 0 {
		return ""
	}

	candidates := make(map[string]float32, len(t.groups))
	for groupID, group := range t.groups {
		if group.pendingPollCount == 0 {
			candidates[groupID] = weights[groupID]
		}
	}
	if len(candidates) == 0 {
		candidates = weights
	}

	groupID := choosePollerGroup(candidates)
	if groupID == "" {
		return ""
	}
	t.groups[groupID].pendingPollCount++
	return groupID
}

// reserveWorkflowPoll first satisfies per-group MCN coverage by selecting the
// highest-weight group that is missing normal or sticky coverage. For that
// selected group, it uses preferredQueueKind first when that kind's coverage is
// missing, then fills whichever required kind is still missing. Once coverage
// is met, it chooses a group by server-provided weight and then chooses that
// group's workflow queue kind from its sticky backlog state.
func (t *pollerGroupTracker) reserveWorkflowPoll(
	weights map[string]float32,
	preferredQueueKind enumspb.TaskQueueKind,
	stickyEnabled bool,
) (string, enumspb.TaskQueueKind) {
	if t == nil {
		return "", preferredQueueKind
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncGroups(weights)

	if len(t.groups) == 0 {
		return "", preferredQueueKind
	}

	groupID := chooseHighestWeightPollerGroup(t.workflowCoverageCandidates(weights, stickyEnabled))
	var queueKind enumspb.TaskQueueKind
	if groupID != "" {
		queueKind = t.workflowCoverageQueueKind(t.groups[groupID], preferredQueueKind, stickyEnabled)
	} else {
		groupID = choosePollerGroup(weights)
		if groupID == "" {
			return "", preferredQueueKind
		}
		queueKind = t.workflowFloatingQueueKind(t.groups[groupID], stickyEnabled)
	}

	if queueKind == enumspb.TASK_QUEUE_KIND_STICKY {
		t.groups[groupID].workflowPendingSticky++
	} else {
		t.groups[groupID].workflowPendingNormal++
	}
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

func (t *pollerGroupTracker) updateStickyBacklog(weights map[string]float32, groupID string, backlogCountHint int64) {
	if t == nil || !t.workflow || groupID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncGroups(weights)

	group, ok := t.groups[groupID]
	if !ok {
		return
	}
	group.workflowStickyBacklog = backlogCountHint
}

func (t *pollerGroupTracker) syncGroups(weights map[string]float32) {
	if t.groups == nil {
		t.groups = make(map[string]*pollerGroupState)
	}
	for groupID := range weights {
		if _, ok := t.groups[groupID]; !ok {
			t.groups[groupID] = &pollerGroupState{}
		}
	}
	for groupID := range t.groups {
		if _, ok := weights[groupID]; !ok {
			delete(t.groups, groupID)
		}
	}
}

func (t *pollerGroupTracker) workflowCoverageCandidates(weights map[string]float32, stickyEnabled bool) map[string]float32 {
	candidates := make(map[string]float32)
	for groupID, group := range t.groups {
		if group.workflowPendingNormal == 0 || stickyEnabled && group.workflowPendingSticky == 0 {
			candidates[groupID] = weights[groupID]
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

func chooseHighestWeightPollerGroup(groups map[string]float32) string {
	var selectedID string
	var selectedWeight float32
	for groupID, weight := range groups {
		if selectedID == "" || weight > selectedWeight {
			selectedID = groupID
			selectedWeight = weight
		}
	}
	return selectedID
}

// choosePollerGroup picks a random group using the configured weights.
// If all weights are zero or negative, it picks uniformly from all groups.
// If floating-point rounding prevents the weighted walk from selecting a group,
// it falls back to the last positive-weight candidate encountered.
func choosePollerGroup(groups map[string]float32) string {
	if len(groups) == 0 {
		return ""
	}

	totalWeight := float32(0)
	for _, weight := range groups {
		if weight > 0 {
			totalWeight += weight
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
	for groupID, weight := range groups {
		if weight <= 0 {
			continue
		}
		lastCandidate = groupID
		if point < weight {
			return groupID
		}
		point -= weight
	}

	// Floating-point rounding fallback.
	return lastCandidate
}

func (s *pollerGroupInfoStore) len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.weights)
}

func (s *pollerGroupInfoStore) snapshot() map[string]float32 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	weights := make(map[string]float32, len(s.weights))
	for groupID, weight := range s.weights {
		weights[groupID] = weight
	}
	return weights
}

func (s *pollerGroupInfoStore) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	if s == nil || info == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versionSet && info.GetVersion() <= s.version {
		return
	}

	weights := make(map[string]float32, len(info.GetPollerGroups()))
	for _, group := range info.GetPollerGroups() {
		if groupID := group.GetId(); groupID != "" {
			weights[groupID] = group.GetWeight()
		}
	}

	s.weights = weights
	s.version = info.GetVersion()
	s.versionSet = true
}
