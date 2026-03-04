package internal

import (
	"math/rand"
	"sync"

	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

// groupAwarePoller is an optional interface implemented by pollers that support
// poller group assignment. When implemented, pollTask will call
// PollTaskWithGroupID instead of PollTask.
type groupAwarePoller interface {
	PollTaskWithGroupID(pollerGroupID string) (taskForWorker, error)
	getPollerGroupTracker() *pollerGroupTracker
}

// pollerGroupTracker distributes pollers across server-provided poller groups
// based on weights. Each call to getNextGroupId returns a group ID such that
// every group has at least one pending (unreleased) request, and beyond that
// minimum the distribution follows group weights.
type pollerGroupTracker struct {
	mu      sync.Mutex
	groups  []*taskqueuepb.PollerGroupInfo
	pending map[string]int // number of unreleased requests per group ID
}

func newPollerGroupTracker() *pollerGroupTracker {
	return &pollerGroupTracker{
		pending: make(map[string]int),
	}
}

// updateGroups updates the available groups from a server response and
// redistributes all registered pollers across the new groups.
func (t *pollerGroupTracker) updateGroups(groups []*taskqueuepb.PollerGroupInfo) {
	if len(groups) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.groups = groups
	// Remove pending entries for groups that no longer exist.
	valid := make(map[string]bool, len(groups))
	for _, g := range groups {
		valid[g.GetId()] = true
	}
	for id := range t.pending {
		if !valid[id] {
			delete(t.pending, id)
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
	var candidates []*taskqueuepb.PollerGroupInfo
	for _, g := range t.groups {
		if t.pending[g.GetId()] == 0 {
			candidates = append(candidates, g)
		}
	}
	// If all groups have pending polls, all groups are candidates.
	if len(candidates) == 0 {
		candidates = t.groups
	}

	chosen := weightedRandom(candidates)
	t.pending[chosen]++
	return chosen
}

// weightedRandom selects a group ID from candidates randomly based on weights.
// candidates must be non-empty.
func weightedRandom(candidates []*taskqueuepb.PollerGroupInfo) string {
	if len(candidates) == 1 {
		return candidates[0].GetId()
	}

	var totalWeight float64
	for _, g := range candidates {
		totalWeight += float64(g.GetWeight())
	}

	// If all weights are zero, pick uniformly at random.
	if totalWeight <= 0 {
		return candidates[rand.Intn(len(candidates))].GetId()
	}

	r := rand.Float64() * totalWeight
	for _, g := range candidates {
		r -= float64(g.GetWeight())
		if r <= 0 {
			return g.GetId()
		}
	}
	// Floating-point rounding fallback.
	return candidates[len(candidates)-1].GetId()
}

// release marks one pending request for the given group as completed.
func (t *pollerGroupTracker) release(groupId string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.pending[groupId] > 0 {
		t.pending[groupId]--
	}
}
