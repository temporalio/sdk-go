package internal

import (
	"context"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	systemnexus "go.temporal.io/api/workflowservice/v1/workflowservicenexus/json"

	"go.temporal.io/sdk/converter"
)

const systemNexusPayloadConverterContextKey contextKey = "systemNexusPayloadConverter"

func getSystemNexusPayloadConverter() converter.DataConverter {
	return converter.GetDefaultDataConverter()
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
		systemnexus.IsTemporalNexusOperation(attrs.GetService(), attrs.GetOperation()) {
		return v.rewriteSystemNexusPayload(ctx, attrs.GetService(), attrs.GetOperation(), payloads[0])
	}
	if v.next == nil {
		return payloads, nil
	}
	return v.next.Visit(ctx, payloads)
}

func (v *systemNexusOutboundPayloadVisitor) rewriteSystemNexusPayload(
	visitCtx *proxy.VisitPayloadsContext,
	service string,
	operation string,
	payload *commonpb.Payload,
) ([]*commonpb.Payload, error) {
	visitor := systemnexus.GetTemporalNexusPayloadVisitor(service, operation)
	if visitor == nil {
		return []*commonpb.Payload{payload}, nil
	}

	rewrittenPayload, err := visitor(payload, func(
		nestedPayloads []*commonpb.Payload,
	) ([]*commonpb.Payload, error) {
		encodedPayloads, err := encodeSystemNexusNestedPayloads(
			v.dataConverterForContext(visitCtx.Context),
			nestedPayloads,
		)
		if err != nil {
			return nil, err
		}
		if v.next == nil {
			return encodedPayloads, nil
		}
		return v.next.Visit(&proxy.VisitPayloadsContext{Context: visitCtx.Context}, encodedPayloads)
	}, false)
	if err != nil {
		return nil, err
	}
	return []*commonpb.Payload{rewrittenPayload}, nil
}

func (v *systemNexusOutboundPayloadVisitor) dataConverterForContext(ctx context.Context) converter.DataConverter {
	if ctx != nil {
		if dc, ok := ctx.Value(systemNexusPayloadConverterContextKey).(converter.DataConverter); ok && dc != nil {
			return dc
		}
	}
	return v.dataConverter
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
