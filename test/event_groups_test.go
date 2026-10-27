package test_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	sdkpb "go.temporal.io/api/sdk/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// History coverage for Event Groups.
//
// Skipped against test-plan-20260915:
//   - EG-LABEL-ID-00..04: retracted; callers supply IDs.
//   - EG-SCOPE-04/05: a goroutine only ever sees the Context value explicitly
//     passed to it; there is no ambient scope stack whose state could depend
//     on when the goroutine resumes relative to a scope's lexical extent.
//   - EG-SCOPE-06: scopes are an immutable Context value; withScope returns a
//     new child Context rather than mutating the caller's, so there is no
//     scope stack that could fail to unwind on panic.
//   - EG-IMPLICIT-00..30: signal delivery has no per-signal context, so channel
//     receives do not create an implicit group.
//   - EG-IMPLICIT-50/51/70: no static or catch-all update handlers.
//   - EG-IMPLICIT-80: the update handler runs with its own derived Context;
//     the main function keeps its original Context value and cannot observe
//     the update's implicit group
//   - EG-COMMANDS-22: no patched / deprecatePatch APIs.
//   - EG-COMMANDS-07-CHILD: no child-workflow cancel method. Canceling the
//     child context still requests cancel and inherits the start command's groups.

func (ts *IntegrationTestSuite) TestEventGroups_Commands() {
	skipOnCloud(ts.T(), cloudNeedsAdaptation, "requires custom namespace search attributes")
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsCommandsWorkflow},
		[]any{eventGroupActivity},
		eventGroupsCommandsWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-COMMANDS-02: Activity commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, "direct", "scope")
	// EG-COMMANDS-00: Timer commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "direct-timer", "scope")
	// EG-COMMANDS-01: Wait conditions with timeouts carry markers to their timers
	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "direct-await", "scope")
	// EG-COMMANDS-03: Local Activity commands carry markers
	ts.requireMarkerGroups(events, "LocalActivity", "direct", "scope")
	// EG-COMMANDS-20: Modify Workflow Properties commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_WORKFLOW_PROPERTIES_MODIFIED, "direct", "scope")
	// EG-COMMANDS-21: Upsert Search Attribute commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES, "direct", "scope")
	// EG-COMMANDS-24: Side Effect commands carry the ambient scope
	ts.requireMarkerGroups(events, "SideEffect", "direct", "scope")
	// EG-COMMANDS-25: Mutable Side Effect commands carry the ambient scope
	ts.requireMarkerGroups(events, "MutableSideEffect", "direct", "scope")
	// EG-COMMANDS-23: Version commands carry the ambient scope
	ts.requireMarkerGroups(events, "Version", "direct-version", "scope")
	ts.requireGroups(events, enumspb.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES, "direct-version", "scope")
}

func (ts *IntegrationTestSuite) TestEventGroups_LocalActivityBackoff() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsLocalActivityBackoffWorkflow},
		nil,
		eventGroupsLocalActivityBackoffWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-COMMANDS-03-BACKOFF: Local Activity Retry Backoff Timer carries LA's markers
	var markers []*historypb.HistoryEvent
	for _, event := range eventsWithGroups(events, enumspb.EVENT_TYPE_MARKER_RECORDED, "direct", "scope") {
		if event.GetMarkerRecordedEventAttributes().GetMarkerName() == "LocalActivity" {
			markers = append(markers, event)
		}
	}
	ts.Len(markers, 2)
	ts.Len(eventsOfType(events, enumspb.EVENT_TYPE_MARKER_RECORDED), 2)
	if len(markers) == 2 {
		ts.NotNil(markers[0].GetMarkerRecordedEventAttributes().GetFailure())
		ts.Nil(markers[1].GetMarkerRecordedEventAttributes().GetFailure())
	}
	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "direct", "scope")
}

func (ts *IntegrationTestSuite) TestEventGroups_LocalActivityCancel() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsLocalActivityCancelWorkflow},
		nil,
		eventGroupsLocalActivityCancelWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-COMMANDS-03-CANCEL: Local Activity Cancellation commands carry LA's markers
	ts.requireMarkerGroups(events, "LocalActivity", "cancel-trigger", "direct", "scope")
	cancelled := eventsWithGroups(events, enumspb.EVENT_TYPE_MARKER_RECORDED, "cancelled-la", "direct", "scope")
	ts.Len(cancelled, 1)
	if len(cancelled) == 1 {
		ts.Equal("LocalActivity", cancelled[0].GetMarkerRecordedEventAttributes().GetMarkerName())
		ts.NotNil(cancelled[0].GetMarkerRecordedEventAttributes().GetFailure().GetCanceledFailureInfo())
	}
}

func (ts *IntegrationTestSuite) TestEventGroups_CancelInherits() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsCancelWorkflow},
		[]any{eventGroupBlockingActivity},
		eventGroupsCancelWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-COMMANDS-02-CANCEL: Activity Cancellation commands carry Activity's markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, "direct", "scope")
	ts.requireGroups(events, enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED, "direct", "scope")
	// EG-COMMANDS-00-CANCEL: Timer Cancellation commands carry Timer's markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "direct", "scope")
	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_CANCELED, "direct", "scope")
}

func (ts *IntegrationTestSuite) TestEventGroups_Child() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsChildWorkflow, eventGroupChild, eventGroupSleepChild},
		nil,
		eventGroupsChildWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-COMMANDS-04: Child Workflow commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED, "direct", "scope")
	// EG-COMMANDS-04-CANCEL: Child Workflow Cancellation commands carry the Child Workflow's markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED, "direct", "scope")
	// EG-COMMANDS-06-CHILD: Signal Child Workflow commands carry markers.
	// This child is started outside the scope, so its start event carries no markers.
	ts.requireGroups(events, enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED)
	ts.requireGroups(events, enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED, "direct", "scope")
}

func (ts *IntegrationTestSuite) TestEventGroups_External() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsExternalWorkflow, eventGroupChild},
		nil,
		eventGroupsExternalWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-COMMANDS-06: Signal External Workflow commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED, "direct", "scope")
	// EG-COMMANDS-07: Cancel External Workflow commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED, "direct", "scope")
}

