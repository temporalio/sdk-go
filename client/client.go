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

//go:generate mockgen -copyright_file ../LICENSE -package client -source client.go -destination client_mock.go
//go:generate go run ../internal/cmd/generateproxy/main.go

// Package client is used by external programs to communicate with Temporal service.
// NOTE: DO NOT USE THIS API INSIDE OF ANY WORKFLOW CODE!!!
package client

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/internal/common/metrics"
)

const (
	// DefaultHostPort is the host:port which is used if not passed with options.
	DefaultHostPort = internal.LocalHostPort

	// DefaultNamespace is the namespace name which is used if not passed with options.
	DefaultNamespace = internal.DefaultNamespace

	// QueryTypeStackTrace is the build in query type for Client.QueryWorkflow() call. Use this query type to get the call
	// stack of the workflow. The result will be a string encoded in the converter.EncodedValue.
	QueryTypeStackTrace string = internal.QueryTypeStackTrace

	// QueryTypeOpenSessions is the build in query type for Client.QueryWorkflow() call. Use this query type to get all open
	// sessions in the workflow. The result will be a list of SessionInfo encoded in the converter.EncodedValue.
	QueryTypeOpenSessions string = internal.QueryTypeOpenSessions
)

type (
	// Options are optional parameters for Client creation.
	Options = internal.ClientOptions

	// ConnectionOptions are optional parameters that can be specified in ClientOptions
	ConnectionOptions = internal.ConnectionOptions

	// StartWorkflowOptions configuration parameters for starting a workflow execution.
	StartWorkflowOptions = internal.StartWorkflowOptions

	// HistoryEventIterator is a iterator which can return history events.
	HistoryEventIterator = internal.HistoryEventIterator

	// WorkflowRun represents a started non child workflow.
	WorkflowRun = internal.WorkflowRun

	// QueryWorkflowWithOptionsRequest defines the request to QueryWorkflowWithOptions.
	QueryWorkflowWithOptionsRequest = internal.QueryWorkflowWithOptionsRequest

	// QueryWorkflowWithOptionsResponse defines the response to QueryWorkflowWithOptions.
	QueryWorkflowWithOptionsResponse = internal.QueryWorkflowWithOptionsResponse

	// Client is the client for starting and getting information about a workflow executions as well as
	// completing activities asynchronously.
	Client interface {
		// ExecuteWorkflow starts a workflow execution and return a WorkflowRun instance and error
		// The user can use this to start using a function or workflow type name.
		// Either by
		//     ExecuteWorkflow(ctx, options, "workflowTypeName", arg1, arg2, arg3)
		//     or
		//     ExecuteWorkflow(ctx, options, workflowExecuteFn, arg1, arg2, arg3)
		// The errors it can return:
		//	- serviceerror.NotFound, if namespace does not exists
		//	- serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		//
		// WorkflowRun has 3 methods:
		//  - GetWorkflowID() string: which return the started workflow ID
		//  - GetRunID() string: which return the first started workflow run ID (please see below)
		//  - Get(ctx context.Context, valuePtr interface{}) error: which will fill the workflow
		//    execution result to valuePtr, if workflow execution is a success, or return corresponding
		//    error. This is a blocking API.
		// NOTE: if the started workflow return ContinueAsNewError during the workflow execution, the
		// return result of GetRunID() will be the started workflow run ID, not the new run ID caused by ContinueAsNewError,
		// however, Get(ctx context.Context, valuePtr interface{}) will return result from the run which did not return ContinueAsNewError.
		// Say ExecuteWorkflow started a workflow, in its first run, has run ID "run ID 1", and returned ContinueAsNewError,
		// the second run has run ID "run ID 2" and return some result other than ContinueAsNewError:
		// GetRunID() will always return "run ID 1" and  Get(ctx context.Context, valuePtr interface{}) will return the result of second run.
		// NOTE: DO NOT USE THIS API INSIDE A WORKFLOW, USE workflow.ExecuteChildWorkflow instead
		ExecuteWorkflow(ctx context.Context, options StartWorkflowOptions, workflow interface{}, args ...interface{}) (WorkflowRun, error)

		// GetWorkflow retrieves a workflow execution and return a WorkflowRun instance (described above)
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the last running execution of that workflow ID.
		//
		// WorkflowRun has 2 methods:
		//  - GetRunID() string: which return the first started workflow run ID (please see below)
		//  - Get(ctx context.Context, valuePtr interface{}) error: which will fill the workflow
		//    execution result to valuePtr, if workflow execution is a success, or return corresponding
		//    error. This is a blocking API.
		// If workflow not found, the Get() will return serviceerror.NotFound.
		// NOTE: if the started workflow return ContinueAsNewError during the workflow execution, the
		// return result of GetRunID() will be the started workflow run ID, not the new run ID caused by ContinueAsNewError,
		// however, Get(ctx context.Context, valuePtr interface{}) will return result from the run which did not return ContinueAsNewError.
		// Say ExecuteWorkflow started a workflow, in its first run, has run ID "run ID 1", and returned ContinueAsNewError,
		// the second run has run ID "run ID 2" and return some result other than ContinueAsNewError:
		// GetRunID() will always return "run ID 1" and  Get(ctx context.Context, valuePtr interface{}) will return the result of second run.
		GetWorkflow(ctx context.Context, workflowID string, runID string) WorkflowRun

		// SignalWorkflow sends a signals to a workflow in execution
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// - signalName name to identify the signal.
		// The errors it can return:
		//	- serviceerror.NotFound
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error

		// SignalWithStartWorkflow sends a signal to a running workflow.
		// If the workflow is not running or not found, it starts the workflow and then sends the signal in transaction.
		// - workflowID, signalName, signalArg are same as SignalWorkflow's parameters
		// - options, workflow, workflowArgs are same as StartWorkflow's parameters
		// - the workflowID parameter is used instead of options.ID. If the latter is present, it must match the workflowID.
		// Note: options.WorkflowIDReusePolicy is default to AllowDuplicate in this API.
		// The errors it can return:
		//  - serviceerror.NotFound, if namespace does not exist
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		SignalWithStartWorkflow(ctx context.Context, workflowID string, signalName string, signalArg interface{},
			options StartWorkflowOptions, workflow interface{}, workflowArgs ...interface{}) (WorkflowRun, error)

		// CancelWorkflow request cancellation of a workflow in execution. Cancellation request closes the channel
		// returned by the workflow.Context.Done() of the workflow that is target of the request.
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the currently running execution of that workflow ID.
		// The errors it can return:
		//	- serviceerror.NotFound
		//	- serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		CancelWorkflow(ctx context.Context, workflowID string, runID string) error

		// TerminateWorkflow terminates a workflow execution. Terminate stops a workflow execution immediately without
		// letting the workflow to perform any cleanup
		// workflowID is required, other parameters are optional.
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// The errors it can return:
		//	- serviceerror.NotFound
		//	- serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error

		// GetWorkflowHistory gets history events of a particular workflow
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the last running execution of that workflow ID.
		// - whether use long poll for tracking new events: when the workflow is running, there can be new events generated during iteration
		// 	 of HistoryEventIterator, if isLongPoll == true, then iterator will do long poll, tracking new history event, i.e. the iteration
		//   will not be finished until workflow is finished; if isLongPoll == false, then iterator will only return current history events.
		// - whether return all history events or just the last event, which contains the workflow execution end result
		// Example:-
		//	To iterate all events,
		// 		iter := GetWorkflowHistory(ctx, workflowID, runID, isLongPoll, filterType)
		//		events := []*shared.HistoryEvent{}
		//		for iter.HasNext() {
		//			event, err := iter.Next()
		//			if err != nil {
		//				return err
		//			}
		//			events = append(events, event)
		//		}
		GetWorkflowHistory(ctx context.Context, workflowID string, runID string, isLongPoll bool, filterType enumspb.HistoryEventFilterType) HistoryEventIterator

		// CompleteActivity reports activity completed.
		// activity Execute method can return activity.ErrResultPending to
		// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
		// should be called when that activity is completed with the actual result and error. If err is nil, activity task
		// completed event will be reported; if err is CanceledError, activity task canceled event will be reported; otherwise,
		// activity task failed event will be reported.
		// An activity implementation should use GetActivityInfo(ctx).TaskToken function to get task token to use for completion.
		// Example:-
		//	To complete with a result.
		//  	CompleteActivity(token, "Done", nil)
		//	To fail the activity with an error.
		//      CompleteActivity(token, nil, temporal.NewApplicationError("reason", details)
		// The activity can fail with below errors ApplicationError, TimeoutError, CanceledError.
		CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error

		// CompleteActivityByID reports activity completed.
		// Similar to CompleteActivity, but may save user from keeping taskToken info.
		// activity Execute method can return activity.ErrResultPending to
		// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivityById() method
		// should be called when that activity is completed with the actual result and error. If err is nil, activity task
		// completed event will be reported; if err is CanceledError, activity task canceled event will be reported; otherwise,
		// activity task failed event will be reported.
		// An activity implementation should use activityID provided in ActivityOption to use for completion.
		// namespace name, workflowID, activityID are required, runID is optional.
		// The errors it can return:
		//  - ApplicationError
		//  - TimeoutError
		//  - CanceledError
		CompleteActivityByID(ctx context.Context, namespace, workflowID, runID, activityID string, result interface{}, err error) error

		// RecordActivityHeartbeat records heartbeat for an activity.
		// taskToken - is the value of the binary "TaskToken" field of the "ActivityInfo" struct retrieved inside the activity.
		// details - is the progress you want to record along with heart beat for this activity.
		// The errors it can return:
		//	- serviceerror.NotFound
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error

		// RecordActivityHeartbeatByID records heartbeat for an activity.
		// details - is the progress you want to record along with heart beat for this activity.
		// The errors it can return:
		//	- serviceerror.NotFound
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		RecordActivityHeartbeatByID(ctx context.Context, namespace, workflowID, runID, activityID string, details ...interface{}) error

		// ListClosedWorkflow gets closed workflow executions based on request filters.
		// Retrieved workflow executions are sorted by close time in descending order.
		// Note: heavy usage of this API may cause huge persistence pressure.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		//  - serviceerror.NotFound
		ListClosedWorkflow(ctx context.Context, request *workflowservice.ListClosedWorkflowExecutionsRequest) (*workflowservice.ListClosedWorkflowExecutionsResponse, error)

		// ListOpenWorkflow gets open workflow executions based on request filters.
		// Retrieved workflow executions are sorted by start time in descending order.
		// Note: heavy usage of this API may cause huge persistence pressure.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		//  - serviceerror.NotFound
		ListOpenWorkflow(ctx context.Context, request *workflowservice.ListOpenWorkflowExecutionsRequest) (*workflowservice.ListOpenWorkflowExecutionsResponse, error)

		// ListWorkflow gets workflow executions based on query. This API only works with ElasticSearch,
		// and will return serviceerror.InvalidArgument when using Cassandra or MySQL. The query is basically the SQL WHERE clause,
		// examples:
		//  - "(WorkflowID = 'wid1' or (WorkflowType = 'type2' and WorkflowID = 'wid2'))".
		//  - "CloseTime between '2019-08-27T15:04:05+00:00' and '2019-08-28T15:04:05+00:00'".
		//  - to list only open workflow use "CloseTime = missing"
		// Retrieved workflow executions are sorted by StartTime in descending order when list open workflow,
		// and sorted by CloseTime in descending order for other queries.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)

		// ListArchivedWorkflow gets archived workflow executions based on query. This API will return BadRequest if Temporal
		// cluster or target namespace is not configured for visibility archival or read is not enabled. The query is basically the SQL WHERE clause.
		// However, different visibility archivers have different limitations on the query. Please check the documentation of the visibility archiver used
		// by your namespace to see what kind of queries are accept and whether retrieved workflow executions are ordered or not.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		ListArchivedWorkflow(ctx context.Context, request *workflowservice.ListArchivedWorkflowExecutionsRequest) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error)

		// ScanWorkflow gets workflow executions based on query. This API only works with ElasticSearch,
		// and will return serviceerror.InvalidArgument when using Cassandra or MySQL. The query is basically the SQL WHERE clause
		// (see ListWorkflow for query examples).
		// ScanWorkflow should be used when retrieving large amount of workflows and order is not needed.
		// It will use more ElasticSearch resources than ListWorkflow, but will be several times faster
		// when retrieving millions of workflows.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		ScanWorkflow(ctx context.Context, request *workflowservice.ScanWorkflowExecutionsRequest) (*workflowservice.ScanWorkflowExecutionsResponse, error)

		// CountWorkflow gets number of workflow executions based on query. This API only works with ElasticSearch,
		// and will return serviceerror.InvalidArgument when using Cassandra or MySQL. The query is basically the SQL WHERE clause
		// (see ListWorkflow for query examples).
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		CountWorkflow(ctx context.Context, request *workflowservice.CountWorkflowExecutionsRequest) (*workflowservice.CountWorkflowExecutionsResponse, error)

		// GetSearchAttributes returns valid search attributes keys and value types.
		// The search attributes can be used in query of List/Scan/Count APIs. Adding new search attributes requires temporal server
		// to update dynamic config ValidSearchAttributes.
		GetSearchAttributes(ctx context.Context) (*workflowservice.GetSearchAttributesResponse, error)

		// QueryWorkflow queries a given workflow's last execution and returns the query result synchronously. Parameter workflowID
		// and queryType are required, other parameters are optional. The workflowID and runID (optional) identify the
		// target workflow execution that this query will be send to. If runID is not specified (empty string), server will
		// use the currently running execution of that workflowID. The queryType specifies the type of query you want to
		// run. By default, temporal supports "__stack_trace" as a standard query type, which will return string value
		// representing the call stack of the target workflow. The target workflow could also setup different query handler
		// to handle custom query types.
		// See comments at workflow.SetQueryHandler(ctx Context, queryType string, handler interface{}) for more details
		// on how to setup query handler within the target workflow.
		// - workflowID is required.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// - queryType is the type of the query.
		// - args... are the optional query parameters.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		//  - serviceerror.NotFound
		//  - serviceerror.QueryFailed
		QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (converter.EncodedValue, error)

		// QueryWorkflowWithOptions queries a given workflow execution and returns the query result synchronously.
		// See QueryWorkflowWithOptionsRequest and QueryWorkflowWithOptionsResponse for more information.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		//  - serviceerror.NotFound
		//  - serviceerror.QueryFailed
		QueryWorkflowWithOptions(ctx context.Context, request *QueryWorkflowWithOptionsRequest) (*QueryWorkflowWithOptionsResponse, error)

		// DescribeWorkflowExecution returns information about the specified workflow execution.
		// - runID can be default(empty string). if empty string then it will pick the last running execution of that workflow ID.
		//
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		//  - serviceerror.NotFound
		DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)

		// DescribeTaskQueue returns information about the target taskqueue, right now this API returns the
		// pollers which polled this taskqueue in last few minutes.
		// The errors it can return:
		//  - serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		//  - serviceerror.NotFound
		DescribeTaskQueue(ctx context.Context, taskqueue string, taskqueueType enumspb.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error)

		// ResetWorkflowExecution reset an existing workflow execution to WorkflowTaskFinishEventId(exclusive).
		// And it will immediately terminating the current execution instance.
		// RequestId is used to deduplicate requests. It will be autogenerated if not set.
		ResetWorkflowExecution(ctx context.Context, request *workflowservice.ResetWorkflowExecutionRequest) (*workflowservice.ResetWorkflowExecutionResponse, error)

		// WorkflowService provides access to the underlying gRPC service. This should only be used for advanced use cases
		// that cannot be accomplished via other Client methods. Unlike calls to other Client methods, calls directly to the
		// service are not configured with internal semantics such as automatic retries.
		WorkflowService() workflowservice.WorkflowServiceClient

		// Close client and clean up underlying resources.
		Close()
	}

	// NamespaceClient is the client for managing operations on the namespace.
	// CLI, tools, ... can use this layer to manager operations on namespace.
	NamespaceClient interface {
		// Register a namespace with temporal server
		// The errors it can throw:
		//	- NamespaceAlreadyExistsError
		//	- serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		Register(ctx context.Context, request *workflowservice.RegisterNamespaceRequest) error

		// Describe a namespace. The namespace has 3 part of information
		// NamespaceInfo - Which has Name, Status, Description, Owner Email
		// NamespaceConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
		// ReplicationConfiguration - replication config like clusters and active cluster name
		// The errors it can throw:
		//	- serviceerror.NotFound
		//	- serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		Describe(ctx context.Context, name string) (*workflowservice.DescribeNamespaceResponse, error)

		// Update a namespace.
		// The errors it can throw:
		//	- serviceerror.NotFound
		//	- serviceerror.InvalidArgument
		//	- serviceerror.Internal
		//	- serviceerror.Unavailable
		Update(ctx context.Context, request *workflowservice.UpdateNamespaceRequest) error

		// Close client and clean up underlying resources.
		Close()
	}
)

