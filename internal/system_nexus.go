package internal

import (
	"context"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	systemnexus "go.temporal.io/sdk/temporalnexus/system"
)

func isSystemNexusOperation(service, operation string) bool {
	return service == systemnexus.WorkflowService.ServiceName &&
		operation == systemnexus.WorkflowService.SignalWithStartWorkflowExecution.Name()
}

func getSystemNexusPayloadConverter() converter.DataConverter {
	return converter.GetDefaultDataConverter()
}

type payloadVisitorFunc func(*proxy.VisitPayloadsContext, []*commonpb.Payload) ([]*commonpb.Payload, error)

func (f payloadVisitorFunc) Visit(
	ctx *proxy.VisitPayloadsContext,
	payloads []*commonpb.Payload,
) ([]*commonpb.Payload, error) {
	return f(ctx, payloads)
}

type systemNexusOutboundPayloadVisitor struct {
	dataConverter converter.DataConverter
	next          PayloadVisitor
}

func newSystemNexusOutboundPayloadVisitor(
	dataConverter converter.DataConverter,
	next PayloadVisitor,
) PayloadVisitor {
	return &systemNexusOutboundPayloadVisitor{
		dataConverter: dataConverter,
		next:          next,
	}
}

func (v *systemNexusOutboundPayloadVisitor) Visit(
	ctx *proxy.VisitPayloadsContext,
	payloads []*commonpb.Payload,
) ([]*commonpb.Payload, error) {
	attrs, ok := ctx.Parent.(*commandpb.ScheduleNexusOperationCommandAttributes)
	if ok &&
		ctx.SinglePayloadRequired &&
		len(payloads) == 1 &&
		isSystemNexusOperation(attrs.GetService(), attrs.GetOperation()) {
		return v.rewriteSystemNexusPayload(ctx.Context, payloads[0])
	}
	if v.next == nil {
		return payloads, nil
	}
	return v.next.Visit(ctx, payloads)
}

func (v *systemNexusOutboundPayloadVisitor) rewriteSystemNexusPayload(
	ctx context.Context,
	payload *commonpb.Payload,
) ([]*commonpb.Payload, error) {
	req := &workflowservicepb.SignalWithStartWorkflowExecutionRequest{}
	if err := getSystemNexusPayloadConverter().FromPayload(payload, req); err != nil {
		return nil, err
	}

	nestedVisitor := payloadVisitorFunc(func(
		visitCtx *proxy.VisitPayloadsContext,
		nestedPayloads []*commonpb.Payload,
	) ([]*commonpb.Payload, error) {
		encodedPayloads, err := encodeSystemNexusNestedPayloads(v.dataConverter, nestedPayloads)
		if err != nil {
			return nil, err
		}
		if v.next == nil {
			return encodedPayloads, nil
		}
		return v.next.Visit(visitCtx, encodedPayloads)
	})
	if err := visitProtoPayloads(ctx, nestedVisitor, req); err != nil {
		return nil, err
	}

	rewrittenPayload, err := getSystemNexusPayloadConverter().ToPayload(req)
	if err != nil {
		return nil, err
	}
	return []*commonpb.Payload{rewrittenPayload}, nil
}

func encodeSystemNexusNestedPayloads(
	dataConverter converter.DataConverter,
	payloads []*commonpb.Payload,
) ([]*commonpb.Payload, error) {
	if len(payloads) == 0 {
		return nil, nil
	}

	rawPayloads := make([]interface{}, len(payloads))
	for i, payload := range payloads {
		rawPayloads[i] = converter.NewRawValue(payload)
	}

	encoded, err := dataConverter.ToPayloads(rawPayloads...)
	if err != nil || encoded == nil {
		return nil, err
	}
	return encoded.Payloads, nil
}
