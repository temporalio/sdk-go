package internal

import (
	"math/rand"
	"sync"

	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

// pollerGroupTracker distributes pollers across server-provided poller groups
// based on weights. Each call to getNextGroupId returns a group ID such that
// every group has at least one pending (unreleased) request, and beyond that
// minimum the distribution follows group weights.
type pollerGroupTracker struct {
	mu     sync.Mutex
	groups map[string]pollerGroupState
}

type pollerGroupState struct {
	pendingRequests int
	weight          float32
}

type pollerGroupCandidate struct {
	id     string
	weight float32
}

func newPollerGroupTracker() *pollerGroupTracker {
	return &pollerGroupTracker{
		groups: make(map[string]pollerGroupState),
	}
}

// updateGroups updates the available groups from a server response and
// cleans up pending entries for groups that no longer exist.
func (t *pollerGroupTracker) updateGroups(groups []*taskqueuepb.PollerGroupInfo) {
	if len(groups) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	valid := make(map[string]struct{}, len(groups))

	// Update weights for persisting groups
	for _, g := range groups {
		id := g.GetId()
		valid[id] = struct{}{}

		state := t.groups[id]
		state.weight = g.GetWeight()
		t.groups[id] = state
	}

	// Remove pending entries for groups that no longer exist.
	for id := range t.groups {
		if _, ok := valid[id]; !ok {
			delete(t.groups, id)
		}
	}
}

// getNextGroupId returns the group ID that should be used for the next poll
// request. Groups with zero pending polls are prioritized as candidates. If all
// groups have pending polls, all groups become candidates. Among candidates, one
// is selected randomly weighted by group weights.
func (t *pollerGroupTracker) getNextGroupId() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.groups) == 0 {
		return ""
	}

	// Candidate set: groups with zero pending polls.
	candidates := make([]pollerGroupCandidate, 0, len(t.groups))
	for id, state := range t.groups {
		if state.pendingRequests == 0 {
			candidates = append(candidates, pollerGroupCandidate{id: id, weight: state.weight})
		}
	}

	// If all groups have pending polls, all groups are candidates.
	if len(candidates) == 0 {
		for id, state := range t.groups {
			candidates = append(candidates, pollerGroupCandidate{id: id, weight: state.weight})
		}
	}

	chosen := weightedRandom(candidates)
	state := t.groups[chosen]
	state.pendingRequests++
	t.groups[chosen] = state
	return chosen
}

// weightedRandom selects a group ID from candidates randomly based on weights.
// candidates must be non-empty.
func weightedRandom(candidates []pollerGroupCandidate) string {
	if len(candidates) == 1 {
		return candidates[0].id
	}

	var totalWeight float64
	for _, g := range candidates {
		totalWeight += float64(g.weight)
	}

	// If all weights are zero, pick uniformly at random.
	if totalWeight <= 0 {
		return candidates[rand.Intn(len(candidates))].id
	}

	r := rand.Float64() * totalWeight
	for _, g := range candidates {
		r -= float64(g.weight)
		if r <= 0 {
			return g.id
		}
	}
	// Floating-point rounding fallback.
	return candidates[len(candidates)-1].id
}

// release marks one pending request for the given group as completed.
func (t *pollerGroupTracker) release(groupId string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.groups[groupId]
	if !ok || state.pendingRequests == 0 {
		return
	}
	state.pendingRequests--
	t.groups[groupId] = state
}
