package internal

import (
	"math/rand"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

// The client owns pollerGroupSnapshotStore. Each poller kind uses an independent
// manager so its autoscaling target and in-flight coverage remain independent.
type (
	// pollerGroupSnapshotStore holds client-wide group membership, weights, and
	// versions learned from poll responses. Managers and schedulers share this
	// routing snapshot but keep their in-flight poll state local.
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
		groups     map[string]pollerGroupSnapshotEntry
		version    int64
		versionSet bool
	}

	// pollerGroupKey identifies one observed lifetime of a group. Incarnation is
	// the snapshot version where the ID first appeared after being absent.
	pollerGroupKey struct {
		id          string
		incarnation int64
	}

	// pollerGroupSnapshotEntry binds a group's identity to its current weight.
	pollerGroupSnapshotEntry struct {
		key    pollerGroupKey
		weight float32
	}

	// pollerGroupManager assigns groups for autoscaling Activity and Nexus polls
	// and worker-command polls. Workflow polls use workflowAutoscalingBalancer.
	pollerGroupManager struct {
		groupStore *pollerGroupSnapshotStore
		mu         sync.Mutex
		groups     map[string]*pollerGroupState
	}

	// pollerGroupLease tracks one poll attempt's group reservation until release.
	pollerGroupLease struct {
		owner pollerGroupLeaseOwner
		group pollerGroupKey
		kind  enumspb.TaskQueueKind
	}

	pollerGroupLeaseOwner interface {
		releaseReservation(pollerGroupLease)
	}

	pollerGroupState struct {
		key              pollerGroupKey
		pendingPollCount int
	}
)

func newPollerGroupSnapshotStore() *pollerGroupSnapshotStore {
	return &pollerGroupSnapshotStore{
		current: pollerGroupSnapshot{
			groups: make(map[string]pollerGroupSnapshotEntry),
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
	group := m.reserveGroup()
	return m.lease(group)
}

// tryReserveRequired reserves an uncovered current group when existing polls
// consume the autoscaling target, ensuring every group has minimum coverage.
// The bool reports whether a group was reserved.
func (m *pollerGroupManager) tryReserveRequired() (pollerGroupLease, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := m.syncGroupsLocked()

	group := m.reserveCandidate(m.coverageCandidates(snapshot.groups))
	if group == nil {
		return pollerGroupLease{}, false
	}
	return m.lease(group), true
}

func (m *pollerGroupManager) updateGroups(info *taskqueuepb.PollerGroupsInfo) {
	m.groupStore.updateGroups(info)
}

func (l pollerGroupLease) groupIDOrEmpty() string {
	return l.group.id
}

func (l pollerGroupLease) release() {
	if l.owner != nil {
		l.owner.releaseReservation(l)
	}
}

func (m *pollerGroupManager) lease(group *pollerGroupState) pollerGroupLease {
	// Poll ungrouped until the server advertises at least one group.
	if group == nil {
		return pollerGroupLease{owner: m}
	}
	return pollerGroupLease{owner: m, group: group.key}
}

func (m *pollerGroupManager) reserveGroup() *pollerGroupState {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := m.syncGroupsLocked()

	candidates := m.coverageCandidates(snapshot.groups)
	if len(candidates) == 0 {
		candidates = snapshot.groups
	}
	return m.reserveCandidate(candidates)
}

// coverageCandidates returns groups without an in-flight poll from this manager.
func (m *pollerGroupManager) coverageCandidates(
	groups map[string]pollerGroupSnapshotEntry,
) map[string]pollerGroupSnapshotEntry {
	candidates := make(map[string]pollerGroupSnapshotEntry)
	for groupID, group := range m.groups {
		if group.pendingPollCount == 0 {
			candidates[groupID] = groups[groupID]
		}
	}
	return candidates
}

func (m *pollerGroupManager) reserveCandidate(
	candidates map[string]pollerGroupSnapshotEntry,
) *pollerGroupState {
	groupID := choosePollerGroup(candidates)
	if groupID == "" {
		return nil
	}
	group := m.groups[groupID]
	group.pendingPollCount++
	return group
}

func (m *pollerGroupManager) releaseReservation(lease pollerGroupLease) {
	if lease.group.id == "" {
		return
	}
	m.mu.Lock()
	group := m.groups[lease.group.id]
	if group != nil && group.key == lease.group && group.pendingPollCount > 0 {
		group.pendingPollCount--
	}
	m.mu.Unlock()
}

// syncGroupsLocked reconciles local groups with the current store snapshot.
// The caller must hold m.mu so snapshots cannot be applied out of order.
func (m *pollerGroupManager) syncGroupsLocked() pollerGroupSnapshot {
	snapshot := m.groupStore.snapshot()
	for groupID, entry := range snapshot.groups {
		group := m.groups[groupID]
		if group == nil || group.key != entry.key {
			m.groups[groupID] = &pollerGroupState{key: entry.key}
		}
	}
	for groupID := range m.groups {
		if _, ok := snapshot.groups[groupID]; !ok {
			delete(m.groups, groupID)
		}
	}

	return snapshot
}

// choosePollerGroup picks a random group using the configured weights.
// If all weights are zero or negative, it picks uniformly from all groups.
// If floating-point rounding prevents the weighted walk from selecting a group,
// it falls back to the last positive-weight candidate encountered.
func choosePollerGroup(groups map[string]pollerGroupSnapshotEntry) string {
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

	// Pick a random point in [0, totalWeight). Subtract each group's weight
	// until the point is less than the current group's weight.
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

func (s *pollerGroupSnapshotStore) len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.current.groups)
}

func (s *pollerGroupSnapshotStore) snapshot() pollerGroupSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// observe returns a snapshot and the notification for its replacement.
func (s *pollerGroupSnapshotStore) observe() (pollerGroupSnapshot, <-chan struct{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current, s.changedCh
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

	groups := make(map[string]pollerGroupSnapshotEntry, len(info.GetPollerGroups()))
	for _, group := range info.GetPollerGroups() {
		if groupID := group.GetId(); groupID != "" {
			entry, ok := s.current.groups[groupID]
			if !ok {
				entry.key = pollerGroupKey{id: groupID, incarnation: info.GetVersion()}
			}
			entry.weight = group.GetWeight()
			groups[groupID] = entry
		}
	}

	s.current = pollerGroupSnapshot{
		groups:     groups,
		version:    info.GetVersion(),
		versionSet: true,
	}
	close(s.changedCh)
	s.changedCh = make(chan struct{})
}
