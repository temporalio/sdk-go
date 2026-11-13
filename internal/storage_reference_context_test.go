package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	sdkpb "go.temporal.io/api/sdk/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
)

func newTestReferenceContexts() *workflowReferenceContexts {
	contexts := newWorkflowReferenceContexts("ns", "wf", "WorkflowType")
	contexts.taskQueue = "workflow-tq"
	return contexts
}

func encodedReference(t *testing.T, codec converter.PayloadCodec) *commonpb.Payload {
	t.Helper()
	data, err := protojson.Marshal(&sdkpb.ExternalStorageReference{DriverName: "d", ClaimData: map[string]string{"key": "k"}})
	require.NoError(t, err)
	encoded, err := codec.Encode([]*commonpb.Payload{{
		Metadata: map[string][]byte{
			converter.MetadataEncoding:    []byte("json/protobuf"),
			converter.MetadataMessageType: []byte("temporal.api.sdk.v1.ExternalStorageReference"),
		},
		Data: data,
	}})
	require.NoError(t, err)
	encoded[0].Metadata["__temporal_external_storage_reference"] = []byte("true")
	return encoded[0]
}

func resolveHistory(t *testing.T, contexts *workflowReferenceContexts, dc converter.DataConverter, events ...*historypb.HistoryEvent) {
	t.Helper()
	inner := newCompositePayloadVisitor(extstore.NewReferenceOnlyDecodingVisitor(newReferenceCodecProvider(dc)))
	visitor := newWorkflowTaskInboundVisitor(inner, dc, contexts)
	require.NoError(t, visitProtoPayloads(t.Context(), visitor, &historypb.History{Events: events}, 0))
}

func activityScheduledEvent(eventID int64) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:   eventID,
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
		Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
			ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
				ActivityType: &commonpb.ActivityType{Name: "MyActivity"},
				TaskQueue:    &taskqueuepb.TaskQueue{Name: "activity-tq"},
			},
		},
	}
}

func activityCompletedEvent(scheduledEventID int64, result *commonpb.Payload) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
		Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
			ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
				ScheduledEventId: scheduledEventID,
				Result:           &commonpb.Payloads{Payloads: []*commonpb.Payload{result}},
			},
		},
	}
}

func TestWorkflowReferenceContexts_EventContexts(t *testing.T) {
	contexts := newTestReferenceContexts()
	workflowContext := converter.WorkflowSerializationContext{Namespace: "ns", WorkflowID: "wf"}
	activityContext := converter.ActivitySerializationContext{
		Namespace: "ns", WorkflowID: "wf", WorkflowType: "WorkflowType", ActivityType: "MyActivity", TaskQueue: "activity-tq",
	}
	nexusContext := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}

	require.Equal(t, activityContext, contexts.eventContext(activityScheduledEvent(5)))
	require.Equal(t, activityContext, contexts.eventContext(activityCompletedEvent(5, nil)))
	// A result whose scheduled event was never seen falls back to the workflow context.
	require.Equal(t, workflowContext, contexts.eventContext(activityCompletedEvent(99, nil)))

	require.Equal(t, nexusContext, contexts.eventContext(&historypb.HistoryEvent{
		EventId:   7,
		EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED,
		Attributes: &historypb.HistoryEvent_NexusOperationScheduledEventAttributes{
			NexusOperationScheduledEventAttributes: &historypb.NexusOperationScheduledEventAttributes{
				Endpoint: "endpoint", Service: "service", Operation: "operation",
			},
		},
	}))
	require.Equal(t, nexusContext, contexts.eventContext(&historypb.HistoryEvent{
		EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_COMPLETED,
		Attributes: &historypb.HistoryEvent_NexusOperationCompletedEventAttributes{
			NexusOperationCompletedEventAttributes: &historypb.NexusOperationCompletedEventAttributes{ScheduledEventId: 7},
		},
	}))

	require.Equal(t, converter.WorkflowSerializationContext{Namespace: "ns", WorkflowID: "child"}, contexts.eventContext(&historypb.HistoryEvent{
		EventType: enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
		Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
			ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
				WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "child"},
			},
		},
	}))

	require.Equal(t, workflowContext, contexts.eventContext(&historypb.HistoryEvent{
		EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED,
	}))
}

func TestWorkflowTaskInboundVisitor_ActivityResultInLaterTask(t *testing.T) {
	var decoded []converter.SerializationContext
	codec := contextRecordingCodec{decoded: &decoded}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
	contexts := newTestReferenceContexts()

	// The scheduled event and its result arrive in separate workflow tasks, as on a
	// sticky worker.
	resolveHistory(t, contexts, dc, activityScheduledEvent(5))
	resolveHistory(t, contexts, dc, activityCompletedEvent(5, encodedReference(t, codec)))

	require.Equal(t, []converter.SerializationContext{converter.ActivitySerializationContext{
		Namespace: "ns", WorkflowID: "wf", WorkflowType: "WorkflowType", ActivityType: "MyActivity", TaskQueue: "activity-tq",
	}}, decoded)
}

func TestWorkflowTaskInboundVisitor_LocalActivityResult(t *testing.T) {
	var decoded []converter.SerializationContext
	codec := contextRecordingCodec{decoded: &decoded}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
	contexts := newTestReferenceContexts()

	markerData, err := dc.ToPayloads(localActivityMarkerData{ActivityID: "1", ActivityType: "MyLocalActivity"})
	require.NoError(t, err)
	resolveHistory(t, contexts, dc, &historypb.HistoryEvent{
		EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{
			MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName: localActivityMarkerName,
				Details: map[string]*commonpb.Payloads{
					localActivityMarkerDataName: markerData,
					localActivityResultName:     {Payloads: []*commonpb.Payload{encodedReference(t, codec)}},
				},
			},
		},
	})

	// The marker data is decoded with the workflow context to learn the activity
	// type, then the result reference with the local activity's context.
	require.Equal(t, []converter.SerializationContext{
		converter.WorkflowSerializationContext{Namespace: "ns", WorkflowID: "wf"},
		converter.ActivitySerializationContext{
			Namespace: "ns", WorkflowID: "wf", WorkflowType: "WorkflowType",
			ActivityType: "MyLocalActivity", TaskQueue: "workflow-tq", IsLocal: true,
		},
	}, decoded)
}

func TestWorkflowTaskInboundVisitor_LocalActivityWithoutReferenceSkipsCodec(t *testing.T) {
	var decoded []converter.SerializationContext
	codec := contextRecordingCodec{decoded: &decoded}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)

	markerData, err := dc.ToPayloads(localActivityMarkerData{ActivityID: "1", ActivityType: "MyLocalActivity"})
	require.NoError(t, err)
	result, err := dc.ToPayloads("result")
	require.NoError(t, err)
	resolveHistory(t, newTestReferenceContexts(), dc, &historypb.HistoryEvent{
		EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{
			MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName: localActivityMarkerName,
				Details: map[string]*commonpb.Payloads{
					localActivityMarkerDataName: markerData,
					localActivityResultName:     result,
				},
			},
		},
	})

	require.Empty(t, decoded)
}
