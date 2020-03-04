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
	TemporalMetricsPrefix            = "temporal-"
	WorkflowStartCounter             = TemporalMetricsPrefix + "workflow-start"
	WorkflowCompletedCounter         = TemporalMetricsPrefix + "workflow-completed"
	WorkflowCanceledCounter          = TemporalMetricsPrefix + "workflow-canceled"
	WorkflowFailedCounter            = TemporalMetricsPrefix + "workflow-failed"
	WorkflowContinueAsNewCounter     = TemporalMetricsPrefix + "workflow-continue-as-new"
	WorkflowEndToEndLatency          = TemporalMetricsPrefix + "workflow-endtoend-latency" // measure workflow execution from start to close
	WorkflowGetHistoryCounter        = TemporalMetricsPrefix + "workflow-get-history-total"
	WorkflowGetHistoryFailedCounter  = TemporalMetricsPrefix + "workflow-get-history-failed"
	WorkflowGetHistorySucceedCounter = TemporalMetricsPrefix + "workflow-get-history-succeed"
	WorkflowGetHistoryLatency        = TemporalMetricsPrefix + "workflow-get-history-latency"
	WorkflowSignalWithStartCounter   = TemporalMetricsPrefix + "workflow-signal-with-start"
	DecisionTimeoutCounter           = TemporalMetricsPrefix + "decision-timeout"

	DecisionPollCounter                = TemporalMetricsPrefix + "decision-poll-total"
	DecisionPollFailedCounter          = TemporalMetricsPrefix + "decision-poll-failed"
	DecisionPollTransientFailedCounter = TemporalMetricsPrefix + "decision-poll-transient-failed"
	DecisionPollNoTaskCounter          = TemporalMetricsPrefix + "decision-poll-no-task"
	DecisionPollSucceedCounter         = TemporalMetricsPrefix + "decision-poll-succeed"
	DecisionPollLatency                = TemporalMetricsPrefix + "decision-poll-latency" // measure succeed poll request latency
	DecisionScheduledToStartLatency    = TemporalMetricsPrefix + "decision-scheduled-to-start-latency"
	DecisionExecutionFailedCounter     = TemporalMetricsPrefix + "decision-execution-failed"
	DecisionExecutionLatency           = TemporalMetricsPrefix + "decision-execution-latency"
	DecisionResponseFailedCounter      = TemporalMetricsPrefix + "decision-response-failed"
	DecisionResponseLatency            = TemporalMetricsPrefix + "decision-response-latency"
	DecisionTaskPanicCounter           = TemporalMetricsPrefix + "decision-task-panic"
	DecisionTaskCompletedCounter       = TemporalMetricsPrefix + "decision-task-completed"
	DecisionTaskForceCompleted         = TemporalMetricsPrefix + "decision-task-force-completed"

	ActivityPollCounter                = TemporalMetricsPrefix + "activity-poll-total"
	ActivityPollFailedCounter          = TemporalMetricsPrefix + "activity-poll-failed"
	ActivityPollTransientFailedCounter = TemporalMetricsPrefix + "activity-poll-transient-failed"
	ActivityPollNoTaskCounter          = TemporalMetricsPrefix + "activity-poll-no-task"
	ActivityPollSucceedCounter         = TemporalMetricsPrefix + "activity-poll-succeed"
	ActivityPollLatency                = TemporalMetricsPrefix + "activity-poll-latency"
	ActivityScheduledToStartLatency    = TemporalMetricsPrefix + "activity-scheduled-to-start-latency"
	ActivityExecutionFailedCounter     = TemporalMetricsPrefix + "activity-execution-failed"
	ActivityExecutionLatency           = TemporalMetricsPrefix + "activity-execution-latency"
	ActivityResponseLatency            = TemporalMetricsPrefix + "activity-response-latency"
	ActivityResponseFailedCounter      = TemporalMetricsPrefix + "activity-response-failed"
	ActivityEndToEndLatency            = TemporalMetricsPrefix + "activity-endtoend-latency"
	ActivityTaskPanicCounter           = TemporalMetricsPrefix + "activity-task-panic"
	ActivityTaskCompletedCounter       = TemporalMetricsPrefix + "activity-task-completed"
	ActivityTaskFailedCounter          = TemporalMetricsPrefix + "activity-task-failed"
	ActivityTaskCanceledCounter        = TemporalMetricsPrefix + "activity-task-canceled"
	ActivityTaskCompletedByIDCounter   = TemporalMetricsPrefix + "activity-task-completed-by-id"
	ActivityTaskFailedByIDCounter      = TemporalMetricsPrefix + "activity-task-failed-by-id"
	ActivityTaskCanceledByIDCounter    = TemporalMetricsPrefix + "activity-task-canceled-by-id"
	LocalActivityTotalCounter          = TemporalMetricsPrefix + "local-activity-total"
	LocalActivityTimeoutCounter        = TemporalMetricsPrefix + "local-activity-timeout"
	LocalActivityCanceledCounter       = TemporalMetricsPrefix + "local-activity-canceled"
	LocalActivityFailedCounter         = TemporalMetricsPrefix + "local-activity-failed"
	LocalActivityPanicCounter          = TemporalMetricsPrefix + "local-activity-panic"
	LocalActivityExecutionLatency      = TemporalMetricsPrefix + "local-activity-execution-latency"
	WorkerPanicCounter                 = TemporalMetricsPrefix + "worker-panic"

	UnhandledSignalsCounter = TemporalMetricsPrefix + "unhandled-signals"
	CorruptedSignalsCounter = TemporalMetricsPrefix + "corrupted-signals"

	WorkerStartCounter = TemporalMetricsPrefix + "worker-start"
	PollerStartCounter = TemporalMetricsPrefix + "poller-start"

	TemporalRequest        = TemporalMetricsPrefix + "request"
	TemporalError          = TemporalMetricsPrefix + "error"
	TemporalLatency        = TemporalMetricsPrefix + "latency"
	TemporalInvalidRequest = TemporalMetricsPrefix + "invalid-request"

	StickyCacheHit   = TemporalMetricsPrefix + "sticky-cache-hit"
	StickyCacheMiss  = TemporalMetricsPrefix + "sticky-cache-miss"
	StickyCacheEvict = TemporalMetricsPrefix + "sticky-cache-evict"
	StickyCacheStall = TemporalMetricsPrefix + "sticky-cache-stall"
	StickyCacheSize  = TemporalMetricsPrefix + "sticky-cache-size"

	NonDeterministicError = TemporalMetricsPrefix + "non-deterministic-error"
)
