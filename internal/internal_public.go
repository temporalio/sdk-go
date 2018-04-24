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
	s "go.uber.org/cadence/.gen/go/shared"
)

type (
	// HistoryIterator iterator through history events
	HistoryIterator interface {
		// GetNextPage returns next page of history events
		GetNextPage() (*s.History, error)
		// Reset resets the internal state so next GetNextPage() call will return first page of events from beginning.
		Reset()
		// HasNextPage returns if there are more page of events
		HasNextPage() bool
	}

	// WorkflowTaskHandler represents decision task handlers.
	WorkflowTaskHandler interface {
		// Processes the workflow task
		// The response is either
		// RespondDecisionTaskCompletedRequest or RespondDecisionTaskFailedRequest
		ProcessWorkflowTask(
			task *s.PollForDecisionTaskResponse,
			historyIterator HistoryIterator,
			emitStack bool) (response interface{}, stackTrace string, err error)
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
