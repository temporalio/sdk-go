package internal

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

func testPollerGroupsInfo(version int64, groups []*taskqueuepb.PollerGroupInfo) *taskqueuepb.PollerGroupsInfo {
	return &taskqueuepb.PollerGroupsInfo{Version: version, PollerGroups: groups}
}

func newTestPollerGroupManager() *pollerGroupManager {
	return newPollerGroupManager(newPollerGroupSnapshotStore())
}

func TestPollerGroupManagerReserveActivityNexusPollFillsCoverageBeforeWeights(t *testing.T) {
	manager := newTestPollerGroupManager()
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "uncovered", Weight: 0},
		{Id: "covered", Weight: 100},
	}))

	lease := manager.reserve()
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, 1, manager.groups["covered"].pendingPollCount)

	lease = manager.reserve()
	require.Equal(t, "uncovered", lease.groupIDOrEmpty())
	require.Equal(t, 1, manager.groups["uncovered"].pendingPollCount)

	lease = manager.reserve()
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, 2, manager.groups["covered"].pendingPollCount)
}

func TestPollerGroupManagerEmptyUpdateClearsGroups(t *testing.T) {
	manager := newTestPollerGroupManager()
	manager.updateGroups(testPollerGroupsInfo(1, nil))
	require.Equal(t, 0, manager.requiredMin())

	manager.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))
	require.Equal(t, 1, manager.requiredMin())

	manager.updateGroups(testPollerGroupsInfo(3, nil))
	require.Equal(t, 0, manager.requiredMin())

	lease := manager.reserve()
	require.Empty(t, lease.groupIDOrEmpty())
}

func TestPollerGroupSnapshotStoreOnlyAppliesNewerVersions(t *testing.T) {
	groupStore := newPollerGroupSnapshotStore()
	groupStore.updateGroups(testPollerGroupsInfo(10, []*taskqueuepb.PollerGroupInfo{
		{Id: "current", Weight: 1},
	}))
	changed := groupStore.changed()

	groupStore.updateGroups(testPollerGroupsInfo(9, []*taskqueuepb.PollerGroupInfo{
		{Id: "stale", Weight: 1},
	}))
	groupStore.updateGroups(testPollerGroupsInfo(10, []*taskqueuepb.PollerGroupInfo{
		{Id: "duplicate", Weight: 1},
	}))
	groupStore.updateGroups(nil)
	require.Equal(t, map[string]float32{"current": 1}, groupStore.snapshot().weights)
	select {
	case <-changed:
		t.Fatal("stale or empty update notified store observers")
	default:
	}

	groupStore.updateGroups(testPollerGroupsInfo(11, []*taskqueuepb.PollerGroupInfo{
		{Id: "new", Weight: 1},
	}))
	require.Equal(t, map[string]float32{"new": 1}, groupStore.snapshot().weights)
	select {
	case <-changed:
	default:
		t.Fatal("newer update did not notify store observers")
	}
}

func TestPollerGroupSnapshotStoreAppliesFirstZeroVersion(t *testing.T) {
	groupStore := newPollerGroupSnapshotStore()
	groupStore.updateGroups(testPollerGroupsInfo(0, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))

	require.Equal(t, map[string]float32{"group-a": 1}, groupStore.snapshot().weights)
}

func TestPollerGroupManagersShareWeightsAndKeepCoverageSeparate(t *testing.T) {
	groupStore := newPollerGroupSnapshotStore()
	external := newPollerGroupManager(groupStore)
	internal := newPollerGroupManager(groupStore)

	external.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 100},
		{Id: "group-b", Weight: 0},
	}))

	externalLease := external.reserve()
	defer externalLease.release()
	require.Equal(t, "group-a", externalLease.groupIDOrEmpty())

	internalALease := internal.reserve()
	defer internalALease.release()
	require.Equal(t, "group-a", internalALease.groupIDOrEmpty(), "external coverage must not satisfy internal coverage")

	internalBLease := internal.reserve()
	defer internalBLease.release()
	require.Equal(t, "group-b", internalBLease.groupIDOrEmpty())

	external.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 0},
		{Id: "group-b", Weight: 100},
	}))

	floatingLease := internal.reserve()
	defer floatingLease.release()
	require.Equal(t, "group-b", floatingLease.groupIDOrEmpty(), "external weight update must affect the internal manager's next floating poll")
	require.Equal(t, 1, external.groups["group-a"].pendingPollCount)
	require.Equal(t, 1, internal.groups["group-a"].pendingPollCount)
	require.Equal(t, 2, internal.groups["group-b"].pendingPollCount)
}

func TestPollerGroupManagerReserveWorkflowPollFallsBackBeforeGroupsKnown(t *testing.T) {
	manager := newTestPollerGroupManager()

	lease := manager.reserve()
	require.Empty(t, lease.groupIDOrEmpty())
	require.Same(t, manager, lease.owner)
}

func TestPollerGroupManagerRemovedGroupLeaseReleaseIsSafe(t *testing.T) {
	manager := newTestPollerGroupManager()
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))
	lease := manager.reserve()

	manager.updateGroups(testPollerGroupsInfo(2, nil))
	require.Empty(t, manager.reserve().groupIDOrEmpty())
	require.NotPanics(t, lease.release)
}

func TestPollerGroupLeaseReleaseDoesNotAffectReaddedGroup(t *testing.T) {
	manager := newTestPollerGroupManager()
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))
	oldLease := manager.reserve()

	manager.updateGroups(testPollerGroupsInfo(2, nil))
	require.Empty(t, manager.reserve().groupIDOrEmpty())

	manager.updateGroups(testPollerGroupsInfo(3, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))
	newLease := manager.reserve()
	newGroup := manager.groups["group-a"]
	require.Equal(t, 1, newGroup.pendingPollCount)

	oldLease.release()
	require.Equal(t, 1, newGroup.pendingPollCount)

	newLease.release()
	require.Equal(t, 0, newGroup.pendingPollCount)
}

func TestPollerGroupManagersConcurrentSharedUpdatesAndReservations(t *testing.T) {
	groupStore := newPollerGroupSnapshotStore()
	activity := newPollerGroupManager(groupStore)
	workflow := newPollerGroupManager(groupStore)
	groupStore.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			activity.reserve().release()
		}()
		go func() {
			defer wg.Done()
			workflow.reserve().release()
		}()
		go func() {
			defer wg.Done()
			groupStore.updateGroups(testPollerGroupsInfo(int64(i+2), []*taskqueuepb.PollerGroupInfo{
				{Id: "group-a", Weight: 1},
				{Id: "group-b", Weight: 2},
			}))
		}()
	}
	wg.Wait()

	require.Equal(t, 2, activity.requiredMin())
	require.Equal(t, 2, workflow.requiredMin())
}
