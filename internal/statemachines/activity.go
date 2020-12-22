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

	"go.temporal.io/sdk/internal"
)

type ActivityStateMachine struct {
	definition               *StateMachineDefinition
	activityID               string
	activityType             internal.ActivityType
	activityCancellationType ActivityCancelationType
	activityParams           ExecuteActivityParameters
	// TODO: Factor out common fields
	commandSink CommandSink
}

type ActivityState int16

const (
	stateCreated ActivityState = iota
	stateScheduleCommandCreated
	stateScheduledEventRecorded
	stateStarted
	stateCompleted
	stateFailed
	stateTimedOut
	stateCanceled
	stateScheduledActivityCancelCommandCreated
	stateScheduledActivityCancelEventRecorded
	stateStartedActivityCancelCommandCreated
	stateStartedActivityCancelEventRecorded
)

func (d ActivityState) Name() string {
	return d.String()
}

func (d ActivityState) String() string {
	switch d {
	case stateCreated:
		return "CREATED"
	case stateScheduleCommandCreated:
		return "SCHEDULE_COMMAND_CREATED"
	case stateScheduledEventRecorded:
		return "SCHEDULED_EVENT_RECORDED"
	case stateStarted:
		return "STARTED"
	case stateCompleted:
		return "COMPLETED"
	case stateFailed:
		return "FAILED"
	case stateTimedOut:
		return "TIMED_OUT"
	case stateCanceled:
		return "CANCELED"
	case stateScheduledActivityCancelCommandCreated:
		return "SCHEDULED_ACTIVITY_CANCEL_COMMAND_CREATED"
	case stateScheduledActivityCancelEventRecorded:
		return "SCHEDULED_ACTIVITY_CANCEL_EVENT_RECORDED"
	case stateStartedActivityCancelCommandCreated:
		return "STARTED_ACTIVITY_CANCEL_COMMAND_CREATED"
	case stateStartedActivityCancelEventRecorded:
		return "STARTED_ACTIVITY_CANCEL_EVENT_RECORDED"
	default:
		panic("Attempted to stringify unknown activity state. This should be impossible.")
	}
}

// TODO: Replace XX. prefixes with different types / constants etc
func NewActivityStateMachine() *ActivityStateMachine {
	fsmDef := BuildStateMachine("ActivityStateMachine", stateCreated, []State{stateCompleted, stateFailed, stateTimedOut, stateCanceled})
	fsmDef.add(stateCreated, ExplicitEventSchedule, stateScheduleCommandCreated)

	fsmDef.addCommand(stateScheduleCommandCreated, enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, stateScheduleCommandCreated)
	fsmDef.addEvent(stateScheduleCommandCreated, enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, stateScheduledEventRecorded)
	fsmDef.add(stateScheduleCommandCreated, ExplicitEventCancel, stateCanceled)

	fsmDef.addEvent(stateScheduledEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED, stateStarted)
	fsmDef.addEvent(stateScheduledEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT, stateTimedOut)
	fsmDef.add(stateScheduledEventRecorded, ExplicitEventCancel, stateScheduledActivityCancelCommandCreated)

	fsmDef.addEvent(stateStarted, enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED, stateCompleted)
	fsmDef.addEvent(stateStarted, enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED, stateFailed)
	fsmDef.addEvent(stateStarted, enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT, stateTimedOut)
	fsmDef.add(stateStarted, ExplicitEventCancel, stateStartedActivityCancelCommandCreated)

	fsmDef.addCommand(stateScheduledActivityCancelCommandCreated, enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK,
		stateScheduledActivityCancelCommandCreated)
	fsmDef.addEvent(stateScheduledActivityCancelCommandCreated, enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED,
		stateScheduledActivityCancelEventRecorded)

	fsmDef.addEvent(stateScheduledActivityCancelEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED,
		stateCanceled)
	fsmDef.addEvent(stateScheduledActivityCancelEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED,
		stateStartedActivityCancelEventRecorded)
	fsmDef.addEvent(stateScheduledActivityCancelEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT,
		stateTimedOut)

	fsmDef.addCommand(stateStartedActivityCancelCommandCreated, enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK,
		stateStartedActivityCancelCommandCreated)
	fsmDef.addEvent(stateStartedActivityCancelCommandCreated, enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED,
		stateStartedActivityCancelEventRecorded)

	fsmDef.addEvent(stateStartedActivityCancelEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED, stateFailed)
	fsmDef.addEvent(stateStartedActivityCancelEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED, stateCompleted)
	fsmDef.addEvent(stateStartedActivityCancelEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT, stateTimedOut)
	fsmDef.addEvent(stateStartedActivityCancelEventRecorded, enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED, stateCanceled)

	asm := ActivityStateMachine{
		definition: &fsmDef,
	}
	return &asm
}

func (sm *ActivityStateMachine) createScheduleActivityTaskCommand() {
	command := CreateNewCommand(enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK)
	command.Attributes = &commandpb.Command_ScheduleActivityTaskCommandAttributes{
		ScheduleActivityTaskCommandAttributes: sm.activityParams.attributes,
	}
	sm.addCommand(command)
}

// TODO: Is probably common / can be factored out
func (sm *ActivityStateMachine) addCommand(cmd *commandpb.Command) {
	cancellable := CancellableCommand{cmd: cmd}
	sm.commandSink.dispatch(cancellable)
}
