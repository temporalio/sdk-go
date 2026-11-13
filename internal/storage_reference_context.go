package internal

import (
	"cmp"
	"context"
	"slices"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/proxy"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
)

// workflowReferenceContexts is keyed by scheduled event ID. A result event refers
// to a scheduled event that may have arrived in an earlier workflow task, so the
// table lives as long as the cached run. It is only used while the run's workflow
// context is locked, or before the context is shared.
type workflowReferenceContexts struct {
	namespace          string
	workflowID         string
	workflowType       string
	taskQueue          string
	byScheduledEventID map[int64]converter.SerializationContext
}

func newWorkflowReferenceContexts(namespace, workflowID, workflowType string) *workflowReferenceContexts {
	return &workflowReferenceContexts{
		namespace:          namespace,
		workflowID:         workflowID,
		workflowType:       workflowType,
		byScheduledEventID: map[int64]converter.SerializationContext{},
	}
}

func (c *workflowReferenceContexts) workflowContext() converter.WorkflowSerializationContext {
	return converter.WorkflowSerializationContext{Namespace: c.namespace, WorkflowID: c.workflowID}
}

func (c *workflowReferenceContexts) otherWorkflowContext(namespace, workflowID string) converter.WorkflowSerializationContext {
	return converter.WorkflowSerializationContext{Namespace: cmp.Or(namespace, c.namespace), WorkflowID: workflowID}
}

func (c *workflowReferenceContexts) scheduledContext(scheduledEventID int64) converter.SerializationContext {
	if sc, ok := c.byScheduledEventID[scheduledEventID]; ok {
		return sc
	}
	return c.workflowContext()
}

