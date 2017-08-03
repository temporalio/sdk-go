// Copyright (c) 2017 Uber Technologies, Inc.
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

package metrics

// Workflow Creation metrics
const (
	WorkflowStartCounter             = "workflow-start"
	WorkflowCompletedCounter         = "workflow-completed"
	WorkflowCanceledCounter          = "workflow-canceled"
	WorkflowFailedCounter            = "workflow-failed"
	WorkflowContinueAsNewCounter     = "workflow-continue-as-new"
	WorkflowEndToEndLatency          = "workflow-endtoend-latency" // measure workflow execution from start to close
	WorkflowGetHistoryCounter        = "workflow-get-history-total"
	WorkflowGetHistoryFailedCounter  = "workflow-get-history-failed"
	WorkflowGetHistorySucceedCounter = "workflow-get-history-succeed"
	WorkflowGetHistoryLatency        = "workflow-get-history-latency"
	DecisionTimeoutCounter           = "decision-timeout"

	DecisionPollCounter            = "decision-poll-total"
	DecisionPollFailedCounter      = "decision-poll-failed"
	DecisionPollNoTaskCounter      = "decision-poll-no-task"
	DecisionPollSucceedCounter     = "decision-poll-succeed"
	DecisionPollLatency            = "decision-poll-latency" // measure succeed poll request latency
	DecisionExecutionFailedCounter = "decision-execution-failed"
	DecisionExecutionLatency       = "decision-execution-latency"
	DecisionResponseFailedCounter  = "decision-response-failed"
	DecisionResponseLatency        = "decision-response-latency"
	DecisionEndToEndLatency        = "decision-endtoend-latency" // measure from poll request start to response completed
	DecisionTaskPanicCounter       = "decision-task-panic"
	DecisionTaskCompletedCounter   = "decision-task-completed"

	ActivityPollCounter            = "activity-poll-total"
	ActivityPollFailedCounter      = "activity-poll-failed"
	ActivityPollNoTaskCounter      = "activity-poll-no-task"
	ActivityPollSucceedCounter     = "activity-poll-succeed"
	ActivityPollLatency            = "activity-poll-latency"
	ActivityExecutionFailedCounter = "activity-execution-failed"
	ActivityExecutionLatency       = "activity-execution-latency"
	ActivityResponseLatency        = "activity-response-latency"
	ActivityResponseFailedCounter  = "activity-response-failed"
	ActivityEndToEndLatency        = "activity-endtoend-latency"
	ActivityTaskPanicCounter       = "activity-task-panic"
	ActivityTaskCompletedCounter   = "activity-task-completed"
	ActivityTaskFailedCounter      = "activity-task-failed"
	ActivityTaskCanceledCounter    = "activity-task-canceled"

	UnhandledSignalsCounter = "unhandled-signals"
)
