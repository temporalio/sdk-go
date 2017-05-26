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

// MetricName is the name of the metric
type MetricName string

// MetricType is the type of the metric, which can be one of the 3 below
type MetricType int

// MetricTypes which are supported
const (
	Counter MetricType = iota
	Timer
	Gauge
)

// Common tags for all services
const (
	HostnameTagName  = "hostname"
	OperationTagName = "operation"
)

// This package should hold all the metrics and tags for cherami
const (
	UnknownDirectoryTagValue = "Unknown"
)

// Common service base metrics
const (
	RestartCount         = "restarts"
	NumGoRoutinesGauge   = "num-goroutines"
	GoMaxProcsGauge      = "gomaxprocs"
	MemoryAllocatedGauge = "memory.allocated"
	MemoryHeapGauge      = "memory.heap"
	MemoryHeapIdleGauge  = "memory.heapidle"
	MemoryHeapInuseGauge = "memory.heapinuse"
	MemoryStackGauge     = "memory.stack"
	NumGCCounter         = "memory.num-gc"
	GcPauseMsTimer       = "memory.gc-pause-ms"
)

// Workflow Creation metrics
const (
	WorkflowsStartTotalCounter      = "workflows-start-total"
	ActivitiesTotalCounter          = "activities-total"
	DecisionsTotalCounter           = "decisions-total"
	DecisionsTimeoutCounter         = "decisions-timeout"
	WorkflowsCompletionTotalCounter = "workflows-completion-total"
	WorkflowEndToEndLatency         = "workflows-endtoend-latency"
	ActivityEndToEndLatency         = "activities-endtoend-latency"
	DecisionsEndToEndLatency        = "decisions-endtoend-latency"
	ActivityPollLatency             = "activities-poll-latency"
	DecisionsPollLatency            = "decisions-poll-latency"
	ActivityExecutionLatency        = "activities-execution-latency"
	DecisionsExecutionLatency       = "decisions-execution-latency"
	ActivityResponseLatency         = "activities-response-latency"
	DecisionsResponseLatency        = "decisions-response-latency"
	UnhandledSignalsTotalCounter    = "unhandled-signals-total"
)

// ServiceMetrics are types for common service base metrics
var ServiceMetrics = map[MetricName]MetricType{
	RestartCount: Counter,
}

// GoRuntimeMetrics represent the runtime stats from go runtime
var GoRuntimeMetrics = map[MetricName]MetricType{
	NumGoRoutinesGauge:   Gauge,
	GoMaxProcsGauge:      Gauge,
	MemoryAllocatedGauge: Gauge,
	MemoryHeapGauge:      Gauge,
	MemoryHeapIdleGauge:  Gauge,
	MemoryHeapInuseGauge: Gauge,
	MemoryStackGauge:     Gauge,
	NumGCCounter:         Counter,
	GcPauseMsTimer:       Timer,
}

// Service names for all service who emit m3 Please keep them in sync with the {Counter,Timer,Gauge}Names below.  Order matters
const (
	Frontend = iota
	NumServices
)

// operation scopes for frontend
const (
	CreateShardScope = iota
	GetShardScope
	UpdateShardScope
	CreateWorkflowExecutionScope
	GetWorkflowExecutionScope
	UpdateWorkflowExecutionScope
	DeleteWorkflowExecutionScope
	GetTransferTasksScope
	CompleteTransferTaskScope
	CreateTaskScope
	GetTasksScope
	CompleteTaskScope
	StartWorkflowExecutionScope
	PollForDecisionTaskScope
	PollForActivityTaskScope
	RespondDecisionTaskCompletedScope
	RespondActivityTaskCompletedScope
	RespondActivityTaskFailedScope
	GetWorkflowExecutionHistoryScope
)

