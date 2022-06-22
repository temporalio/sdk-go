// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
)

func Test_TimerStateMachine_CancelBeforeSent(t *testing.T) {
	t.Parallel()
	timerID := "test-timer-1"
	attributes := &commandpb.StartTimerCommandAttributes{
		TimerId: timerID,
	}
	h := newCommandsHelper()
	d := h.startTimer(attributes)
	require.Equal(t, commandStateCreated, d.getState())
	h.cancelTimer(TimerID{timerID})
	require.Equal(t, commandStateCanceledBeforeSent, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, 2, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_START_TIMER, commands[0].GetCommandType())
	require.Equal(t, enumspb.COMMAND_TYPE_CANCEL_TIMER, commands[1].GetCommandType())
}

func Test_TimerStateMachine_CancelAfterInitiated(t *testing.T) {
	t.Parallel()
	timerID := "test-timer-1"
	attributes := &commandpb.StartTimerCommandAttributes{
		TimerId: timerID,
	}
	h := newCommandsHelper()
	d := h.startTimer(attributes)
	require.Equal(t, commandStateCreated, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, commandStateCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_START_TIMER, commands[0].GetCommandType())
	require.Equal(t, attributes, commands[0].GetStartTimerCommandAttributes())
	h.handleTimerStarted(timerID)
	require.Equal(t, commandStateInitiated, d.getState())
	h.cancelTimer(TimerID{timerID})
	require.Equal(t, commandStateCanceledAfterInitiated, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_CANCEL_TIMER, commands[0].GetCommandType())
	require.Equal(t, commandStateCancellationCommandSent, d.getState())
	h.handleTimerCanceled(timerID)
	require.Equal(t, commandStateCompleted, d.getState())
}

func Test_TimerStateMachine_CompletedAfterCancel(t *testing.T) {
	t.Parallel()
	timerID := "test-timer-1"
	attributes := &commandpb.StartTimerCommandAttributes{
		TimerId: timerID,
	}
	h := newCommandsHelper()
	d := h.startTimer(attributes)
	require.Equal(t, commandStateCreated, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, commandStateCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_START_TIMER, commands[0].GetCommandType())
	h.cancelTimer(TimerID{timerID})
	require.Equal(t, commandStateCancellationCommandSent, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_CANCEL_TIMER, commands[0].GetCommandType())
	// The timer ends up being started after we issued a cancel command
	h.handleTimerStarted(timerID)
	require.Equal(t, commandStateCanceledAfterInitiated, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 0, len(commands))
	// Oops it completed anyway, fine, we're done.
	h.handleTimerClosed(timerID)
	require.Equal(t, commandStateCompletedAfterCancellationCommandSent, d.getState())
}

func Test_TimerStateMachine_CompleteWithoutCancel(t *testing.T) {
	t.Parallel()
	timerID := "test-timer-1"
	attributes := &commandpb.StartTimerCommandAttributes{
		TimerId: timerID,
	}
	h := newCommandsHelper()
	d := h.startTimer(attributes)
	require.Equal(t, commandStateCreated, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, commandStateCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_START_TIMER, commands[0].GetCommandType())
	h.handleTimerStarted(timerID)
	require.Equal(t, commandStateInitiated, d.getState())
	require.Equal(t, 0, len(h.getCommands(false)))
	h.handleTimerClosed(timerID)
	require.Equal(t, commandStateCompleted, d.getState())
}

func Test_TimerCancelEventOrdering(t *testing.T) {
	t.Parallel()
	timerID := "test-timer-1"
	localActivityID := "test-activity-1"
	attributes := &commandpb.StartTimerCommandAttributes{
		TimerId: timerID,
	}
	h := newCommandsHelper()
	d := h.startTimer(attributes)
	require.Equal(t, commandStateCreated, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, commandStateCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_START_TIMER, commands[0].GetCommandType())
	require.Equal(t, attributes, commands[0].GetStartTimerCommandAttributes())
	h.handleTimerStarted(timerID)
	require.Equal(t, commandStateInitiated, d.getState())
	m := h.recordLocalActivityMarker(localActivityID, map[string]*commonpb.Payloads{}, nil)
	require.Equal(t, commandStateCreated, m.getState())
	h.cancelTimer(TimerID{timerID})
	require.Equal(t, commandStateCanceledAfterInitiated, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 2, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_RECORD_MARKER, commands[0].GetCommandType())
	require.Equal(t, enumspb.COMMAND_TYPE_CANCEL_TIMER, commands[1].GetCommandType())
}

