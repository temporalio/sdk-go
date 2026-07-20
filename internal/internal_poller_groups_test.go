package internal

import (
	"testing"

	"github.com/stretchr/testify/require"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

func TestPollerGroupTrackerReserveActivityNexusPollFillsCoverageBeforeWeights(t *testing.T) {
	tracker := newPollerGroupTracker(false)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "uncovered", Weight: 0},
		{Id: "covered", Weight: 100},
	})

	groupID := tracker.reserve()
	require.Equal(t, "covered", groupID)
	require.Equal(t, 1, tracker.groups["covered"].pendingPollCount)

	groupID = tracker.reserve()
	require.Equal(t, "uncovered", groupID)
	require.Equal(t, 1, tracker.groups["uncovered"].pendingPollCount)

	groupID = tracker.reserve()
	require.Equal(t, "covered", groupID)
	require.Equal(t, 2, tracker.groups["covered"].pendingPollCount)
}

func TestPollerGroupTrackerEmptyUpdateClearsGroups(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	require.False(t, tracker.updateGroups(nil))

	require.True(t, tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	}))
	require.Equal(t, 1, tracker.requiredMin(enumspb.TASK_QUEUE_KIND_NORMAL))

	require.True(t, tracker.updateGroups(nil))
	require.Equal(t, 0, tracker.requiredMin(enumspb.TASK_QUEUE_KIND_NORMAL))

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Empty(t, groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)
}

// Signaling is only needed when group membership changes, not when weight-only changes occur
func TestPollerGroupManagerUpdateSignalsOnlyOnMembershipChanges(t *testing.T) {
	manager := newPollerGroupManager(true)
	signals := 0
	manager.addListener(func() {
		signals++
	})

	manager.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 100},
		{Id: "group-b", Weight: 0},
	})
	require.Equal(t, 1, signals)

	manager.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 0},
		{Id: "group-b", Weight: 100},
	})
	require.Equal(t, 1, signals, "weight-only updates should not signal listeners")

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, false)
	defer lease.release()
	require.Equal(t, "group-b", lease.groupIDOrEmpty(), "weight-only updates should affect future reservations")

	manager.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 0},
		{Id: "group-b", Weight: 100},
		{Id: "group-c", Weight: 1},
	})
	require.Equal(t, 2, signals, "adding a group should signal listeners")

	manager.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-b", Weight: 100},
		{Id: "group-c", Weight: 1},
	})
	require.Equal(t, 3, signals, "removing a group should signal listeners")

	manager.updateGroups(nil)
	require.Equal(t, 4, signals, "clearing groups should signal listeners")

	manager.updateGroups(nil)
	require.Equal(t, 4, signals, "empty update should not signal when groups are already empty")
}

func TestPollerGroupTrackerReserveWorkflowPollSatisfiesCoverageFirst(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	})

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)

	groupID, queueKind = tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, queueKind)
}

func TestPollerGroupTrackerReserveWorkflowPollFillsCoverageBeforeWeights(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "uncovered", Weight: 0},
		{Id: "covered", Weight: 100},
	})

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "covered", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)

	groupID, queueKind = tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "covered", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, queueKind)

	groupID, queueKind = tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "uncovered", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)

	groupID, queueKind = tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "uncovered", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, queueKind)

	groupID, queueKind = tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "covered", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)
}

func TestPollerGroupTrackerReserveWorkflowPollPrefersFallbackKindForCoverage(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	})

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	require.Equal(t, "group-a", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, queueKind)

	groupID, queueKind = tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)
}

func TestPollerGroupTrackerReserveWorkflowPollStickyCoverageAfterNormalRelease(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	})

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)
	tracker.release(groupID, queueKind)

	groupID, queueKind = tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	require.Equal(t, "group-a", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, queueKind)
}

func TestPollerGroupTrackerReserveWorkflowPollUsesGroupStickyBacklogForFloatingPoll(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 0},
		{Id: "group-b", Weight: 1},
	})
	tracker.groups["group-a"].workflowPendingNormal = 1
	tracker.groups["group-a"].workflowPendingSticky = 1
	tracker.groups["group-b"].workflowPendingNormal = 1
	tracker.groups["group-b"].workflowPendingSticky = 1
	tracker.groups["group-b"].workflowStickyBacklog = 2

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-b", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, queueKind)
	require.Equal(t, 2, tracker.groups["group-b"].workflowPendingSticky)
}

func TestPollerGroupTrackerReserveWorkflowPollUsesNormalWhenStickyBacklogCovered(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
	})
	tracker.groups["group-a"].workflowPendingNormal = 1
	tracker.groups["group-a"].workflowPendingSticky = 2
	tracker.groups["group-a"].workflowStickyBacklog = 2

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "group-a", groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, queueKind)
	require.Equal(t, 2, tracker.groups["group-a"].workflowPendingNormal)
}

func TestPollerGroupManagerWorkflowStickyBacklogUsesResponseGroupID(t *testing.T) {
	manager := newPollerGroupManager(true)
	manager.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "request-group", Weight: 100},
		{Id: "response-group", Weight: 0},
	})

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

	manager.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "request-group", Weight: 0},
		{Id: "response-group", Weight: 100},
	})
	manager.updateStickyBacklog("response-group", 2)

	require.Equal(t, int64(0), manager.tracker.groups["request-group"].workflowStickyBacklog)
	require.Equal(t, int64(2), manager.tracker.groups["response-group"].workflowStickyBacklog)

	floatingLease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	defer floatingLease.release()
	require.Equal(t, "response-group", floatingLease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, floatingLease.queueKind)
}

func TestPollerGroupLeaseReleaseUsesRequestGroupID(t *testing.T) {
	manager := newPollerGroupManager(true)
	manager.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "request-group", Weight: 100},
		{Id: "response-group", Weight: 0},
	})

	lease := manager.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_NORMAL, true)
	require.Equal(t, "request-group", lease.groupIDOrEmpty())
	require.Equal(t, enumspb.TASK_QUEUE_KIND_NORMAL, lease.queueKind)

	manager.tracker.groups["response-group"].workflowPendingNormal = 1

	lease.release()

	require.Equal(t, 0, manager.tracker.groups["request-group"].workflowPendingNormal)
	require.Equal(t, 1, manager.tracker.groups["response-group"].workflowPendingNormal)
}

func TestPollerGroupTrackerUpdateStickyBacklogUsesKnownWorkflowGroup(t *testing.T) {
	tracker := newPollerGroupTracker(true)
	tracker.updateGroups([]*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 1},
	})

	tracker.updateStickyBacklog("group-b", 42)
	tracker.updateStickyBacklog("unknown", 100)

	require.Equal(t, int64(0), tracker.groups["group-a"].workflowStickyBacklog)
	require.Equal(t, int64(42), tracker.groups["group-b"].workflowStickyBacklog)
}

func TestPollerGroupTrackerReserveWorkflowPollFallsBackBeforeGroupsKnown(t *testing.T) {
	tracker := newPollerGroupTracker(true)

	groupID, queueKind := tracker.reserveWorkflowPoll(enumspb.TASK_QUEUE_KIND_STICKY, true)
	require.Empty(t, groupID)
	require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, queueKind)
}
