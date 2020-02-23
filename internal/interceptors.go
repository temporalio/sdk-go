// Copyright (c) 2020 Temporal Technologies, Inc.
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
	"github.com/uber-go/tally"
	"time"
)
import "go.uber.org/zap"

// WorkflowInterceptorFactory is used to create a single link in the interceptor chain
type WorkflowInterceptorFactory interface {
	// NewInterceptor creates an interceptor instance. The created instance must delegate every call to
	// the next parameter for workflow code function correctly.
	NewInterceptor(next WorkflowInterceptor) WorkflowInterceptor
}

// WorkflowInterceptor is an interface that can be implemented to intercept calls done by the workflow code.
type WorkflowInterceptor interface {
	//ExecuteWorkflow(ctx Context, args ...interface{}) []interface{}
	//GetWorkflowFunctionSignature() (argTypes []reflect.Type, resultTypes []reflect.Type)

	ExecuteActivity(ctx Context, activity interface{}, args ...interface{}) Future
	ExecuteLocalActivity(ctx Context, activity interface{}, args ...interface{}) Future
	ExecuteChildWorkflow(ctx Context, childWorkflow interface{}, args ...interface{}) ChildWorkflowFuture
	GetWorkflowInfo(ctx Context) *WorkflowInfo
	GetLogger(ctx Context) *zap.Logger
	GetMetricsScope(ctx Context) tally.Scope
	Now(ctx Context) time.Time
	NewTimer(ctx Context, d time.Duration) Future
	Sleep(ctx Context, d time.Duration) (err error)
	RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future
	SignalExternalWorkflow(ctx Context, workflowID, runID, signalName string, arg interface{}) Future
	UpsertSearchAttributes(ctx Context, attributes map[string]interface{}) error
	GetSignalChannel(ctx Context, signalName string) Channel
	SideEffect(ctx Context, f func(ctx Context) interface{}) Value
	MutableSideEffect(ctx Context, id string, f func(ctx Context) interface{}, equals func(a, b interface{}) bool) Value
	GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version
	SetQueryHandler(ctx Context, queryType string, handler interface{}) error
	IsReplaying(ctx Context) bool
	HasLastCompletionResult(ctx Context) bool
	GetLastCompletionResult(ctx Context, d ...interface{}) error
}

var _ WorkflowInterceptor = (*WorkflowInterceptorBase)(nil)

// WorkflowInterceptorBase is a helper type that can simplify creation of WorkflowInterceptors
type WorkflowInterceptorBase struct {
	Next WorkflowInterceptor
}

// ExecuteActivity forwards to t.Next
func (t *WorkflowInterceptorBase) ExecuteActivity(ctx Context, activity interface{}, args ...interface{}) Future {
	return t.Next.ExecuteActivity(ctx, activity, args...)
}

// ExecuteLocalActivity forwards to t.Next
func (t *WorkflowInterceptorBase) ExecuteLocalActivity(ctx Context, activity interface{}, args ...interface{}) Future {
	return t.Next.ExecuteLocalActivity(ctx, activity, args...)
}

// ExecuteChildWorkflow forwards to t.Next
func (t *WorkflowInterceptorBase) ExecuteChildWorkflow(ctx Context, childWorkflow interface{}, args ...interface{}) ChildWorkflowFuture {
	return t.Next.ExecuteChildWorkflow(ctx, childWorkflow, args...)
}

// GetWorkflowInfo forwards to t.Next
func (t *WorkflowInterceptorBase) GetWorkflowInfo(ctx Context) *WorkflowInfo {
	return t.Next.GetWorkflowInfo(ctx)
}

// GetLogger forwards to t.Next
func (t *WorkflowInterceptorBase) GetLogger(ctx Context) *zap.Logger {
	return t.Next.GetLogger(ctx)
}

// GetMetricsScope forwards to t.Next
func (t *WorkflowInterceptorBase) GetMetricsScope(ctx Context) tally.Scope {
	return t.Next.GetMetricsScope(ctx)
}

// Now forwards to t.Next
func (t *WorkflowInterceptorBase) Now(ctx Context) time.Time {
	return t.Next.Now(ctx)
}

// NewTimer forwards to t.Next
func (t *WorkflowInterceptorBase) NewTimer(ctx Context, d time.Duration) Future {
	return t.Next.NewTimer(ctx, d)
}

// Sleep forwards to t.Next
func (t *WorkflowInterceptorBase) Sleep(ctx Context, d time.Duration) (err error) {
	return t.Next.Sleep(ctx, d)
}

// RequestCancelExternalWorkflow forwards to t.Next
func (t *WorkflowInterceptorBase) RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future {
	return t.Next.RequestCancelExternalWorkflow(ctx, workflowID, runID)
}

// SignalExternalWorkflow forwards to t.Next
func (t *WorkflowInterceptorBase) SignalExternalWorkflow(ctx Context, workflowID, runID, signalName string, arg interface{}) Future {
	return t.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}

// UpsertSearchAttributes forwards to t.Next
func (t *WorkflowInterceptorBase) UpsertSearchAttributes(ctx Context, attributes map[string]interface{}) error {
	return t.Next.UpsertSearchAttributes(ctx, attributes)
}

// GetSignalChannel forwards to t.Next
func (t *WorkflowInterceptorBase) GetSignalChannel(ctx Context, signalName string) Channel {
	return t.Next.GetSignalChannel(ctx, signalName)
}

// SideEffect forwards to t.Next
func (t *WorkflowInterceptorBase) SideEffect(ctx Context, f func(ctx Context) interface{}) Value {
	return t.Next.SideEffect(ctx, f)
}

// MutableSideEffect forwards to t.Next
func (t *WorkflowInterceptorBase) MutableSideEffect(ctx Context, id string, f func(ctx Context) interface{}, equals func(a, b interface{}) bool) Value {
	return t.Next.MutableSideEffect(ctx, id, f, equals)
}

// GetVersion forwards to t.Next
func (t *WorkflowInterceptorBase) GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version {
	return t.Next.GetVersion(ctx, changeID, minSupported, maxSupported)
}

// SetQueryHandler forwards to t.Next
func (t *WorkflowInterceptorBase) SetQueryHandler(ctx Context, queryType string, handler interface{}) error {
	return t.Next.SetQueryHandler(ctx, queryType, handler)
}

// IsReplaying forwards to t.Next
func (t *WorkflowInterceptorBase) IsReplaying(ctx Context) bool {
	return t.Next.IsReplaying(ctx)
}

// HasLastCompletionResult forwards to t.Next
func (t *WorkflowInterceptorBase) HasLastCompletionResult(ctx Context) bool {
	return t.Next.HasLastCompletionResult(ctx)
}

// GetLastCompletionResult forwards to t.Next
func (t *WorkflowInterceptorBase) GetLastCompletionResult(ctx Context, d ...interface{}) error {
	return t.Next.GetLastCompletionResult(ctx, d...)
}