func Test_ActivityStateMachine_CompleteWithoutCancel(t *testing.T) {
	t.Parallel()
	activityID := "test-activity-1"
	attributes := &commandpb.ScheduleActivityTaskCommandAttributes{
		ActivityId: activityID,
	}
	h := newCommandsHelper()
	h.setCurrentWorkflowTaskStartedEventID(3)

	// schedule activity
	scheduleID := h.getNextID()
	d := h.scheduleActivityTask(scheduleID, attributes)
	require.Equal(t, commandStateCreated, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, commandStateCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, commands[0].GetCommandType())

	// activity scheduled
	h.handleActivityTaskScheduled(activityID, scheduleID)
	require.Equal(t, commandStateInitiated, d.getState())

	// activity completed
	h.handleActivityTaskClosed(activityID, scheduleID)
	require.Equal(t, commandStateCompleted, d.getState())
}

func Test_ActivityStateMachine_CancelBeforeSent(t *testing.T) {
	t.Parallel()
	activityID := "test-activity-1"
	attributes := &commandpb.ScheduleActivityTaskCommandAttributes{
		ActivityId: activityID,
	}
	h := newCommandsHelper()
	h.setCurrentWorkflowTaskStartedEventID(3)

	// schedule activity
	scheduleID := h.getNextID()
	d := h.scheduleActivityTask(scheduleID, attributes)
	require.Equal(t, commandStateCreated, d.getState())

	// Cancel before command sent. We will send the command and the cancellation.
	h.requestCancelActivityTask(activityID)
	require.Equal(t, commandStateCanceledBeforeSent, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, 2, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, commands[0].GetCommandType())
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK, commands[1].GetCommandType())
}

func Test_ActivityStateMachine_CancelAfterSent(t *testing.T) {
	t.Parallel()
	activityID := "test-activity-1"
	attributes := &commandpb.ScheduleActivityTaskCommandAttributes{
		ActivityId: activityID,
	}
	h := newCommandsHelper()
	h.setCurrentWorkflowTaskStartedEventID(3)

	// schedule activity
	scheduleID := h.getNextID()
	d := h.scheduleActivityTask(scheduleID, attributes)
	require.Equal(t, commandStateCreated, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, commands[0].GetCommandType())

	// cancel activity
	h.requestCancelActivityTask(activityID)
	require.Equal(t, commandStateCancellationCommandSent, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK, commands[0].GetCommandType())

	// activity scheduled
	h.handleActivityTaskScheduled(activityID, scheduleID)
	require.Equal(t, commandStateCanceledAfterInitiated, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 0, len(commands))

	// activity canceled
	h.handleActivityTaskCanceled(activityID, scheduleID)
	require.Equal(t, commandStateCompleted, d.getState())
	require.Equal(t, 0, len(h.getCommands(false)))
}

func Test_ActivityStateMachine_CompletedAfterCancel(t *testing.T) {
	t.Parallel()
	activityID := "test-activity-1"
	attributes := &commandpb.ScheduleActivityTaskCommandAttributes{
		ActivityId: activityID,
	}
	h := newCommandsHelper()
	h.setCurrentWorkflowTaskStartedEventID(3)

	// schedule activity
	scheduleID := h.getNextID()
	d := h.scheduleActivityTask(scheduleID, attributes)
	require.Equal(t, commandStateCreated, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, commands[0].GetCommandType())

	// cancel activity
	h.requestCancelActivityTask(activityID)
	require.Equal(t, commandStateCancellationCommandSent, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK, commands[0].GetCommandType())

	// activity scheduled
	h.handleActivityTaskScheduled(activityID, scheduleID)
	require.Equal(t, commandStateCanceledAfterInitiated, d.getState())
	commands = h.getCommands(true)
	require.Equal(t, 0, len(commands))

	// activity completed after cancel
	h.handleActivityTaskClosed(activityID, scheduleID)
	require.Equal(t, commandStateCompletedAfterCancellationCommandSent, d.getState())
	require.Equal(t, 0, len(h.getCommands(false)))
}

