package workflow

import (
	"go.temporal.io/sdk/internal"
)

// EventGroupOptions are optional settings for an explicit Event Group.
//
// NOTE: Experimental (Event Groups)
type EventGroupOptions = internal.EventGroupOptions

// EventGroup is a user-facing handle for an Event Group Marker.
//
// NOTE: Experimental (Event Groups)
type EventGroup = internal.EventGroup

// NewEventGroup creates an explicit Event Group with the given ID and no label.
// The ID is recorded verbatim and must not be empty.
//
// NOTE: Experimental (Event Groups)
func NewEventGroup(id string) EventGroup {
	return internal.NewEventGroup(id)
}

// NewEventGroupWithOptions creates an explicit Event Group with the given ID
// and options.
//
// NOTE: Experimental (Event Groups)
func NewEventGroupWithOptions(id string, options EventGroupOptions) EventGroup {
	return internal.NewEventGroupWithOptions(id, options)
}

// WithEventGroups returns a derived context whose commands also carry groups.
// Nested calls are additive. Groups that share an identity collapse to the first one.
//
// NOTE: Experimental (Event Groups)
func WithEventGroups(ctx Context, groups ...EventGroup) Context {
	return internal.WithEventGroups(ctx, groups...)
}
