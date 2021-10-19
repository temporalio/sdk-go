// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
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

import (
	"context"
	"time"

	"github.com/uber-go/tally/v4"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

type Interceptor interface {
	ClientInterceptor
	WorkerInterceptor
}

type WorkerInterceptor interface {
	InterceptActivity(ctx Context, next ActivityInboundInterceptor) ActivityInboundInterceptor

	InterceptWorkflow(ctx Context, next WorkflowInboundInterceptor) WorkflowInboundInterceptor

	mustEmbedWorkerInterceptorBase()
}

type ActivityInboundInterceptor interface {
	Init(outbound ActivityOutboundInterceptor) error

	ExecuteActivity(ctx context.Context, in *ExecuteActivityInput) (interface{}, error)

	mustEmbedActivityInboundInterceptorBase()
}

type ExecuteActivityInput struct {
	Args []interface{}
}

type ActivityOutboundInterceptor interface {
	GetInfo(ctx context.Context) ActivityInfo

	GetLogger(ctx context.Context) log.Logger

	GetMetricsScope(ctx context.Context) tally.Scope

	RecordHeartbeat(ctx context.Context, details ...interface{})

	HasHeartbeatDetails(ctx context.Context) bool

	GetHeartbeatDetails(ctx context.Context, d ...interface{}) error

	GetWorkerStopChannel(ctx context.Context) <-chan struct{}

	mustEmbedActivityOutboundInterceptorBase()
}

type WorkflowInboundInterceptor interface {
	Init(outbound WorkflowOutboundInterceptor) error

	ExecuteWorkflow(ctx Context, in *ExecuteWorkflowInput) (interface{}, error)

	HandleSignal(ctx Context, in *HandleSignalInput) error

	HandleQuery(ctx Context, in *HandleQueryInput) (interface{}, error)

	mustEmbedWorkflowInboundInterceptorBase()
}

type ExecuteWorkflowInput struct {
	Args []interface{}
}

type HandleSignalInput struct {
	SignalName string
	Arg        interface{}
}

type HandleQueryInput struct {
	QueryType string
	Args      []interface{}
}

type WorkflowOutboundInterceptor interface {
	Go(ctx Context, name string, f func(ctx Context)) Context

	ExecuteActivity(ctx Context, activityType string, args ...interface{}) Future

	ExecuteLocalActivity(ctx Context, activityType string, args ...interface{}) Future

	ExecuteChildWorkflow(ctx Context, childWorkflowType string, args ...interface{}) ChildWorkflowFuture

	GetInfo(ctx Context) *WorkflowInfo

	GetLogger(ctx Context) log.Logger

	GetMetricsScope(ctx Context) tally.Scope

	Now(ctx Context) time.Time

	NewTimer(ctx Context, d time.Duration) Future

	Sleep(ctx Context, d time.Duration) (err error)

	RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future

	SignalExternalWorkflow(ctx Context, workflowID, runID, signalName string, arg interface{}) Future

	UpsertSearchAttributes(ctx Context, attributes map[string]interface{}) error

	GetSignalChannel(ctx Context, signalName string) ReceiveChannel

	SideEffect(ctx Context, f func(ctx Context) interface{}) converter.EncodedValue

	MutableSideEffect(
		ctx Context,
		id string,
		f func(ctx Context) interface{},
		equals func(a, b interface{}) bool,
	) converter.EncodedValue

	GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version

	SetQueryHandler(ctx Context, queryType string, handler interface{}) error

	IsReplaying(ctx Context) bool

	HasLastCompletionResult(ctx Context) bool

	GetLastCompletionResult(ctx Context, d ...interface{}) error

	GetLastError(ctx Context) error

	mustEmbedWorkflowOutboundInterceptorBase()
}

type ClientInterceptor interface {
	// This is called on client creation if set via client options, or worker
	// creation if set via workers
	InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor

	mustEmbedClientInterceptorBase()
}

type ClientOutboundInterceptor interface {
	ExecuteWorkflow(
		ctx context.Context,
		options StartWorkflowOptions,
		workflow interface{},
		args ...interface{},
	) (WorkflowRun, error)

	GetWorkflow(ctx context.Context, workflowID string, runID string) WorkflowRun

	SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error

	SignalWithStartWorkflow(
		ctx context.Context,
		workflowID string,
		signalName string,
		signalArg interface{},
		options StartWorkflowOptions,
		workflow interface{},
		workflowArgs ...interface{},
	) (WorkflowRun, error)

	CancelWorkflow(ctx context.Context, workflowID string, runID string) error

	TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error

	GetWorkflowHistory(
		ctx context.Context,
		workflowID string,
		runID string,
		isLongPoll bool,
		filterType enumspb.HistoryEventFilterType,
	) HistoryEventIterator

	CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error

	CompleteActivityByID(
		ctx context.Context,
		namespace string,
		workflowID string,
		runID string,
		activityID string,
		result interface{},
		err error,
	) error

	RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error

	RecordActivityHeartbeatByID(
		ctx context.Context,
		namespace string,
		workflowID string,
		runID string,
		activityID string,
		details ...interface{},
	) error

	ListClosedWorkflow(
		ctx context.Context,
		request *workflowservice.ListClosedWorkflowExecutionsRequest,
	) (*workflowservice.ListClosedWorkflowExecutionsResponse, error)

	ListOpenWorkflow(
		ctx context.Context,
		request *workflowservice.ListOpenWorkflowExecutionsRequest,
	) (*workflowservice.ListOpenWorkflowExecutionsResponse, error)

	ListWorkflow(
		ctx context.Context,
		request *workflowservice.ListWorkflowExecutionsRequest,
	) (*workflowservice.ListWorkflowExecutionsResponse, error)

	ListArchivedWorkflow(
		ctx context.Context,
		request *workflowservice.ListArchivedWorkflowExecutionsRequest,
	) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error)

	ScanWorkflow(
		ctx context.Context,
		request *workflowservice.ScanWorkflowExecutionsRequest,
	) (*workflowservice.ScanWorkflowExecutionsResponse, error)

	CountWorkflow(
		ctx context.Context,
		request *workflowservice.CountWorkflowExecutionsRequest,
	) (*workflowservice.CountWorkflowExecutionsResponse, error)

	GetSearchAttributes(ctx context.Context) (*workflowservice.GetSearchAttributesResponse, error)

	QueryWorkflow(
		ctx context.Context,
		workflowID string,
		runID string,
		queryType string,
		args ...interface{},
	) (converter.EncodedValue, error)

	QueryWorkflowWithOptions(
		ctx context.Context,
		request *QueryWorkflowWithOptionsRequest,
	) (*QueryWorkflowWithOptionsResponse, error)

	DescribeWorkflowExecution(
		ctx context.Context,
		workflowID string,
		runID string,
	) (*workflowservice.DescribeWorkflowExecutionResponse, error)

	DescribeTaskQueue(
		ctx context.Context,
		taskqueue string,
		taskqueueType enumspb.TaskQueueType,
	) (*workflowservice.DescribeTaskQueueResponse, error)

	ResetWorkflowExecution(
		ctx context.Context,
		request *workflowservice.ResetWorkflowExecutionRequest,
	) (*workflowservice.ResetWorkflowExecutionResponse, error)

	Close()

	mustEmbedClientOutboundInterceptorBase()
}

// ClientOutboundInterceptor must match Client
var _ Client = (ClientOutboundInterceptor)(nil)
