package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
)

func TestMergeEventGroupsFirstIdentityWins(t *testing.T) {
	first := NewEventGroupWithOptions("same", EventGroupOptions{Label: "first"})
	second := NewEventGroupWithOptions("same", EventGroupOptions{Label: "second"})
	other := NewEventGroup("other")
	merged := mergeEventGroups([]EventGroup{first}, []EventGroup{second, other})
	require.Equal(t, []EventGroup{first, other}, merged)
}

func TestEventGroupLabelUsesDefaultConverterThenCodecs(t *testing.T) {
	group := NewEventGroupWithOptions("customer-123", EventGroupOptions{Label: "Customer: Jane"})
	codec := &prefixCodec{prefix: "encoded:"}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)

	marker, err := group.toMarker(dc)
	require.NoError(t, err)
	label := marker.GetLabel()
	require.Equal(t, "customer-123", label.GetId())
	require.Equal(t, []byte("encoded:json/plain"), label.GetLabel().GetMetadata()[converter.MetadataEncoding])

	plain, err := NewEventGroup("id-only").toMarker(dc)
	require.NoError(t, err)
	require.Nil(t, plain.GetLabel().GetLabel())
	require.Equal(t, "id-only", plain.GetLabel().GetId())
}

func TestImplicitUpdateEventGroupReplacesAmbient(t *testing.T) {
	ctx := newTestWorkflowContext()
	ctx = WithEventGroups(ctx, NewEventGroup("ambient"))
	ctx = withImplicitUpdateEventGroup(ctx, "update-1")
	groups := ambientEventGroups(ctx)
	require.Len(t, groups, 1)
	require.Equal(t, eventGroupKindInboundUpdate, groups[0].kind)
	require.Equal(t, "inbound-update:update-1", groups[0].ID)
	require.Equal(t, "update-1", groups[0].inboundUpdateID)

	marker, err := groups[0].toMarker(converter.GetDefaultDataConverter())
	require.NoError(t, err)
	require.Equal(t, "update-1", marker.GetInboundUpdate().GetInboundUpdateId())

	ctx = WithEventGroups(ctx, NewEventGroup("inside"))
	require.ElementsMatch(t, []string{"inbound-update:update-1", "inside"}, eventGroupIDs(ambientEventGroups(ctx)))
}

func TestNestedEventGroupScopesKeepOuterLabel(t *testing.T) {
	ctx := newTestWorkflowContext()
	outer := NewEventGroupWithOptions("same", EventGroupOptions{Label: "outer"})
	inner := NewEventGroupWithOptions("same", EventGroupOptions{Label: "inner"})
	added := []EventGroup{inner, NewEventGroup("other")}
	ctx = WithEventGroups(ctx, outer)
	ctx = WithEventGroups(ctx, added...)
	added[0] = NewEventGroup("mutated")

	groups := ambientEventGroups(ctx)
	require.ElementsMatch(t, []EventGroup{outer, NewEventGroup("other")}, groups)
}

func TestMergeAmbientAndDirectEventGroups(t *testing.T) {
	ctx := newTestWorkflowContext()
	scope := NewEventGroupWithOptions("same", EventGroupOptions{Label: "from-scope"})
	direct := NewEventGroupWithOptions("same", EventGroupOptions{Label: "from-direct"})
	ctx = WithEventGroups(ctx, scope)
	require.Equal(t, []EventGroup{scope, NewEventGroup("only-direct")},
		mergeAmbientAndDirectEventGroups(ctx, []EventGroup{direct, NewEventGroup("only-direct")}))
	require.Equal(t, []EventGroup{scope}, mergeAmbientAndDirectEventGroups(ctx, nil))
}

func eventGroupIDs(groups []EventGroup) []string {
	ids := make([]string, len(groups))
	for i, group := range groups {
		ids[i] = group.ID
	}
	return ids
}

type prefixCodec struct {
	prefix string
}

func (c *prefixCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		encoding := append([]byte(c.prefix), payload.GetMetadata()[converter.MetadataEncoding]...)
		meta := map[string][]byte{converter.MetadataEncoding: encoding}
		out[i] = &commonpb.Payload{Metadata: meta, Data: payload.Data}
	}
	return out, nil
}

func (c *prefixCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return payloads, nil
}
