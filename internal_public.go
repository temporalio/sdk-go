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

// WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!
// Any of the APIs in this file are not supported for application level developers
// and are subject to change without any notice.
//
// APIs that are internal to Cadence system developers and are public from the Go
// point of view only to access them from other packages.

import (
	"context"

	m "go.uber.org/cadence/.gen/go/cadence"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/zap"
)

type (
	// WorkflowTaskHandler represents decision task handlers.
	WorkflowTaskHandler interface {
		// Process the workflow task
		ProcessWorkflowTask(task *s.PollForDecisionTaskResponse, emitStack bool) (response *s.RespondDecisionTaskCompletedRequest, stackTrace string, err error)
	}

	// ActivityTaskHandler represents activity task handlers.
	ActivityTaskHandler interface {
		// Execute the activity task
		// The return interface{} can have three requests, use switch to find the type of it.
		// - RespondActivityTaskCompletedRequest
		// - RespondActivityTaskFailedRequest
		// - RespondActivityTaskCancelRequest
		Execute(task *s.PollForActivityTaskResponse) (interface{}, error)
	}
)

var enableVerboseLogging = false

// NewWorkflowTaskWorker returns an instance of a workflow task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewWorkflowTaskWorker(
	taskHandler WorkflowTaskHandler,
	service m.TChanWorkflowService,
	domain string,
	taskList string,
	options WorkerOptions,
) (worker Worker) {
	wOptions := fillWorkerOptionsDefaults(options)
	workerParams := workerExecutionParameters{
		TaskList:                  taskList,
		ConcurrentPollRoutineSize: defaultConcurrentPollRoutineSize,
		Identity:                  wOptions.Identity,
		MetricsScope:              wOptions.MetricsScope,
		Logger:                    wOptions.Logger,
	}

	processTestTags(&wOptions, &workerParams)
	return newWorkflowTaskWorkerInternal(taskHandler, service, domain, workerParams)
}

// NewActivityTaskWorker returns instance of an activity task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewActivityTaskWorker(
	taskHandler ActivityTaskHandler,
	service m.TChanWorkflowService,
	domain string,
	taskList string,
	options WorkerOptions,
) Worker {
	wOptions := fillWorkerOptionsDefaults(options)
	workerParams := workerExecutionParameters{
		TaskList:                  taskList,
		ConcurrentPollRoutineSize: defaultConcurrentPollRoutineSize,
		Identity:                  wOptions.Identity,
		MetricsScope:              wOptions.MetricsScope,
		Logger:                    wOptions.Logger,
		EnableLoggingInReplay:     wOptions.EnableLoggingInReplay,
		UserContext:               wOptions.BackgroundActivityContext,
	}

	processTestTags(&wOptions, &workerParams)
	return newActivityTaskWorker(taskHandler, service, domain, workerParams)
}

// NewWorkflowTaskHandler creates an instance of a WorkflowTaskHandler from a decision poll response
// using workflow functions registered through RegisterWorkflow
// To be used to replay a workflow in a debugger.
func NewWorkflowTaskHandler(domain string, identity string, logger *zap.Logger) WorkflowTaskHandler {
	params := workerExecutionParameters{
		Identity: identity,
		Logger:   logger,
	}
	return newWorkflowTaskHandler(
		getWorkflowDefinitionFactory(newRegisteredWorkflowFactory()),
		domain,
		params,
		nil)
}

// NewActivityTaskHandler creates an instance of a WorkflowTaskHandler from a decision poll response
// using activity functions registered through RegisterActivity. service parameter is used for
// heartbeating from activity implementation.
// To be used to invoke registered functions for debugging purposes.
func NewActivityTaskHandler(service m.TChanWorkflowService, identity string, logger *zap.Logger) ActivityTaskHandler {
	params := workerExecutionParameters{
		Identity: identity,
		Logger:   logger,
	}
	return newActivityTaskHandler(
		getHostEnvironment().getRegisteredActivities(),
		service,
		params)
}

// AddWorkflowRegistrationInterceptor adds interceptor that is called for each RegisterWorkflow call.
// This function guarantees that the interceptor function is called for each registration even
// if it itself is called from init()
func AddWorkflowRegistrationInterceptor(
	i func(name string, workflow interface{}) (string, interface{}),
) {
	getHostEnvironment().AddWorkflowRegistrationInterceptor(i)
}

// AddActivityRegistrationInterceptor adds interceptor that is called for each RegisterActivity call.
// This function guarantees that the interceptor function is called for each registration even
// if it itself is called from init()
func AddActivityRegistrationInterceptor(
	i func(name string, activity interface{}) (string, interface{})) {
	getHostEnvironment().AddActivityRegistrationInterceptor(i)
}

// SerializeFnArgs serializes an activity function arguments.
func SerializeFnArgs(args ...interface{}) ([]byte, error) {
	return getHostEnvironment().encodeArgs(args)
}

// DeserializeFnResults de-serializes a function results.
// The input result doesn't include the error. The cadence server has result, error.
// This is to de-serialize the result.
func DeserializeFnResults(result []byte, to interface{}) error {
	return getHostEnvironment().decodeArg(result, to)
}

// EnableVerboseLogging enable or disable verbose logging. This is for internal use only.
func EnableVerboseLogging(enable bool) {
	enableVerboseLogging = enable
}

// WithTestTags - is used for internal cadence use to pass any test tags.
// TODO: Build the tags on top of the context and pass it around instead of map of maps.
func WithTestTags(ctx context.Context, testTags map[string]map[string]string) context.Context {
	return context.WithValue(ctx, testTagsContextKey, testTags)
}