func (ts *IntegrationTestSuite) TestEventGroups_ScopeAndAggregation() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsScopeWorkflow},
		nil,
		eventGroupsScopeWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "extra", "shared")
	ts.Len(eventsWithGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "shared"), 1)
	plain := 0
	for _, event := range events {
		if event.GetEventType() == enumspb.EVENT_TYPE_TIMER_STARTED && len(event.GetEventGroupMarkers()) == 0 {
			plain++
		}
	}
	ts.Equal(1, plain)

	completed := eventsOfType(events, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED)
	ts.Len(completed, 1)
	ts.Empty(completed[0].GetEventGroupMarkers())
}

func (ts *IntegrationTestSuite) TestEventGroups_UpdateImplicit() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsUpdateWorkflow},
		[]any{eventGroupActivity},
		eventGroupsUpdateWorkflow,
	)
	ctx := context.Background()
	handle, err := ts.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   run.GetID(),
		RunID:        run.GetRunID(),
		UpdateID:     "upd-1",
		UpdateName:   "tick",
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	ts.NoError(err)
	ts.NoError(handle.Get(ctx, nil))
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "done", nil))
	ts.NoError(run.Get(ctx, nil))

	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())
	// EG-IMPLICIT-60: A runtime-registered update handler does not inherit its registration scope
	ev := ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "update:upd-1")
	ts.NotContains(markerIDs(ev), "outside")

	// EG-IMPLICIT-61: An explicit scope composes with a runtime-registered update handler's implicit scope
	inside := ts.requireActivityScheduled(events, "update-inside")
	ts.Equal([]string{"inside", "update:upd-1"}, markerIDs(inside))
}

func (ts *IntegrationTestSuite) TestEventGroups_ContinueAsNew() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsCANWorkflow},
		nil,
		eventGroupsCANWorkflow,
		true,
	)
	firstRunID := run.GetRunID()
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), firstRunID)
	// EG-COMMANDS-40: Continue-As-New carries directly attached and ambient markers
	continued := ts.requireGroups(events, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW, "can-ambient", "can-direct")
	nextRunID := continued.GetWorkflowExecutionContinuedAsNewEventAttributes().GetNewExecutionRunId()
	ts.NotEmpty(nextRunID)

	next := ts.eventGroupHistory(run.GetID(), nextRunID)
	completed := eventsOfType(next, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED)
	ts.Len(completed, 1)
	ts.Empty(completed[0].GetEventGroupMarkers())
}

func (ts *IntegrationTestSuite) TestEventGroups_LabelPayload() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsLabelWorkflow},
		nil,
		eventGroupsLabelWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())
	ev := ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "nolabel", "withlabel")

	// EG-LABEL-PAYLOAD-02: An omitted label produces no payload
	ts.Nil(eventGroupLabelPayload(ev, "nolabel"))
	// EG-LABEL-PAYLOAD-00: Label Payload converts to a json/plain JSON string
	payload := eventGroupLabelPayload(ev, "withlabel")
	ts.NotNil(payload)
	ts.Equal([]byte("json/plain"), payload.GetMetadata()[converter.MetadataEncoding])
	expected, err := converter.GetDefaultDataConverter().ToPayload("Hello")
	ts.NoError(err)
	ts.Equal(expected.GetData(), payload.GetData())
}

func (ts *IntegrationTestSuite) TestEventGroups_LabelPayloadIgnoresCustomConverter() {
	// The control activity's input is the proof the custom converter is actually
	// installed. Without it, a misconfigured converter could make the label
	// assertion below pass for the wrong reason.
	customDC := converter.NewCompositeDataConverter(
		converter.NewNilPayloadConverter(),
		converter.NewByteSlicePayloadConverter(),
		converter.NewProtoJSONPayloadConverter(),
		converter.NewProtoPayloadConverter(),
		customStringPayloadConverter{},
		converter.NewJSONPayloadConverter(),
	)
	c, err := ts.newIntegrationTestClient(func(options *client.Options) {
		options.DataConverter = customDC
	})
	ts.NoError(err)
	ts.T().Cleanup(c.Close)

	taskQueue := ts.taskQueueName + "-eg-" + uuid.NewString()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(eventGroupsLabelPayloadControlWorkflow)
	w.RegisterActivity(eventGroupControlActivity)
	w.RegisterActivity(eventGroupActivity)
	ts.NoError(w.Start())
	ts.T().Cleanup(w.Stop)

	opts := ts.startWorkflowOptions("eg-" + uuid.NewString())
	opts.TaskQueue = taskQueue
	run, err := c.ExecuteWorkflow(context.Background(), opts, eventGroupsLabelPayloadControlWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(context.Background(), nil))

	iter := c.GetWorkflowHistory(context.Background(), run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var events []*historypb.HistoryEvent
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		events = append(events, event)
	}

	var controlInput *commonpb.Payload
	for _, event := range eventsOfType(events, enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED) {
		if event.GetActivityTaskScheduledEventAttributes().GetActivityId() == "control" {
			controlInput = event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0]
		}
	}
	ts.NotNil(controlInput, "control activity event not found")
	ts.Equal([]byte("custom"), controlInput.GetMetadata()[converter.MetadataEncoding])
	ts.Equal([]byte("custom-converter-control"), controlInput.GetData())

	// EG-LABEL-PAYLOAD-01: Label Payload goes through the SDK's Default Payload Converter
	ev := ts.requireGroups(events, enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, "bbb")
	payload := eventGroupLabelPayload(ev, "bbb")
	ts.NotNil(payload)
	ts.Equal([]byte("json/plain"), payload.GetMetadata()[converter.MetadataEncoding])
	expected, err := converter.GetDefaultDataConverter().ToPayload("Label B")
	ts.NoError(err)
	ts.Equal(expected.GetData(), payload.GetData())
}

func (ts *IntegrationTestSuite) TestEventGroups_ScopeBaseline() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsScopeBaselineWorkflow, eventGroupNoopChildWorkflow},
		[]any{eventGroupActivity},
		eventGroupsScopeBaselineWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-SCOPE-00: Commands in an Event Group scope carry its marker
	ts.requireGroups(events, enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, "aaa")
	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "aaa")
	ts.requireGroups(events, enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED, "aaa")
}