// MetricsHandler is a handler for metrics emitted by the SDK. This interface is
// intentionally limited to only what the SDK needs to emit metrics and is not
// built to be a general purpose metrics abstraction for all uses.
//
// A common implementation is at
// go.temporal.io/sdk/contrib/tally.NewMetricsHandler. The MetricsNopHandler is
// a noop handler. A handler may implement "Unwrap() client.MetricsHandler" if
// it wraps a handler.
type MetricsHandler = metrics.Handler

// MetricsCounter is an ever-increasing counter.
type MetricsCounter = metrics.Counter

// MetricsGauge can be set to any float.
type MetricsGauge = metrics.Gauge

// MetricsTimer records time durations.
type MetricsTimer = metrics.Timer

// MetricsNopHandler is a noop handler that does nothing with the metrics.
var MetricsNopHandler = metrics.NopHandler

// NewClient creates an instance of a workflow client. This will attempt to
// connect to the server eagerly and will return an error if the server is not
// available.
func NewClient(options Options) (Client, error) {
	return internal.NewClient(options)
}

// NewNamespaceClient creates an instance of a namespace client, to manage
// lifecycle of namespaces. This will not attempt to connect to the server
// eagerly and therefore may not fail for an unreachable server until a call is
// made. grpc.WithBlock can be passed as a gRPC dial option to connection
// options to eagerly connect.
func NewNamespaceClient(options Options) (NamespaceClient, error) {
	return internal.NewNamespaceClient(options)
}

