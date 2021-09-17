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

import (
	"context"
	"time"

	"github.com/uber-go/tally"
	"go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

// WorkflowInterceptor is used to create a single link in the interceptor chain
type WorkflowInterceptor interface {
	// InterceptWorkflow creates an interceptor instance. The created instance must delegate every call to
	// the next parameter for workflow code function correctly.
	InterceptWorkflow(info *WorkflowInfo, next WorkflowInboundCallsInterceptor) WorkflowInboundCallsInterceptor
}

// WorkflowInboundCallsInterceptor is an interface that can be implemented to intercept calls to the workflow.
// Use WorkflowInboundCallsInterceptorBase as a base struct for implementations that do not want to implement every method.
// Interceptor implementation must forward calls to the next in the interceptor chain.
// All code in the interceptor is executed in the workflow.Context of a workflow. So all the rules and restrictions
// that apply to the workflow code should be obeyed by the interceptor implementation.
// Use workflow.IsReplaying(ctx) to filter out duplicated calls.
type WorkflowInboundCallsInterceptor interface {
	Init(outbound WorkflowOutboundCallsInterceptor) error

	// ExecuteWorkflow intercepts workflow function invocation. As calls to other intercepted functions are done from
	// a workflow function this function is the first to be called and completes workflow as soon as it returns.
	// WorkflowType argument is for information purposes only and should not be mutated.
	ExecuteWorkflow(ctx Context, workflowType string, args ...interface{}) []interface{}

	// ProcessSignal is called before the signal is passed to the workflow implementation, note that this function does NOT
	// have any flow control and can not modify the signal or prevent it from being passed to the workflow.
	ProcessSignal(ctx Context, signalName string, arg interface{})

	// HandleQuery is invoked when query request is received, this function HAS flow control and can alter parameters
	// or values returned by the query. Handler that is passed as a parameter MUST be called in order to execute the query.
	HandleQuery(ctx Context, queryType string, args *commonpb.Payloads,
		handler func(*commonpb.Payloads) (*commonpb.Payloads, error)) (*commonpb.Payloads, error)
}

// WorkflowOutboundCallsInterceptor is an interface that can be implemented to intercept calls to the SDK APIs done
// by the workflow code.
// Use worker.WorkflowOutboundCallsInterceptorBase as a base struct for implementations that do not want to implement every method.
// Interceptor implementation must forward calls to the next in the interceptor chain.
// All code in the interceptor is executed in the workflow.Context of a workflow. So all the rules and restrictions
// that apply to the workflow code should be obeyed by the interceptor implementation.
// Use workflow.IsReplaying(ctx) to filter out duplicated calls.
type WorkflowOutboundCallsInterceptor interface {
	Go(ctx Context, name string, f func(ctx Context)) Context
	ExecuteActivity(ctx Context, activityType string, args ...interface{}) Future
	ExecuteLocalActivity(ctx Context, activityType string, args ...interface{}) Future
	ExecuteChildWorkflow(ctx Context, childWorkflowType string, args ...interface{}) ChildWorkflowFuture
	GetWorkflowInfo(ctx Context) *WorkflowInfo
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
	MutableSideEffect(ctx Context, id string, f func(ctx Context) interface{}, equals func(a, b interface{}) bool) converter.EncodedValue
	GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version
	SetQueryHandler(ctx Context, queryType string, handler interface{}) error
	IsReplaying(ctx Context) bool
	HasLastCompletionResult(ctx Context) bool
	GetLastCompletionResult(ctx Context, d ...interface{}) error
	GetLastError(ctx Context) error
}

var _ WorkflowOutboundCallsInterceptor = (*WorkflowOutboundCallsInterceptorBase)(nil)
var _ WorkflowInboundCallsInterceptor = (*WorkflowInboundCallsInterceptorBase)(nil)

// WorkflowInboundCallsInterceptorBase is a noop implementation of WorkflowInboundCallsInterceptor that just forwards requests
// to the next link in an interceptor chain. To be used as base implementation of interceptors.
type WorkflowInboundCallsInterceptorBase struct {
	Next WorkflowInboundCallsInterceptor
}

// Init called before the workflow function is invoked
func (w WorkflowInboundCallsInterceptorBase) Init(outbound WorkflowOutboundCallsInterceptor) error {
	return w.Next.Init(outbound)
}

// ExecuteWorkflow intercepts invocation of the workflow function
func (w WorkflowInboundCallsInterceptorBase) ExecuteWorkflow(ctx Context, workflowType string, args ...interface{}) []interface{} {
	return w.Next.ExecuteWorkflow(ctx, workflowType, args...)
}

// ProcessSignal process inbound signal notification
func (w WorkflowInboundCallsInterceptorBase) ProcessSignal(ctx Context, signalName string, arg interface{}) {
	w.Next.ProcessSignal(ctx, signalName, arg)
}

// HandleQuery handles inbound query request
func (w WorkflowInboundCallsInterceptorBase) HandleQuery(ctx Context, queryType string, args *commonpb.Payloads,
	handler func(*commonpb.Payloads) (*commonpb.Payloads, error)) (*commonpb.Payloads, error) {
	return w.Next.HandleQuery(ctx, queryType, args, handler)
}

// WorkflowOutboundCallsInterceptorBase is a noop implementation of WorkflowOutboundCallsInterceptor that just forwards requests
// to the next link in an interceptor chain. To be used as base implementation of interceptors.
type WorkflowOutboundCallsInterceptorBase struct {
	Next WorkflowOutboundCallsInterceptor
}

// Go forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) Go(ctx Context, name string, f func(ctx Context)) Context {
	return t.Next.Go(ctx, name, f)
}

// ExecuteActivity forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) ExecuteActivity(ctx Context, activityType string, args ...interface{}) Future {
	return t.Next.ExecuteActivity(ctx, activityType, args...)
}

// ExecuteLocalActivity forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) ExecuteLocalActivity(ctx Context, activityType string, args ...interface{}) Future {
	return t.Next.ExecuteLocalActivity(ctx, activityType, args...)
}

// ExecuteChildWorkflow forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) ExecuteChildWorkflow(ctx Context, childWorkflowType string, args ...interface{}) ChildWorkflowFuture {
	return t.Next.ExecuteChildWorkflow(ctx, childWorkflowType, args...)
}

// GetWorkflowInfo forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) GetWorkflowInfo(ctx Context) *WorkflowInfo {
	return t.Next.GetWorkflowInfo(ctx)
}

// GetLogger forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) GetLogger(ctx Context) log.Logger {
	return t.Next.GetLogger(ctx)
}

// GetMetricsScope forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) GetMetricsScope(ctx Context) tally.Scope {
	return t.Next.GetMetricsScope(ctx)
}

// Now forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) Now(ctx Context) time.Time {
	return t.Next.Now(ctx)
}

// NewTimer forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) NewTimer(ctx Context, d time.Duration) Future {
	return t.Next.NewTimer(ctx, d)
}

// Sleep forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) Sleep(ctx Context, d time.Duration) (err error) {
	return t.Next.Sleep(ctx, d)
}

// RequestCancelExternalWorkflow forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future {
	return t.Next.RequestCancelExternalWorkflow(ctx, workflowID, runID)
}

// SignalExternalWorkflow forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) SignalExternalWorkflow(ctx Context, workflowID, runID, signalName string, arg interface{}) Future {
	return t.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}

// UpsertSearchAttributes forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) UpsertSearchAttributes(ctx Context, attributes map[string]interface{}) error {
	return t.Next.UpsertSearchAttributes(ctx, attributes)
}

// GetSignalChannel forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) GetSignalChannel(ctx Context, signalName string) ReceiveChannel {
	return t.Next.GetSignalChannel(ctx, signalName)
}

// SideEffect forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) SideEffect(ctx Context, f func(ctx Context) interface{}) converter.EncodedValue {
	return t.Next.SideEffect(ctx, f)
}

// MutableSideEffect forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) MutableSideEffect(ctx Context, id string, f func(ctx Context) interface{}, equals func(a, b interface{}) bool) converter.EncodedValue {
	return t.Next.MutableSideEffect(ctx, id, f, equals)
}

