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