func (ts *IntegrationTestSuite) TestEventGroups_ScopeNesting() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsScopeNestingWorkflow},
		[]any{eventGroupActivity},
		eventGroupsScopeNestingWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-SCOPE-01: Nesting Event Group Scopes composes correctly
	cases := []struct {
		activityID string
		ids        []string
	}{
		{"a-before", []string{"aaa"}},
		{"a-b", []string{"aaa", "bbb"}},
		{"a-after", []string{"aaa"}},
	}
	for _, tc := range cases {
		ev := ts.requireActivityScheduled(events, tc.activityID)
		ts.Equal(tc.ids, markerIDs(ev), tc.activityID)
	}
	outside := ts.requireActivityScheduled(events, "outside")
	ts.Empty(markerIDs(outside))
}

func (ts *IntegrationTestSuite) TestEventGroups_ScopeReenter() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsScopeReenterWorkflow},
		[]any{eventGroupActivity},
		eventGroupsScopeReenterWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-SCOPE-02: Re-entering an Event Group instance nests correctly
	for _, activityID := range []string{"a-before", "a-inner", "a-after"} {
		ev := ts.requireActivityScheduled(events, activityID)
		ts.Equal([]string{"aaa"}, markerIDs(ev), activityID)
	}
	outside := ts.requireActivityScheduled(events, "outside")
	ts.Empty(markerIDs(outside))
}

func (ts *IntegrationTestSuite) TestEventGroups_ScopeConcurrent() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsScopeConcurrentWorkflow},
		[]any{eventGroupActivity},
		eventGroupsScopeConcurrentWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-SCOPE-03: An Event Group instance can be scoped concurrently from two branches
	cases := []struct {
		activityID string
		ids        []string
	}{
		{"b-a-c", []string{"aaa", "bbb", "ccc"}},
		{"b-a", []string{"aaa", "bbb"}},
		{"b-after-a", []string{"bbb"}},
		{"d-a-e", []string{"aaa", "ddd", "eee"}},
		{"d-a", []string{"aaa", "ddd"}},
		{"d-after-a", []string{"ddd"}},
	}
	for _, tc := range cases {
		ev := ts.requireActivityScheduled(events, tc.activityID)
		ts.Equal(tc.ids, markerIDs(ev), tc.activityID)
	}
	outside := ts.requireActivityScheduled(events, "outside")
	ts.Empty(markerIDs(outside))
}

func (ts *IntegrationTestSuite) TestEventGroups_Aggregation() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsAggregationWorkflow},
		[]any{eventGroupActivity},
		eventGroupsAggregationWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	// EG-AGGREGATION-00: Duplicate markers directly attached to one command collapse into a set
	duplicates := ts.requireActivityScheduled(events, "direct-duplicates")
	ts.Equal([]string{"aaa", "b-id"}, markerIDs(duplicates))
	// EG-AGGREGATION-01: Nested scopes of duplicate groups collapse into a set
	nested := ts.requireActivityScheduled(events, "nested-scopes")
	ts.Equal([]string{"aaa", "b-id"}, markerIDs(nested))
	// EG-AGGREGATION-02: Scoped and directly attached markers collapse into one set
	scopeAndDirectB := ts.requireActivityScheduled(events, "scope-and-direct-b")
	ts.Equal([]string{"aaa", "b-id"}, markerIDs(scopeAndDirectB))
	scopeAndDirectAB := ts.requireActivityScheduled(events, "scope-and-direct-a-b")
	ts.Equal([]string{"aaa", "b-id"}, markerIDs(scopeAndDirectAB))
	// EG-AGGREGATION-03: The same group instance listed twice contributes one marker
	same := ts.requireActivityScheduled(events, "same-instance-twice")
	ts.Equal([]string{"aaa"}, markerIDs(same))
	// EG-AGGREGATION-04: Aggregation keys label markers by ID, disregarding labels
	sameIDDirect := ts.requireActivityScheduled(events, "same-id-direct")
	ts.Equal([]string{"b-id"}, markerIDs(sameIDDirect))
	sameIDScopeAndDirect := ts.requireActivityScheduled(events, "same-id-scope-and-direct")
	ts.Equal([]string{"b-id"}, markerIDs(sameIDScopeAndDirect))
}

func (ts *IntegrationTestSuite) TestEventGroups_LabelIDs() {
	run := ts.executeEventGroupWorkflow(
		[]any{eventGroupsLabelIDsWorkflow},
		[]any{eventGroupActivity},
		eventGroupsLabelIDsWorkflow,
	)
	ts.NoError(run.Get(context.Background(), nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())

	activityC := ts.requireActivityScheduled(events, "activity-c")
	activityD1 := ts.requireActivityScheduled(events, "activity-d1")
	activityD2 := ts.requireActivityScheduled(events, "activity-d2")
	activityNotC := ts.requireActivityScheduled(events, "activity-notC")
	// EG-LABEL-ID-20: IDs are used verbatim
	ts.Equal([]string{"c-id"}, markerIDs(activityC))
	// EG-LABEL-ID-21: Different labels + same ID => same group
	ts.Equal(markerIDs(activityD1), markerIDs(activityD2))
	ts.Equal([]string{"d-id"}, markerIDs(activityD1))
	// EG-LABEL-ID-22: Same label + different IDs => distinct groups
	ts.NotEqual(markerIDs(activityC), markerIDs(activityNotC))
	ts.Equal([]string{"not-c-id"}, markerIDs(activityNotC))
}

func (ts *IntegrationTestSuite) TestEventGroups_LabelPayloadCodec() {
	codec := converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true})
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
	c, err := ts.newIntegrationTestClient(func(options *client.Options) {
		options.DataConverter = codecDC
	})
	ts.NoError(err)
	ts.T().Cleanup(c.Close)

	taskQueue := ts.taskQueueName + "-eg-" + uuid.NewString()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(eventGroupsLabelPayloadCodecWorkflow)
	w.RegisterActivity(eventGroupActivity)
	ts.NoError(w.Start())
	ts.T().Cleanup(w.Stop)

	opts := ts.startWorkflowOptions("eg-" + uuid.NewString())
	opts.TaskQueue = taskQueue
	run, err := c.ExecuteWorkflow(context.Background(), opts, eventGroupsLabelPayloadCodecWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(context.Background(), nil))

	iter := c.GetWorkflowHistory(context.Background(), run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var events []*historypb.HistoryEvent
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		events = append(events, event)
	}

	activityA := ts.requireActivityScheduled(events, "activity-a")
	activityB := ts.requireActivityScheduled(events, "activity-b")
	// EG-LABEL-PAYLOAD-21: Label ID is not codec-encoded
	ts.Equal([]string{"aaa"}, markerIDs(activityA))
	ts.Equal([]string{"bbb"}, markerIDs(activityB))
	ts.Nil(eventGroupLabelPayload(activityA, "aaa"))

	// EG-LABEL-PAYLOAD-20: Label Payload is codec-encoded
	stored := eventGroupLabelPayload(activityB, "bbb")
	ts.NotNil(stored)
	plain, err := converter.GetDefaultDataConverter().ToPayload("Label B")
	ts.NoError(err)
	expected, err := codecDC.ToPayload(converter.NewRawValue(plain))
	ts.NoError(err)
	ts.True(proto.Equal(expected, stored))
	if stored != nil {
		decoded, err := codec.Decode([]*commonpb.Payload{stored})
		ts.NoError(err)
		ts.Len(decoded, 1)
		if len(decoded) == 1 {
			ts.True(proto.Equal(plain, decoded[0]))
		}
	}
}

