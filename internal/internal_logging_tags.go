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

package internal

const (
	TagActivityID                   = "ActivityID"
	TagActivityType                 = "ActivityType"
	TagNamespace                    = "Namespace"
	TagEventID                      = "EventID"
	TagEventType                    = "EventType"
	TagRunID                        = "RunID"
	TagTaskQueue                    = "TaskQueue"
	TagTimerID                      = "TimerID"
	TagWorkflowID                   = "WorkflowID"
	TagWorkflowType                 = "WorkflowType"
	TagWorkerID                     = "WorkerID"
	TagWorkerType                   = "WorkerType"
	TagSideEffectID                 = "SideEffectID"
	TagChildWorkflowID              = "ChildWorkflowID"
	TagLocalActivityType            = "LocalActivityType"
	TagQueryType                    = "QueryType"
	TagResult                       = "Result"
	TagError                        = "Error"
	TagStackTrace                   = "StackTrace"
	TagAttempt                      = "Attempt"
	TagTaskFirstEventID             = "TaskFirstEventID"
	TagTaskStartedEventID           = "TaskStartedEventID"
	TagPreviousStartedEventID       = "PreviousStartedEventID"
	TagCachedPreviousStartedEventID = "CachedPreviousStartedEventID"
	TagPanicError                   = "PanicError"
	TagPanicStack                   = "PanicStack"
)