func Test_ActivityStateMachine_CancelInitiated_After_CanceledBeforeSent(t *testing.T) {
	t.Parallel()
	activityID := "test-activity-1"
	attributes := &commandpb.ScheduleActivityTaskCommandAttributes{
		ActivityId: activityID,
	}
	h := newCommandsHelper()
	h.setCurrentWorkflowTaskStartedEventID(3)

	// schedule activity
	scheduleID := h.getNextID()
	d := h.scheduleActivityTask(scheduleID, attributes)
	require.Equal(t, commandStateCreated, d.getState())

	// cancel activity before sent
	h.requestCancelActivityTask(activityID)
	require.Equal(t, commandStateCanceledBeforeSent, d.getState())
	commands := h.getCommands(true)
	require.Equal(t, 2, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK, commands[0].GetCommandType())
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK, commands[1].GetCommandType())

	// Activity initiated
	h.handleActivityTaskScheduled(activityID, scheduleID)
	require.Equal(t, commandStateCanceledAfterInitiated, d.getState())
	// no commands fetched though!

	// Cancel requested event comes in
	h.handleActivityTaskCancelRequested(scheduleID)
	require.Equal(t, commandStateCanceledAfterInitiated, d.getState())

	// activity completed after cancel
	h.handleActivityTaskClosed(activityID, scheduleID)
	require.Equal(t, commandStateCompleted, d.getState())
	require.Equal(t, 0, len(h.getCommands(false)))
}

func Test_ActivityStateMachine_PanicInvalidStateTransition(t *testing.T) {
	t.Parallel()
	activityID := "test-activity-1"
	attributes := &commandpb.ScheduleActivityTaskCommandAttributes{
		ActivityId: activityID,
	}
	h := newCommandsHelper()
	h.setCurrentWorkflowTaskStartedEventID(3)

	// schedule activity
	scheduleID := h.getNextID()
	h.scheduleActivityTask(scheduleID, attributes)

	// verify that using invalid activity id will panic
	err := runAndCatchPanic(func() {
		h.handleActivityTaskClosed("invalid-activity-id", scheduleID)
	})
	require.NotNil(t, err)

	// send schedule command
	h.getCommands(true)
	// activity scheduled
	h.handleActivityTaskScheduled(activityID, scheduleID)

	// now simulate activity canceled, which is invalid transition
	err = runAndCatchPanic(func() {
		h.handleActivityTaskCanceled(activityID, scheduleID)
	})
	require.NotNil(t, err)
}

func Test_ChildWorkflowStateMachine_Basic(t *testing.T) {
	t.Parallel()
	workflowID := "test-child-workflow-1"
	attributes := &commandpb.StartChildWorkflowExecutionCommandAttributes{
		WorkflowId: workflowID,
	}
	h := newCommandsHelper()

	// start child workflow
	d := h.startChildWorkflowExecution(attributes)
	require.Equal(t, commandStateCreated, d.getState())

	// send command
	commands := h.getCommands(true)
	require.Equal(t, commandStateCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_START_CHILD_WORKFLOW_EXECUTION, commands[0].GetCommandType())

	// child workflow initiated
	h.handleStartChildWorkflowExecutionInitiated(workflowID)
	require.Equal(t, commandStateInitiated, d.getState())
	require.Equal(t, 0, len(h.getCommands(true)))

	// child workflow started
	h.handleChildWorkflowExecutionStarted(workflowID)
	require.Equal(t, commandStateStarted, d.getState())
	require.Equal(t, 0, len(h.getCommands(true)))

	// child workflow completed
	h.handleChildWorkflowExecutionClosed(workflowID)
	require.Equal(t, commandStateCompleted, d.getState())
	require.Equal(t, 0, len(h.getCommands(true)))
}

