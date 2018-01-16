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

// Package worker contains functions to manage lifecycle of a Cadence client side worker.
package worker

import (
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/internal"
)

type (
	// Worker represents objects that can be started and stopped.
	Worker = internal.Worker

	// Options is used to configure a worker instance.
	Options = internal.WorkerOptions
)

// New creates an instance of worker for managing workflow and activity executions.
// service 	- thrift connection to the cadence server.
// domain - the name of the cadence domain.
// taskList 	- is the task list name you use to identify your client worker, also
// 		  identifies group of workflow and activity implementations that are hosted by a single worker process.
// options 	-  configure any worker specific options like logger, metrics, identity.
func New(
	service workflowserviceclient.Interface,
	domain string,
	taskList string,
	options Options,
) Worker {
	return internal.NewWorker(service, domain, taskList, options)
}

// EnableVerboseLogging enable or disable verbose logging of internal Cadence library components.
// Most customers don't need this feature, unless advised by the Cadence team member.
// Also there is no guarantee that this API is not going to change.
func EnableVerboseLogging(enable bool) {
	internal.EnableVerboseLogging(enable)
}