// GetVersion forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version {
	return t.Next.GetVersion(ctx, changeID, minSupported, maxSupported)
}

// SetQueryHandler forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) SetQueryHandler(ctx Context, queryType string, handler interface{}) error {
	return t.Next.SetQueryHandler(ctx, queryType, handler)
}

// IsReplaying forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) IsReplaying(ctx Context) bool {
	return t.Next.IsReplaying(ctx)
}

// HasLastCompletionResult forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) HasLastCompletionResult(ctx Context) bool {
	return t.Next.HasLastCompletionResult(ctx)
}

// GetLastCompletionResult forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) GetLastCompletionResult(ctx Context, d ...interface{}) error {
	return t.Next.GetLastCompletionResult(ctx, d...)
}

// GetLastError forwards to t.Next
func (t *WorkflowOutboundCallsInterceptorBase) GetLastError(ctx Context) error {
	return t.Next.GetLastError(ctx)
}

type interceptorsContextKeyType struct{}

var InterceptorsContextKey = interceptorsContextKeyType{}

func WithInterceptorWorkflowInfo(ctx context.Context, workflowInfo *WorkflowInfo) context.Context {
	return context.WithValue(ctx, InterceptorsContextKey, workflowInfo)
}

func GetInterceptorWorkflowInfo(ctx context.Context) *WorkflowInfo {
	v := ctx.Value(InterceptorsContextKey)
	if info, ok := v.(*WorkflowInfo); ok {
		return info
	}
	return nil
}