func Test_ChildWorkflowStateMachine_CancelSucceed(t *testing.T) {
	t.Parallel()
	namespace := "test-namespace"
	workflowID := "test-child-workflow"
	runID := ""
	cancellationID := ""
	initiatedEventID := int64(28)
	attributes := &commandpb.StartChildWorkflowExecutionCommandAttributes{
		WorkflowId: workflowID,
	}
	h := newCommandsHelper()

	// start child workflow
	d := h.startChildWorkflowExecution(attributes)
	// send command
	_ = h.getCommands(true)
	// child workflow initiated
	h.handleStartChildWorkflowExecutionInitiated(workflowID)
	// child workflow started
	h.handleChildWorkflowExecutionStarted(workflowID)

	// cancel child workflow
	h.requestCancelExternalWorkflowExecution(namespace, workflowID, runID, cancellationID, true)
	require.Equal(t, commandStateCanceledAfterStarted, d.getState())

	// send cancel request
	commands := h.getCommands(true)
	require.Equal(t, commandStateCancellationCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION, commands[0].GetCommandType())

	// cancel request initiated
	h.handleRequestCancelExternalWorkflowExecutionInitiated(initiatedEventID, workflowID, cancellationID)
	require.Equal(t, commandStateCancellationCommandSent, d.getState())

	// cancel request accepted
	h.handleExternalWorkflowExecutionCancelRequested(initiatedEventID, workflowID)
	require.Equal(t, commandStateCancellationCommandAccepted, d.getState())

	// child workflow canceled
	h.handleChildWorkflowExecutionCanceled(workflowID)
	require.Equal(t, commandStateCompleted, d.getState())
}

func Test_ChildWorkflowStateMachine_InvalidStates(t *testing.T) {
	t.Parallel()
	namespace := "test-namespace"
	workflowID := "test-workflow-id"
	runID := ""
	attributes := &commandpb.StartChildWorkflowExecutionCommandAttributes{
		WorkflowId: workflowID,
	}
	cancellationID := ""
	initiatedEventID := int64(28)
	h := newCommandsHelper()

	// start child workflow
	d := h.startChildWorkflowExecution(attributes)
	require.Equal(t, commandStateCreated, d.getState())

	// invalid: start child workflow failed before command was sent
	err := runAndCatchPanic(func() {
		h.handleStartChildWorkflowExecutionFailed(workflowID)
	})
	require.NotNil(t, err)

	// send command
	commands := h.getCommands(true)
	require.Equal(t, commandStateCommandSent, d.getState())
	require.Equal(t, 1, len(commands))

	// invalid: child workflow completed before it was initiated
	err = runAndCatchPanic(func() {
		h.handleChildWorkflowExecutionClosed(workflowID)
	})
	require.NotNil(t, err)

	// child workflow initiated
	h.handleStartChildWorkflowExecutionInitiated(workflowID)
	require.Equal(t, commandStateInitiated, d.getState())

	h.handleChildWorkflowExecutionStarted(workflowID)
	require.Equal(t, commandStateStarted, d.getState())
	// invalid: cancel child workflow failed before cancel request
	err = runAndCatchPanic(func() {
		h.handleRequestCancelExternalWorkflowExecutionFailed(initiatedEventID, workflowID)
	})
	require.NotNil(t, err)

	// cancel child workflow after child workflow is started
	h.requestCancelExternalWorkflowExecution(namespace, workflowID, runID, cancellationID, true)
	require.Equal(t, commandStateCanceledAfterStarted, d.getState())

	// send cancel request
	commands = h.getCommands(true)
	require.Equal(t, commandStateCancellationCommandSent, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION, commands[0].GetCommandType())

	// cancel request initiated
	h.handleRequestCancelExternalWorkflowExecutionInitiated(initiatedEventID, workflowID, cancellationID)
	require.Equal(t, commandStateCancellationCommandSent, d.getState())

	// invalid: child workflow initiated again
	err = runAndCatchPanic(func() {
		h.handleStartChildWorkflowExecutionInitiated(workflowID)
	})
	require.NotNil(t, err)

	// child workflow completed
	h.handleChildWorkflowExecutionClosed(workflowID)
	require.Equal(t, commandStateCompletedAfterCancellationCommandSent, d.getState())

	// invalid: child workflow canceled after it was completed
	err = runAndCatchPanic(func() {
		h.handleChildWorkflowExecutionCanceled(workflowID)
	})
	require.NotNil(t, err)
}

