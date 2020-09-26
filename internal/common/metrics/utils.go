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

package metrics

import (
	"github.com/uber-go/tally"
)

// TagScope return a scope with one or multiple tags,
// input should be key value pairs like: tagScope(tag1, val1, tag2, val2).
func TagScope(metricsScope tally.Scope, keyValuePairs ...string) tally.Scope {
	if metricsScope == nil {
		metricsScope = tally.NoopScope
	}

	if len(keyValuePairs)%2 != 0 {
		panic("TagScope key value are not in pairs")
	}

	tagsMap := map[string]string{}
	for i := 0; i < len(keyValuePairs); i += 2 {
		tagName := keyValuePairs[i]
		tagValue := keyValuePairs[i+1]
		tagsMap[tagName] = tagValue
	}

	return metricsScope.Tagged(tagsMap)
}

// GetRootScope return properly tagged tally scope with base tags included on all metric emitted by the client
func GetRootScope(ts tally.Scope, namespace string) tally.Scope {
	// Include all tags on the root scope which are emitted by rpc calls to Temporal
	return TagScope(ts, NamespaceTagName, namespace, ClientTagName, ClientTagValue, WorkerTypeTagName, NoneTagValue,
		WorkflowTypeNameTagName, NoneTagValue, ActivityTypeNameTagName, NoneTagValue, TaskQueueTagName, NoneTagValue)
}

// GetWorkerScope return properly tagged tally scope worker type tag
func GetWorkerScope(ts tally.Scope, workerType string) tally.Scope {
	return TagScope(ts, WorkerTypeTagName, workerType)
}

// GetMetricsScopeForActivity return properly tagged tally scope for activity
func GetMetricsScopeForActivity(ts tally.Scope, workflowType, activityType string) tally.Scope {
	return TagScope(ts, WorkflowTypeNameTagName, workflowType, ActivityTypeNameTagName,
		activityType)
}

// GetMetricsScopeForLocalActivity return properly tagged tally scope for local activity
func GetMetricsScopeForLocalActivity(ts tally.Scope, workflowType, localActivityType string) tally.Scope {
	return TagScope(ts, WorkflowTypeNameTagName, workflowType, ActivityTypeNameTagName,
		localActivityType)
}

// GetMetricsScopeForWorkflow return properly tagged tally scope for workflow execution
func GetMetricsScopeForWorkflow(ts tally.Scope, workflowType string) tally.Scope {
	return TagScope(ts, WorkflowTypeNameTagName, workflowType)
}

// GetMetricsScopeForRPC return properly tagged tally scope for workflow execution
func GetMetricsScopeForRPC(ts tally.Scope, workflowType, activityType, taskqueueName string) tally.Scope {
	return TagScope(ts, WorkflowTypeNameTagName, workflowType, ActivityTypeNameTagName,
		activityType, TaskQueueTagName, taskqueueName)
}

// getMetricsScopeForOperation return properly tagged tally scope for rpc operation
func getMetricsScopeForOperation(ts tally.Scope, operation string) tally.Scope {
	return TagScope(ts, OperationTagName, operation)
}