func (ts *IntegrationTestSuite) TestEventGroups_Nexus() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "Temporal-backed Nexus tests create namespace endpoints through Operator Service")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	endpointName := "sdk-go-event-groups-" + uuid.NewString()
	taskQueue := ts.taskQueueName + "-nexus-" + uuid.NewString()
	res, err := ts.client.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: endpointName,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: ts.config.Namespace,
						TaskQueue: taskQueue,
					},
				},
			},
		},
	})
	ts.NoError(err)
	ts.T().Cleanup(func() {
		_, _ = ts.client.OperatorService().DeleteNexusEndpoint(context.Background(), &operatorservice.DeleteNexusEndpointRequest{
			Id:      res.GetEndpoint().GetId(),
			Version: res.GetEndpoint().GetVersion(),
		})
	})

	op := nexus.NewSyncOperation("op", func(ctx context.Context, _ string, _ nexus.StartOperationOptions) (string, error) {
		return "ok", nil
	})
	blockingOp := temporalnexus.NewWorkflowRunOperation(
		"blocking-op",
		eventGroupNexusHandlerWorkflow,
		func(_ context.Context, _ string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{ID: soo.RequestID}, nil
		},
	)
	wf := func(ctx workflow.Context) error {
		ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("scope"))
		c := workflow.NewNexusClient(endpointName, "test")
		fut := c.ExecuteOperation(ctx, op, "in", workflow.NexusOperationOptions{
			EventGroups: []workflow.EventGroup{workflow.NewEventGroup("direct")},
		})
		var result string
		if err := fut.Get(ctx, &result); err != nil {
			return err
		}

		cancelCtx, cancelOp := workflow.WithCancel(ctx)
		cancelFut := c.ExecuteOperation(cancelCtx, blockingOp, "in", workflow.NexusOperationOptions{
			CancellationType: workflow.NexusOperationCancellationTypeWaitRequested,
			EventGroups:      []workflow.EventGroup{workflow.NewEventGroup("direct-cancel")},
		})
		if err := cancelFut.GetNexusOperationExecution().Get(ctx, nil); err != nil {
			return err
		}
		cancelOp()
		if err := cancelFut.Get(ctx, nil); !temporal.IsCanceledError(err) {
			return fmt.Errorf("expected nexus operation to be canceled, got %v", err)
		}
		return nil
	}

	w := worker.New(ts.client, taskQueue, worker.Options{})
	service := nexus.NewService("test")
	ts.NoError(service.Register(op, blockingOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(wf)
	w.RegisterWorkflow(eventGroupNexusHandlerWorkflow)
	ts.NoError(w.Start())
	ts.T().Cleanup(w.Stop)

	opts := ts.startWorkflowOptions("eg-nexus-" + uuid.NewString())
	opts.TaskQueue = taskQueue
	opts.WorkflowExecutionTimeout = 30 * time.Second
	run, err := ts.client.ExecuteWorkflow(ctx, opts, wf)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())
	// EG-COMMANDS-05: Nexus Operation commands carry markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED, "direct", "scope")
	// EG-COMMANDS-05-CANCEL: Nexus Operation Cancellation commands carry the Nexus Operation's markers
	ts.requireGroups(events, enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED, "direct-cancel", "scope")
	ts.requireGroups(events, enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCEL_REQUESTED, "direct-cancel", "scope")
}

func eventGroupsCommandsWorkflow(ctx workflow.Context) error {
	scoped := workflow.WithEventGroups(ctx, workflow.NewEventGroup("scope"))
	direct := workflow.NewEventGroup("direct")

	actCtx := workflow.WithActivityOptions(scoped, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		EventGroups:         []workflow.EventGroup{direct},
	})
	if err := workflow.ExecuteActivity(actCtx, eventGroupActivity).Get(actCtx, nil); err != nil {
		return err
	}

	if err := workflow.NewTimerWithOptions(scoped, time.Millisecond, workflow.TimerOptions{
		EventGroups: []workflow.EventGroup{workflow.NewEventGroup("direct-timer")},
	}).Get(scoped, nil); err != nil {
		return err
	}

	_, err := workflow.AwaitWithOptions(scoped, workflow.AwaitOptions{
		Timeout: time.Millisecond,
		TimerOptions: workflow.TimerOptions{
			EventGroups: []workflow.EventGroup{workflow.NewEventGroup("direct-await")},
		},
	}, func() bool { return false })
	if err != nil {
		return err
	}

	laCtx := workflow.WithLocalActivityOptions(scoped, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
		EventGroups:            []workflow.EventGroup{direct},
	})
	if err := workflow.ExecuteLocalActivity(laCtx, eventGroupActivity).Get(laCtx, nil); err != nil {
		return err
	}

	if err := workflow.UpsertMemoWithOptions(scoped, map[string]any{"k": "v"}, workflow.UpsertMemoOptions{
		EventGroups: []workflow.EventGroup{direct},
	}); err != nil {
		return err
	}
	sa := temporal.NewSearchAttributeKeyKeyword("CustomKeywordField")
	if err := workflow.UpsertTypedSearchAttributesWithOptions(scoped, workflow.UpsertSearchAttributesOptions{
		EventGroups: []workflow.EventGroup{direct},
	}, sa.ValueSet("eg")); err != nil {
		return err
	}

	if err := workflow.SideEffectWithOptions(scoped, workflow.SideEffectOptions{
		EventGroups: []workflow.EventGroup{direct},
	}, func(workflow.Context) any { return 1 }).Get(new(int)); err != nil {
		return err
	}
	if err := workflow.MutableSideEffectWithOptions(scoped, "cfg", workflow.MutableSideEffectOptions{
		EventGroups: []workflow.EventGroup{direct},
	}, func(workflow.Context) any { return "a" }, func(a, b any) bool { return a == b }).Get(new(string)); err != nil {
		return err
	}
	_ = workflow.GetVersionWithOptions(scoped, "event-groups", workflow.DefaultVersion, 1, workflow.GetVersionOptions{
		EventGroups: []workflow.EventGroup{workflow.NewEventGroup("direct-version")},
	})
	return nil
}

func eventGroupsLocalActivityBackoffWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("scope"))
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			// Longer than the 1s workflow task timeout, forcing a server-side backoff timer.
			InitialInterval: 2 * time.Second,
			MaximumAttempts: 2,
		},
		EventGroups: []workflow.EventGroup{workflow.NewEventGroup("direct")},
	})
	return workflow.ExecuteLocalActivity(ctx, eventGroupFailFirstAttemptActivity).Get(ctx, nil)
}

func eventGroupsLocalActivityCancelWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("scope"))
	ctx, cancel := workflow.WithCancel(ctx)
	direct := workflow.NewEventGroup("direct")
	triggerCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
		EventGroups:            []workflow.EventGroup{direct, workflow.NewEventGroup("cancel-trigger")},
	})
	cancelledCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
		EventGroups:            []workflow.EventGroup{direct, workflow.NewEventGroup("cancelled-la")},
	})

	triggerFut := workflow.ExecuteLocalActivity(triggerCtx, eventGroupActivity)
	cancelledFut := workflow.ExecuteLocalActivity(cancelledCtx, eventGroupSleepActivity)
	if err := triggerFut.Get(ctx, nil); err != nil {
		return err
	}
	cancel()
	if err := cancelledFut.Get(ctx, nil); !temporal.IsCanceledError(err) {
		return fmt.Errorf("expected local activity to be canceled, got %v", err)
	}
	return nil
}

func eventGroupsCancelWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("scope"))
	direct := workflow.NewEventGroup("direct")

	actBase, cancelAct := workflow.WithCancel(ctx)
	actCtx := workflow.WithActivityOptions(actBase, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		WaitForCancellation: true,
		EventGroups:         []workflow.EventGroup{direct},
	})
	actFut := workflow.ExecuteActivity(actCtx, eventGroupBlockingActivity)
	cancelAct()
	_ = actFut.Get(actCtx, nil)

	timerBase, cancelTimer := workflow.WithCancel(ctx)
	timerFut := workflow.NewTimerWithOptions(timerBase, time.Hour, workflow.TimerOptions{
		EventGroups: []workflow.EventGroup{direct},
	})
	cancelTimer()
	_ = timerFut.Get(timerBase, nil)
	return nil
}

func eventGroupsChildWorkflow(ctx workflow.Context) error {
	scoped := workflow.WithEventGroups(ctx, workflow.NewEventGroup("scope"))
	direct := workflow.NewEventGroup("direct")

	cancelBase, cancelChild := workflow.WithCancel(scoped)
	cancelCtx := workflow.WithChildOptions(cancelBase, workflow.ChildWorkflowOptions{
		WorkflowRunTimeout: 10 * time.Second,
		EventGroups:        []workflow.EventGroup{direct},
	})
	cancelFut := workflow.ExecuteChildWorkflow(cancelCtx, eventGroupSleepChild)
	if err := cancelFut.GetChildWorkflowExecution().Get(cancelCtx, nil); err != nil {
		return err
	}
	cancelChild()
	if err := cancelFut.Get(ctx, nil); !temporal.IsCanceledError(err) {
		return fmt.Errorf("expected child workflow to be canceled, got %v", err)
	}

	signalCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowRunTimeout: 10 * time.Second,
	})
	signalFut := workflow.ExecuteChildWorkflow(signalCtx, eventGroupChild)
	if err := signalFut.SignalChildWorkflowWithOptions(scoped, "hold", nil, workflow.SignalChildWorkflowOptions{
		EventGroups: []workflow.EventGroup{direct},
	}).Get(ctx, nil); err != nil {
		return err
	}
	return signalFut.Get(ctx, nil)
}

func eventGroupsExternalWorkflow(ctx workflow.Context) error {
	scoped := workflow.WithEventGroups(ctx, workflow.NewEventGroup("scope"))
	direct := workflow.NewEventGroup("direct")

	signalCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowRunTimeout: 10 * time.Second,
	})
	signalFut := workflow.ExecuteChildWorkflow(signalCtx, eventGroupChild)
	var signalExec workflow.Execution
	if err := signalFut.GetChildWorkflowExecution().Get(ctx, &signalExec); err != nil {
		return err
	}
	if err := workflow.SignalExternalWorkflowWithOptions(scoped, signalExec.ID, "", "hold", nil, workflow.SignalExternalWorkflowOptions{
		EventGroups: []workflow.EventGroup{direct},
	}).Get(ctx, nil); err != nil {
		return err
	}
	if err := signalFut.Get(ctx, nil); err != nil {
		return err
	}

	cancelCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowRunTimeout: 10 * time.Second,
	})
	cancelFut := workflow.ExecuteChildWorkflow(cancelCtx, eventGroupChild)
	var cancelExec workflow.Execution
	if err := cancelFut.GetChildWorkflowExecution().Get(ctx, &cancelExec); err != nil {
		return err
	}
	return workflow.RequestCancelExternalWorkflowWithOptions(scoped, cancelExec.ID, "", workflow.CancelExternalWorkflowOptions{
		EventGroups: []workflow.EventGroup{direct},
	}).Get(ctx, nil)
}

