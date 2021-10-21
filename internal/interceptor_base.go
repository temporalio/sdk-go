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

type InterceptorBase struct {
	ClientInterceptorBase
	WorkerInterceptorBase
}

type WorkerInterceptorBase struct{}

var _ WorkerInterceptor = &WorkerInterceptorBase{}

func (*WorkerInterceptorBase) InterceptActivity(
	ctx context.Context,
	next ActivityInboundInterceptor,
) ActivityInboundInterceptor {
	return &ActivityInboundInterceptorBase{Next: next}
}

func (*WorkerInterceptorBase) InterceptWorkflow(
	ctx Context,
	next WorkflowInboundInterceptor,
) WorkflowInboundInterceptor {
	return &WorkflowInboundInterceptorBase{Next: next}
}

func (*WorkerInterceptorBase) mustEmbedWorkerInterceptorBase() {}

type ActivityInboundInterceptorBase struct {
	Next ActivityInboundInterceptor
}

var _ ActivityInboundInterceptor = &ActivityInboundInterceptorBase{}

func (a *ActivityInboundInterceptorBase) Init(outbound ActivityOutboundInterceptor) error {
	return a.Next.Init(outbound)
}

func (a *ActivityInboundInterceptorBase) ExecuteActivity(
	ctx context.Context,
	in *ExecuteActivityInput,
) (interface{}, error) {
	return a.Next.ExecuteActivity(ctx, in)
}

func (*ActivityInboundInterceptorBase) mustEmbedActivityInboundInterceptorBase() {}

type ActivityOutboundInterceptorBase struct {
	Next ActivityOutboundInterceptor
}

var _ ActivityOutboundInterceptor = &ActivityOutboundInterceptorBase{}

func (a *ActivityOutboundInterceptorBase) GetInfo(ctx context.Context) ActivityInfo {
	return a.Next.GetInfo(ctx)
}

func (a *ActivityOutboundInterceptorBase) GetLogger(ctx context.Context) log.Logger {
	return a.Next.GetLogger(ctx)
}

func (a *ActivityOutboundInterceptorBase) GetMetricsScope(ctx context.Context) tally.Scope {
	return a.Next.GetMetricsScope(ctx)
}

func (a *ActivityOutboundInterceptorBase) RecordHeartbeat(ctx context.Context, details ...interface{}) {
	a.Next.RecordHeartbeat(ctx, details...)
}

func (a *ActivityOutboundInterceptorBase) HasHeartbeatDetails(ctx context.Context) bool {
	return a.Next.HasHeartbeatDetails(ctx)
}

func (a *ActivityOutboundInterceptorBase) GetHeartbeatDetails(ctx context.Context, d ...interface{}) error {
	return a.Next.GetHeartbeatDetails(ctx, d...)
}

func (a *ActivityOutboundInterceptorBase) GetWorkerStopChannel(ctx context.Context) <-chan struct{} {
	return a.Next.GetWorkerStopChannel(ctx)
}

func (*ActivityOutboundInterceptorBase) mustEmbedActivityOutboundInterceptorBase() {}

type WorkflowInboundInterceptorBase struct {
	Next WorkflowInboundInterceptor
}

var _ WorkflowInboundInterceptor = &WorkflowInboundInterceptorBase{}

func (w *WorkflowInboundInterceptorBase) Init(outbound WorkflowOutboundInterceptor) error {
	return w.Next.Init(outbound)
}

func (w *WorkflowInboundInterceptorBase) ExecuteWorkflow(ctx Context, in *ExecuteWorkflowInput) (interface{}, error) {
	return w.Next.ExecuteWorkflow(ctx, in)
}

func (w *WorkflowInboundInterceptorBase) HandleSignal(ctx Context, in *HandleSignalInput) error {
	return w.Next.HandleSignal(ctx, in)
}

func (w *WorkflowInboundInterceptorBase) HandleQuery(ctx Context, in *HandleQueryInput) (interface{}, error) {
	return w.Next.HandleQuery(ctx, in)
}

func (*WorkflowInboundInterceptorBase) mustEmbedWorkflowInboundInterceptorBase() {}

type WorkflowOutboundInterceptorBase struct {
	Next WorkflowOutboundInterceptor
}

var _ WorkflowOutboundInterceptor = &WorkflowOutboundInterceptorBase{}

func (w *WorkflowOutboundInterceptorBase) Go(ctx Context, name string, f func(ctx Context)) Context {
	return w.Next.Go(ctx, name, f)
}

func (w *WorkflowOutboundInterceptorBase) ExecuteActivity(ctx Context, activityType string, args ...interface{}) Future {
	return w.Next.ExecuteActivity(ctx, activityType, args...)
}

func (w *WorkflowOutboundInterceptorBase) ExecuteLocalActivity(ctx Context, activityType string, args ...interface{}) Future {
	return w.Next.ExecuteLocalActivity(ctx, activityType, args...)
}

func (w *WorkflowOutboundInterceptorBase) ExecuteChildWorkflow(ctx Context, childWorkflowType string, args ...interface{}) ChildWorkflowFuture {
	return w.Next.ExecuteChildWorkflow(ctx, childWorkflowType, args...)
}

func (w *WorkflowOutboundInterceptorBase) GetInfo(ctx Context) *WorkflowInfo {
	return w.Next.GetInfo(ctx)
}

func (w *WorkflowOutboundInterceptorBase) GetLogger(ctx Context) log.Logger {
	return w.Next.GetLogger(ctx)
}

