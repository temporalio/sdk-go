package internal

import (
	"math/rand"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

// Poller-group relationships:
//
// The client owns pollerGroupInfoStore, the shared routing snapshot. A manager
// references that store and owns a pollerGroupTracker, which owns its local
// pollerGroupState entries. Each in-flight poll owns a pollerGroupLease that
// references its manager and selected state.
//
// Workflow normal and sticky pollers share one manager and tracker. Other
// polling paths keep independent coverage trackers while sharing the store.
type (
	pollerGroupMode uint8

	// pollerGroupInfoStore is the client-owned, versioned routing snapshot shared
	// by pollerGroupManagers.
	pollerGroupInfoStore struct {
		mu      sync.RWMutex
		current pollerGroupSnapshot
		// changedCh is closed and replaced whenever a newer group snapshot is
		// accepted. Runners use it to observe updates published through any
		// manager sharing this store.
		changedCh chan struct{}
	}

	// pollerGroupSnapshot is immutable after publication.
	pollerGroupSnapshot struct {
		weights map[string]float32
		// generations identifies the current incarnation of each group ID. A
		// group keeps its generation across weight updates, but receives a new one
		// when removed and re-added.
		generations map[string]int64
		version     int64
		versionSet  bool
	}

	// pollerGroupManager combines shared routing state with local poll coverage.
	// For Workflow polling, it also selects whether normal or sticky may poll
	// next; workflowPollAdmission enforces their shared admission budget.
	pollerGroupManager struct {
		groupInfos *pollerGroupInfoStore
		tracker    *pollerGroupTracker

		wakeMu sync.Mutex
		// wakeFns notify runners attached to this manager to recheck admission.
		// They do not correspond one-to-one with events and do not start or cancel
		// poll RPCs themselves.
		wakeFns []func()
	}

	// pollerGroupLease is owned by one in-flight poll and reserves its
	// request-selected group in the manager's tracker.
	pollerGroupLease struct {
		manager *pollerGroupManager
		// Retain the selected state for the unlikely case that its ID is removed
		// and re-added before the lease is released.
		group     *pollerGroupState
		queueKind enumspb.TaskQueueKind
	}

	// pollerGroupTracker is the manager-owned pending-poll coverage accounting.
	pollerGroupTracker struct {
		mode             pollerGroupMode
		mu               sync.Mutex
		groups           map[string]*pollerGroupState
		workflowDecision workflowPollDecision
	}

	workflowPollDecision struct {
		group     *pollerGroupState
		queueKind enumspb.TaskQueueKind
		version   int64
	}

	// pollerGroupState is the tracker-owned pending-poll state for one group.
	pollerGroupState struct {
		groupID string
		// generation prevents a re-added group from inheriting pending counts or
		// sticky backlog when this tracker did not observe the removal snapshot.
		generation int64

		// Activity/Nexus use total pending
		pendingPollCount int

		// Workflow uses queue-kind-specific pending counts
		workflowPendingNormal int
		workflowPendingSticky int
		stickyBacklog         int64
	}
)

const (
	pollerGroupModeNonWorkflow pollerGroupMode = iota
	pollerGroupModeWorkflow
	pollerGroupModeWorkflowWithSticky
)

func newPollerGroupInfoStore() *pollerGroupInfoStore {
	return &pollerGroupInfoStore{
		current: pollerGroupSnapshot{
			weights:     make(map[string]float32),
			generations: make(map[string]int64),
		},
		changedCh: make(chan struct{}),
	}
}

func newPollerGroupManager(mode pollerGroupMode, groupInfos *pollerGroupInfoStore) *pollerGroupManager {
	if groupInfos == nil {
		groupInfos = newPollerGroupInfoStore()
	}
	return &pollerGroupManager{
		groupInfos: groupInfos,
		tracker: &pollerGroupTracker{
			mode:   mode,
			groups: make(map[string]*pollerGroupState),
		},
	}
}

func (m *pollerGroupManager) requiredMin() int {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return 0
	}
	return m.groupInfos.len()
}

func (m *pollerGroupManager) reserve() pollerGroupLease {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return pollerGroupLease{}
	}
	snapshot := m.groupInfos.snapshot()
	group := m.tracker.reserve(snapshot.weights, snapshot.generations)
	return pollerGroupLease{
		manager: m,
		group:   group,
	}
}

// tryReserveRequired reserves only missing coverage for the requested queue
// kind. It is used when ordinary autoscaling capacity is full but existing
// polls no longer cover the current group snapshot.
func (m *pollerGroupManager) tryReserveRequired(
	queueKind enumspb.TaskQueueKind,
) (pollerGroupLease, bool) {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return pollerGroupLease{}, false
	}
	snapshot := m.groupInfos.snapshot()
	group := m.tracker.tryReserveRequired(snapshot.weights, snapshot.generations, queueKind)
	if group == nil {
		return pollerGroupLease{}, false
	}
	lease := pollerGroupLease{
		manager:   m,
		group:     group,
		queueKind: queueKind,
	}
	m.signalWaiters()
	return lease, true
}