func eventGroupsScopeWorkflow(ctx workflow.Context) error {
	scoped := workflow.WithEventGroups(ctx, workflow.NewEventGroupWithOptions("shared", workflow.EventGroupOptions{Label: "from-scope"}))
	if err := workflow.NewTimerWithOptions(scoped, time.Millisecond, workflow.TimerOptions{
		EventGroups: []workflow.EventGroup{
			workflow.NewEventGroupWithOptions("shared", workflow.EventGroupOptions{Label: "from-direct"}),
			workflow.NewEventGroup("extra"),
		},
	}).Get(scoped, nil); err != nil {
		return err
	}

	done := workflow.NewChannel(ctx)
	workflow.Go(scoped, func(ctx workflow.Context) {
		_ = workflow.Sleep(ctx, time.Millisecond)
		done.Send(ctx, nil)
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		_ = workflow.Sleep(ctx, time.Millisecond)
		done.Send(ctx, nil)
	})
	done.Receive(ctx, nil)
	done.Receive(ctx, nil)
	return nil
}

func eventGroupsUpdateWorkflow(ctx workflow.Context) error {
	outer := workflow.WithEventGroups(ctx, workflow.NewEventGroup("outside"))
	if err := workflow.SetUpdateHandler(outer, "tick", func(ctx workflow.Context) error {
		if err := workflow.Sleep(ctx, time.Millisecond); err != nil {
			return err
		}
		insideCtx := workflow.WithEventGroups(ctx, workflow.NewEventGroup("inside"))
		return executeGroupActivity(insideCtx, "update-inside")
	}); err != nil {
		return err
	}
	workflow.GetSignalChannel(ctx, "done").Receive(ctx, nil)
	return nil
}

func eventGroupsScopeBaselineWorkflow(ctx workflow.Context) error {
	scoped := workflow.WithEventGroups(ctx, workflow.NewEventGroup("aaa"))
	if err := executeGroupActivity(scoped, "activity"); err != nil {
		return err
	}
	if err := workflow.NewTimer(scoped, time.Millisecond).Get(scoped, nil); err != nil {
		return err
	}
	childCtx := workflow.WithChildOptions(scoped, workflow.ChildWorkflowOptions{
		WorkflowRunTimeout: 10 * time.Second,
	})
	return workflow.ExecuteChildWorkflow(childCtx, eventGroupNoopChildWorkflow).Get(childCtx, nil)
}

func eventGroupsScopeNestingWorkflow(ctx workflow.Context) error {
	a := workflow.NewEventGroup("aaa")
	b := workflow.NewEventGroup("bbb")
	aCtx := workflow.WithEventGroups(ctx, a)
	if err := executeGroupActivity(aCtx, "a-before"); err != nil {
		return err
	}
	if err := executeGroupActivity(workflow.WithEventGroups(aCtx, b), "a-b"); err != nil {
		return err
	}
	if err := executeGroupActivity(aCtx, "a-after"); err != nil {
		return err
	}
	return executeGroupActivity(ctx, "outside")
}

func eventGroupsScopeReenterWorkflow(ctx workflow.Context) error {
	a := workflow.NewEventGroup("aaa")
	scoped := workflow.WithEventGroups(ctx, a)
	if err := executeGroupActivity(scoped, "a-before"); err != nil {
		return err
	}
	if err := executeGroupActivity(workflow.WithEventGroups(scoped, a), "a-inner"); err != nil {
		return err
	}
	if err := executeGroupActivity(scoped, "a-after"); err != nil {
		return err
	}
	return executeGroupActivity(ctx, "outside")
}

func eventGroupsScopeConcurrentWorkflow(ctx workflow.Context) error {
	a := workflow.NewEventGroup("aaa")
	b := workflow.NewEventGroup("bbb")
	c := workflow.NewEventGroup("ccc")
	d := workflow.NewEventGroup("ddd")
	e := workflow.NewEventGroup("eee")

	done := workflow.NewChannel(ctx)
	workflow.Go(ctx, func(ctx workflow.Context) {
		bCtx := workflow.WithEventGroups(ctx, b)
		aCtx := workflow.WithEventGroups(bCtx, a)
		_ = executeGroupActivity(workflow.WithEventGroups(aCtx, c), "b-a-c")
		_ = executeGroupActivity(aCtx, "b-a")
		_ = executeGroupActivity(bCtx, "b-after-a")
		done.Send(ctx, nil)
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		dCtx := workflow.WithEventGroups(ctx, d)
		aCtx := workflow.WithEventGroups(dCtx, a)
		_ = executeGroupActivity(workflow.WithEventGroups(aCtx, e), "d-a-e")
		_ = executeGroupActivity(aCtx, "d-a")
		_ = executeGroupActivity(dCtx, "d-after-a")
		done.Send(ctx, nil)
	})
	done.Receive(ctx, nil)
	done.Receive(ctx, nil)
	return executeGroupActivity(ctx, "outside")
}

func eventGroupsAggregationWorkflow(ctx workflow.Context) error {
	a1 := workflow.NewEventGroup("aaa")
	a2 := workflow.NewEventGroup("aaa")
	b1 := workflow.NewEventGroupWithOptions("b-id", workflow.EventGroupOptions{Label: "bbb1"})
	b2 := workflow.NewEventGroupWithOptions("b-id", workflow.EventGroupOptions{Label: "bbb2"})

	if err := executeGroupActivity(ctx, "direct-duplicates", a2, b1, a1, b1, a2, a1); err != nil {
		return err
	}

	a1Ctx := workflow.WithEventGroups(ctx, a1)
	a1a2Ctx := workflow.WithEventGroups(a1Ctx, a2)
	a1a2b1Ctx := workflow.WithEventGroups(a1a2Ctx, b1)
	if err := executeGroupActivity(a1a2b1Ctx, "nested-scopes"); err != nil {
		return err
	}

	a1b1Ctx := workflow.WithEventGroups(a1Ctx, b1)
	if err := executeGroupActivity(a1b1Ctx, "scope-and-direct-b", b1); err != nil {
		return err
	}
	if err := executeGroupActivity(a1b1Ctx, "scope-and-direct-a-b", b1, a1); err != nil {
		return err
	}

	if err := executeGroupActivity(ctx, "same-instance-twice", a1, a1); err != nil {
		return err
	}

	if err := executeGroupActivity(ctx, "same-id-direct", b1, b2); err != nil {
		return err
	}
	b1Ctx := workflow.WithEventGroups(ctx, b1)
	return executeGroupActivity(b1Ctx, "same-id-scope-and-direct", b2)
}