func (w *WorkflowOutboundInterceptorBase) GetMetricsScope(ctx Context) tally.Scope {
	return w.Next.GetMetricsScope(ctx)
}

func (w *WorkflowOutboundInterceptorBase) Now(ctx Context) time.Time {
	return w.Next.Now(ctx)
}

func (w *WorkflowOutboundInterceptorBase) NewTimer(ctx Context, d time.Duration) Future {
	return w.Next.NewTimer(ctx, d)
}

func (w *WorkflowOutboundInterceptorBase) Sleep(ctx Context, d time.Duration) (err error) {
	return w.Next.Sleep(ctx, d)
}

func (w *WorkflowOutboundInterceptorBase) RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future {
	return w.Next.RequestCancelExternalWorkflow(ctx, workflowID, runID)
}

func (w *WorkflowOutboundInterceptorBase) SignalExternalWorkflow(
	ctx Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) Future {
	return w.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (w *WorkflowOutboundInterceptorBase) UpsertSearchAttributes(ctx Context, attributes map[string]interface{}) error {
	return w.Next.UpsertSearchAttributes(ctx, attributes)
}

func (w *WorkflowOutboundInterceptorBase) GetSignalChannel(ctx Context, signalName string) ReceiveChannel {
	return w.Next.GetSignalChannel(ctx, signalName)
}

func (w *WorkflowOutboundInterceptorBase) SideEffect(
	ctx Context,
	f func(ctx Context) interface{},
) converter.EncodedValue {
	return w.Next.SideEffect(ctx, f)
}

func (w *WorkflowOutboundInterceptorBase) MutableSideEffect(
	ctx Context,
	id string,
	f func(ctx Context) interface{},
	equals func(a, b interface{}) bool,
) converter.EncodedValue {
	return w.Next.MutableSideEffect(ctx, id, f, equals)
}

func (w *WorkflowOutboundInterceptorBase) GetVersion(
	ctx Context,
	changeID string,
	minSupported Version,
	maxSupported Version,
) Version {
	return w.Next.GetVersion(ctx, changeID, minSupported, maxSupported)
}

func (w *WorkflowOutboundInterceptorBase) SetQueryHandler(ctx Context, queryType string, handler interface{}) error {
	return w.Next.SetQueryHandler(ctx, queryType, handler)
}

func (w *WorkflowOutboundInterceptorBase) IsReplaying(ctx Context) bool {
	return w.Next.IsReplaying(ctx)
}

func (w *WorkflowOutboundInterceptorBase) HasLastCompletionResult(ctx Context) bool {
	return w.Next.HasLastCompletionResult(ctx)
}

func (w *WorkflowOutboundInterceptorBase) GetLastCompletionResult(ctx Context, d ...interface{}) error {
	return w.Next.GetLastCompletionResult(ctx, d...)
}

func (w *WorkflowOutboundInterceptorBase) GetLastError(ctx Context) error {
	return w.Next.GetLastError(ctx)
}

func (w *WorkflowOutboundInterceptorBase) NewContinueAsNewError(
	ctx Context,
	wfn interface{},
	args ...interface{},
) error {
	return w.Next.NewContinueAsNewError(ctx, wfn, args...)
}

func (*WorkflowOutboundInterceptorBase) mustEmbedWorkflowOutboundInterceptorBase() {}

type ClientInterceptorBase struct{}

var _ ClientInterceptor = &ClientInterceptorBase{}

func (*ClientInterceptorBase) InterceptClient(
	next ClientOutboundInterceptor,
) ClientOutboundInterceptor {
	return &ClientOutboundInterceptorBase{Next: next}
}

func (*ClientInterceptorBase) mustEmbedClientInterceptorBase() {}

type ClientOutboundInterceptorBase struct {
	Next ClientOutboundInterceptor
}

var _ ClientOutboundInterceptor = &ClientOutboundInterceptorBase{}

func (c *ClientOutboundInterceptorBase) ExecuteWorkflow(
	ctx context.Context,
	in *ClientExecuteWorkflowInput,
) (WorkflowRun, error) {
	return c.Next.ExecuteWorkflow(ctx, in)
}

func (c *ClientOutboundInterceptorBase) SignalWorkflow(ctx context.Context, in *ClientSignalWorkflowInput) error {
	return c.Next.SignalWorkflow(ctx, in)
}

func (c *ClientOutboundInterceptorBase) SignalWithStartWorkflow(
	ctx context.Context,
	in *ClientSignalWithStartWorkflowInput,
) (WorkflowRun, error) {
	return c.Next.SignalWithStartWorkflow(ctx, in)
}

func (c *ClientOutboundInterceptorBase) CancelWorkflow(ctx context.Context, in *ClientCancelWorkflowInput) error {
	return c.Next.CancelWorkflow(ctx, in)
}

func (c *ClientOutboundInterceptorBase) TerminateWorkflow(ctx context.Context, in *ClientTerminateWorkflowInput) error {
	return c.Next.TerminateWorkflow(ctx, in)
}

func (c *ClientOutboundInterceptorBase) QueryWorkflow(
	ctx context.Context,
	in *ClientQueryWorkflowInput,
) (converter.EncodedValue, error) {
	return c.Next.QueryWorkflow(ctx, in)
}

func (*ClientOutboundInterceptorBase) mustEmbedClientOutboundInterceptorBase() {}