// ScopeToTags record the scope name for all services
var ScopeToTags = [NumServices][]map[string]string{
	// frontend Scope Names
	{
		{OperationTagName: CreateShardOperationTagValue},
		{OperationTagName: GetShardOperationTagValue},
		{OperationTagName: UpdateShardOperationTagValue},
		{OperationTagName: CreateWorkflowExecutionOperationTagValue},
		{OperationTagName: GetWorkflowExecutionOperationTagValue},
		{OperationTagName: UpdateWorkflowExecutionOperationTagValue},
		{OperationTagName: DeleteWorkflowExecutionOperationTagValue},
		{OperationTagName: GetTransferTasksOperationTagValue},
		{OperationTagName: CompleteTransferTaskOperationTagValue},
		{OperationTagName: CreateTaskOperationTagValue},
		{OperationTagName: GetTasksOperationTagValue},
		{OperationTagName: CompleteTaskOperationTagValue},
		{OperationTagName: StartWorkflowExecutionOperationTagValue},
		{OperationTagName: PollForDecisionTaskOperationTagValue},
		{OperationTagName: PollForActivityTaskOperationTagValue},
		{OperationTagName: RespondDecisionTaskCompletedOperationTagValue},
		{OperationTagName: RespondActivityTaskCompletedOperationTagValue},
		{OperationTagName: RespondActivityTaskFailedOperationTagValue},
		{OperationTagName: GetWorkflowExecutionHistoryOperationTagValue},
	},
}

// Counter enums for frontend.  Please keep them in sync with the Frontend Service counter names below.
// Order between the two also matters.
const (
	WorkflowRequests = iota
	WorkflowFailures
)

// Timer enums for frontend.  Please keep them in sync with the Workflow Service timers below.  Order between the
// two also matters.
const (
	WorkflowLatencyTimer = iota
)

// CounterNames is counter names for metrics
var CounterNames = [NumServices]map[int]string{
	// Frontend Counter Names
	{
		WorkflowRequests: "workflow.requests",
		WorkflowFailures: "workflow.errors",
	},
}

// TimerNames is timer names for metrics
var TimerNames = [NumServices]map[int]string{
	// Frontend Timer Names
	{
		WorkflowLatencyTimer: "workflow.latency",
	},
}

// GaugeNames is gauge names for metrics
var GaugeNames = [NumServices]map[int]string{}

// Frontend operation tag values as seen by the M3 backend
const (
	CreateShardOperationTagValue                  = "CreateShard"
	GetShardOperationTagValue                     = "GetShard"
	UpdateShardOperationTagValue                  = "UpdateShard"
	CreateWorkflowExecutionOperationTagValue      = "CreateWorkflowExecution"
	GetWorkflowExecutionOperationTagValue         = "GetWorkflowExecution"
	UpdateWorkflowExecutionOperationTagValue      = "UpdateWorkflowExecution"
	DeleteWorkflowExecutionOperationTagValue      = "DeleteWorkflowExecution"
	GetTransferTasksOperationTagValue             = "GetTransferTasks"
	CompleteTransferTaskOperationTagValue         = "CompleteTransferTask"
	CreateTaskOperationTagValue                   = "CreateTask"
	GetTasksOperationTagValue                     = "GetTasks"
	CompleteTaskOperationTagValue                 = "CompleteTask"
	StartWorkflowExecutionOperationTagValue       = "StartWorkflowExecution"
	PollForDecisionTaskOperationTagValue          = "PollForDecisionTask"
	PollForActivityTaskOperationTagValue          = "PollForActivityTask"
	RespondDecisionTaskCompletedOperationTagValue = "RespondDecisionTaskCompleted"
	RespondActivityTaskCompletedOperationTagValue = "RespondActivityTaskCompleted"
	RespondActivityTaskFailedOperationTagValue    = "RespondActivityTaskFailed"
	GetWorkflowExecutionHistoryOperationTagValue  = "GetWorkflowExecutionHistory"
)

// ErrorClass is an enum to help with classifying SLA vs. non-SLA errors (SLA = "service level agreement")
type ErrorClass uint8

const (
	// NoError indicates that there is no error (error should be nil)
	NoError = ErrorClass(iota)
	// UserError indicates that this is NOT an SLA-reportable error
	UserError
	// InternalError indicates that this is an SLA-reportable error
	InternalError
)
