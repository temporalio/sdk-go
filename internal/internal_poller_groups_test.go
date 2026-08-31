package internal

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

func testPollerGroupsInfo(version int64, groups []*taskqueuepb.PollerGroupInfo) *taskqueuepb.PollerGroupsInfo {
	return &taskqueuepb.PollerGroupsInfo{Version: version, PollerGroups: groups}
}

func requireWorkflowPollLease(
	t *testing.T,
	manager *pollerGroupManager,
	queueKind enumspb.TaskQueueKind,
) pollerGroupLease {
	t.Helper()
	lease, ok := manager.tryReserveWorkflowPoll(queueKind)
	require.True(t, ok)
	return lease
}

func TestPollerGroupManagerReserveActivityNexusPollFillsCoverageBeforeWeights(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeNonWorkflow, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "uncovered", Weight: 0},
		{Id: "covered", Weight: 100},
	}))

	lease := manager.reserve()
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, 1, manager.tracker.groups["covered"].pendingPollCount)

	lease = manager.reserve()
	require.Equal(t, "uncovered", lease.groupIDOrEmpty())
	require.Equal(t, 1, manager.tracker.groups["uncovered"].pendingPollCount)

	lease = manager.reserve()
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, 2, manager.tracker.groups["covered"].pendingPollCount)
}

func TestPollerGroupManagerEmptyUpdateClearsGroups(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	manager.updateGroups(testPollerGroupsInfo(1, nil))
	require.Equal(t, 0, manager.requiredMin())

	manager.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))
	require.Equal(t, 1, manager.requiredMin())

	manager.updateGroups(testPollerGroupsInfo(3, nil))
	require.Equal(t, 0, manager.requiredMin())

	lease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Empty(t, lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
}

func TestPollerGroupInfoStoreOnlyAppliesNewerVersions(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	groupInfos.updateGroups(testPollerGroupsInfo(10, []*taskqueuepb.PollerGroupInfo{
		{Id: "current", Weight: 1},
	}))
	changed := groupInfos.changed()

	groupInfos.updateGroups(testPollerGroupsInfo(9, []*taskqueuepb.PollerGroupInfo{
		{Id: "stale", Weight: 1},
	}))
	groupInfos.updateGroups(testPollerGroupsInfo(10, []*taskqueuepb.PollerGroupInfo{
		{Id: "duplicate", Weight: 1},
	}))
	groupInfos.updateGroups(nil)
	require.Equal(t, map[string]float32{"current": 1}, groupInfos.snapshot().weights)
	select {
	case <-changed:
		t.Fatal("stale or empty update notified store observers")
	default:
	}

	groupInfos.updateGroups(testPollerGroupsInfo(11, []*taskqueuepb.PollerGroupInfo{
		{Id: "new", Weight: 1},
	}))
	require.Equal(t, map[string]float32{"new": 1}, groupInfos.snapshot().weights)
	select {
	case <-changed:
	default:
		t.Fatal("newer update did not notify store observers")
	}
}

func TestPollerGroupInfoStoreAppliesFirstZeroVersion(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	groupInfos.updateGroups(testPollerGroupsInfo(0, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))

	require.Equal(t, map[string]float32{"group-a": 1}, groupInfos.snapshot().weights)
}

func TestPollerGroupManagersShareWeightsAndKeepCoverageSeparate(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	external := newPollerGroupManager(pollerGroupModeNonWorkflow, groupInfos)
	internal := newPollerGroupManager(pollerGroupModeNonWorkflow, groupInfos)

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
	require.Equal(t, 1, external.tracker.groups["group-a"].pendingPollCount)
	require.Equal(t, 1, internal.tracker.groups["group-a"].pendingPollCount)
	require.Equal(t, 2, internal.tracker.groups["group-b"].pendingPollCount)
}

func TestPollerGroupManagerReserveWorkflowPollSatisfiesCoverageFirst(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	lease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	_, ok := manager.tryReserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL)
	require.False(t, ok, "additional normal polls must wait for sticky coverage")

	lease = requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)

	lease = requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
}

func TestPollerGroupManagerStickyDisabledDoesNotRequireStickyCoverage(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflow, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	coverageLease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	defer coverageLease.release()

	floatingLease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	defer floatingLease.release()
	require.Equal(t, "group-a", floatingLease.groupIDOrEmpty())
}