func (m *pollerGroupManager) tryReserveWorkflowPoll(
	queueKind enumspb.TaskQueueKind,
) (pollerGroupLease, bool) {
	if m == nil || m.groupInfos == nil || m.tracker == nil {
		return pollerGroupLease{queueKind: queueKind}, true
	}
	snapshot := m.groupInfos.snapshot()
	if len(snapshot.weights) == 0 {
		return pollerGroupLease{manager: m, queueKind: queueKind}, true
	}
	group, wakeWaiters := m.tracker.tryReserveWorkflowPoll(snapshot, queueKind)
	if wakeWaiters {
		m.signalWaiters()
	}
	if group == nil {
		return pollerGroupLease{}, false
	}
	lease := pollerGroupLease{
		manager:   m,
		group:     group,
		queueKind: queueKind,
	}
	return lease, true
}

func (m *pollerGroupManager) updateWorkflowStickyBacklog(groupID string, backlog int64) {
	if m == nil || m.tracker == nil || groupID == "" {
		return
	}
	snapshot := m.groupInfos.snapshot()
	if m.tracker.updateWorkflowStickyBacklog(
		snapshot,
		groupID,
		backlog,
	) {
		m.signalWaiters()
	}
}

func (m *pollerGroupManager) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	if m == nil || m.groupInfos == nil {
		return
	}
	m.groupInfos.updateGroups(info)
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
	if l.group == nil {
		return ""
	}
	return l.group.groupID
}

func (l pollerGroupLease) release() {
	if l.manager == nil || l.manager.tracker == nil || l.group == nil {
		return
	}
	l.manager.tracker.release(l.group, l.queueKind)
	l.manager.signalWaiters()
}

func (t *pollerGroupTracker) reserve(weights map[string]float32, generations map[string]int64) *pollerGroupState {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncGroups(weights, generations)

	if len(t.groups) == 0 {
		return nil
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
		return nil
	}
	group := t.groups[groupID]
	group.pendingPollCount++
	return group
}

func (t *pollerGroupTracker) tryReserveRequired(
	weights map[string]float32,
	generations map[string]int64,
	queueKind enumspb.TaskQueueKind,
) *pollerGroupState {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncGroups(weights, generations)

	var candidates map[string]float32
	if t.mode == pollerGroupModeNonWorkflow {
		candidates = make(map[string]float32)
		for groupID, group := range t.groups {
			if group.pendingPollCount == 0 {
				candidates[groupID] = weights[groupID]
			}
		}
	} else {
		candidates = t.workflowCoverageCandidates(weights, queueKind)
	}
	groupID := choosePollerGroup(candidates)
	if groupID == "" {
		return nil
	}
	group := t.groups[groupID]
	if t.mode == pollerGroupModeNonWorkflow {
		group.pendingPollCount++
	} else {
		t.incrementWorkflowPending(group, queueKind)
	}
	return group
}

// tryReserveWorkflowPoll first satisfies normal and sticky coverage. Once
// coverage is complete, it retains one weighted group decision until the
// runner selected by that group's sticky backlog claims it. The manager handles
// empty snapshots, so a nil group means the requested runner must wait.
// wakeWaiters reports whether another runner should recheck admission.
func (t *pollerGroupTracker) tryReserveWorkflowPoll(
	snapshot pollerGroupSnapshot,
	queueKind enumspb.TaskQueueKind,
) (selectedGroup *pollerGroupState, wakeWaiters bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	wakeWaiters = t.syncWorkflowSnapshot(snapshot)

	if len(t.groups) == 0 {
		return nil, wakeWaiters
	}

	if t.workflowCoverageMissing() {
		if t.workflowDecision.group != nil {
			t.workflowDecision = workflowPollDecision{}
			wakeWaiters = true
		}
		candidates := t.workflowCoverageCandidates(snapshot.weights, queueKind)
		if len(candidates) == 0 {
			return nil, wakeWaiters
		}
		groupID := choosePollerGroup(candidates)
		if groupID == "" {
			return nil, wakeWaiters
		}
		group := t.groups[groupID]
		t.incrementWorkflowPending(group, queueKind)
		return group, true
	}

	if t.workflowDecision.group == nil {
		groupID := choosePollerGroup(snapshot.weights)
		if groupID == "" {
			return nil, wakeWaiters
		}
		group := t.groups[groupID]
		decisionKind := enumspb.TASK_QUEUE_KIND_NORMAL
		if t.mode == pollerGroupModeWorkflowWithSticky && group.stickyBacklog > 0 {
			decisionKind = enumspb.TASK_QUEUE_KIND_STICKY
		}
		t.workflowDecision = workflowPollDecision{
			group:     group,
			queueKind: decisionKind,
			version:   snapshot.version,
		}
		wakeWaiters = true
	}

	if t.workflowDecision.queueKind != queueKind {
		return nil, wakeWaiters
	}
	group := t.workflowDecision.group
	t.workflowDecision = workflowPollDecision{}
	if t.groups[group.groupID] != group {
		return nil, true
	}
	t.incrementWorkflowPending(group, queueKind)
	return group, true
}

