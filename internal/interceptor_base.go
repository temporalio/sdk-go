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

type InterceptorBase struct {
	ClientInterceptorBase
	WorkerInterceptorBase
}

type WorkerInterceptorBase struct{}

var _ WorkerInterceptor = &WorkerInterceptorBase{}

func (*WorkerInterceptorBase) InterceptActivity(
	ctx Context,
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

func (*WorkflowOutboundInterceptorBase) mustEmbedWorkflowOutboundInterceptorBase() {}

type ClientInterceptorBase struct{}

var _ ClientInterceptor = &ClientInterceptorBase{}

func (*ClientInterceptorBase) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &ClientOutboundInterceptorBase{Next: next}
}

func (*ClientInterceptorBase) mustEmbedClientInterceptorBase() {}

type ClientOutboundInterceptorBase struct {
	Next ClientOutboundInterceptor
}

var _ ClientOutboundInterceptor = &ClientOutboundInterceptorBase{}

func (c *ClientOutboundInterceptorBase) ExecuteWorkflow(
	ctx context.Context,
	options StartWorkflowOptions,
	workflow interface{},
	args ...interface{},
) (WorkflowRun, error) {
	return c.Next.ExecuteWorkflow(ctx, options, workflow, args...)
}

func (c *ClientOutboundInterceptorBase) GetWorkflow(ctx context.Context, workflowID string, runID string) WorkflowRun {
	return c.Next.GetWorkflow(ctx, workflowID, runID)
}

func (c *ClientOutboundInterceptorBase) SignalWorkflow(
	ctx context.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) error {
	return c.Next.SignalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (c *ClientOutboundInterceptorBase) SignalWithStartWorkflow(
	ctx context.Context,
	workflowID string,
	signalName string,
	signalArg interface{},
	options StartWorkflowOptions,
	workflow interface{},
	workflowArgs ...interface{},
) (WorkflowRun, error) {
	return c.Next.SignalWithStartWorkflow(ctx, workflowID, signalName, signalArg, options, workflow, workflowArgs...)
}

func (c *ClientOutboundInterceptorBase) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	return c.Next.CancelWorkflow(ctx, workflowID, runID)
}

func (c *ClientOutboundInterceptorBase) TerminateWorkflow(
	ctx context.Context,
	workflowID string,
	runID string,
	reason string,
	details ...interface{},
) error {
	return c.Next.TerminateWorkflow(ctx, workflowID, runID, reason, details...)
}

func (c *ClientOutboundInterceptorBase) GetWorkflowHistory(
	ctx context.Context,
	workflowID string,
	runID string,
	isLongPoll bool,
	filterType enumspb.HistoryEventFilterType,
) HistoryEventIterator {
	return c.Next.GetWorkflowHistory(ctx, workflowID, runID, isLongPoll, filterType)
}

func (c *ClientOutboundInterceptorBase) CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error {
	return c.Next.CompleteActivity(ctx, taskToken, result, err)
}

func (c *ClientOutboundInterceptorBase) CompleteActivityByID(
	ctx context.Context,
	namespace string,
	workflowID string,
	runID string,
	activityID string,
	result interface{},
	err error,
) error {
	return c.Next.CompleteActivityByID(ctx, namespace, workflowID, runID, activityID, result, err)
}

func (c *ClientOutboundInterceptorBase) RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error {
	return c.Next.RecordActivityHeartbeat(ctx, taskToken, details...)
}

func (c *ClientOutboundInterceptorBase) RecordActivityHeartbeatByID(
	ctx context.Context,
	namespace string,
	workflowID string,
	runID string,
	activityID string,
	details ...interface{},
) error {
	return c.Next.RecordActivityHeartbeatByID(ctx, namespace, workflowID, runID, activityID, details...)
}

func (c *ClientOutboundInterceptorBase) ListClosedWorkflow(
	ctx context.Context,
	request *workflowservice.ListClosedWorkflowExecutionsRequest,
) (*workflowservice.ListClosedWorkflowExecutionsResponse, error) {
	return c.Next.ListClosedWorkflow(ctx, request)
}

func (c *ClientOutboundInterceptorBase) ListOpenWorkflow(
	ctx context.Context,
	request *workflowservice.ListOpenWorkflowExecutionsRequest,
) (*workflowservice.ListOpenWorkflowExecutionsResponse, error) {
	return c.Next.ListOpenWorkflow(ctx, request)
}

func (c *ClientOutboundInterceptorBase) ListWorkflow(
	ctx context.Context,
	request *workflowservice.ListWorkflowExecutionsRequest,
) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	return c.Next.ListWorkflow(ctx, request)
}

func (c *ClientOutboundInterceptorBase) ListArchivedWorkflow(
	ctx context.Context,
	request *workflowservice.ListArchivedWorkflowExecutionsRequest,
) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error) {
	return c.Next.ListArchivedWorkflow(ctx, request)
}

func (c *ClientOutboundInterceptorBase) ScanWorkflow(
	ctx context.Context,
	request *workflowservice.ScanWorkflowExecutionsRequest,
) (*workflowservice.ScanWorkflowExecutionsResponse, error) {
	return c.Next.ScanWorkflow(ctx, request)
}

func (c *ClientOutboundInterceptorBase) CountWorkflow(
	ctx context.Context,
	request *workflowservice.CountWorkflowExecutionsRequest,
) (*workflowservice.CountWorkflowExecutionsResponse, error) {
	return c.Next.CountWorkflow(ctx, request)
}

func (c *ClientOutboundInterceptorBase) GetSearchAttributes(
	ctx context.Context,
) (*workflowservice.GetSearchAttributesResponse, error) {
	return c.Next.GetSearchAttributes(ctx)
}

func (c *ClientOutboundInterceptorBase) QueryWorkflow(
	ctx context.Context,
	workflowID string,
	runID string,
	queryType string,
	args ...interface{},
) (converter.EncodedValue, error) {
	return c.Next.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
}

func (c *ClientOutboundInterceptorBase) QueryWorkflowWithOptions(
	ctx context.Context,
	request *QueryWorkflowWithOptionsRequest,
) (*QueryWorkflowWithOptionsResponse, error) {
	return c.Next.QueryWorkflowWithOptions(ctx, request)
}

func (c *ClientOutboundInterceptorBase) DescribeWorkflowExecution(
	ctx context.Context,
	workflowID string,
	runID string,
) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	return c.Next.DescribeWorkflowExecution(ctx, workflowID, runID)
}

func (c *ClientOutboundInterceptorBase) DescribeTaskQueue(
	ctx context.Context,
	taskqueue string,
	taskqueueType enumspb.TaskQueueType,
) (*workflowservice.DescribeTaskQueueResponse, error) {
	return c.Next.DescribeTaskQueue(ctx, taskqueue, taskqueueType)
}

func (c *ClientOutboundInterceptorBase) ResetWorkflowExecution(
	ctx context.Context,
	request *workflowservice.ResetWorkflowExecutionRequest,
) (*workflowservice.ResetWorkflowExecutionResponse, error) {
	return c.Next.ResetWorkflowExecution(ctx, request)
}

func (c *ClientOutboundInterceptorBase) Close() {
	c.Next.Close()
}

func (*ClientOutboundInterceptorBase) mustEmbedClientOutboundInterceptorBase() {}
