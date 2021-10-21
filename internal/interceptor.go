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
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

type Interceptor interface {
	ClientInterceptor
	WorkerInterceptor
}

type WorkerInterceptor interface {
	InterceptActivity(ctx context.Context, next ActivityInboundInterceptor) ActivityInboundInterceptor

	InterceptWorkflow(ctx Context, next WorkflowInboundInterceptor) WorkflowInboundInterceptor

	mustEmbedWorkerInterceptorBase()
}

type ActivityInboundInterceptor interface {
	Init(outbound ActivityOutboundInterceptor) error

	// Context has header
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

	// Context has header
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

	// Context has header
	ExecuteActivity(ctx Context, activityType string, args ...interface{}) Future

	// Context has header
	ExecuteLocalActivity(ctx Context, activityType string, args ...interface{}) Future

	// Context has header
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

	// Context has header
	NewContinueAsNewError(ctx Context, wfn interface{}, args ...interface{}) error

	mustEmbedWorkflowOutboundInterceptorBase()
}

type ClientInterceptor interface {
	// This is called on client creation if set via client options
	InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor

	mustEmbedClientInterceptorBase()
}

// Note, this only intercepts a specific subset of client calls by intention
type ClientOutboundInterceptor interface {
	// Context has header
	ExecuteWorkflow(context.Context, *ClientExecuteWorkflowInput) (WorkflowRun, error)

	SignalWorkflow(context.Context, *ClientSignalWorkflowInput) error

	// Context has header
	SignalWithStartWorkflow(context.Context, *ClientSignalWithStartWorkflowInput) (WorkflowRun, error)

	CancelWorkflow(context.Context, *ClientCancelWorkflowInput) error

	TerminateWorkflow(context.Context, *ClientTerminateWorkflowInput) error

	QueryWorkflow(context.Context, *ClientQueryWorkflowInput) (converter.EncodedValue, error)

	mustEmbedClientOutboundInterceptorBase()
}

type ClientExecuteWorkflowInput struct {
	Options      *StartWorkflowOptions
	WorkflowType string
	Args         []interface{}
}

type ClientSignalWorkflowInput struct {
	WorkflowID string
	RunID      string
	SignalName string
	Arg        interface{}
}

type ClientSignalWithStartWorkflowInput struct {
	SignalName   string
	SignalArg    interface{}
	Options      *StartWorkflowOptions
	WorkflowType string
	Args         []interface{}
}

type ClientCancelWorkflowInput struct {
	WorkflowID string
	RunID      string
}

type ClientTerminateWorkflowInput struct {
	WorkflowID string
	RunID      string
	Reason     string
	Details    []interface{}
}

type ClientQueryWorkflowInput struct {
	WorkflowID string
	RunID      string
	QueryType  string
	Args       []interface{}
}