func eventGroupsLabelIDsWorkflow(ctx workflow.Context) error {
	c := workflow.NewEventGroupWithOptions("c-id", workflow.EventGroupOptions{Label: "ccc"})
	d1 := workflow.NewEventGroupWithOptions("d-id", workflow.EventGroupOptions{Label: "ddd1"})
	d2 := workflow.NewEventGroupWithOptions("d-id", workflow.EventGroupOptions{Label: "ddd2"})
	notC := workflow.NewEventGroupWithOptions("not-c-id", workflow.EventGroupOptions{Label: "ccc"})
	if err := executeGroupActivity(ctx, "activity-c", c); err != nil {
		return err
	}
	if err := executeGroupActivity(ctx, "activity-d1", d1); err != nil {
		return err
	}
	if err := executeGroupActivity(ctx, "activity-d2", d2); err != nil {
		return err
	}
	return executeGroupActivity(ctx, "activity-notC", notC)
}

func eventGroupsLabelPayloadCodecWorkflow(ctx workflow.Context) error {
	if err := executeGroupActivity(ctx, "activity-a", workflow.NewEventGroup("aaa")); err != nil {
		return err
	}
	return executeGroupActivity(ctx, "activity-b", workflow.NewEventGroupWithOptions("bbb", workflow.EventGroupOptions{Label: "Label B"}))
}

func eventGroupsCANWorkflow(ctx workflow.Context, cont bool) error {
	if !cont {
		return nil
	}
	ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("can-ambient"))
	return workflow.NewContinueAsNewErrorWithOptions(ctx, workflow.ContinueAsNewErrorOptions{
		EventGroups: []workflow.EventGroup{workflow.NewEventGroup("can-direct")},
	}, eventGroupsCANWorkflow, false)
}

func eventGroupsLabelWorkflow(ctx workflow.Context) error {
	return workflow.NewTimerWithOptions(ctx, time.Millisecond, workflow.TimerOptions{
		EventGroups: []workflow.EventGroup{
			workflow.NewEventGroup("nolabel"),
			workflow.NewEventGroupWithOptions("withlabel", workflow.EventGroupOptions{Label: "Hello"}),
		},
	}).Get(ctx, nil)
}

func eventGroupsLabelPayloadControlWorkflow(ctx workflow.Context) error {
	controlCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		ActivityID:          "control",
	})
	if err := workflow.ExecuteActivity(controlCtx, eventGroupControlActivity, "control").Get(controlCtx, nil); err != nil {
		return err
	}

	labelCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		EventGroups:         []workflow.EventGroup{workflow.NewEventGroupWithOptions("bbb", workflow.EventGroupOptions{Label: "Label B"})},
	})
	return workflow.ExecuteActivity(labelCtx, eventGroupActivity).Get(labelCtx, nil)
}

func eventGroupChild(ctx workflow.Context) error {
	workflow.GetSignalChannel(ctx, "hold").Receive(ctx, nil)
	return nil
}

func eventGroupSleepChild(ctx workflow.Context) error {
	return workflow.Sleep(ctx, 10*time.Second)
}

func eventGroupNoopChildWorkflow(ctx workflow.Context) error { return nil }

func eventGroupNexusHandlerWorkflow(ctx workflow.Context, _ string) (string, error) {
	return "", workflow.Await(ctx, func() bool { return false })
}

func eventGroupActivity(context.Context) error { return nil }

func executeGroupActivity(ctx workflow.Context, activityID string, groups ...workflow.EventGroup) error {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		ActivityID:          activityID,
		EventGroups:         groups,
	})
	return workflow.ExecuteActivity(actCtx, eventGroupActivity).Get(actCtx, nil)
}

func eventGroupFailFirstAttemptActivity(ctx context.Context) error {
	if activity.GetInfo(ctx).Attempt == 1 {
		return temporal.NewApplicationError("retry", "Retry")
	}
	return nil
}

func eventGroupSleepActivity(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return nil
	}
}

func eventGroupControlActivity(_ context.Context, _ string) error { return nil }

// customStringPayloadConverter stands in for a user's custom payload converter. It
// gives strings a distinct encoding and a recognizable data prefix, so a test can
// tell whether a payload actually went through it or through the default converter.
type customStringPayloadConverter struct{}

const (
	customStringEncoding = "custom"
	customStringPrefix   = "custom-converter-"
)

func (customStringPayloadConverter) ToPayload(value any) (*commonpb.Payload, error) {
	str, ok := value.(string)
	if !ok {
		return nil, nil
	}
	return &commonpb.Payload{
		Metadata: map[string][]byte{converter.MetadataEncoding: []byte(customStringEncoding)},
		Data:     []byte(customStringPrefix + str),
	}, nil
}

func (customStringPayloadConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	strPtr, ok := valuePtr.(*string)
	if !ok {
		return fmt.Errorf("customStringPayloadConverter: value pointer must be *string, got %T", valuePtr)
	}
	*strPtr = strings.TrimPrefix(string(payload.GetData()), customStringPrefix)
	return nil
}

func (customStringPayloadConverter) ToString(payload *commonpb.Payload) string {
	return strings.TrimPrefix(string(payload.GetData()), customStringPrefix)
}

func (customStringPayloadConverter) Encoding() string {
	return customStringEncoding
}

