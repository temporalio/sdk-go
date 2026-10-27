package internal

import (
	"fmt"

	sdkpb "go.temporal.io/api/sdk/v1"

	"go.temporal.io/sdk/converter"
)

type eventGroupKind int

const (
	eventGroupKindLabel eventGroupKind = iota
	eventGroupKindInboundUpdate
)

// EventGroupOptions are optional settings for an explicit Event Group.
//
// NOTE: Experimental (Event Groups)
//
// Exposed as: [go.temporal.io/sdk/workflow.EventGroupOptions]
type EventGroupOptions struct {
	// Label is an optional display string for this group. When empty, the marker
	// carries only its ID and no label payload is emitted. When set, the label is
	// converted with the default payload converter (json/plain) and then given to
	// the workflow data converter as a raw value, so payload codecs run and the
	// label is not converted again. The ID itself is never codec-encoded.
	//
	// Do not put secrets in IDs. Labels may contain sensitive data because they
	// are codec-encoded.
	//
	// NOTE: Experimental (Event Groups)
	Label string
}

// EventGroup is a user-facing handle for an Event Group Marker.
//
// Note: Contrary to other SDKs, the Go SDK does not emit implicit Event Groups
// for inbound signals.
//
// NOTE: Experimental (Event Groups)
//
// Exposed as: [go.temporal.io/sdk/workflow.EventGroup]
type EventGroup struct {
	// ID is the group identity. An explicit group records the caller-supplied ID
	// verbatim. The implicit update group uses "inbound-update:" plus the update
	// ID. The marker variant is not derived from this string.
	//
	// Do not put secrets in IDs. The ID is never codec-encoded.
	//
	// NOTE: Experimental (Event Groups)
	ID string

	kind            eventGroupKind
	label           string
	inboundUpdateID string
}

type eventGroupsContextKey struct{}

// eventGroupFrame is the effective ambient set for one scope. groups is keyed
// by explicit group ID and already excludes later duplicates, so the earliest
// label for an ID wins. implicit is the update handler's group when one is
// active. It is not a label, so it stays out of that ID space. A nested scope
// copies groups so inserting into the child does not change the parent.
type eventGroupFrame struct {
	groups   map[string]EventGroup
	implicit *EventGroup
}

// NewEventGroup creates an explicit Event Group with the given ID and no label.
// The ID is recorded verbatim. It must not be empty.
//
// NOTE: Experimental (Event Groups)
//
// Exposed as: [go.temporal.io/sdk/workflow.NewEventGroup]
func NewEventGroup(id string) EventGroup {
	return NewEventGroupWithOptions(id, EventGroupOptions{})
}

// NewEventGroupWithOptions creates an explicit Event Group with the given ID
// and options.
//
// NOTE: Experimental (Event Groups)
//
// Exposed as: [go.temporal.io/sdk/workflow.NewEventGroupWithOptions]
func NewEventGroupWithOptions(id string, options EventGroupOptions) EventGroup {
	if id == "" {
		panic("event group ID must not be empty")
	}
	return EventGroup{
		kind:  eventGroupKindLabel,
		ID:    id,
		label: options.Label,
	}
}

func implicitUpdateEventGroup(updateID string) EventGroup {
	return EventGroup{
		kind:            eventGroupKindInboundUpdate,
		ID:              "inbound-update:" + updateID,
		inboundUpdateID: updateID,
	}
}

func (group EventGroup) toMarker(dc converter.DataConverter) (*sdkpb.EventGroupMarker, error) {
	switch group.kind {
	case eventGroupKindInboundUpdate:
		return &sdkpb.EventGroupMarker{
			Variant: &sdkpb.EventGroupMarker_InboundUpdate_{
				InboundUpdate: &sdkpb.EventGroupMarker_InboundUpdate{
					InboundUpdateId: group.inboundUpdateID,
				},
			},
		}, nil
	default:
		label := &sdkpb.EventGroupMarker_Label{Id: group.ID}
		if group.label != "" {
			payload, err := converter.GetDefaultDataConverter().ToPayload(group.label)
			if err != nil {
				return nil, fmt.Errorf("convert event group label: %w", err)
			}
			// Hand the default-converter payload to the workflow data converter as a
			// raw value so codecs apply without running user payload conversion.
			if dc != nil {
				payload, err = dc.ToPayload(converter.NewRawValue(payload))
				if err != nil {
					return nil, fmt.Errorf("encode event group label: %w", err)
				}
			}
			label.Label = payload
		}
		return &sdkpb.EventGroupMarker{Variant: &sdkpb.EventGroupMarker_Label_{Label: label}}, nil
	}
}

