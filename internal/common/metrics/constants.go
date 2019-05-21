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
	CadenceMetricsPrefix             = "cadence-"
	WorkflowStartCounter             = CadenceMetricsPrefix + "workflow-start"
	WorkflowCompletedCounter         = CadenceMetricsPrefix + "workflow-completed"
	WorkflowCanceledCounter          = CadenceMetricsPrefix + "workflow-canceled"
	WorkflowFailedCounter            = CadenceMetricsPrefix + "workflow-failed"
	WorkflowContinueAsNewCounter     = CadenceMetricsPrefix + "workflow-continue-as-new"
	WorkflowEndToEndLatency          = CadenceMetricsPrefix + "workflow-endtoend-latency" // measure workflow execution from start to close
	WorkflowGetHistoryCounter        = CadenceMetricsPrefix + "workflow-get-history-total"
	WorkflowGetHistoryFailedCounter  = CadenceMetricsPrefix + "workflow-get-history-failed"
	WorkflowGetHistorySucceedCounter = CadenceMetricsPrefix + "workflow-get-history-succeed"
	WorkflowGetHistoryLatency        = CadenceMetricsPrefix + "workflow-get-history-latency"
	WorkflowSignalWithStartCounter   = CadenceMetricsPrefix + "workflow-signal-with-start"
	DecisionTimeoutCounter           = CadenceMetricsPrefix + "decision-timeout"

	DecisionPollCounter                = CadenceMetricsPrefix + "decision-poll-total"
	DecisionPollFailedCounter          = CadenceMetricsPrefix + "decision-poll-failed"
	DecisionPollTransientFailedCounter = CadenceMetricsPrefix + "decision-poll-transient-failed"
	DecisionPollNoTaskCounter          = CadenceMetricsPrefix + "decision-poll-no-task"
	DecisionPollSucceedCounter         = CadenceMetricsPrefix + "decision-poll-succeed"
	DecisionPollLatency                = CadenceMetricsPrefix + "decision-poll-latency" // measure succeed poll request latency
	DecisionScheduledToStartLatency    = CadenceMetricsPrefix + "decision-scheduled-to-start-latency"
	DecisionExecutionFailedCounter     = CadenceMetricsPrefix + "decision-execution-failed"
	DecisionExecutionLatency           = CadenceMetricsPrefix + "decision-execution-latency"
	DecisionResponseFailedCounter      = CadenceMetricsPrefix + "decision-response-failed"
	DecisionResponseLatency            = CadenceMetricsPrefix + "decision-response-latency"
	DecisionTaskPanicCounter           = CadenceMetricsPrefix + "decision-task-panic"
	DecisionTaskCompletedCounter       = CadenceMetricsPrefix + "decision-task-completed"
	DecisionTaskForceCompleted         = CadenceMetricsPrefix + "decision-task-force-completed"

	ActivityPollCounter                = CadenceMetricsPrefix + "activity-poll-total"
	ActivityPollFailedCounter          = CadenceMetricsPrefix + "activity-poll-failed"
	ActivityPollTransientFailedCounter = CadenceMetricsPrefix + "activity-poll-transient-failed"
	ActivityPollNoTaskCounter          = CadenceMetricsPrefix + "activity-poll-no-task"
	ActivityPollSucceedCounter         = CadenceMetricsPrefix + "activity-poll-succeed"
	ActivityPollLatency                = CadenceMetricsPrefix + "activity-poll-latency"
	ActivityScheduledToStartLatency    = CadenceMetricsPrefix + "activity-scheduled-to-start-latency"
	ActivityExecutionFailedCounter     = CadenceMetricsPrefix + "activity-execution-failed"
	ActivityExecutionLatency           = CadenceMetricsPrefix + "activity-execution-latency"
	ActivityResponseLatency            = CadenceMetricsPrefix + "activity-response-latency"
	ActivityResponseFailedCounter      = CadenceMetricsPrefix + "activity-response-failed"
	ActivityEndToEndLatency            = CadenceMetricsPrefix + "activity-endtoend-latency"
	ActivityTaskPanicCounter           = CadenceMetricsPrefix + "activity-task-panic"
	ActivityTaskCompletedCounter       = CadenceMetricsPrefix + "activity-task-completed"
	ActivityTaskFailedCounter          = CadenceMetricsPrefix + "activity-task-failed"
	ActivityTaskCanceledCounter        = CadenceMetricsPrefix + "activity-task-canceled"
	ActivityTaskCompletedByIDCounter   = CadenceMetricsPrefix + "activity-task-completed-by-id"
	ActivityTaskFailedByIDCounter      = CadenceMetricsPrefix + "activity-task-failed-by-id"
	ActivityTaskCanceledByIDCounter    = CadenceMetricsPrefix + "activity-task-canceled-by-id"
	LocalActivityTotalCounter          = CadenceMetricsPrefix + "local-activity-total"
	LocalActivityTimeoutCounter        = CadenceMetricsPrefix + "local-activity-timeout"
	LocalActivityCanceledCounter       = CadenceMetricsPrefix + "local-activity-canceled"
	LocalActivityFailedCounter         = CadenceMetricsPrefix + "local-activity-failed"
	LocalActivityPanicCounter          = CadenceMetricsPrefix + "local-activity-panic"
	LocalActivityExecutionLatency      = CadenceMetricsPrefix + "local-activity-execution-latency"
	WorkerPanicCounter                 = CadenceMetricsPrefix + "worker-panic"

	UnhandledSignalsCounter = CadenceMetricsPrefix + "unhandled-signals"
	CorruptedSignalsCounter = CadenceMetricsPrefix + "corrupted-signals"

	WorkerStartCounter = CadenceMetricsPrefix + "worker-start"
	PollerStartCounter = CadenceMetricsPrefix + "poller-start"

	CadenceRequest        = CadenceMetricsPrefix + "request"
	CadenceError          = CadenceMetricsPrefix + "error"
	CadenceLatency        = CadenceMetricsPrefix + "latency"
	CadenceInvalidRequest = CadenceMetricsPrefix + "invalid-request"

	StickyCacheHit   = CadenceMetricsPrefix + "sticky-cache-hit"
	StickyCacheMiss  = CadenceMetricsPrefix + "sticky-cache-miss"
	StickyCacheEvict = CadenceMetricsPrefix + "sticky-cache-evict"
	StickyCacheStall = CadenceMetricsPrefix + "sticky-cache-stall"
	StickyCacheSize  = CadenceMetricsPrefix + "sticky-cache-size"

	NonDeterministicError = CadenceMetricsPrefix + "non-deterministic-error"
)
