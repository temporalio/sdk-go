package internal

import (
	"math/rand"
	"sync"

	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

// The client owns pollerGroupSnapshotStore. Each poller kind uses an independent
// manager so its autoscaling target and in-flight coverage remain independent.
type (
	// pollerGroupSnapshotStore holds the server's namespace-wide routing snapshot.
	// All task queues share it, but keep their in-flight poll state local.
	pollerGroupSnapshotStore struct {
		mu      sync.RWMutex
		current pollerGroupSnapshot
		// changedCh is closed and replaced whenever a newer group snapshot is
		// accepted. Runners use it to observe updates published through any
		// poller sharing this store.
		changedCh chan struct{}
	}

	// pollerGroupSnapshot is immutable after publication.
	pollerGroupSnapshot struct {
		weights map[string]float32
		// generations identifies the current incarnation of each group ID. A
		// group keeps its generation across weight updates, but receives a new one
		// when removed and re-added.
		generations map[string]int64 // todo: don't re-use cell names
		version     int64
		versionSet  bool
	}

	// pollerGroupManager owns one poller kind's in-flight group coverage.
	pollerGroupManager struct {
		groupStore *pollerGroupSnapshotStore
		mu         sync.Mutex
		groups     map[string]*pollerGroupState
	}

	// pollerGroupLease reserves one request-selected group.
	pollerGroupLease struct {
		owner      *pollerGroupManager
		groupID    string
		generation int64
	}

	pollerGroupState struct {
		groupID          string
		generation       int64
		pendingPollCount int
	}
)

func newPollerGroupSnapshotStore() *pollerGroupSnapshotStore {
	return &pollerGroupSnapshotStore{
		current: pollerGroupSnapshot{
			weights:     make(map[string]float32),
			generations: make(map[string]int64),
		},
		changedCh: make(chan struct{}),
	}
}

func newPollerGroupManager(groupStore *pollerGroupSnapshotStore) *pollerGroupManager {
	return &pollerGroupManager{
		groupStore: groupStore,
		groups:     make(map[string]*pollerGroupState),
	}
}

func (m *pollerGroupManager) requiredMin() int {
	return m.groupStore.len()
}

func (m *pollerGroupManager) reserve() pollerGroupLease {
	snapshot := m.groupStore.snapshot()
	group := m.reserveGroup(snapshot)
	return m.lease(group)
}

// tryReserveRequired restores coverage above the autoscaling target while
// polls for stale groups drain.
func (m *pollerGroupManager) tryReserveRequired() (pollerGroupLease, bool) {
	snapshot := m.groupStore.snapshot()
	group := m.reserveRequired(snapshot)
	if group == nil {
		return pollerGroupLease{}, false
	}
	return m.lease(group), true
}

func (m *pollerGroupManager) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	m.groupStore.updateGroups(info)
}

func (l pollerGroupLease) groupIDOrEmpty() string {
	return l.groupID
}

func (l pollerGroupLease) release() {
	if l.owner != nil {
		l.owner.releaseLease(l)
	}
}

func (m *pollerGroupManager) lease(group *pollerGroupState) pollerGroupLease {
	if group == nil {
		return pollerGroupLease{owner: m}
	}
	return pollerGroupLease{owner: m, groupID: group.groupID, generation: group.generation}
}

func (m *pollerGroupManager) reserveGroup(snapshot pollerGroupSnapshot) *pollerGroupState {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncGroups(snapshot)

	candidates := m.coverageCandidates(snapshot.weights)
	if len(candidates) == 0 {
		candidates = snapshot.weights
	}
	return m.reserveCandidate(candidates)
}

func (m *pollerGroupManager) reserveRequired(snapshot pollerGroupSnapshot) *pollerGroupState {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncGroups(snapshot)

	return m.reserveCandidate(m.coverageCandidates(snapshot.weights))
}

// coverageCandidates returns groups without an in-flight poll.
func (m *pollerGroupManager) coverageCandidates(weights map[string]float32) map[string]float32 {
	candidates := make(map[string]float32)
	for groupID, group := range m.groups {
		if group.pendingPollCount == 0 {
			candidates[groupID] = weights[groupID]
		}
	}
	return candidates
}

func (m *pollerGroupManager) reserveCandidate(candidates map[string]float32) *pollerGroupState {
	groupID := choosePollerGroup(candidates)
	if groupID == "" {
		return nil
	}
	group := m.groups[groupID]
	group.pendingPollCount++
	return group
}

func (m *pollerGroupManager) releaseLease(lease pollerGroupLease) {
	if lease.groupID == "" {
		return
	}
	m.mu.Lock()
	group := m.groups[lease.groupID]
	if group != nil && group.generation == lease.generation && group.pendingPollCount > 0 {
		group.pendingPollCount--
	}
	m.mu.Unlock()
}

func (m *pollerGroupManager) syncGroups(snapshot pollerGroupSnapshot) {
	for groupID := range snapshot.weights {
		group := m.groups[groupID]
		if group == nil || group.generation != snapshot.generations[groupID] {
			m.groups[groupID] = &pollerGroupState{
				groupID:    groupID,
				generation: snapshot.generations[groupID],
			}
		}
	}
	for groupID := range m.groups {
		if _, ok := snapshot.weights[groupID]; !ok {
			delete(m.groups, groupID)
		}
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

func (s *pollerGroupSnapshotStore) len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.current.weights)
}

func (s *pollerGroupSnapshotStore) snapshot() pollerGroupSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *pollerGroupSnapshotStore) changed() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.changedCh
}

func (s *pollerGroupSnapshotStore) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	if info == nil {
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