func eventGroupBlockingActivity(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (ts *IntegrationTestSuite) executeEventGroupWorkflow(workflows []any, activities []any, wf any, args ...any) client.WorkflowRun {
	taskQueue := ts.taskQueueName + "-eg-" + uuid.NewString()
	w := worker.New(ts.client, taskQueue, worker.Options{})
	for _, workflowFn := range workflows {
		w.RegisterWorkflow(workflowFn)
	}
	for _, activityFn := range activities {
		w.RegisterActivity(activityFn)
	}
	ts.NoError(w.Start())
	ts.T().Cleanup(w.Stop)

	opts := ts.startWorkflowOptions("eg-" + uuid.NewString())
	opts.TaskQueue = taskQueue
	run, err := ts.client.ExecuteWorkflow(context.Background(), opts, wf, args...)
	ts.NoError(err)
	return run
}

func (ts *IntegrationTestSuite) eventGroupHistory(workflowID, runID string) []*historypb.HistoryEvent {
	iter := ts.client.GetWorkflowHistory(context.Background(), workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var events []*historypb.HistoryEvent
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		events = append(events, event)
	}
	return events
}

func (ts *IntegrationTestSuite) requireActivityScheduled(events []*historypb.HistoryEvent, activityID string) *historypb.HistoryEvent {
	var matched *historypb.HistoryEvent
	count := 0
	for _, event := range events {
		if event.GetEventType() != enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED {
			continue
		}
		if event.GetActivityTaskScheduledEventAttributes().GetActivityId() != activityID {
			continue
		}
		count++
		matched = event
	}
	ts.Equal(1, count, "activity %s scheduled count", activityID)
	return matched
}

func (ts *IntegrationTestSuite) requireGroups(events []*historypb.HistoryEvent, typ enumspb.EventType, ids ...string) *historypb.HistoryEvent {
	matched := eventsWithGroups(events, typ, ids...)
	ts.NotEmptyf(matched, "no %s event with groups %v", typ, ids)
	if len(matched) == 0 {
		return nil
	}
	return matched[0]
}

func (ts *IntegrationTestSuite) requireMarkerGroups(events []*historypb.HistoryEvent, markerName string, ids ...string) {
	var matched []*historypb.HistoryEvent
	for _, event := range eventsWithGroups(events, enumspb.EVENT_TYPE_MARKER_RECORDED, ids...) {
		if event.GetMarkerRecordedEventAttributes().GetMarkerName() == markerName {
			matched = append(matched, event)
		}
	}
	ts.NotEmptyf(matched, "no %s marker with groups %v", markerName, ids)
}

func eventsWithGroups(events []*historypb.HistoryEvent, typ enumspb.EventType, ids ...string) []*historypb.HistoryEvent {
	want := append([]string(nil), ids...)
	slices.Sort(want)
	var matched []*historypb.HistoryEvent
	for _, event := range events {
		if event.GetEventType() != typ {
			continue
		}
		if slices.Equal(markerIDs(event), want) {
			matched = append(matched, event)
		}
	}
	return matched
}

func eventsOfType(events []*historypb.HistoryEvent, typ enumspb.EventType) []*historypb.HistoryEvent {
	var matched []*historypb.HistoryEvent
	for _, event := range events {
		if event.GetEventType() == typ {
			matched = append(matched, event)
		}
	}
	return matched
}

func markerIDs(event *historypb.HistoryEvent) []string {
	ids := make([]string, 0, len(event.GetEventGroupMarkers()))
	for _, marker := range event.GetEventGroupMarkers() {
		switch variant := marker.GetVariant().(type) {
		case *sdkpb.EventGroupMarker_Label_:
			ids = append(ids, variant.Label.GetId())
		case *sdkpb.EventGroupMarker_InboundUpdate_:
			ids = append(ids, "update:"+variant.InboundUpdate.GetInboundUpdateId())
		case *sdkpb.EventGroupMarker_InboundEvent_:
			ids = append(ids, fmt.Sprintf("event:%d", variant.InboundEvent.GetInboundEventId()))
		}
	}
	slices.Sort(ids)
	return ids
}

func eventGroupLabelPayload(event *historypb.HistoryEvent, id string) *commonpb.Payload {
	for _, marker := range event.GetEventGroupMarkers() {
		if label := marker.GetLabel(); label.GetId() == id {
			return label.GetLabel()
		}
	}
	return nil
}

func (ts *IntegrationTestSuite) TestEventGroups_InterceptorCanAdjustGroups() {
	taskQueue := ts.taskQueueName + "-eg-" + uuid.NewString()
	w := worker.New(ts.client, taskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{&eventGroupWorkerInterceptor{}},
	})
	w.RegisterWorkflow(eventGroupsInterceptorWorkflow)
	ts.NoError(w.Start())
	ts.T().Cleanup(w.Stop)

	opts := ts.startWorkflowOptions("eg-" + uuid.NewString())
	opts.TaskQueue = taskQueue
	run, err := ts.client.ExecuteWorkflow(context.Background(), opts, eventGroupsInterceptorWorkflow)
	ts.NoError(err)
	_ = run.Get(context.Background(), nil)

	events := ts.eventGroupHistory(run.GetID(), run.GetRunID())
	ts.requireGroups(events, enumspb.EVENT_TYPE_TIMER_STARTED, "caller-scope", "interceptor-scope", "interceptor-direct")
	ts.requireGroups(events, enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED, "caller-scope", "interceptor-scope")
	ts.requireGroups(events, enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED, "caller-scope", "caller-signal")
}

func eventGroupsInterceptorWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("caller-scope"))
	if err := workflow.NewTimerWithOptions(ctx, time.Millisecond, workflow.TimerOptions{
		EventGroups: []workflow.EventGroup{workflow.NewEventGroup("caller-direct")},
	}).Get(ctx, nil); err != nil {
		return err
	}
	// The target does not exist, so Get fails after the command is recorded.
	_ = workflow.SignalExternalWorkflow(ctx, "external-target", "", "ping", nil).Get(ctx, nil)
	return workflow.SignalExternalWorkflowWithOptions(ctx, "external-target", "", "ping-options", nil, workflow.SignalExternalWorkflowOptions{
		EventGroups: []workflow.EventGroup{workflow.NewEventGroup("caller-signal")},
	}).Get(ctx, nil)
}

type eventGroupWorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func (eventGroupWorkerInterceptor) InterceptWorkflow(_ workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	i := &eventGroupWorkflowInbound{}
	i.Next = next
	return i
}

type eventGroupWorkflowInbound struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (e *eventGroupWorkflowInbound) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	i := &eventGroupWorkflowOutbound{}
	i.Next = outbound
	return e.Next.Init(i)
}

type eventGroupWorkflowOutbound struct {
	interceptor.WorkflowOutboundInterceptorBase
}

func (e *eventGroupWorkflowOutbound) NewTimerWithOptions(ctx workflow.Context, d time.Duration, options workflow.TimerOptions) workflow.Future {
	ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("interceptor-scope"))
	options.EventGroups = []workflow.EventGroup{workflow.NewEventGroup("interceptor-direct")}
	return e.Next.NewTimerWithOptions(ctx, d, options)
}

func (e *eventGroupWorkflowOutbound) SignalExternalWorkflow(ctx workflow.Context, workflowID, runID, signalName string, arg any) workflow.Future {
	ctx = workflow.WithEventGroups(ctx, workflow.NewEventGroup("interceptor-scope"))
	return e.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}
