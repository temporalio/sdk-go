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

package internal

// WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!
// Any of the APIs in this file are not supported for application level developers
// and are subject to change without any notice.
//
// APIs that are internal to Cadence system developers and are public from the Go
// point of view only to access them from other packages.

import (
	"time"

	s "go.uber.org/cadence/.gen/go/shared"
)

type (
	decisionHeartbeatFunc func(response interface{}, startTime time.Time) (*workflowTask, error)

	// HistoryIterator iterator through history events
	HistoryIterator interface {
		// GetNextPage returns next page of history events
		GetNextPage() (*s.History, error)
		// Reset resets the internal state so next GetNextPage() call will return first page of events from beginning.
		Reset()
		// HasNextPage returns if there are more page of events
		HasNextPage() bool
	}

	// WorkflowExecutionContext represents one instance of workflow execution state in memory. Lock must be obtained before
	// calling into any methods.
	WorkflowExecutionContext interface {
		Lock()
		Unlock(err error)
		ProcessWorkflowTask(workflowTask *workflowTask) (completeRequest interface{}, err error)
		ProcessLocalActivityResult(workflowTask *workflowTask, lar *localActivityResult) (interface{}, error)
		// CompleteDecisionTask try to complete current decision task and get response that needs to be sent back to server.
		// The waitLocalActivity is used to control if we should wait for outstanding local activities.
		// If there is no outstanding local activities or if waitLocalActivity is false, the complete will return response
		// which will be one of following:
		// - RespondDecisionTaskCompletedRequest
		// - RespondDecisionTaskFailedRequest
		// - RespondQueryTaskCompletedRequest
		// If waitLocalActivity is true, and there is outstanding local activities, this call will return nil.
		CompleteDecisionTask(workflowTask *workflowTask, waitLocalActivity bool) interface{}
		// GetDecisionTimeout returns the TaskStartToCloseTimeout
		GetDecisionTimeout() time.Duration
		GetCurrentDecisionTask() *s.PollForDecisionTaskResponse
		IsDestroyed() bool
		StackTrace() string
	}

	// WorkflowTaskHandler represents decision task handlers.
	WorkflowTaskHandler interface {
		// Processes the workflow task
		// The response could be:
		// - RespondDecisionTaskCompletedRequest
		// - RespondDecisionTaskFailedRequest
		// - RespondQueryTaskCompletedRequest
		ProcessWorkflowTask(
			task *workflowTask,
			f decisionHeartbeatFunc,
		) (response interface{}, err error)
	}

	// ActivityTaskHandler represents activity task handlers.
	ActivityTaskHandler interface {
		// Executes the activity task
		// The response is one of the types:
		// - RespondActivityTaskCompletedRequest
		// - RespondActivityTaskFailedRequest
		// - RespondActivityTaskCanceledRequest
		Execute(taskList string, task *s.PollForActivityTaskResponse) (interface{}, error)
	}
)

var enableVerboseLogging = false

// EnableVerboseLogging enable or disable verbose logging. This is for internal use only.
func EnableVerboseLogging(enable bool) {
	enableVerboseLogging = enable
}
