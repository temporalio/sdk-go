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

func TestPollerGroupManagerReserveActivityNexusPollFillsCoverageBeforeWeights(t *testing.T) {
	manager := newPollerGroupManager(false, nil)
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
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, nil))
	require.Equal(t, 0, manager.requiredMin(enumspb.TASK_QUEUE_KIND_NORMAL))

	manager.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))
	require.Equal(t, 1, manager.requiredMin(enumspb.TASK_QUEUE_KIND_NORMAL))

	manager.updateGroups(testPollerGroupsInfo(3, nil))
	require.Equal(t, 0, manager.requiredMin(enumspb.TASK_QUEUE_KIND_NORMAL))

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Empty(t, lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
}

func TestPollerGroupInfoStoreOnlyAppliesNewerVersions(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	groupInfos.updateGroups(testPollerGroupsInfo(10, []*taskqueuepb.PollerGroupInfo{
		{Id: "current", Weight: 1},
	}))

	groupInfos.updateGroups(testPollerGroupsInfo(9, []*taskqueuepb.PollerGroupInfo{
		{Id: "stale", Weight: 1},
	}))
	groupInfos.updateGroups(testPollerGroupsInfo(10, []*taskqueuepb.PollerGroupInfo{
		{Id: "duplicate", Weight: 1},
	}))
	groupInfos.updateGroups(nil)
	require.Equal(t, map[string]float32{"current": 1}, groupInfos.snapshot())

	groupInfos.updateGroups(testPollerGroupsInfo(11, []*taskqueuepb.PollerGroupInfo{
		{Id: "new", Weight: 1},
	}))
	require.Equal(t, map[string]float32{"new": 1}, groupInfos.snapshot())
}

func TestPollerGroupInfoStoreAppliesFirstZeroVersion(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	groupInfos.updateGroups(testPollerGroupsInfo(0, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))

	require.Equal(t, map[string]float32{"group-a": 1}, groupInfos.snapshot())
}

func TestPollerGroupManagersShareWeightsAndKeepCoverageSeparate(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	external := newPollerGroupManager(false, groupInfos)
	internal := newPollerGroupManager(false, groupInfos)

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
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	lease = manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)
}

func TestPollerGroupManagerReserveWorkflowPollFillsCoverageBeforeWeights(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "uncovered", Weight: 0},
		{Id: "covered", Weight: 100},
	}))

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	lease = manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)

	lease = manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "uncovered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	lease = manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "uncovered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)

	lease = manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "covered", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
}

func TestPollerGroupManagerReserveWorkflowPollPrefersFallbackKindForCoverage(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)

	lease = manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
}

func TestPollerGroupManagerReserveWorkflowPollStickyCoverageAfterNormalRelease(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
	lease.release()

	lease = manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)
}

func TestPollerGroupManagerReserveWorkflowPollUsesGroupStickyBacklogForFloatingPoll(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 0},
		{Id: "group-b", Weight: 1},
	}))

	for range 4 {
		manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	}
	manager.updateStickyBacklog("group-b", 2)

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-b", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)
	require.Equal(t, 2, manager.tracker.groups["group-b"].workflowPendingSticky)
}

func TestPollerGroupManagerReserveWorkflowPollUsesNormalWhenStickyBacklogCovered(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))

	manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	manager.updateStickyBacklog("group-a", 2)
	manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)
	require.Equal(t, 2, manager.tracker.groups["group-a"].workflowPendingNormal)
}

func TestPollerGroupManagerWorkflowStickyBacklogUsesResponseGroupID(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "request-group", Weight: 100},
		{Id: "response-group", Weight: 0},
	}))

	requestNormalLease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	defer requestNormalLease.release()
	require.Equal(t, "request-group", requestNormalLease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, requestNormalLease.queueKind)

	requestStickyLease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	defer requestStickyLease.release()
	require.Equal(t, "request-group", requestStickyLease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, requestStickyLease.queueKind)

	responseNormalLease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	defer responseNormalLease.release()
	require.Equal(t, "response-group", responseNormalLease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, responseNormalLease.queueKind)

	responseStickyLease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	defer responseStickyLease.release()
	require.Equal(t, "response-group", responseStickyLease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, responseStickyLease.queueKind)

	manager.updateGroups(testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{
		{Id: "request-group", Weight: 0},
		{Id: "response-group", Weight: 100},
	}))
	manager.updateStickyBacklog("response-group", 2)

	require.Equal(t, int64(0), manager.tracker.groups["request-group"].workflowStickyBacklog)
	require.Equal(t, int64(2), manager.tracker.groups["response-group"].workflowStickyBacklog)

	floatingLease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	defer floatingLease.release()
	require.Equal(t, "response-group", floatingLease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, floatingLease.queueKind)
}

func TestPollerGroupLeaseReleaseUsesRequestGroupID(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "request-group", Weight: 100},
		{Id: "response-group", Weight: 0},
	}))

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "request-group", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	manager.tracker.groups["response-group"].workflowPendingNormal = 1
	lease.release()

	require.Equal(t, 0, manager.tracker.groups["request-group"].workflowPendingNormal)
	require.Equal(t, 1, manager.tracker.groups["response-group"].workflowPendingNormal)
}

func TestPollerGroupManagerUpdateStickyBacklogUsesKnownWorkflowGroup(t *testing.T) {
	manager := newPollerGroupManager(true, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 1},
	}))

	manager.updateStickyBacklog("group-b", 42)
	manager.updateStickyBacklog("unknown", 100)

	require.Equal(t, int64(0), manager.tracker.groups["group-a"].workflowStickyBacklog)
	require.Equal(t, int64(42), manager.tracker.groups["group-b"].workflowStickyBacklog)
}

func TestPollerGroupManagerReserveWorkflowPollFallsBackBeforeGroupsKnown(t *testing.T) {
	manager := newPollerGroupManager(true, nil)

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	require.Empty(t, lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, lease.queueKind)
}

func TestPollerGroupManagerRemovedGroupLeaseReleaseIsSafe(t *testing.T) {
	manager := newPollerGroupManager(false, nil)
	manager.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: "group-a", Weight: 1}}))
	lease := manager.reserve()

	manager.updateGroups(testPollerGroupsInfo(2, nil))
	require.Empty(t, manager.reserve().groupIDOrEmpty())
	require.NotPanics(t, lease.release)
}

func TestPollerGroupManagersConcurrentSharedUpdatesAndReservations(t *testing.T) {
	groupInfos := newPollerGroupInfoStore()
	activity := newPollerGroupManager(false, groupInfos)
	workflow := newPollerGroupManager(true, groupInfos)
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
			workflow.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true).release()
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

	require.Equal(t, 2, activity.requiredMin(enumspb.TASK_QUEUE_KIND_NORMAL))
	require.Equal(t, 2, workflow.requiredMin(enumspb.TASK_QUEUE_KIND_NORMAL))
}