func Test_ChildWorkflow_UnusualCancelationOrdering(t *testing.T) {
	t.Parallel()
	namespace := "test-namespace"
	workflowID := "test-workflow-id"
	runID := ""
	attributes := &commandpb.StartChildWorkflowExecutionCommandAttributes{
		WorkflowId: workflowID,
	}
	cancellationID := ""
	initiatedEventID := int64(28)
	h := newCommandsHelper()

	// start child workflow
	h.startChildWorkflowExecution(attributes)
	// send command
	h.getCommands(true)
	// child workflow initiated
	h.handleStartChildWorkflowExecutionInitiated(workflowID)
	h.handleChildWorkflowExecutionStarted(workflowID)
	// cancel child workflow after child workflow is started
	h.requestCancelExternalWorkflowExecution(namespace, workflowID, runID, cancellationID, true)
	// send cancel request
	h.getCommands(true)
	h.handleRequestCancelExternalWorkflowExecutionInitiated(initiatedEventID, workflowID, cancellationID)
	// Now, the unusual part. The cancellation happens before we get the external cancel request
	h.handleChildWorkflowExecutionCanceled(workflowID)
	// Oh no, server took a bit.
	err := runAndCatchPanic(func() {
		h.handleExternalWorkflowExecutionCancelRequested(initiatedEventID, workflowID)
	})
	require.Nil(t, err)
}

func Test_ChildWorkflowStateMachine_CancelFailed(t *testing.T) {
	t.Parallel()
	namespace := "test-namespace"
	workflowID := "test-workflow-id"
	runID := ""
	attributes := &commandpb.StartChildWorkflowExecutionCommandAttributes{
		WorkflowId: workflowID,
	}
	cancellationID := ""
	initiatedEventID := int64(28)
	h := newCommandsHelper()

	// start child workflow
	d := h.startChildWorkflowExecution(attributes)
	// send command
	h.getCommands(true)
	// child workflow initiated
	h.handleStartChildWorkflowExecutionInitiated(workflowID)
	// child workflow started
	h.handleChildWorkflowExecutionStarted(workflowID)
	// cancel child workflow
	h.requestCancelExternalWorkflowExecution(namespace, workflowID, runID, cancellationID, true)
	// send cancel request
	h.getCommands(true)
	// cancel request initiated
	h.handleRequestCancelExternalWorkflowExecutionInitiated(initiatedEventID, workflowID, cancellationID)

	// cancel request failed
	h.handleRequestCancelExternalWorkflowExecutionFailed(initiatedEventID, workflowID)
	require.Equal(t, commandStateStarted, d.getState())

	// child workflow completed
	h.handleChildWorkflowExecutionClosed(workflowID)
	require.Equal(t, commandStateCompleted, d.getState())
}

func Test_MarkerStateMachine(t *testing.T) {
	t.Parallel()
	h := newCommandsHelper()

	// record marker for side effect
	d := h.recordSideEffectMarker(1, nil, converter.GetDefaultDataConverter())
	require.Equal(t, commandStateCreated, d.getState())

	// send commands
	commands := h.getCommands(true)
	require.Equal(t, commandStateCompleted, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_RECORD_MARKER, commands[0].GetCommandType())
}

func Test_UpsertSearchAttributesCommandStateMachine(t *testing.T) {
	t.Parallel()
	h := newCommandsHelper()

	attr := &commonpb.SearchAttributes{}
	d := h.upsertSearchAttributes("1", attr)
	require.Equal(t, commandStateCreated, d.getState())

	commands := h.getCommands(true)
	require.Equal(t, commandStateCompleted, d.getState())
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES, commands[0].GetCommandType())
}