func (t *pollerGroupTracker) release(group *pollerGroupState, queueKind enumspb.TaskQueueKind) {
	if t == nil || group == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if current := t.groups[group.groupID]; current != group {
		return
	}
	becameUncovered := t.decrementPending(group, queueKind)
	if t.mode != pollerGroupModeNonWorkflow && becameUncovered {
		t.workflowDecision = workflowPollDecision{}
	}
}

func (t *pollerGroupTracker) updateWorkflowStickyBacklog(
	snapshot pollerGroupSnapshot,
	groupID string,
	backlog int64,
) bool {
	if t == nil || t.mode == pollerGroupModeNonWorkflow || groupID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	wakeWaiters := t.syncWorkflowSnapshot(snapshot)
	group := t.groups[groupID]
	if group == nil || group.stickyBacklog == backlog {
		return wakeWaiters
	}
	group.stickyBacklog = backlog
	t.workflowDecision = workflowPollDecision{}
	return true
}

func (t *pollerGroupTracker) syncWorkflowSnapshot(snapshot pollerGroupSnapshot) bool {
	t.syncGroups(snapshot.weights, snapshot.generations)
	// Discard decisions made with stale routing data.
	if t.workflowDecision.group == nil ||
		t.workflowDecision.version == snapshot.version {
		return false
	}

	t.workflowDecision = workflowPollDecision{}
	return true
}

func (t *pollerGroupTracker) syncGroups(weights map[string]float32, generations map[string]int64) {
	if t.groups == nil {
		t.groups = make(map[string]*pollerGroupState)
	}
	for groupID := range weights {
		group, ok := t.groups[groupID]
		if !ok || group.generation != generations[groupID] {
			t.groups[groupID] = &pollerGroupState{
				groupID:    groupID,
				generation: generations[groupID],
			}
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
		stickyUncovered := queueKind == enumspb.TASK_QUEUE_KIND_STICKY && group.workflowPendingSticky == 0
		normalUncovered := queueKind == enumspb.TASK_QUEUE_KIND_NORMAL && group.workflowPendingNormal == 0
		if stickyUncovered || normalUncovered {
			candidates[groupID] = weights[groupID]
		}
	}
	return candidates
}

func (t *pollerGroupTracker) workflowCoverageMissing() bool {
	for _, group := range t.groups {
		if group.workflowPendingNormal == 0 {
			return true
		}
		if t.mode == pollerGroupModeWorkflowWithSticky && group.workflowPendingSticky == 0 {
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

func (t *pollerGroupTracker) decrementPending(group *pollerGroupState, queueKind enumspb.TaskQueueKind) bool {
	if group == nil {
		return false
	}
	if t.mode == pollerGroupModeNonWorkflow {
		if group.pendingPollCount > 0 {
			group.pendingPollCount--
		}
		return false
	}
	if queueKind == enumspb.TASK_QUEUE_KIND_STICKY {
		if group.workflowPendingSticky > 0 {
			group.workflowPendingSticky--
		}
		return group.workflowPendingSticky == 0
	}
	if group.workflowPendingNormal > 0 {
		group.workflowPendingNormal--
	}
	return group.workflowPendingNormal == 0
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
	return len(s.current.weights)
}

func (s *pollerGroupInfoStore) snapshot() pollerGroupSnapshot {
	if s == nil {
		return pollerGroupSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *pollerGroupInfoStore) changed() <-chan struct{} {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.changedCh
}

func (s *pollerGroupInfoStore) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	if s == nil || info == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.versionSet && info.GetVersion() <= s.current.version {
		return
	}

	weights := make(map[string]float32, len(info.GetPollerGroups()))
	generations := make(map[string]int64, len(info.GetPollerGroups()))
	for _, group := range info.GetPollerGroups() {
		if groupID := group.GetId(); groupID != "" {
			weights[groupID] = group.GetWeight()
			generation, ok := s.current.generations[groupID]
			if !ok {
				generation = info.GetVersion()
			}
			generations[groupID] = generation
		}
	}

	s.current = pollerGroupSnapshot{
		weights:     weights,
		generations: generations,
		version:     info.GetVersion(),
		versionSet:  true,
	}
	close(s.changedCh)
	s.changedCh = make(chan struct{})
}
