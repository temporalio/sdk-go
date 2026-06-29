package internal

import (
	"testing"

	"github.com/stretchr/testify/require"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
)

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