type (
	// Intercept Request/Response from workflowservice
	RequestResponseInterceptor interface {
		GetHeaderForWorkflowExecution(string) *commonpb.Header
		SetHeaderForWorkflowExecution(string, *commonpb.Header)

		RegisterNamespaceRequest(req *workflowservice.RegisterNamespaceRequest) error
		RegisterNamespaceResponse(req *workflowservice.RegisterNamespaceResponse) error
		ListNamespacesRequest(req *workflowservice.ListNamespacesRequest) error
		ListNamespacesResponse(req *workflowservice.ListNamespacesResponse) error
		DescribeNamespaceRequest(req *workflowservice.DescribeNamespaceRequest) error
		DescribeNamespaceResponse(req *workflowservice.DescribeNamespaceResponse) error
		UpdateNamespaceRequest(req *workflowservice.UpdateNamespaceRequest) error
		UpdateNamespaceResponse(req *workflowservice.UpdateNamespaceResponse) error
		DeprecateNamespaceRequest(req *workflowservice.DeprecateNamespaceRequest) error
		DeprecateNamespaceResponse(req *workflowservice.DeprecateNamespaceResponse) error
		StartWorkflowExecutionRequest(req *workflowservice.StartWorkflowExecutionRequest) error
		StartWorkflowExecutionResponse(req *workflowservice.StartWorkflowExecutionResponse) error
		GetWorkflowExecutionHistoryRequest(req *workflowservice.GetWorkflowExecutionHistoryRequest) error
		GetWorkflowExecutionHistoryResponse(req *workflowservice.GetWorkflowExecutionHistoryResponse) error
		PollWorkflowTaskQueueRequest(req *workflowservice.PollWorkflowTaskQueueRequest) error
		PollWorkflowTaskQueueResponse(req *workflowservice.PollWorkflowTaskQueueResponse) error
		RespondWorkflowTaskCompletedRequest(req *workflowservice.RespondWorkflowTaskCompletedRequest) error
		RespondWorkflowTaskCompletedResponse(req *workflowservice.RespondWorkflowTaskCompletedResponse) error
		RespondWorkflowTaskFailedRequest(req *workflowservice.RespondWorkflowTaskFailedRequest) error
		RespondWorkflowTaskFailedResponse(req *workflowservice.RespondWorkflowTaskFailedResponse) error
		PollActivityTaskQueueRequest(req *workflowservice.PollActivityTaskQueueRequest) error
		PollActivityTaskQueueResponse(req *workflowservice.PollActivityTaskQueueResponse) error
		RecordActivityTaskHeartbeatRequest(req *workflowservice.RecordActivityTaskHeartbeatRequest) error
		RecordActivityTaskHeartbeatResponse(req *workflowservice.RecordActivityTaskHeartbeatResponse) error
		RecordActivityTaskHeartbeatByIdRequest(req *workflowservice.RecordActivityTaskHeartbeatByIdRequest) error
		RecordActivityTaskHeartbeatByIdResponse(req *workflowservice.RecordActivityTaskHeartbeatByIdResponse) error
		RespondActivityTaskCompletedRequest(req *workflowservice.RespondActivityTaskCompletedRequest) error
		RespondActivityTaskCompletedResponse(req *workflowservice.RespondActivityTaskCompletedResponse) error
		RespondActivityTaskCompletedByIdRequest(req *workflowservice.RespondActivityTaskCompletedByIdRequest) error
		RespondActivityTaskCompletedByIdResponse(req *workflowservice.RespondActivityTaskCompletedByIdResponse) error
		RespondActivityTaskFailedRequest(req *workflowservice.RespondActivityTaskFailedRequest) error
		RespondActivityTaskFailedResponse(req *workflowservice.RespondActivityTaskFailedResponse) error
		RespondActivityTaskFailedByIdRequest(req *workflowservice.RespondActivityTaskFailedByIdRequest) error
		RespondActivityTaskFailedByIdResponse(req *workflowservice.RespondActivityTaskFailedByIdResponse) error
		RespondActivityTaskCanceledRequest(req *workflowservice.RespondActivityTaskCanceledRequest) error
		RespondActivityTaskCanceledResponse(req *workflowservice.RespondActivityTaskCanceledResponse) error
		RespondActivityTaskCanceledByIdRequest(req *workflowservice.RespondActivityTaskCanceledByIdRequest) error
		RespondActivityTaskCanceledByIdResponse(req *workflowservice.RespondActivityTaskCanceledByIdResponse) error
		RequestCancelWorkflowExecutionRequest(req *workflowservice.RequestCancelWorkflowExecutionRequest) error
		RequestCancelWorkflowExecutionResponse(req *workflowservice.RequestCancelWorkflowExecutionResponse) error
		SignalWorkflowExecutionRequest(req *workflowservice.SignalWorkflowExecutionRequest) error
		SignalWorkflowExecutionResponse(req *workflowservice.SignalWorkflowExecutionResponse) error
		SignalWithStartWorkflowExecutionRequest(req *workflowservice.SignalWithStartWorkflowExecutionRequest) error
		SignalWithStartWorkflowExecutionResponse(req *workflowservice.SignalWithStartWorkflowExecutionResponse) error
		ResetWorkflowExecutionRequest(req *workflowservice.ResetWorkflowExecutionRequest) error
		ResetWorkflowExecutionResponse(req *workflowservice.ResetWorkflowExecutionResponse) error
		TerminateWorkflowExecutionRequest(req *workflowservice.TerminateWorkflowExecutionRequest) error
		TerminateWorkflowExecutionResponse(req *workflowservice.TerminateWorkflowExecutionResponse) error
		ListOpenWorkflowExecutionsRequest(req *workflowservice.ListOpenWorkflowExecutionsRequest) error
		ListOpenWorkflowExecutionsResponse(req *workflowservice.ListOpenWorkflowExecutionsResponse) error
		ListClosedWorkflowExecutionsRequest(req *workflowservice.ListClosedWorkflowExecutionsRequest) error
		ListClosedWorkflowExecutionsResponse(req *workflowservice.ListClosedWorkflowExecutionsResponse) error
		ListWorkflowExecutionsRequest(req *workflowservice.ListWorkflowExecutionsRequest) error
		ListWorkflowExecutionsResponse(req *workflowservice.ListWorkflowExecutionsResponse) error
		ListArchivedWorkflowExecutionsRequest(req *workflowservice.ListArchivedWorkflowExecutionsRequest) error
		ListArchivedWorkflowExecutionsResponse(req *workflowservice.ListArchivedWorkflowExecutionsResponse) error
		ScanWorkflowExecutionsRequest(req *workflowservice.ScanWorkflowExecutionsRequest) error
		ScanWorkflowExecutionsResponse(req *workflowservice.ScanWorkflowExecutionsResponse) error
		CountWorkflowExecutionsRequest(req *workflowservice.CountWorkflowExecutionsRequest) error
		CountWorkflowExecutionsResponse(req *workflowservice.CountWorkflowExecutionsResponse) error
		GetSearchAttributesRequest(req *workflowservice.GetSearchAttributesRequest) error
		GetSearchAttributesResponse(req *workflowservice.GetSearchAttributesResponse) error
		RespondQueryTaskCompletedRequest(req *workflowservice.RespondQueryTaskCompletedRequest) error
		RespondQueryTaskCompletedResponse(req *workflowservice.RespondQueryTaskCompletedResponse) error
		ResetStickyTaskQueueRequest(req *workflowservice.ResetStickyTaskQueueRequest) error
		ResetStickyTaskQueueResponse(req *workflowservice.ResetStickyTaskQueueResponse) error
		QueryWorkflowRequest(req *workflowservice.QueryWorkflowRequest) error
		QueryWorkflowResponse(req *workflowservice.QueryWorkflowResponse) error
		DescribeWorkflowExecutionRequest(req *workflowservice.DescribeWorkflowExecutionRequest) error
		DescribeWorkflowExecutionResponse(req *workflowservice.DescribeWorkflowExecutionResponse) error
		DescribeTaskQueueRequest(req *workflowservice.DescribeTaskQueueRequest) error
		DescribeTaskQueueResponse(req *workflowservice.DescribeTaskQueueResponse) error
		GetClusterInfoRequest(req *workflowservice.GetClusterInfoRequest) error
		GetClusterInfoResponse(req *workflowservice.GetClusterInfoResponse) error
		ListTaskQueuePartitionsRequest(req *workflowservice.ListTaskQueuePartitionsRequest) error
		ListTaskQueuePartitionsResponse(req *workflowservice.ListTaskQueuePartitionsResponse) error
	}

	// Intercept commands contained within workflowservice requests/responses
	CommandInterceptor interface {
		ScheduleActivityTask(attrs *command.ScheduleActivityTaskCommandAttributes) error
		RequestCancelActivityTask(attrs *command.RequestCancelActivityTaskCommandAttributes) error
		StartTimer(attrs *command.StartTimerCommandAttributes) error
		CompleteWorkflowExecution(attrs *command.CompleteWorkflowExecutionCommandAttributes) error
		FailWorkflowExecution(attrs *command.FailWorkflowExecutionCommandAttributes) error
		CancelTimer(attrs *command.CancelTimerCommandAttributes) error
		CancelWorkflowExecution(attrs *command.CancelWorkflowExecutionCommandAttributes) error
		RequestCancelExternalWorkflowExecution(attrs *command.RequestCancelExternalWorkflowExecutionCommandAttributes) error
		SignalExternalWorkflowExecution(attrs *command.SignalExternalWorkflowExecutionCommandAttributes) error
		UpsertWorkflowSearchAttributes(attrs *command.UpsertWorkflowSearchAttributesCommandAttributes) error
		RecordMarker(attrs *command.RecordMarkerCommandAttributes) error
		ContinueAsNewWorkflowExecution(attrs *command.ContinueAsNewWorkflowExecutionCommandAttributes) error
		StartChildWorkflowExecution(attrs *command.StartChildWorkflowExecutionCommandAttributes) error
	}

	// Intercept history events contained within workflow service requests/responses
	EventInterceptor interface {
		WorkflowExecutionStarted(attrs *historypb.WorkflowExecutionStartedEventAttributes) error
		WorkflowExecutionCompleted(attrs *historypb.WorkflowExecutionCompletedEventAttributes) error
		WorkflowExecutionFailed(attrs *historypb.WorkflowExecutionFailedEventAttributes) error
		WorkflowExecutionTimedOut(attrs *historypb.WorkflowExecutionTimedOutEventAttributes) error
		WorkflowExecutionContinuedAsNew(attrs *historypb.WorkflowExecutionContinuedAsNewEventAttributes) error
		WorkflowTaskScheduled(attrs *historypb.WorkflowTaskScheduledEventAttributes) error
		WorkflowTaskStarted(attrs *historypb.WorkflowTaskStartedEventAttributes) error
		WorkflowTaskCompleted(attrs *historypb.WorkflowTaskCompletedEventAttributes) error
		WorkflowTaskTimedOut(attrs *historypb.WorkflowTaskTimedOutEventAttributes) error
		WorkflowTaskFailed(attrs *historypb.WorkflowTaskFailedEventAttributes) error
		ActivityTaskScheduled(attrs *historypb.ActivityTaskScheduledEventAttributes) error
		ActivityTaskStarted(attrs *historypb.ActivityTaskStartedEventAttributes) error
		ActivityTaskCompleted(attrs *historypb.ActivityTaskCompletedEventAttributes) error
		ActivityTaskFailed(attrs *historypb.ActivityTaskFailedEventAttributes) error
		ActivityTaskTimedOut(attrs *historypb.ActivityTaskTimedOutEventAttributes) error
		ActivityTaskCancelRequested(attrs *historypb.ActivityTaskCancelRequestedEventAttributes) error
		ActivityTaskCanceled(attrs *historypb.ActivityTaskCanceledEventAttributes) error
		TimerStarted(attrs *historypb.TimerStartedEventAttributes) error
		TimerFired(attrs *historypb.TimerFiredEventAttributes) error
		TimerCanceled(attrs *historypb.TimerCanceledEventAttributes) error
		WorkflowExecutionCancelRequested(attrs *historypb.WorkflowExecutionCancelRequestedEventAttributes) error
		WorkflowExecutionCanceled(attrs *historypb.WorkflowExecutionCanceledEventAttributes) error
		MarkerRecorded(attrs *historypb.MarkerRecordedEventAttributes) error
		WorkflowExecutionSignaled(attrs *historypb.WorkflowExecutionSignaledEventAttributes) error
		WorkflowExecutionTerminated(attrs *historypb.WorkflowExecutionTerminatedEventAttributes) error
		RequestCancelExternalWorkflowExecutionInitiated(attrs *historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes) error
		RequestCancelExternalWorkflowExecutionFailed(attrs *historypb.RequestCancelExternalWorkflowExecutionFailedEventAttributes) error
		ExternalWorkflowExecutionCancelRequested(attrs *historypb.ExternalWorkflowExecutionCancelRequestedEventAttributes) error
		SignalExternalWorkflowExecutionInitiated(attrs *historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes) error
		SignalExternalWorkflowExecutionFailed(attrs *historypb.SignalExternalWorkflowExecutionFailedEventAttributes) error
		ExternalWorkflowExecutionSignaled(attrs *historypb.ExternalWorkflowExecutionSignaledEventAttributes) error
		UpsertWorkflowSearchAttributes(attrs *historypb.UpsertWorkflowSearchAttributesEventAttributes) error
		StartChildWorkflowExecutionInitiated(attrs *historypb.StartChildWorkflowExecutionInitiatedEventAttributes) error
		StartChildWorkflowExecutionFailed(attrs *historypb.StartChildWorkflowExecutionFailedEventAttributes) error
		ChildWorkflowExecutionStarted(attrs *historypb.ChildWorkflowExecutionStartedEventAttributes) error
		ChildWorkflowExecutionCompleted(attrs *historypb.ChildWorkflowExecutionCompletedEventAttributes) error
		ChildWorkflowExecutionFailed(attrs *historypb.ChildWorkflowExecutionFailedEventAttributes) error
		ChildWorkflowExecutionCanceled(attrs *historypb.ChildWorkflowExecutionCanceledEventAttributes) error
		ChildWorkflowExecutionTimedOut(attrs *historypb.ChildWorkflowExecutionTimedOutEventAttributes) error
		ChildWorkflowExecutionTerminated(attrs *historypb.ChildWorkflowExecutionTerminatedEventAttributes) error
	}

	ServiceInterceptor struct {
		RequestResponse RequestResponseInterceptor
		Command         CommandInterceptor
		Event           EventInterceptor
	}
)

