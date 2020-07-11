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

package metrics

// Workflow Creation metrics
const (
	TemporalMetricsPrefix            = "temporal_"
	WorkflowStartCounter             = TemporalMetricsPrefix + "workflow_start"
	WorkflowCompletedCounter         = TemporalMetricsPrefix + "workflow_completed"
	WorkflowCanceledCounter          = TemporalMetricsPrefix + "workflow_canceled"
	WorkflowFailedCounter            = TemporalMetricsPrefix + "workflow_failed"
	WorkflowContinueAsNewCounter     = TemporalMetricsPrefix + "workflow_continue_as_new"
	WorkflowEndToEndLatency          = TemporalMetricsPrefix + "workflow_endtoend_latency" // measure workflow execution from start to close
	WorkflowGetHistoryCounter        = TemporalMetricsPrefix + "workflow_get_history_total"
	WorkflowGetHistoryFailedCounter  = TemporalMetricsPrefix + "workflow_get_history_failed"
	WorkflowGetHistorySucceedCounter = TemporalMetricsPrefix + "workflow_get_history_succeed"
	WorkflowGetHistoryLatency        = TemporalMetricsPrefix + "workflow_get_history_latency"
	WorkflowSignalWithStartCounter   = TemporalMetricsPrefix + "workflow_signal_with_start"
	DecisionTimeoutCounter           = TemporalMetricsPrefix + "decision_timeout"

	DecisionPollCounter                = TemporalMetricsPrefix + "decision_poll_total"
	DecisionPollFailedCounter          = TemporalMetricsPrefix + "decision_poll_failed"
	DecisionPollTransientFailedCounter = TemporalMetricsPrefix + "decision_poll_transient_failed"
	DecisionPollNoTaskCounter          = TemporalMetricsPrefix + "decision_poll_no_task"
	DecisionPollSucceedCounter         = TemporalMetricsPrefix + "decision_poll_succeed"
	DecisionPollLatency                = TemporalMetricsPrefix + "decision_poll_latency" // measure succeed poll request latency
	DecisionScheduledToStartLatency    = TemporalMetricsPrefix + "decision_scheduled_to_start_latency"
	DecisionExecutionFailedCounter     = TemporalMetricsPrefix + "decision_execution_failed"
	DecisionExecutionLatency           = TemporalMetricsPrefix + "decision_execution_latency"
	DecisionResponseFailedCounter      = TemporalMetricsPrefix + "decision_response_failed"
	DecisionResponseLatency            = TemporalMetricsPrefix + "decision_response_latency"
	WorkflowTaskPanicCounter           = TemporalMetricsPrefix + "workflow_task_panic"
	WorkflowTaskCompletedCounter       = TemporalMetricsPrefix + "workflow_task_completed"
	WorkflowTaskForceCompleted         = TemporalMetricsPrefix + "workflow_task_force_completed"

	ActivityPollCounter                = TemporalMetricsPrefix + "activity_poll_total"
	ActivityPollFailedCounter          = TemporalMetricsPrefix + "activity_poll_failed"
	ActivityPollTransientFailedCounter = TemporalMetricsPrefix + "activity_poll_transient_failed"
	ActivityPollNoTaskCounter          = TemporalMetricsPrefix + "activity_poll_no_task"
	ActivityPollSucceedCounter         = TemporalMetricsPrefix + "activity_poll_succeed"
	ActivityPollLatency                = TemporalMetricsPrefix + "activity_poll_latency"
	ActivityScheduledToStartLatency    = TemporalMetricsPrefix + "activity_scheduled_to_start_latency"
	ActivityExecutionFailedCounter     = TemporalMetricsPrefix + "activity_execution_failed"
	ActivityExecutionLatency           = TemporalMetricsPrefix + "activity_execution_latency"
	ActivityResponseLatency            = TemporalMetricsPrefix + "activity_response_latency"
	ActivityResponseFailedCounter      = TemporalMetricsPrefix + "activity_response_failed"
	ActivityEndToEndLatency            = TemporalMetricsPrefix + "activity_endtoend_latency"
	ActivityTaskPanicCounter           = TemporalMetricsPrefix + "activity_task_panic"
	ActivityTaskCompletedCounter       = TemporalMetricsPrefix + "activity_task_completed"
	ActivityTaskFailedCounter          = TemporalMetricsPrefix + "activity_task_failed"
	ActivityTaskCanceledCounter        = TemporalMetricsPrefix + "activity_task_canceled"
	ActivityTaskCompletedByIDCounter   = TemporalMetricsPrefix + "activity_task_completed_by_id"
	ActivityTaskFailedByIDCounter      = TemporalMetricsPrefix + "activity_task_failed_by_id"
	ActivityTaskCanceledByIDCounter    = TemporalMetricsPrefix + "activity_task_canceled_by_id"
	LocalActivityTotalCounter          = TemporalMetricsPrefix + "local_activity_total"
	LocalActivityTimeoutCounter        = TemporalMetricsPrefix + "local_activity_timeout"
	LocalActivityCanceledCounter       = TemporalMetricsPrefix + "local_activity_canceled"
	LocalActivityFailedCounter         = TemporalMetricsPrefix + "local_activity_failed"
	LocalActivityPanicCounter          = TemporalMetricsPrefix + "local_activity_panic"
	LocalActivityExecutionLatency      = TemporalMetricsPrefix + "local_activity_execution_latency"
	WorkerPanicCounter                 = TemporalMetricsPrefix + "worker_panic"

	UnhandledSignalsCounter = TemporalMetricsPrefix + "unhandled_signals"
	CorruptedSignalsCounter = TemporalMetricsPrefix + "corrupted_signals"

	WorkerStartCounter = TemporalMetricsPrefix + "worker_start"
	PollerStartCounter = TemporalMetricsPrefix + "poller_start"

	TemporalRequest        = TemporalMetricsPrefix + "request"
	TemporalError          = TemporalMetricsPrefix + "error"
	TemporalLatency        = TemporalMetricsPrefix + "latency"
	TemporalInvalidRequest = TemporalMetricsPrefix + "invalid_request"

	StickyCacheHit   = TemporalMetricsPrefix + "sticky_cache_hit"
	StickyCacheMiss  = TemporalMetricsPrefix + "sticky_cache_miss"
	StickyCacheEvict = TemporalMetricsPrefix + "sticky_cache_evict"
	StickyCacheStall = TemporalMetricsPrefix + "sticky_cache_stall"
	StickyCacheSize  = TemporalMetricsPrefix + "sticky_cache_size"

	NonDeterministicError = TemporalMetricsPrefix + "non_deterministic_error"
)