func (c *workflowReferenceContexts) eventContext(event *historypb.HistoryEvent) converter.SerializationContext {
	switch event.GetEventType() {
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
		c.taskQueue = event.GetWorkflowExecutionStartedEventAttributes().GetTaskQueue().GetName()
	case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
		attrs := event.GetActivityTaskScheduledEventAttributes()
		sc := converter.ActivitySerializationContext{
			Namespace:    c.namespace,
			WorkflowID:   c.workflowID,
			WorkflowType: c.workflowType,
			ActivityType: attrs.GetActivityType().GetName(),
			TaskQueue:    attrs.GetTaskQueue().GetName(),
		}
		c.byScheduledEventID[event.GetEventId()] = sc
		return sc
	case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
		return c.scheduledContext(event.GetActivityTaskCompletedEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
		return c.scheduledContext(event.GetActivityTaskFailedEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
		return c.scheduledContext(event.GetActivityTaskTimedOutEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED:
		return c.scheduledContext(event.GetActivityTaskCanceledEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED:
		attrs := event.GetNexusOperationScheduledEventAttributes()
		sc := converter.NexusSerializationContext{
			Endpoint:  attrs.GetEndpoint(),
			Service:   attrs.GetService(),
			Operation: attrs.GetOperation(),
		}
		c.byScheduledEventID[event.GetEventId()] = sc
		return sc
	case enumspb.EVENT_TYPE_NEXUS_OPERATION_COMPLETED:
		return c.scheduledContext(event.GetNexusOperationCompletedEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_NEXUS_OPERATION_FAILED:
		return c.scheduledContext(event.GetNexusOperationFailedEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCELED:
		return c.scheduledContext(event.GetNexusOperationCanceledEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_NEXUS_OPERATION_TIMED_OUT:
		return c.scheduledContext(event.GetNexusOperationTimedOutEventAttributes().GetScheduledEventId())
	case enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED:
		attrs := event.GetStartChildWorkflowExecutionInitiatedEventAttributes()
		return c.otherWorkflowContext(attrs.GetNamespace(), attrs.GetWorkflowId())
	case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
		attrs := event.GetChildWorkflowExecutionCompletedEventAttributes()
		return c.otherWorkflowContext(attrs.GetNamespace(), attrs.GetWorkflowExecution().GetWorkflowId())
	case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_FAILED:
		attrs := event.GetChildWorkflowExecutionFailedEventAttributes()
		return c.otherWorkflowContext(attrs.GetNamespace(), attrs.GetWorkflowExecution().GetWorkflowId())
	case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_CANCELED:
		attrs := event.GetChildWorkflowExecutionCanceledEventAttributes()
		return c.otherWorkflowContext(attrs.GetNamespace(), attrs.GetWorkflowExecution().GetWorkflowId())
	case enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED:
		attrs := event.GetSignalExternalWorkflowExecutionInitiatedEventAttributes()
		return c.otherWorkflowContext(attrs.GetNamespace(), attrs.GetWorkflowExecution().GetWorkflowId())
	}
	return c.workflowContext()
}

type workflowTaskInboundVisitor struct {
	inner         PayloadVisitor
	dataConverter converter.DataConverter
	contexts      *workflowReferenceContexts
}

func newWorkflowTaskInboundVisitor(
	inner PayloadVisitor,
	dataConverter converter.DataConverter,
	contexts *workflowReferenceContexts,
) PayloadVisitor {
	if inner == nil {
		return nil
	}
	if dataConverter == nil {
		dataConverter = converter.GetDefaultDataConverter()
	}
	return &workflowTaskInboundVisitor{inner: inner, dataConverter: dataConverter, contexts: contexts}
}

func (v *workflowTaskInboundVisitor) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return v.inner.Visit(ctx, payloads)
}

func (v *workflowTaskInboundVisitor) WithMessageLimit(ctx context.Context) context.Context {
	if limited, ok := v.inner.(PayloadVisitorWithMessageLimit); ok {
		return limited.WithMessageLimit(ctx)
	}
	return ctx
}

func (v *workflowTaskInboundVisitor) ContextHook(ctx context.Context, msg proto.Message) (context.Context, error) {
	if event, ok := msg.(*historypb.HistoryEvent); ok {
		sc := v.contexts.eventContext(event)
		if laContext, ok, err := v.localActivityContext(ctx, event); err != nil {
			return nil, err
		} else if ok {
			sc = laContext
		}
		ctx = withReferenceSerializationContext(ctx, sc)
	} else if _, ok := ctx.Value(referenceSerializationContextKey{}).(converter.SerializationContext); !ok {
		// Payloads outside history events, such as queries and update requests,
		// are decoded with the workflow context.
		ctx = withReferenceSerializationContext(ctx, v.contexts.workflowContext())
	}
	if hookVisitor, ok := v.inner.(PayloadVisitorWithContextHook); ok {
		return hookVisitor.ContextHook(ctx, msg)
	}
	return ctx, nil
}

// localActivityContext decodes the marker's data payload first because the
// activity type is only recorded there, encoded with the workflow context.
func (v *workflowTaskInboundVisitor) localActivityContext(ctx context.Context, event *historypb.HistoryEvent) (converter.SerializationContext, bool, error) {
	attrs := event.GetMarkerRecordedEventAttributes()
	if attrs.GetMarkerName() != localActivityMarkerName {
		return nil, false, nil
	}
	data := attrs.GetDetails()[localActivityMarkerDataName]
	if len(data.GetPayloads()) == 0 {
		return nil, false, nil
	}
	// Learning the activity type costs a codec call, so only pay it when the
	// marker's result or failure holds a reference to decode.
	if hasReference, err := markerHasReference(ctx, attrs); err != nil || !hasReference {
		return nil, false, err
	}

	workflowContext := v.contexts.workflowContext()
	for i, p := range data.Payloads {
		resolved, err := visitPayload(withReferenceSerializationContext(ctx, workflowContext), v.inner, p)
		if err != nil {
			return nil, false, err
		}
		data.Payloads[i] = resolved
	}
	var markerData localActivityMarkerData
	dc := converter.WithDataConverterSerializationContext(v.dataConverter, workflowContext)
	if err := dc.FromPayloads(data, &markerData); err != nil {
		return nil, false, err
	}
	return converter.ActivitySerializationContext{
		Namespace:    v.contexts.namespace,
		WorkflowID:   v.contexts.workflowID,
		WorkflowType: v.contexts.workflowType,
		ActivityType: markerData.ActivityType,
		TaskQueue:    v.contexts.taskQueue,
		IsLocal:      true,
	}, true, nil
}

func markerHasReference(ctx context.Context, attrs *historypb.MarkerRecordedEventAttributes) (bool, error) {
	for name, payloads := range attrs.GetDetails() {
		if name != localActivityMarkerDataName && slices.ContainsFunc(payloads.GetPayloads(), extstore.HasReferenceMarker) {
			return true, nil
		}
	}
	if attrs.GetFailure() == nil {
		return false, nil
	}
	found := false
	err := proxy.VisitPayloads(ctx, attrs.GetFailure(), proxy.VisitPayloadsOptions{
		Visitor: func(_ *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
			found = found || slices.ContainsFunc(payloads, extstore.HasReferenceMarker)
			return payloads, nil
		},
	})
	return found, err
}
