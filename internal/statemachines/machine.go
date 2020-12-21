package statemachines

import (
	commandpb "go.temporal.io/api/command/v1"
	enumspb "go.temporal.io/api/enums/v1"
)

type ExplicitEvent int16

const (
	ExplicitEventSchedule ExplicitEvent = iota
	ExplicitEventCancel
)

func (e ExplicitEvent) String() string {
	switch e {
	case ExplicitEventSchedule:
		return "SCHEDULE"
	case ExplicitEventCancel:
		return "CANCEL"
	default:
		panic("Tried to stringify unknown explicit event")
	}
}

type (
	StateMachine interface {
		applyExplicitEvent(explicitEvent ExplicitEvent)
		applyCommand(commandType *enumspb.CommandType)
		handleHistoryEvent(eventType *enumspb.EventType)
	}

	CancellableCommand struct {
		cmd *commandpb.Command
	}

	CommandSink interface {
		dispatch(cmd CancellableCommand) error
	}

	MachineEvent interface {
		// Marker method to register types as machine events. Should only ever be ExplicitEvent, CommandEvent, and
		// HistoricalEvent
		isMachineEvent()
		name() string
	}

	CommandEvent struct {
		enumspb.CommandType
	}

	HistoricalEvent struct {
		enumspb.EventType
	}
)

func (ExplicitEvent) isMachineEvent()   {}
func (e ExplicitEvent) name() string    { return e.String() }
func (CommandEvent) isMachineEvent()    {}
func (c CommandEvent) name() string     { return c.String() }
func (HistoricalEvent) isMachineEvent() {}
func (h HistoricalEvent) name() string  { return h.String() }

// TODO: Dedupe other versions of this same thing
func CreateNewCommand(commandType enumspb.CommandType) *commandpb.Command {
	return &commandpb.Command{
		CommandType: commandType,
	}
}

// TODO: Probably move elsewhere
type ActivityCancelationType int16

const (
	activityCancellationWaitCancellationCompleted ActivityState = iota
	activityCancellationTryCancel
	activityCancellationAbandon
)

type ExecuteActivityParameters struct {
	attributes       *commandpb.ScheduleActivityTaskCommandAttributes
	cancellationType ActivityCancelationType
}
