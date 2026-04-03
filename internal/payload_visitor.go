package internal

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"google.golang.org/protobuf/proto"
)

type PayloadVisitor interface {
	Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error)
}

type compositePayloadVisitor struct {
	visitors []PayloadVisitor
}

var _ PayloadVisitor = (*compositePayloadVisitor)(nil)

func (v *compositePayloadVisitor) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	var err error
	for _, visitor := range v.visitors {
		payloads, err = visitor.Visit(ctx, payloads)
		if err != nil {
			return nil, err
		}
	}
	return payloads, err
}

func newCompositePayloadVisitor(visitors ...PayloadVisitor) PayloadVisitor {
	return &compositePayloadVisitor{
		visitors: visitors,
	}
}

// visitProtoPayloads runs visitor over all payloads in msg, skipping search
// attributes. If visitor is nil, msg is unchanged.
func visitProtoPayloads(ctx context.Context, visitor PayloadVisitor, msg proto.Message) error {
	if visitor == nil {
		return nil
	}
	return proxy.VisitPayloads(ctx, msg, proxy.VisitPayloadsOptions{
		Visitor:              visitor.Visit,
		SkipSearchAttributes: true,
	})
}

// visitPayload runs visitor over a single payload. If visitor is nil
// the original payload is returned unchanged.
func visitPayload(ctx context.Context, visitor PayloadVisitor, p *commonpb.Payload) (*commonpb.Payload, error) {
	if visitor == nil {
		return p, nil
	}
	vpc := &proxy.VisitPayloadsContext{Context: ctx}
	visited, err := visitor.Visit(vpc, []*commonpb.Payload{p})
	if err != nil {
		return nil, err
	}
	if len(visited) == 0 {
		return nil, nil
	}
	return visited[0], nil
}
