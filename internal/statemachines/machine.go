// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