func Test_CancelExternalWorkflowStateMachine_Succeed(t *testing.T) {
	t.Parallel()
	namespace := "test-namespace"
	workflowID := "test-workflow-id"
	runID := "test-run-id"
	cancellationID := "1"
	initiatedEventID := int64(28)
	h := newCommandsHelper()

	// request cancel external workflow
	command := h.requestCancelExternalWorkflowExecution(namespace, workflowID, runID, cancellationID, false)
	require.False(t, command.isDone())
	d := h.getCommand(makeCommandID(commandTypeCancellation, cancellationID))
	require.Equal(t, commandStateCreated, d.getState())

	// send commands
	commands := h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION, commands[0].GetCommandType())
	require.Equal(
		t,
		&commandpb.RequestCancelExternalWorkflowExecutionCommandAttributes{
			Namespace:         namespace,
			WorkflowId:        workflowID,
			RunId:             runID,
			Control:           cancellationID,
			ChildWorkflowOnly: false,
		},
		commands[0].GetRequestCancelExternalWorkflowExecutionCommandAttributes(),
	)

	// cancel request initiated
	h.handleRequestCancelExternalWorkflowExecutionInitiated(initiatedEventID, workflowID, cancellationID)
	require.Equal(t, commandStateInitiated, d.getState())

	// cancel requested
	h.handleExternalWorkflowExecutionCancelRequested(initiatedEventID, workflowID)
	require.Equal(t, commandStateCompleted, d.getState())

	// mark the cancel request failed now will make it invalid state transition
	err := runAndCatchPanic(func() {
		h.handleRequestCancelExternalWorkflowExecutionFailed(initiatedEventID, workflowID)
	})
	require.NotNil(t, err)
}

func Test_CancelExternalWorkflowStateMachine_Failed(t *testing.T) {
	t.Parallel()
	namespace := "test-namespace"
	workflowID := "test-workflow-id"
	runID := "test-run-id"
	cancellationID := "2"
	initiatedEventID := int64(28)
	h := newCommandsHelper()

	// request cancel external workflow
	command := h.requestCancelExternalWorkflowExecution(namespace, workflowID, runID, cancellationID, false)
	require.False(t, command.isDone())
	d := h.getCommand(makeCommandID(commandTypeCancellation, cancellationID))
	require.Equal(t, commandStateCreated, d.getState())

	// send commands
	commands := h.getCommands(true)
	require.Equal(t, 1, len(commands))
	require.Equal(t, enumspb.COMMAND_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION, commands[0].GetCommandType())
	require.Equal(
		t,
		&commandpb.RequestCancelExternalWorkflowExecutionCommandAttributes{
			Namespace:         namespace,
			WorkflowId:        workflowID,
			RunId:             runID,
			Control:           cancellationID,
			ChildWorkflowOnly: false,
		},
		commands[0].GetRequestCancelExternalWorkflowExecutionCommandAttributes(),
	)

	// cancel request initiated
	h.handleRequestCancelExternalWorkflowExecutionInitiated(initiatedEventID, workflowID, cancellationID)
	require.Equal(t, commandStateInitiated, d.getState())

	// cancel request failed
	h.handleRequestCancelExternalWorkflowExecutionFailed(initiatedEventID, workflowID)
	require.Equal(t, commandStateCompleted, d.getState())

	// mark the cancel request succeed now will make it invalid state transition
	err := runAndCatchPanic(func() {
		h.handleExternalWorkflowExecutionCancelRequested(initiatedEventID, workflowID)
	})
	require.NotNil(t, err)
}

func runAndCatchPanic(f func()) (err error) {
	// panic handler
	defer func() {
		if p := recover(); p != nil {
			topLine := "runAndCatchPanic [panic]:"
			st := getStackTraceRaw(topLine, 7, 0)
			err = newPanicError(p, st) // Fail command on panic
		}
	}()

	f()
	return nil
}