// make sure if new methods are added to internal.Client they are also added to public Client.
var _ Client = internal.Client(nil)
var _ internal.Client = Client(nil)
var _ NamespaceClient = internal.NamespaceClient(nil)
var _ internal.NamespaceClient = NamespaceClient(nil)

// NewValue creates a new converter.EncodedValue which can be used to decode binary data returned by Temporal.  For example:
// User had Activity.RecordHeartbeat(ctx, "my-heartbeat") and then got response from calling Client.DescribeWorkflowExecution.
// The response contains binary field PendingActivityInfo.HeartbeatDetails,
// which can be decoded by using:
//   var result string // This need to be same type as the one passed to RecordHeartbeat
//   NewValue(data).Get(&result)
func NewValue(data *commonpb.Payloads) converter.EncodedValue {
	return internal.NewValue(data)
}

// NewValues creates a new converter.EncodedValues which can be used to decode binary data returned by Temporal. For example:
// User had Activity.RecordHeartbeat(ctx, "my-heartbeat", 123) and then got response from calling Client.DescribeWorkflowExecution.
// The response contains binary field PendingActivityInfo.HeartbeatDetails,
// which can be decoded by using:
//   var result1 string
//   var result2 int // These need to be same type as those arguments passed to RecordHeartbeat
//   NewValues(data).Get(&result1, &result2)
func NewValues(data *commonpb.Payloads) converter.EncodedValues {
	return internal.NewValues(data)
}
