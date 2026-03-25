package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

func makeGroups(ids ...string) []*taskqueuepb.PollerGroupInfo {
	groups := make([]*taskqueuepb.PollerGroupInfo, len(ids))
	for i, id := range ids {
		groups[i] = &taskqueuepb.PollerGroupInfo{Id: id, Weight: 1.0}
	}
	return groups
}

func makeWeightedGroups(pairs ...interface{}) []*taskqueuepb.PollerGroupInfo {
	var groups []*taskqueuepb.PollerGroupInfo
	for i := 0; i < len(pairs); i += 2 {
		groups = append(groups, &taskqueuepb.PollerGroupInfo{
			Id:     pairs[i].(string),
			Weight: pairs[i+1].(float32),
		})
	}
	return groups
}

func TestPollerGroupTracker_NoGroups(t *testing.T) {
	tracker := newPollerGroupTracker()
	// With no groups, should return empty string.
	assert.Equal(t, "", tracker.getNextGroupId())
}

func TestPollerGroupTracker_SingleGroup(t *testing.T) {
	tracker := newPollerGroupTracker()
	tracker.updateGroups(makeGroups("group-a"))

	id := tracker.getNextGroupId()
	assert.Equal(t, "group-a", id)

	// Second call should also return group-a (only option).
	id2 := tracker.getNextGroupId()
	assert.Equal(t, "group-a", id2)

	tracker.release(id)
	tracker.release(id2)
}

func TestPollerGroupTracker_ZeroPendingThenFallback(t *testing.T) {
	tracker := newPollerGroupTracker()
	tracker.updateGroups(makeGroups("a", "b", "c"))

	// Each call must pick a different group while zero-pending groups remain.
	id1 := tracker.getNextGroupId()
	id2 := tracker.getNextGroupId()
	id3 := tracker.getNextGroupId()
	require.NotEqual(t, id1, id2)
	require.NotEqual(t, id1, id3)
	require.NotEqual(t, id2, id3)

	// All groups now have pending=1. Next call falls back to any group.
	id4 := tracker.getNextGroupId()
	assert.Contains(t, []string{"a", "b", "c"}, id4)

	tracker.release(id1)
	tracker.release(id2)
	tracker.release(id3)
	tracker.release(id4)
}

func TestPollerGroupTracker_Release(t *testing.T) {
	tracker := newPollerGroupTracker()
	tracker.updateGroups(makeGroups("a", "b"))

	id1 := tracker.getNextGroupId()
	id2 := tracker.getNextGroupId()
	// Both groups have pending=1.

	// Release one and get next: should pick the released one (zero pending).
	tracker.release(id1)
	id3 := tracker.getNextGroupId()
	assert.Equal(t, id1, id3, "should prefer the group with zero pending after release")

	tracker.release(id2)
	tracker.release(id3)
}

func TestPollerGroupTracker_ReleaseFloorAtZero(t *testing.T) {
	tracker := newPollerGroupTracker()
	tracker.updateGroups(makeGroups("a"))

	// Release without ever getting should not panic or go negative.
	tracker.release("a")
	tracker.release("nonexistent")

	id := tracker.getNextGroupId()
	assert.Equal(t, "a", id)
	tracker.release(id)
}

func TestPollerGroupTracker_UpdateGroupsCleansStale(t *testing.T) {
	tracker := newPollerGroupTracker()
	tracker.updateGroups(makeGroups("a", "b"))

	// Create pending for both.
	id1 := tracker.getNextGroupId()
	id2 := tracker.getNextGroupId()

	// Update to only have "b" and "c".
	tracker.updateGroups(makeGroups("b", "c"))

	// Pending for "a" should be cleaned up.
	tracker.mu.Lock()
	_, hasPendingA := tracker.pending["a"]
	tracker.mu.Unlock()
	assert.False(t, hasPendingA, "pending for removed group should be cleaned up")

	tracker.release(id1)
	tracker.release(id2)
}

func TestPollerGroupTracker_UpdateGroupsEmptyNoOp(t *testing.T) {
	tracker := newPollerGroupTracker()
	tracker.updateGroups(makeGroups("a"))

	// Empty update should not clear groups.
	tracker.updateGroups(nil)

	id := tracker.getNextGroupId()
	assert.Equal(t, "a", id)
	tracker.release(id)
}

func TestWeightedRandom_SingleCandidate(t *testing.T) {
	groups := makeGroups("only")
	assert.Equal(t, "only", weightedRandom(groups))
}

func TestWeightedRandom_ZeroWeights(t *testing.T) {
	groups := makeWeightedGroups("a", float32(0), "b", float32(0))
	// Should still return a valid group.
	id := weightedRandom(groups)
	assert.Contains(t, []string{"a", "b"}, id)
}

func TestPollerGroupTracker_UpdateGroupsPreservesSurvivingPending(t *testing.T) {
	tracker := newPollerGroupTracker()
	tracker.updateGroups(makeGroups("a", "b", "c"))

	// Create pending for all three.
	id1 := tracker.getNextGroupId()
	id2 := tracker.getNextGroupId()
	id3 := tracker.getNextGroupId()

	// Update groups: drop "a", keep "b" and "c", add "d".
	tracker.updateGroups(makeGroups("b", "c", "d"))

	// "d" has zero pending, so it must be picked next.
	id4 := tracker.getNextGroupId()
	assert.Equal(t, "d", id4)

	// "b" and "c" still have pending=1 from before the update.
	// Release one of them and verify it becomes preferred over the other.
	// Find which of id1/id2/id3 was "b".
	var bId string
	for _, id := range []string{id1, id2, id3} {
		if id == "b" {
			bId = id
			break
		}
	}
	require.NotEmpty(t, bId, "b should have been selected in initial round")
	tracker.release(bId)
	tracker.release(id4)

	// Now "b" and "d" have zero pending, "c" has pending=1. Next pick must not be "c".
	id5 := tracker.getNextGroupId()
	assert.NotEqual(t, "c", id5, "should prefer zero-pending groups b or d over c")

	tracker.release(id1)
	tracker.release(id2)
	tracker.release(id3)
	tracker.release(id5)
}

func TestWeightedRandom_DistributionConverges(t *testing.T) {
	groups := makeWeightedGroups("a", float32(3.0), "b", float32(1.0))

	counts := map[string]int{}
	iterations := 10000
	for i := 0; i < iterations; i++ {
		counts[weightedRandom(groups)]++
	}

	// With weights 3:1, "a" should get ~75% of selections.
	ratioA := float64(counts["a"]) / float64(iterations)
	assert.InDelta(t, 0.75, ratioA, 0.05, "expected ~75%% for weight-3 group, got %.2f%%", ratioA*100)
}