var HeadersCache = map[string]*commonpb.Header{}

type BaseRequestResponseInterceptor struct{}

var _ RequestResponseInterceptor = &BaseRequestResponseInterceptor{}

func (i *BaseRequestResponseInterceptor) SetHeaderForWorkflowExecution(token string, header *commonpb.Header) {
	HeadersCache[token] = header
}

func (i *BaseRequestResponseInterceptor) GetHeaderForWorkflowExecution(token string) *commonpb.Header {
	return HeadersCache[token]
}

func (*BaseRequestResponseInterceptor) RegisterNamespaceRequest(req *workflowservice.RegisterNamespaceRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RegisterNamespaceResponse(req *workflowservice.RegisterNamespaceResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListNamespacesRequest(req *workflowservice.ListNamespacesRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListNamespacesResponse(req *workflowservice.ListNamespacesResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DescribeNamespaceRequest(req *workflowservice.DescribeNamespaceRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DescribeNamespaceResponse(req *workflowservice.DescribeNamespaceResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) UpdateNamespaceRequest(req *workflowservice.UpdateNamespaceRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) UpdateNamespaceResponse(req *workflowservice.UpdateNamespaceResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DeprecateNamespaceRequest(req *workflowservice.DeprecateNamespaceRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DeprecateNamespaceResponse(req *workflowservice.DeprecateNamespaceResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) StartWorkflowExecutionRequest(req *workflowservice.StartWorkflowExecutionRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) StartWorkflowExecutionResponse(req *workflowservice.StartWorkflowExecutionResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) GetWorkflowExecutionHistoryRequest(req *workflowservice.GetWorkflowExecutionHistoryRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) GetWorkflowExecutionHistoryResponse(req *workflowservice.GetWorkflowExecutionHistoryResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) PollWorkflowTaskQueueRequest(req *workflowservice.PollWorkflowTaskQueueRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) PollWorkflowTaskQueueResponse(req *workflowservice.PollWorkflowTaskQueueResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondWorkflowTaskCompletedRequest(req *workflowservice.RespondWorkflowTaskCompletedRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondWorkflowTaskCompletedResponse(req *workflowservice.RespondWorkflowTaskCompletedResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondWorkflowTaskFailedRequest(req *workflowservice.RespondWorkflowTaskFailedRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondWorkflowTaskFailedResponse(req *workflowservice.RespondWorkflowTaskFailedResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) PollActivityTaskQueueRequest(req *workflowservice.PollActivityTaskQueueRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) PollActivityTaskQueueResponse(req *workflowservice.PollActivityTaskQueueResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RecordActivityTaskHeartbeatRequest(req *workflowservice.RecordActivityTaskHeartbeatRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RecordActivityTaskHeartbeatResponse(req *workflowservice.RecordActivityTaskHeartbeatResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RecordActivityTaskHeartbeatByIdRequest(req *workflowservice.RecordActivityTaskHeartbeatByIdRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RecordActivityTaskHeartbeatByIdResponse(req *workflowservice.RecordActivityTaskHeartbeatByIdResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCompletedRequest(req *workflowservice.RespondActivityTaskCompletedRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCompletedResponse(req *workflowservice.RespondActivityTaskCompletedResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCompletedByIdRequest(req *workflowservice.RespondActivityTaskCompletedByIdRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCompletedByIdResponse(req *workflowservice.RespondActivityTaskCompletedByIdResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskFailedRequest(req *workflowservice.RespondActivityTaskFailedRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskFailedResponse(req *workflowservice.RespondActivityTaskFailedResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskFailedByIdRequest(req *workflowservice.RespondActivityTaskFailedByIdRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskFailedByIdResponse(req *workflowservice.RespondActivityTaskFailedByIdResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCanceledRequest(req *workflowservice.RespondActivityTaskCanceledRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCanceledResponse(req *workflowservice.RespondActivityTaskCanceledResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCanceledByIdRequest(req *workflowservice.RespondActivityTaskCanceledByIdRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondActivityTaskCanceledByIdResponse(req *workflowservice.RespondActivityTaskCanceledByIdResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RequestCancelWorkflowExecutionRequest(req *workflowservice.RequestCancelWorkflowExecutionRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RequestCancelWorkflowExecutionResponse(req *workflowservice.RequestCancelWorkflowExecutionResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) SignalWorkflowExecutionRequest(req *workflowservice.SignalWorkflowExecutionRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) SignalWorkflowExecutionResponse(req *workflowservice.SignalWorkflowExecutionResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) SignalWithStartWorkflowExecutionRequest(req *workflowservice.SignalWithStartWorkflowExecutionRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) SignalWithStartWorkflowExecutionResponse(req *workflowservice.SignalWithStartWorkflowExecutionResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ResetWorkflowExecutionRequest(req *workflowservice.ResetWorkflowExecutionRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ResetWorkflowExecutionResponse(req *workflowservice.ResetWorkflowExecutionResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) TerminateWorkflowExecutionRequest(req *workflowservice.TerminateWorkflowExecutionRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) TerminateWorkflowExecutionResponse(req *workflowservice.TerminateWorkflowExecutionResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListOpenWorkflowExecutionsRequest(req *workflowservice.ListOpenWorkflowExecutionsRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListOpenWorkflowExecutionsResponse(req *workflowservice.ListOpenWorkflowExecutionsResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListClosedWorkflowExecutionsRequest(req *workflowservice.ListClosedWorkflowExecutionsRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListClosedWorkflowExecutionsResponse(req *workflowservice.ListClosedWorkflowExecutionsResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListWorkflowExecutionsRequest(req *workflowservice.ListWorkflowExecutionsRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListWorkflowExecutionsResponse(req *workflowservice.ListWorkflowExecutionsResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListArchivedWorkflowExecutionsRequest(req *workflowservice.ListArchivedWorkflowExecutionsRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListArchivedWorkflowExecutionsResponse(req *workflowservice.ListArchivedWorkflowExecutionsResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ScanWorkflowExecutionsRequest(req *workflowservice.ScanWorkflowExecutionsRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ScanWorkflowExecutionsResponse(req *workflowservice.ScanWorkflowExecutionsResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) CountWorkflowExecutionsRequest(req *workflowservice.CountWorkflowExecutionsRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) CountWorkflowExecutionsResponse(req *workflowservice.CountWorkflowExecutionsResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) GetSearchAttributesRequest(req *workflowservice.GetSearchAttributesRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) GetSearchAttributesResponse(req *workflowservice.GetSearchAttributesResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondQueryTaskCompletedRequest(req *workflowservice.RespondQueryTaskCompletedRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) RespondQueryTaskCompletedResponse(req *workflowservice.RespondQueryTaskCompletedResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ResetStickyTaskQueueRequest(req *workflowservice.ResetStickyTaskQueueRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ResetStickyTaskQueueResponse(req *workflowservice.ResetStickyTaskQueueResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) QueryWorkflowRequest(req *workflowservice.QueryWorkflowRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) QueryWorkflowResponse(req *workflowservice.QueryWorkflowResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DescribeWorkflowExecutionRequest(req *workflowservice.DescribeWorkflowExecutionRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DescribeWorkflowExecutionResponse(req *workflowservice.DescribeWorkflowExecutionResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DescribeTaskQueueRequest(req *workflowservice.DescribeTaskQueueRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) DescribeTaskQueueResponse(req *workflowservice.DescribeTaskQueueResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) GetClusterInfoRequest(req *workflowservice.GetClusterInfoRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) GetClusterInfoResponse(req *workflowservice.GetClusterInfoResponse) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListTaskQueuePartitionsRequest(req *workflowservice.ListTaskQueuePartitionsRequest) error {
	return nil
}

func (*BaseRequestResponseInterceptor) ListTaskQueuePartitionsResponse(req *workflowservice.ListTaskQueuePartitionsResponse) error {
	return nil
}

type BaseCommandInterceptor struct{}

var _ CommandInterceptor = &BaseCommandInterceptor{}

func (*BaseCommandInterceptor) ScheduleActivityTask(attrs *command.ScheduleActivityTaskCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) RequestCancelActivityTask(attrs *command.RequestCancelActivityTaskCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) StartTimer(attrs *command.StartTimerCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) CompleteWorkflowExecution(attrs *command.CompleteWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) FailWorkflowExecution(attrs *command.FailWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) CancelTimer(attrs *command.CancelTimerCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) CancelWorkflowExecution(attrs *command.CancelWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) RequestCancelExternalWorkflowExecution(attrs *command.RequestCancelExternalWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) SignalExternalWorkflowExecution(attrs *command.SignalExternalWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) UpsertWorkflowSearchAttributes(attrs *command.UpsertWorkflowSearchAttributesCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) RecordMarker(attrs *command.RecordMarkerCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) ContinueAsNewWorkflowExecution(attrs *command.ContinueAsNewWorkflowExecutionCommandAttributes) error {
	return nil
}

func (*BaseCommandInterceptor) StartChildWorkflowExecution(attrs *command.StartChildWorkflowExecutionCommandAttributes) error {
	return nil
}

type BaseEventInterceptor struct{}

var _ EventInterceptor = &BaseEventInterceptor{}

func (*BaseEventInterceptor) WorkflowExecutionStarted(attrs *historypb.WorkflowExecutionStartedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionCompleted(attrs *historypb.WorkflowExecutionCompletedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionFailed(attrs *historypb.WorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionTimedOut(attrs *historypb.WorkflowExecutionTimedOutEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionContinuedAsNew(attrs *historypb.WorkflowExecutionContinuedAsNewEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowTaskScheduled(attrs *historypb.WorkflowTaskScheduledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowTaskStarted(attrs *historypb.WorkflowTaskStartedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowTaskCompleted(attrs *historypb.WorkflowTaskCompletedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowTaskTimedOut(attrs *historypb.WorkflowTaskTimedOutEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowTaskFailed(attrs *historypb.WorkflowTaskFailedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ActivityTaskScheduled(attrs *historypb.ActivityTaskScheduledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ActivityTaskStarted(attrs *historypb.ActivityTaskStartedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ActivityTaskCompleted(attrs *historypb.ActivityTaskCompletedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ActivityTaskFailed(attrs *historypb.ActivityTaskFailedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ActivityTaskTimedOut(attrs *historypb.ActivityTaskTimedOutEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ActivityTaskCancelRequested(attrs *historypb.ActivityTaskCancelRequestedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ActivityTaskCanceled(attrs *historypb.ActivityTaskCanceledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) TimerStarted(attrs *historypb.TimerStartedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) TimerFired(attrs *historypb.TimerFiredEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) TimerCanceled(attrs *historypb.TimerCanceledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionCancelRequested(attrs *historypb.WorkflowExecutionCancelRequestedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionCanceled(attrs *historypb.WorkflowExecutionCanceledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) MarkerRecorded(attrs *historypb.MarkerRecordedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionSignaled(attrs *historypb.WorkflowExecutionSignaledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) WorkflowExecutionTerminated(attrs *historypb.WorkflowExecutionTerminatedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) RequestCancelExternalWorkflowExecutionInitiated(attrs *historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) RequestCancelExternalWorkflowExecutionFailed(attrs *historypb.RequestCancelExternalWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ExternalWorkflowExecutionCancelRequested(attrs *historypb.ExternalWorkflowExecutionCancelRequestedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) SignalExternalWorkflowExecutionInitiated(attrs *historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) SignalExternalWorkflowExecutionFailed(attrs *historypb.SignalExternalWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ExternalWorkflowExecutionSignaled(attrs *historypb.ExternalWorkflowExecutionSignaledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) UpsertWorkflowSearchAttributes(attrs *historypb.UpsertWorkflowSearchAttributesEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) StartChildWorkflowExecutionInitiated(attrs *historypb.StartChildWorkflowExecutionInitiatedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) StartChildWorkflowExecutionFailed(attrs *historypb.StartChildWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ChildWorkflowExecutionStarted(attrs *historypb.ChildWorkflowExecutionStartedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ChildWorkflowExecutionCompleted(attrs *historypb.ChildWorkflowExecutionCompletedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ChildWorkflowExecutionFailed(attrs *historypb.ChildWorkflowExecutionFailedEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ChildWorkflowExecutionCanceled(attrs *historypb.ChildWorkflowExecutionCanceledEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ChildWorkflowExecutionTimedOut(attrs *historypb.ChildWorkflowExecutionTimedOutEventAttributes) error {
	return nil
}

func (*BaseEventInterceptor) ChildWorkflowExecutionTerminated(attrs *historypb.ChildWorkflowExecutionTerminatedEventAttributes) error {
	return nil
}

func processCommands(commandInterceptor CommandInterceptor, commands []*command.Command) error {
	var err error

	for _, c := range commands {
		switch c.CommandType {
		case enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK:
			err = commandInterceptor.ScheduleActivityTask(c.GetScheduleActivityTaskCommandAttributes())
		case enumspb.COMMAND_TYPE_REQUEST_CANCEL_ACTIVITY_TASK:
			err = commandInterceptor.RequestCancelActivityTask(c.GetRequestCancelActivityTaskCommandAttributes())
		case enumspb.COMMAND_TYPE_START_TIMER:
			err = commandInterceptor.StartTimer(c.GetStartTimerCommandAttributes())
		case enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION:
			err = commandInterceptor.CompleteWorkflowExecution(c.GetCompleteWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_FAIL_WORKFLOW_EXECUTION:
			err = commandInterceptor.FailWorkflowExecution(c.GetFailWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_CANCEL_TIMER:
			err = commandInterceptor.CancelTimer(c.GetCancelTimerCommandAttributes())
		case enumspb.COMMAND_TYPE_CANCEL_WORKFLOW_EXECUTION:
			err = commandInterceptor.CancelWorkflowExecution(c.GetCancelWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION:
			err = commandInterceptor.RequestCancelExternalWorkflowExecution(c.GetRequestCancelExternalWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_RECORD_MARKER:
			err = commandInterceptor.RecordMarker(c.GetRecordMarkerCommandAttributes())
		case enumspb.COMMAND_TYPE_CONTINUE_AS_NEW_WORKFLOW_EXECUTION:
			err = commandInterceptor.ContinueAsNewWorkflowExecution(c.GetContinueAsNewWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_START_CHILD_WORKFLOW_EXECUTION:
			err = commandInterceptor.StartChildWorkflowExecution(c.GetStartChildWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION:
			err = commandInterceptor.SignalExternalWorkflowExecution(c.GetSignalExternalWorkflowExecutionCommandAttributes())
		case enumspb.COMMAND_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES:
			err = commandInterceptor.UpsertWorkflowSearchAttributes(c.GetUpsertWorkflowSearchAttributesCommandAttributes())
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func processEvents(eventInterceptor EventInterceptor, events []*historypb.HistoryEvent) error {
	var err error

	for _, e := range events {
		switch e.EventType {
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
			err = eventInterceptor.WorkflowExecutionStarted(e.GetWorkflowExecutionStartedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
			err = eventInterceptor.WorkflowExecutionCompleted(e.GetWorkflowExecutionCompletedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
			err = eventInterceptor.WorkflowExecutionFailed(e.GetWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
			err = eventInterceptor.WorkflowExecutionTimedOut(e.GetWorkflowExecutionTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED:
			err = eventInterceptor.WorkflowTaskScheduled(e.GetWorkflowTaskScheduledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED:
			err = eventInterceptor.WorkflowTaskStarted(e.GetWorkflowTaskStartedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			err = eventInterceptor.ActivityTaskScheduled(e.GetActivityTaskScheduledEventAttributes())
		case enumspb.EVENT_TYPE_TIMER_STARTED:
			err = eventInterceptor.TimerStarted(e.GetTimerStartedEventAttributes())
		case enumspb.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES:
			err = eventInterceptor.UpsertWorkflowSearchAttributes(e.GetUpsertWorkflowSearchAttributesEventAttributes())
		case enumspb.EVENT_TYPE_MARKER_RECORDED:
			err = eventInterceptor.MarkerRecorded(e.GetMarkerRecordedEventAttributes())
		case enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED:
			err = eventInterceptor.StartChildWorkflowExecutionInitiated(e.GetStartChildWorkflowExecutionInitiatedEventAttributes())
		case enumspb.EVENT_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED:
			err = eventInterceptor.RequestCancelExternalWorkflowExecutionInitiated(e.GetRequestCancelExternalWorkflowExecutionInitiatedEventAttributes())
		case enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED:
			err = eventInterceptor.SignalExternalWorkflowExecutionInitiated(e.GetSignalExternalWorkflowExecutionInitiatedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
			err = eventInterceptor.WorkflowExecutionCanceled(e.GetWorkflowExecutionCanceledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
			err = eventInterceptor.WorkflowExecutionContinuedAsNew(e.GetWorkflowExecutionContinuedAsNewEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED:
			err = eventInterceptor.WorkflowTaskCompleted(e.GetWorkflowTaskCompletedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_TIMED_OUT:
			err = eventInterceptor.WorkflowTaskTimedOut(e.GetWorkflowTaskTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED:
			err = eventInterceptor.WorkflowTaskFailed(e.GetWorkflowTaskFailedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED:
			err = eventInterceptor.ActivityTaskStarted(e.GetActivityTaskStartedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			err = eventInterceptor.ActivityTaskCompleted(e.GetActivityTaskCompletedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
			err = eventInterceptor.ActivityTaskFailed(e.GetActivityTaskFailedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			err = eventInterceptor.ActivityTaskTimedOut(e.GetActivityTaskTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED:
			err = eventInterceptor.ActivityTaskCancelRequested(e.GetActivityTaskCancelRequestedEventAttributes())
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED:
			err = eventInterceptor.ActivityTaskCanceled(e.GetActivityTaskCanceledEventAttributes())
		case enumspb.EVENT_TYPE_TIMER_FIRED:
			err = eventInterceptor.TimerFired(e.GetTimerFiredEventAttributes())
		case enumspb.EVENT_TYPE_TIMER_CANCELED:
			err = eventInterceptor.TimerCanceled(e.GetTimerCanceledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCEL_REQUESTED:
			err = eventInterceptor.WorkflowExecutionCancelRequested(e.GetWorkflowExecutionCancelRequestedEventAttributes())
		case enumspb.EVENT_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION_FAILED:
			err = eventInterceptor.RequestCancelExternalWorkflowExecutionFailed(e.GetRequestCancelExternalWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_EXTERNAL_WORKFLOW_EXECUTION_CANCEL_REQUESTED:
			err = eventInterceptor.ExternalWorkflowExecutionCancelRequested(e.GetExternalWorkflowExecutionCancelRequestedEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
			err = eventInterceptor.WorkflowExecutionSignaled(e.GetWorkflowExecutionSignaledEventAttributes())
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
			err = eventInterceptor.WorkflowExecutionTerminated(e.GetWorkflowExecutionTerminatedEventAttributes())
		case enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_FAILED:
			err = eventInterceptor.StartChildWorkflowExecutionFailed(e.GetStartChildWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_STARTED:
			err = eventInterceptor.ChildWorkflowExecutionStarted(e.GetChildWorkflowExecutionStartedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
			err = eventInterceptor.ChildWorkflowExecutionCompleted(e.GetChildWorkflowExecutionCompletedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_FAILED:
			err = eventInterceptor.ChildWorkflowExecutionFailed(e.GetChildWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_CANCELED:
			err = eventInterceptor.ChildWorkflowExecutionCanceled(e.GetChildWorkflowExecutionCanceledEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_TIMED_OUT:
			err = eventInterceptor.ChildWorkflowExecutionTimedOut(e.GetChildWorkflowExecutionTimedOutEventAttributes())
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_TERMINATED:
			err = eventInterceptor.ChildWorkflowExecutionTerminated(e.GetChildWorkflowExecutionTerminatedEventAttributes())
		case enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_FAILED:
			err = eventInterceptor.SignalExternalWorkflowExecutionFailed(e.GetSignalExternalWorkflowExecutionFailedEventAttributes())
		case enumspb.EVENT_TYPE_EXTERNAL_WORKFLOW_EXECUTION_SIGNALED:
			err = eventInterceptor.ExternalWorkflowExecutionSignaled(e.GetExternalWorkflowExecutionSignaledEventAttributes())
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func NewServiceInterceptor(serviceInterceptor ServiceInterceptor) grpc.UnaryClientInterceptor {
	requestResponseInterceptor := serviceInterceptor.RequestResponse
	if requestResponseInterceptor == nil {
		requestResponseInterceptor = &BaseRequestResponseInterceptor{}
	}
	commandInterceptor := serviceInterceptor.Command
	if commandInterceptor == nil {
		commandInterceptor = &BaseCommandInterceptor{}
	}
	eventInterceptor := serviceInterceptor.Event
	if eventInterceptor == nil {
		eventInterceptor = &BaseEventInterceptor{}
	}

	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var err error

		switch r := req.(type) {
		case *workflowservice.RegisterNamespaceRequest: // No context
			err = requestResponseInterceptor.RegisterNamespaceRequest(r)
		case *workflowservice.ListNamespacesRequest: // No context
			err = requestResponseInterceptor.ListNamespacesRequest(r)
		case *workflowservice.DescribeNamespaceRequest: // No context
			err = requestResponseInterceptor.DescribeNamespaceRequest(r)
		case *workflowservice.UpdateNamespaceRequest: // No context
			err = requestResponseInterceptor.UpdateNamespaceRequest(r)
		case *workflowservice.DeprecateNamespaceRequest: // No context
			err = requestResponseInterceptor.DeprecateNamespaceRequest(r)
		case *workflowservice.StartWorkflowExecutionRequest: // User set context
			err = requestResponseInterceptor.StartWorkflowExecutionRequest(r)
		case *workflowservice.GetWorkflowExecutionHistoryRequest: // No context
			err = requestResponseInterceptor.GetWorkflowExecutionHistoryRequest(r)
		case *workflowservice.PollWorkflowTaskQueueRequest: // No context
			err = requestResponseInterceptor.PollWorkflowTaskQueueRequest(r)
		case *workflowservice.RespondWorkflowTaskCompletedRequest: // WorkflowInfo
			err = requestResponseInterceptor.RespondWorkflowTaskCompletedRequest(r)
			if err != nil {
				return err
			}
			err = processCommands(commandInterceptor, r.Commands)
		case *workflowservice.RespondWorkflowTaskFailedRequest: // WorkflowInfo
			err = requestResponseInterceptor.RespondWorkflowTaskFailedRequest(r)
		case *workflowservice.PollActivityTaskQueueRequest: // No context
			err = requestResponseInterceptor.PollActivityTaskQueueRequest(r)
		case *workflowservice.RecordActivityTaskHeartbeatRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RecordActivityTaskHeartbeatRequest(r)
		case *workflowservice.RecordActivityTaskHeartbeatByIdRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RecordActivityTaskHeartbeatByIdRequest(r)
		case *workflowservice.RespondActivityTaskCompletedRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RespondActivityTaskCompletedRequest(r)
		case *workflowservice.RespondActivityTaskCompletedByIdRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RespondActivityTaskCompletedByIdRequest(r)
		case *workflowservice.RespondActivityTaskFailedRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RespondActivityTaskFailedRequest(r)
		case *workflowservice.RespondActivityTaskFailedByIdRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RespondActivityTaskFailedByIdRequest(r)
		case *workflowservice.RespondActivityTaskCanceledRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RespondActivityTaskCanceledRequest(r)
		case *workflowservice.RespondActivityTaskCanceledByIdRequest: // WorkflowInfo / ActivityEnvironment
			err = requestResponseInterceptor.RespondActivityTaskCanceledByIdRequest(r)
		case *workflowservice.RequestCancelWorkflowExecutionRequest: // No context
			err = requestResponseInterceptor.RequestCancelWorkflowExecutionRequest(r)
		case *workflowservice.SignalWorkflowExecutionRequest: // Caller's WorkflowInfo
			err = requestResponseInterceptor.SignalWorkflowExecutionRequest(r)
		case *workflowservice.SignalWithStartWorkflowExecutionRequest: // Caller's WorkflowInfo
			err = requestResponseInterceptor.SignalWithStartWorkflowExecutionRequest(r)
		case *workflowservice.ResetWorkflowExecutionRequest: // No context
			err = requestResponseInterceptor.ResetWorkflowExecutionRequest(r)
		case *workflowservice.TerminateWorkflowExecutionRequest: // No context
			err = requestResponseInterceptor.TerminateWorkflowExecutionRequest(r)
		case *workflowservice.ListOpenWorkflowExecutionsRequest: // No context
			err = requestResponseInterceptor.ListOpenWorkflowExecutionsRequest(r)
		case *workflowservice.ListClosedWorkflowExecutionsRequest: // No context
			err = requestResponseInterceptor.ListClosedWorkflowExecutionsRequest(r)
		case *workflowservice.ListWorkflowExecutionsRequest: // No context
			err = requestResponseInterceptor.ListWorkflowExecutionsRequest(r)
		case *workflowservice.ListArchivedWorkflowExecutionsRequest: // No context
			err = requestResponseInterceptor.ListArchivedWorkflowExecutionsRequest(r)
		case *workflowservice.ScanWorkflowExecutionsRequest: // No context
			err = requestResponseInterceptor.ScanWorkflowExecutionsRequest(r)
		case *workflowservice.CountWorkflowExecutionsRequest: // No context
			err = requestResponseInterceptor.CountWorkflowExecutionsRequest(r)
		case *workflowservice.GetSearchAttributesRequest: // No context
			err = requestResponseInterceptor.GetSearchAttributesRequest(r)
		case *workflowservice.RespondQueryTaskCompletedRequest: // WorkflowInfo
			err = requestResponseInterceptor.RespondQueryTaskCompletedRequest(r)
		case *workflowservice.ResetStickyTaskQueueRequest: // No context
			err = requestResponseInterceptor.ResetStickyTaskQueueRequest(r)
		case *workflowservice.QueryWorkflowRequest: // No context
			err = requestResponseInterceptor.QueryWorkflowRequest(r)
		case *workflowservice.DescribeWorkflowExecutionRequest: // No context
			err = requestResponseInterceptor.DescribeWorkflowExecutionRequest(r)
		case *workflowservice.DescribeTaskQueueRequest: // No context
			err = requestResponseInterceptor.DescribeTaskQueueRequest(r)
		case *workflowservice.GetClusterInfoRequest: // No context
			err = requestResponseInterceptor.GetClusterInfoRequest(r)
		case *workflowservice.ListTaskQueuePartitionsRequest: // No context
			err = requestResponseInterceptor.ListTaskQueuePartitionsRequest(r)
		}

		if err != nil {
			return err
		}

		err = invoker(ctx, method, req, reply, cc, opts...)

		if err != nil {
			return err
		}

		switch r := reply.(type) {
		case *workflowservice.RegisterNamespaceResponse:
			err = requestResponseInterceptor.RegisterNamespaceResponse(r)
		case *workflowservice.ListNamespacesResponse:
			err = requestResponseInterceptor.ListNamespacesResponse(r)
		case *workflowservice.DescribeNamespaceResponse:
			err = requestResponseInterceptor.DescribeNamespaceResponse(r)
		case *workflowservice.UpdateNamespaceResponse:
			err = requestResponseInterceptor.UpdateNamespaceResponse(r)
		case *workflowservice.DeprecateNamespaceResponse:
			err = requestResponseInterceptor.DeprecateNamespaceResponse(r)
		case *workflowservice.StartWorkflowExecutionResponse:
			err = requestResponseInterceptor.StartWorkflowExecutionResponse(r)
		case *workflowservice.GetWorkflowExecutionHistoryResponse:
			err = requestResponseInterceptor.GetWorkflowExecutionHistoryResponse(r)
			if err != nil {
				return err
			}

			err = processEvents(eventInterceptor, r.History.Events)
		case *workflowservice.PollWorkflowTaskQueueResponse:
			if r.WorkflowType != nil {
				err = requestResponseInterceptor.PollWorkflowTaskQueueResponse(r)
				if err != nil {
					return err
				}
				err = processEvents(eventInterceptor, r.History.Events)
			}
		case *workflowservice.RespondWorkflowTaskCompletedResponse:
			err = requestResponseInterceptor.RespondWorkflowTaskCompletedResponse(r)
		case *workflowservice.RespondWorkflowTaskFailedResponse:
			err = requestResponseInterceptor.RespondWorkflowTaskFailedResponse(r)
		case *workflowservice.PollActivityTaskQueueResponse:
			if r.ActivityType != nil {
				err = requestResponseInterceptor.PollActivityTaskQueueResponse(r)
			}
		case *workflowservice.RecordActivityTaskHeartbeatResponse:
			err = requestResponseInterceptor.RecordActivityTaskHeartbeatResponse(r)
		case *workflowservice.RecordActivityTaskHeartbeatByIdResponse:
			err = requestResponseInterceptor.RecordActivityTaskHeartbeatByIdResponse(r)
		case *workflowservice.RespondActivityTaskCompletedResponse:
			err = requestResponseInterceptor.RespondActivityTaskCompletedResponse(r)
		case *workflowservice.RespondActivityTaskCompletedByIdResponse:
			err = requestResponseInterceptor.RespondActivityTaskCompletedByIdResponse(r)
		case *workflowservice.RespondActivityTaskFailedResponse:
			err = requestResponseInterceptor.RespondActivityTaskFailedResponse(r)
		case *workflowservice.RespondActivityTaskFailedByIdResponse:
			err = requestResponseInterceptor.RespondActivityTaskFailedByIdResponse(r)
		case *workflowservice.RespondActivityTaskCanceledResponse:
			err = requestResponseInterceptor.RespondActivityTaskCanceledResponse(r)
		case *workflowservice.RespondActivityTaskCanceledByIdResponse:
			err = requestResponseInterceptor.RespondActivityTaskCanceledByIdResponse(r)
		case *workflowservice.RequestCancelWorkflowExecutionResponse:
			err = requestResponseInterceptor.RequestCancelWorkflowExecutionResponse(r)
		case *workflowservice.SignalWorkflowExecutionResponse:
			err = requestResponseInterceptor.SignalWorkflowExecutionResponse(r)
		case *workflowservice.SignalWithStartWorkflowExecutionResponse:
			err = requestResponseInterceptor.SignalWithStartWorkflowExecutionResponse(r)
		case *workflowservice.ResetWorkflowExecutionResponse:
			err = requestResponseInterceptor.ResetWorkflowExecutionResponse(r)
		case *workflowservice.TerminateWorkflowExecutionResponse:
			err = requestResponseInterceptor.TerminateWorkflowExecutionResponse(r)
		case *workflowservice.ListOpenWorkflowExecutionsResponse:
			err = requestResponseInterceptor.ListOpenWorkflowExecutionsResponse(r)
		case *workflowservice.ListClosedWorkflowExecutionsResponse:
			err = requestResponseInterceptor.ListClosedWorkflowExecutionsResponse(r)
		case *workflowservice.ListWorkflowExecutionsResponse:
			err = requestResponseInterceptor.ListWorkflowExecutionsResponse(r)
		case *workflowservice.ListArchivedWorkflowExecutionsResponse:
			err = requestResponseInterceptor.ListArchivedWorkflowExecutionsResponse(r)
		case *workflowservice.ScanWorkflowExecutionsResponse:
			err = requestResponseInterceptor.ScanWorkflowExecutionsResponse(r)
		case *workflowservice.CountWorkflowExecutionsResponse:
			err = requestResponseInterceptor.CountWorkflowExecutionsResponse(r)
		case *workflowservice.GetSearchAttributesResponse:
			err = requestResponseInterceptor.GetSearchAttributesResponse(r)
		case *workflowservice.RespondQueryTaskCompletedResponse:
			err = requestResponseInterceptor.RespondQueryTaskCompletedResponse(r)
		case *workflowservice.ResetStickyTaskQueueResponse:
			err = requestResponseInterceptor.ResetStickyTaskQueueResponse(r)
		case *workflowservice.QueryWorkflowResponse:
			err = requestResponseInterceptor.QueryWorkflowResponse(r)
		case *workflowservice.DescribeWorkflowExecutionResponse:
			err = requestResponseInterceptor.DescribeWorkflowExecutionResponse(r)
		case *workflowservice.DescribeTaskQueueResponse:
			err = requestResponseInterceptor.DescribeTaskQueueResponse(r)
		case *workflowservice.GetClusterInfoResponse:
			err = requestResponseInterceptor.GetClusterInfoResponse(r)
		case *workflowservice.ListTaskQueuePartitionsResponse:
			err = requestResponseInterceptor.ListTaskQueuePartitionsResponse(r)
		}

		return err
	}
}

type (
	payloadsEncoder func(*commonpb.Payloads) (*commonpb.Payloads, error)
	payloadsDecoder func(*commonpb.Payloads) (*commonpb.Payloads, error)

	InterceptorEncoder struct {
		Encode payloadsEncoder
		Decode payloadsDecoder
	}

	InputsResultsRequestResponseInterceptor struct {
		InterceptorEncoder
		BaseRequestResponseInterceptor
	}

	InputsResultsCommandInterceptor struct {
		InterceptorEncoder
		BaseCommandInterceptor
	}

	InputsResultsEventInterceptor struct {
		InterceptorEncoder
		BaseEventInterceptor
	}

	HeartbeatDetailsRequestResponseInterceptor struct {
		InterceptorEncoder
		BaseRequestResponseInterceptor
	}
)

func NewInputsResultsServiceInterceptor(encoder InterceptorEncoder) ServiceInterceptor {
	return ServiceInterceptor{
		RequestResponse: &InputsResultsRequestResponseInterceptor{InterceptorEncoder: encoder},
		Command:         &InputsResultsCommandInterceptor{InterceptorEncoder: encoder},
		Event:           &InputsResultsEventInterceptor{InterceptorEncoder: encoder},
	}
}

func NewHeartbeatDetailsServiceInterceptor(encoder InterceptorEncoder) ServiceInterceptor {
	return ServiceInterceptor{
		RequestResponse: &HeartbeatDetailsRequestResponseInterceptor{InterceptorEncoder: encoder},
	}
}

func encodePayloads(encode payloadsEncoder, payloadsPtr **commonpb.Payloads) error {
	encoded, err := encode(*payloadsPtr)
	if err != nil {
		return err
	}
	*payloadsPtr = encoded

	return nil
}

func decodePayloads(decode payloadsDecoder, payloadsPtr **commonpb.Payloads) error {
	decoded, err := decode(*payloadsPtr)
	if err != nil {
		return err
	}
	*payloadsPtr = decoded

	return nil
}

func (i *InputsResultsRequestResponseInterceptor) StartWorkflowExecutionRequest(req *workflowservice.StartWorkflowExecutionRequest) error {
	return encodePayloads(i.Encode, &req.Input)
}

func (i *InputsResultsRequestResponseInterceptor) SignalWorkflowExecutionRequest(req *workflowservice.SignalWorkflowExecutionRequest) error {
	return encodePayloads(i.Encode, &req.Input)
}

func (i *InputsResultsRequestResponseInterceptor) SignalWithStartWorkflowExecutionRequest(req *workflowservice.SignalWithStartWorkflowExecutionRequest) error {
	err := encodePayloads(i.Encode, &req.Input)
	if err != nil {
		return err
	}

	return encodePayloads(i.Encode, &req.SignalInput)
}

func (i *InputsResultsRequestResponseInterceptor) RespondActivityTaskCompletedRequest(req *workflowservice.RespondActivityTaskCompletedRequest) error {
	return encodePayloads(i.Encode, &req.Result)
}

func (i *InputsResultsRequestResponseInterceptor) RespondActivityTaskCompletedByIdRequest(req *workflowservice.RespondActivityTaskCompletedByIdRequest) error {
	return encodePayloads(i.Encode, &req.Result)
}

func (i *InputsResultsRequestResponseInterceptor) PollActivityTaskQueueResponse(res *workflowservice.PollActivityTaskQueueResponse) error {
	return decodePayloads(i.Decode, &res.Input)
}

func (i *InputsResultsCommandInterceptor) ScheduleActivityTask(attrs *command.ScheduleActivityTaskCommandAttributes) error {
	return encodePayloads(i.Encode, &attrs.Input)
}

func (i *InputsResultsCommandInterceptor) CompleteWorkflowExecution(attrs *command.CompleteWorkflowExecutionCommandAttributes) error {
	return encodePayloads(i.Encode, &attrs.Result)
}

func (i *InputsResultsCommandInterceptor) SignalExternalWorkflowExecution(attrs *command.SignalExternalWorkflowExecutionCommandAttributes) error {
	return encodePayloads(i.Encode, &attrs.Input)
}

func (i *InputsResultsCommandInterceptor) ContinueAsNewWorkflowExecution(attrs *command.ContinueAsNewWorkflowExecutionCommandAttributes) error {
	return encodePayloads(i.Encode, &attrs.Input)
}

func (i *InputsResultsCommandInterceptor) StartChildWorkflowExecution(attrs *command.StartChildWorkflowExecutionCommandAttributes) error {
	return encodePayloads(i.Encode, &attrs.Input)
}

func (i *InputsResultsEventInterceptor) WorkflowExecutionStarted(attrs *historypb.WorkflowExecutionStartedEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Input)
}

func (i *InputsResultsEventInterceptor) WorkflowExecutionContinuedAsNew(attrs *historypb.WorkflowExecutionContinuedAsNewEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Input)
}

func (i *InputsResultsEventInterceptor) ActivityTaskScheduled(attrs *historypb.ActivityTaskScheduledEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Input)
}

func (i *InputsResultsEventInterceptor) ActivityTaskCompleted(attrs *historypb.ActivityTaskCompletedEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Result)
}

func (i *InputsResultsEventInterceptor) WorkflowExecutionSignaled(attrs *historypb.WorkflowExecutionSignaledEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Input)
}

func (i *InputsResultsEventInterceptor) WorkflowExecutionCompleted(attrs *historypb.WorkflowExecutionCompletedEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Result)
}

func (i *InputsResultsEventInterceptor) SignalExternalWorkflowExecutionInitiated(attrs *historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Input)
}

func (i *InputsResultsEventInterceptor) StartChildWorkflowExecutionInitiated(attrs *historypb.StartChildWorkflowExecutionInitiatedEventAttributes) error {
	return decodePayloads(i.Decode, &attrs.Input)
}

func (i *HeartbeatDetailsRequestResponseInterceptor) RecordActivityTaskHeartbeatRequest(req *workflowservice.RecordActivityTaskHeartbeatRequest) error {
	return encodePayloads(i.Encode, &req.Details)
}

func (i *HeartbeatDetailsRequestResponseInterceptor) RecordActivityTaskHeartbeatByIdRequest(req *workflowservice.RecordActivityTaskHeartbeatByIdRequest) error {
	return encodePayloads(i.Encode, &req.Details)
}

func (i *HeartbeatDetailsRequestResponseInterceptor) PollActivityTaskQueueResponse(req *workflowservice.PollActivityTaskQueueResponse) error {
	if req.ActivityType == nil || req.HeartbeatDetails == nil {
		return nil
	}

	return decodePayloads(i.Decode, &req.HeartbeatDetails)
}
