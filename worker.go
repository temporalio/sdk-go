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

package cadence

import (
	"context"
	"time"

	"github.com/uber-go/tally"

	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/zap"
)

type (
	// Worker represents objects that can be started and stopped.
	Worker interface {
		// Start starts the worker in a non-blocking fashion
		Start() error
		// Run is a blocking start and cleans up resources when killed
		// returns error only if it fails to start the worker
		Run() error
		// Stop cleans up any resources opened by worker
		Stop()
	}

	// WorkerOptions is used to configure a worker instance.
	WorkerOptions struct {
		// Optional: To set the maximum concurrent activity executions this host can have.
		// The zero value of this uses the default value.
		// default: defaultMaxConcurrentActivityExecutionSize(1k)
		MaxConcurrentActivityExecutionSize int

		// Optional: Sets the rate limiting on number of activities that can be executed per second. Notice that the
		// number is represented in float, so that you can set it to less than 1 if needed. For example, set the number
		// to 0.1 means you want your activity to be executed once for every 10 seconds. This can be used to protect
		// down stream services from flooding.
		// The zero value of this uses the default value.
		// default: defaultMaxActivityExecutionRate(100k)
		// Warning: activity's StartToCloseTimeout starts ticking even if a task is blocked due to rate limiting.
		MaxActivityExecutionPerSecond float64

		// Optional: if the activities need auto heart beating for those activities
		// by the framework
		// default: false not to heartbeat.
		AutoHeartBeat bool

		// Optional: Sets an identify that can be used to track this host for debugging.
		// default: default identity that include hostname, groupName and process ID.
		Identity string

		// Optional: Metrics to be reported.
		// default: no metrics.
		MetricsScope tally.Scope

		// Optional: Logger framework can use to log.
		// default: default logger provided.
		Logger *zap.Logger

		// Optional: Enable logging in replay.
		// In the workflow code you can use cadence.GetLogger(ctx) to write logs. By default, the logger will skip log
		// entry during replay mode so you won't see duplicate logs. This option will enable the logging in replay mode.
		// This is only useful for debugging purpose.
		// default: false
		EnableLoggingInReplay bool

		// Optional: Disable running workflow workers.
		// default: false
		DisableWorkflowWorker bool

		// Optional: Disable running activity workers.
		// default: false
		DisableActivityWorker bool

		// Optional: Disable sticky execution.
		// default: false
		// Sticky Execution is to run the decision tasks for one workflow execution on same worker host. This is an
		// optimization for workflow execution. When sticky execution is enabled, worker keeps the workflow state in
		// memory. New decision task contains the new history events will be dispatched to the same worker. If this
		// worker crashes, the sticky decision task will timeout after StickyScheduleToStartTimeout, and cadence server
		// will clear the stickiness for that workflow execution and automatically reschedule a new decision task that
		// is available for any worker to pick up and resume the progress.
		DisableStickyExecution bool

		// Optional: Sticky schedule to start timeout.
		// default: 5s
		// The resolution is seconds. See details about StickyExecution on the comments for DisableStickyExecution.
		StickyScheduleToStartTimeout time.Duration

		// Optional: sets context for activity. The context can be used to pass any configuration to activity
		// like common logger for all activities.
		BackgroundActivityContext context.Context
	}
)

// NewWorker creates an instance of worker for managing workflow and activity executions.
// service 	- thrift connection to the cadence server.
// domain - the name of the cadence domain.
// taskList 	- is the task list name you use to identify your client worker, also
// 		  identifies group of workflow and activity implementations that are hosted by a single worker process.
// options 	-  configure any worker specific options like logger, metrics, identity.
func NewWorker(
	service workflowserviceclient.Interface,
	domain string,
	taskList string,
	options WorkerOptions,
) Worker {
	return newAggregatedWorker(service, domain, taskList, options)
}

// GetWorkflowStackTrace returns a stack trace of all goroutines of a workflow given its current history.
// It requires workflow function that was used to create the history to be registered
// through RegisterWorkflow.
// Use Client.GetWorkflowStackTrace to get a stack trace given workflowID and runID.
func GetWorkflowStackTrace(h *s.History) (string, error) {
	getHistoryPage := func(nextPageToken []byte) (*s.History, []byte, error) {
		return h, nil, nil
	}
	return getWorkflowStackTraceImpl("unknown", "unknown", getHistoryPage)
}
