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

	m "go.uber.org/cadence/.gen/go/cadence"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/zap"
)

type (
	// Worker represents objects that can be started and stopped.
	Worker interface {
		// Start starts the worker in a non-blocking fashion
		Start() error
		// Run is a blocking start and cleans up resources when killed
		Run()
		// Stop cleans up any resources opened by worker
		Stop()
	}

	// WorkerOptions is to configure a worker instance,
	// for example (1) the logger or any specific metrics.
	// 	       (2) Whether to heart beat for activities automatically.
	// Use NewWorkerOptions function to create an instance.
	WorkerOptions struct {
		// Optional: To set the maximum concurrent activity executions this host can have.
		// The zero value of this uses the default value.
		// default: defaultMaxConcurrentActivityExecutionSize(1k)
		MaxConcurrentActivityExecutionSize int

		// Optional: Sets the rate limiting on number of activities that can be executed per refresh duration.
		// This can be used to protect down stream services from flooding.
		// The zero value of this uses the default value.
		// default: defaultMaxActivityExecutionRate(100k)
		// Warning: activity's StartToCloseTimeout starts ticking even if a task is blocked due to rate limiting.
		MaxActivityExecutionRate int

		// Optional: Sets the refresh duration for rate limit. If not specified, it uses 1s as default.
		// Use this to fine tune your rate limiter. For example, you could set this duration to 10s with max rate set to
		// 1 which means we rate limit the activity task to at most 1 per every 10s. You could also set this duration to
		// 100ms with max rate set wo 10 which means rate limit to 10 activity task execution per every 100ms.
		MaxActivityExecutionRateRefreshDuration time.Duration

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
		// In the decider you can use Cadence.GetLogger(ctx) to access logger that is replay aware.
		// This will enable workflow decider code to log during
		// the replay mode as well. This will be too verbose and often repeated logs.
		// default: false
		EnableLoggingInReplay bool

		// Optional: Disable running workflow workers.
		// default: false
		DisableWorkflowWorker bool

		// Optional: Disable running activity workers.
		// default: false
		DisableActivityWorker bool

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
	service m.TChanWorkflowService,
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
