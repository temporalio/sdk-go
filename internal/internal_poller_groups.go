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
	// managers, while each tracker owns its poll coverage.
	pollerGroupManager struct {
		groupInfos *pollerGroupInfoStore
		tracker    *pollerGroupTracker
		wakeMu     sync.Mutex
		wakeFns    []func()
	}

	// pollerGroupLease represents the SDK-local group reservation for one
	// poll request RPC. Response group IDs are handled separately because the
	// server may return a different group ID for follow-up routing.
	pollerGroupLease struct {
		manager *pollerGroupManager
		groupID string
		// Retain the selected state for the unlikely case that its ID is removed
		// and re-added before the lease is released.
		group     *pollerGroupState
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
	groupID, group := m.tracker.reserve(m.groupInfos.snapshot())
	return pollerGroupLease{
		manager: m,
		groupID: groupID,
		group:   group,
	}
}

func (m *pollerGroupManager) tryReserveWorkflowPoll(
	queueKind enumspb.TaskQueueKind,
	stickyEnabled bool,
) (pollerGroupLease, bool) {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return pollerGroupLease{queueKind: queueKind}, true
	}
	groupID, group, ok := m.tracker.tryReserveWorkflowPoll(m.groupInfos.snapshot(), queueKind, stickyEnabled)
	if !ok {
		return pollerGroupLease{}, false
	}
	lease := pollerGroupLease{
		manager:   m,
		groupID:   groupID,
		group:     group,
		queueKind: queueKind,
	}
	m.signalWaiters()
	return lease, true
}

func (m *pollerGroupManager) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	if m == nil || m.groupInfos == nil {
		return
	}
	m.groupInfos.updateGroups(info)
	m.signalWaiters()
}

func (m *pollerGroupManager) registerWaiter(wake func()) {
	if m == nil || wake == nil {
		return
	}
	m.wakeMu.Lock()
	m.wakeFns = append(m.wakeFns, wake)
	m.wakeMu.Unlock()
}

func (m *pollerGroupManager) signalWaiters() {
	if m == nil {
		return
	}
	m.wakeMu.Lock()
	wakeFns := append([]func(){}, m.wakeFns...)
	m.wakeMu.Unlock()
	for _, wake := range wakeFns {
		wake()
	}
}

func (l pollerGroupLease) groupIDOrEmpty() string {
	return l.groupID
}

func (l pollerGroupLease) release() {
	if l.manager == nil || l.manager.tracker == nil || l.group == nil {
		return
	}
	l.manager.tracker.release(l.group, l.queueKind)
	l.manager.signalWaiters()
}

func newPollerGroupTracker(workflow bool) *pollerGroupTracker {
	return &pollerGroupTracker{
		workflow: workflow,
		mu:       sync.Mutex{},
		groups:   make(map[string]*pollerGroupState),
	}
}

func (t *pollerGroupTracker) reserve(weights map[string]float32) (string, *pollerGroupState) {
	if t == nil {
		return "", nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncGroups(weights)

	if len(t.groups) == 0 {
		return "", nil
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
		return "", nil
	}
	group := t.groups[groupID]
	group.pendingPollCount++
	return groupID, group
}

// tryReserveWorkflowPoll first satisfies per-group coverage for queueKind. It
// blocks additional polls for that kind while the other required workflow kind
// still has a coverage gap. Once both kinds are covered, it selects additional
// polls using the server-provided weights.
func (t *pollerGroupTracker) tryReserveWorkflowPoll(
	weights map[string]float32,
	queueKind enumspb.TaskQueueKind,
	stickyEnabled bool,
) (string, *pollerGroupState, bool) {
	if t == nil {
		return "", nil, true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncGroups(weights)

	if len(t.groups) == 0 {
		return "", nil, true
	}

	candidates := t.workflowCoverageCandidates(weights, queueKind)
	if len(candidates) == 0 && t.workflowCoverageMissing(stickyEnabled) {
		return "", nil, false
	}
	if len(candidates) == 0 {
		candidates = weights
	}

	groupID := choosePollerGroup(candidates)
	if groupID == "" {
		return "", nil, true
	}
	group := t.groups[groupID]
	t.incrementWorkflowPending(group, queueKind)
	return groupID, group, true
}

func (t *pollerGroupTracker) release(group *pollerGroupState, queueKind enumspb.TaskQueueKind) {
	if t == nil || group == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.decrementPending(group, queueKind)
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

func (t *pollerGroupTracker) workflowCoverageCandidates(
	weights map[string]float32,
	queueKind enumspb.TaskQueueKind,
) map[string]float32 {
	candidates := make(map[string]float32)
	for groupID, group := range t.groups {
		if queueKind == enumspb.TASK_QUEUE_KIND_STICKY && group.workflowPendingSticky == 0 ||
			queueKind == enumspb.TASK_QUEUE_KIND_NORMAL && group.workflowPendingNormal == 0 {
			candidates[groupID] = weights[groupID]
		}
	}
	return candidates
}

func (t *pollerGroupTracker) workflowCoverageMissing(stickyEnabled bool) bool {
	for _, group := range t.groups {
		if group.workflowPendingNormal == 0 || stickyEnabled && group.workflowPendingSticky == 0 {
			return true
		}
	}
	return false
}

func (t *pollerGroupTracker) incrementWorkflowPending(group *pollerGroupState, queueKind enumspb.TaskQueueKind) {
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
