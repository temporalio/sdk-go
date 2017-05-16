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

	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	"github.com/uber-go/tally"
	"go.uber.org/zap"
)

type (
	// Worker represents objects that can be started and stopped.
	Worker interface {
		Stop()
		Start() error
	}

	// WorkerOptions is to configure a worker instance,
	// for example (1) the logger or any specific metrics.
	// 	       (2) Whether to heart beat for activities automatically.
	// Use NewWorkerOptions function to create an instance.
	WorkerOptions interface {
		// Optional: To set the maximum concurrent activity executions this host can have.
		// default: defaultMaxConcurrentActivityExecutionSize(10k)
		SetMaxConcurrentActivityExecutionSize(size int) WorkerOptions

		// Optional: Sets the rate limiting on number of activities that can be executed.
		// This can be used to protect down stream services from flooding.
		// default: defaultMaxActivityExecutionRate(100k)
		SetMaxActivityExecutionRate(requestPerSecond float32) WorkerOptions

		// Optional: if the activities need auto heart beating for those activities
		// by the framework
		// default: false not to heartbeat.
		SetAutoHeartBeat(auto bool) WorkerOptions

		// Optional: Sets an identify that can be used to track this host for debugging.
		// default: default identity that include hostname, groupName and process ID.
		SetIdentity(identity string) WorkerOptions

		// Optional: Metrics to be reported.
		// default: no metrics.
		SetMetrics(metricsScope tally.Scope) WorkerOptions

		// Optional: Logger framework can use to log.
		// default: default logger provided.
		SetLogger(logger *zap.Logger) WorkerOptions

		// Optional: Enable logging in replay
		// default: false
		SetEnableLoggingInReplay(enable bool) WorkerOptions

		// Optional: Disable running workflow workers.
		// default: false
		SetDisableWorkflowWorker(disable bool) WorkerOptions

		// Optional: Disable running activity workers.
		// default: false
		SetDisableActivityWorker(disable bool) WorkerOptions

		// Optional: sets context for activity
		WithActivityContext(ctx context.Context) WorkerOptions
	}
)

// NewWorkerOptions returns an instance of worker options to configure.
func NewWorkerOptions() WorkerOptions {
	return NewWorkerOptionsInternal(nil)
}

// NewWorker creates an instance of worker for managing workflow and activity executions.
// service 	- thrift connection to the cadence server.
// domain - the name of the cadence domain.
// groupName 	- is the name you use to identify your client worker, also
// 		  identifies group of workflow and activity implementations that are hosted by a single worker process.
// options 	-  configure any worker specific options like logger, metrics, identity.
func NewWorker(
	service m.TChanWorkflowService,
	domain string,
	groupName string,
	options WorkerOptions,
) Worker {
	return newAggregatedWorker(service, domain, groupName, options)
}