func TestPollerGroupManagerReserveWorkflowPollFillsCoverageBeforeWeights(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "uncovered", Weight: 0},
		{Id: "covered", Weight: 100},
	}))

	lease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	lease = requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, "uncovered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	_, ok := manager.tryReserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL)
	require.False(t, ok, "weighted normal polls must wait for sticky coverage")

	lease = requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)

	lease = requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Equal(t, "uncovered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)

	lease = requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
}

func TestPollerGroupManagerReserveWorkflowPollKeepsRunnerQueueKind(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	stickyLease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, stickyLease.queueKind)

	normalLease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, normalLease.queueKind)

	extraStickyLease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, extraStickyLease.queueKind)
}

func TestPollerGroupManagerBlocksExtraNormalUntilAllStickyGroupsCovered(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 1},
		{Id: "group-c", Weight: 1},
	}))

	for range 3 {
		lease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
		require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
	}
	firstSticky := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, firstSticky.queueKind)

	_, ok := manager.tryReserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL)
	require.False(t, ok, "a fourth normal poll must wait while two sticky groups are uncovered")

	secondSticky := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	thirdSticky := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.NotEqual(t, firstSticky.groupIDOrEmpty(), secondSticky.groupIDOrEmpty())
	require.NotEqual(t, firstSticky.groupIDOrEmpty(), thirdSticky.groupIDOrEmpty())
	require.NotEqual(t, secondSticky.groupIDOrEmpty(), thirdSticky.groupIDOrEmpty())

	extraNormal := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, extraNormal.queueKind)
}

func TestPollerGroupManagerReserveWorkflowPollStickyCoverageAfterNormalRelease(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	lease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
	lease.release()

	lease = requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)

	_, ok := manager.tryReserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY)
	require.False(t, ok, "additional sticky polls must wait for normal coverage to be restored")
}

func TestPollerGroupLeaseReleaseUsesRequestGroupID(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "request-group", Weight: 100},
		{Id: "response-group", Weight: 0},
	}))

	lease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_NORMAL)
	require.Equal(t, "request-group", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	manager.tracker.groups["response-group"].workflowPendingNormal = 1
	lease.release()

	require.Equal(t, 0, manager.tracker.groups["request-group"].workflowPendingNormal)
	require.Equal(t, 1, manager.tracker.groups["response-group"].workflowPendingNormal)
}

func TestPollerGroupManagerReserveWorkflowPollFallsBackBeforeGroupsKnown(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeWorkflowWithSticky, nil)

	lease := requireWorkflowPollLease(t, manager, enumspb.TASK_QUEUE_KIND_STICKY)
	require.Empty(t, lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)
}

func TestPollerGroupManagerRemovedGroupLeaseReleaseIsSafe(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeNonWorkflow, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))
	lease := manager.reserve()

	manager.updateGroups(testPollerGroupsInfo(2, nil))
	require.Empty(t, manager.reserve().groupIDOrEmpty())
	require.NotPanics(t, lease.release)
}

func TestPollerGroupLeaseReleaseDoesNotAffectReaddedGroup(t *testing.T) {
	manager := newPollerGroupManager(pollerGroupModeNonWorkflow, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))
	oldLease := manager.reserve()

	manager.updateGroups(testPollerGroupsInfo(2, nil))
	require.Empty(t, manager.reserve().groupIDOrEmpty())

	manager.updateGroups(testPollerGroupsInfo(3, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))
	newLease := manager.reserve()
	newGroup := manager.tracker.groups["group-a"]
	require.Equal(t, 1, newGroup.pendingPollCount)

	oldLease.release()
	require.Equal(t, 1, newGroup.pendingPollCount)

	newLease.release()
	require.Equal(t, 0, newGroup.pendingPollCount)
}

func TestPollerGroupManagersConcurrentSharedUpdatesAndReservations(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	activity := newPollerGroupManager(pollerGroupModeNonWorkflow, groupInfos)
	workflow := newPollerGroupManager(pollerGroupModeWorkflow, groupInfos)
	groupInfos.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			activity.reserve().release()
		}()
		go func() {
			defer wg.Done()
			lease, ok := workflow.tryReserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL)
			if ok {
				lease.release()
			}
		}()
		go func() {
			defer wg.Done()
			groupInfos.updateGroups(testPollerGroupsInfo(int64(i+2), []*taskqueuepb.PollerGroupInfo{
				{Id: "group-a", Weight: 1},
				{Id: "group-b", Weight: 2},
			}))
		}()
	}
	wg.Wait()

	require.Equal(t, 2, activity.requiredMin())
	require.Equal(t, 2, workflow.requiredMin())
}