// WithEventGroups returns a derived context whose commands also carry groups.
// Nested calls are additive. Groups that share an identity collapse to the
// first one.
//
// NOTE: Experimental (Event Groups)
//
// Exposed as: [go.temporal.io/sdk/workflow.WithEventGroups]
func WithEventGroups(ctx Context, groups ...EventGroup) Context {
	if len(groups) == 0 {
		return ctx
	}
	parent := ambientFrame(ctx)
	frame := &eventGroupFrame{groups: make(map[string]EventGroup, len(groups))}
	if parent != nil {
		frame.implicit = parent.implicit
		if len(parent.groups) > 0 {
			frame.groups = make(map[string]EventGroup, len(parent.groups)+len(groups))
			for id, group := range parent.groups {
				frame.groups[id] = group
			}
		}
	}
	for _, group := range groups {
		if _, exists := frame.groups[group.ID]; exists {
			continue
		}
		frame.groups[group.ID] = group
	}
	return WithValue(ctx, eventGroupsContextKey{}, frame)
}

// withImplicitUpdateEventGroup replaces ambient groups with the update's
// implicit group. Handlers must not inherit an explicit scope that was active
// when the handler was registered or when the update was dispatched.
// A blank update ID fails the workflow task: the handler would otherwise run
// with neither an implicit group nor the surrounding explicit scope.
func withImplicitUpdateEventGroup(ctx Context, updateID string) Context {
	if updateID == "" {
		panic("update ID must not be empty")
	}
	group := implicitUpdateEventGroup(updateID)
	frame := &eventGroupFrame{implicit: &group}
	return WithValue(ctx, eventGroupsContextKey{}, frame)
}

func ambientFrame(ctx Context) *eventGroupFrame {
	frame, _ := ctx.Value(eventGroupsContextKey{}).(*eventGroupFrame)
	return frame
}

func ambientEventGroups(ctx Context) []EventGroup {
	frame := ambientFrame(ctx)
	if frame == nil {
		return nil
	}
	groups := make([]EventGroup, 0, len(frame.groups)+1)
	if frame.implicit != nil {
		groups = append(groups, *frame.implicit)
	}
	for _, group := range frame.groups {
		groups = append(groups, group)
	}
	return groups
}

func explicitAmbientEventGroups(ctx Context) []EventGroup {
	frame := ambientFrame(ctx)
	if frame == nil || len(frame.groups) == 0 {
		return nil
	}
	groups := make([]EventGroup, 0, len(frame.groups))
	for _, group := range frame.groups {
		groups = append(groups, group)
	}
	return groups
}

// mergeEventGroups unions explicit groups in order. The first group for an ID
// wins, including which label is kept when IDs collide. Implicit groups are
// not part of this ID space.
func mergeEventGroups(sets ...[]EventGroup) []EventGroup {
	seen := make(map[string]struct{})
	var merged []EventGroup
	for _, set := range sets {
		for _, group := range set {
			if _, ok := seen[group.ID]; ok {
				continue
			}
			seen[group.ID] = struct{}{}
			merged = append(merged, group)
		}
	}
	return merged
}

func mergeAmbientAndDirectEventGroups(ctx Context, direct []EventGroup) []EventGroup {
	explicit := mergeEventGroups(explicitAmbientEventGroups(ctx), direct)
	frame := ambientFrame(ctx)
	if frame == nil || frame.implicit == nil {
		return explicit
	}
	groups := make([]EventGroup, 0, len(explicit)+1)
	groups = append(groups, *frame.implicit)
	groups = append(groups, explicit...)
	return groups
}

func eventGroupMarkersToProto(ctx Context, direct []EventGroup) []*sdkpb.EventGroupMarker {
	groups := mergeAmbientAndDirectEventGroups(ctx, direct)
	if len(groups) == 0 {
		return nil
	}
	dc := GetDataConverterFromWorkflowContext(ctx)
	markers := make([]*sdkpb.EventGroupMarker, 0, len(groups))
	for _, group := range groups {
		marker, err := group.toMarker(dc)
		if err != nil {
			panic(fmt.Sprintf("failed to build event group marker: %v", err))
		}
		markers = append(markers, marker)
	}
	return markers
}
