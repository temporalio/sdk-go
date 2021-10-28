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

package interceptortest

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uber-go/tally/v4"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

type ProxyCall struct {
	Interface reflect.Type
	Next      reflect.Value
	Method    reflect.Method
	Args      []reflect.Value
}

func (p *ProxyCall) Call() []reflect.Value {
	// Put receiver before args
	args := append([]reflect.Value{p.Next}, p.Args...)
	// If call is variadic, have to use call slice
	if p.Method.Type.IsVariadic() {
		return p.Method.Func.CallSlice(args)
	}
	return p.Method.Func.Call(args)
}

type Invoker interface {
	// Unlike normal reflect.Method, the Func will have its receiver already set
	Invoke(*ProxyCall) []reflect.Value
}

type InvokerFunc func(*ProxyCall) []reflect.Value

var _ Invoker = (InvokerFunc)(nil)

func (i InvokerFunc) Invoke(p *ProxyCall) []reflect.Value { return i(p) }

type proxy struct {
	interceptor.InterceptorBase
	nextProxy
}

// Invoker may be called on multiple threads
func NewProxy(invoker Invoker) interceptor.Interceptor {
	return &proxy{nextProxy: nextProxy{invoker: invoker}}
}

type CallRecordingInvoker struct {
	calls     []*RecordedCall
	callsLock sync.RWMutex
}

func (c *CallRecordingInvoker) Calls() []*RecordedCall {
	c.callsLock.RLock()
	defer c.callsLock.RUnlock()
	ret := make([]*RecordedCall, len(c.calls))
	copy(ret, c.calls)
	return ret
}

func (c *CallRecordingInvoker) Invoke(p *ProxyCall) []reflect.Value {
	call := &RecordedCall{ProxyCall: p}
	c.callsLock.Lock()
	c.calls = append(c.calls, call)
	c.callsLock.Unlock()
	call.Results = call.Call()
	return call.Results
}

type RecordedCall struct {
	*ProxyCall
	// May be set later in non-thread-safe way if still running
	Results []reflect.Value
}

type nextProxy struct {
	iface   reflect.Type
	next    reflect.Value
	invoker Invoker
}

func (n *nextProxy) proxyWithNext(ifacePtr interface{}, next interface{}) *nextProxy {
	return &nextProxy{
		iface:   reflect.TypeOf(ifacePtr).Elem(),
		next:    reflect.ValueOf(next),
		invoker: n.invoker,
	}
}

func (n *nextProxy) invoke(args ...interface{}) []reflect.Value {
	// Grab caller function name
	pc, _, _, ok := runtime.Caller(1)
	if !ok {
		panic("failed getting caller info")
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		panic("failed getting caller func")
	}
	fnName := fn.Name()
	fnName = fnName[strings.LastIndex(fnName, ".")+1:]

	// Get method and args
	call := &ProxyCall{Interface: n.iface, Next: n.next, Args: make([]reflect.Value, len(args))}
	call.Method, ok = n.next.Type().MethodByName(fnName)
	if !ok {
		panic("failed getting method")
	}
	for i, arg := range args {
		call.Args[i] = reflect.ValueOf(arg)
		// If it's not valid, make a new instance of the type
		if !call.Args[i].IsValid() {
			call.Args[i] = reflect.New(call.Method.Func.Type().In(i + 1)).Elem()
		}
	}
	return n.invoker.Invoke(call)
}

func (p *proxy) InterceptActivity(
	ctx context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &proxyActivityInbound{nextProxy: p.proxyWithNext((*interceptor.ActivityInboundInterceptor)(nil), next)}
	i.Next = next
	return i
}

func (p *proxy) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	i := &proxyWorkflowInbound{nextProxy: p.proxyWithNext((*interceptor.WorkflowInboundInterceptor)(nil), next)}
	i.Next = next
	return i
}

func (p *proxy) InterceptClient(
	next interceptor.ClientOutboundInterceptor,
) interceptor.ClientOutboundInterceptor {
	i := &proxyClientOutbound{nextProxy: p.proxyWithNext((*interceptor.ClientOutboundInterceptor)(nil), next)}
	i.Next = next
	return i
}

type proxyActivityInbound struct {
	interceptor.ActivityInboundInterceptorBase
	*nextProxy
}

func (p *proxyActivityInbound) Init(outbound interceptor.ActivityOutboundInterceptor) (err error) {
	// Wrap outbound first
	i := &proxyActivityOutbound{nextProxy: p.proxyWithNext((*interceptor.ActivityOutboundInterceptor)(nil), outbound)}
	i.Next = outbound
	err, _ = p.invoke(i)[0].Interface().(error)
	return
}

func (p *proxyActivityInbound) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ExecuteActivityInput,
) (ret interface{}, err error) {
	vals := p.invoke(ctx, in)
	ret = vals[0].Interface()
	err, _ = vals[1].Interface().(error)
	return
}

type proxyActivityOutbound struct {
	interceptor.ActivityOutboundInterceptorBase
	*nextProxy
}

func (p *proxyActivityOutbound) GetInfo(ctx context.Context) (ret activity.Info) {
	ret, _ = p.invoke(ctx)[0].Interface().(activity.Info)
	return
}

func (p *proxyActivityOutbound) GetLogger(ctx context.Context) (ret log.Logger) {
	ret, _ = p.invoke(ctx)[0].Interface().(log.Logger)
	return
}

func (p *proxyActivityOutbound) GetMetricsScope(ctx context.Context) (ret tally.Scope) {
	ret, _ = p.invoke(ctx)[0].Interface().(tally.Scope)
	return
}

func (p *proxyActivityOutbound) RecordHeartbeat(ctx context.Context, details ...interface{}) {
	p.invoke(ctx, details)
}

func (p *proxyActivityOutbound) HasHeartbeatDetails(ctx context.Context) (ret bool) {
	ret, _ = p.invoke(ctx)[0].Interface().(bool)
	return
}

func (p *proxyActivityOutbound) GetHeartbeatDetails(ctx context.Context, d ...interface{}) (err error) {
	err, _ = p.invoke(ctx, d)[0].Interface().(error)
	return
}

func (p *proxyActivityOutbound) GetWorkerStopChannel(ctx context.Context) (ret <-chan struct{}) {
	ret, _ = p.invoke(ctx)[0].Interface().(<-chan struct{})
	return
}

type proxyWorkflowInbound struct {
	interceptor.WorkflowInboundInterceptorBase
	*nextProxy
}

func (p *proxyWorkflowInbound) Init(outbound interceptor.WorkflowOutboundInterceptor) (err error) {
	// Wrap outbound first
	i := &proxyWorkflowOutbound{nextProxy: p.proxyWithNext((*interceptor.WorkflowOutboundInterceptor)(nil), outbound)}
	i.Next = outbound
	err, _ = p.invoke(i)[0].Interface().(error)
	return
}

func (p *proxyWorkflowInbound) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (ret interface{}, err error) {
	vals := p.invoke(ctx, in)
	ret = vals[0].Interface()
	err, _ = vals[1].Interface().(error)
	return
}

func (p *proxyWorkflowInbound) HandleSignal(ctx workflow.Context, in *interceptor.HandleSignalInput) (err error) {
	err, _ = p.invoke(ctx, in)[0].Interface().(error)
	return
}

func (p *proxyWorkflowInbound) HandleQuery(
	ctx workflow.Context,
	in *interceptor.HandleQueryInput,
) (ret interface{}, err error) {
	vals := p.invoke(ctx, in)
	ret = vals[0].Interface()
	err, _ = vals[1].Interface().(error)
	return
}

type proxyWorkflowOutbound struct {
	interceptor.WorkflowOutboundInterceptorBase
	*nextProxy
}

func (p *proxyWorkflowOutbound) Go(
	ctx workflow.Context,
	name string,
	f func(ctx workflow.Context),
) (ret workflow.Context) {
	ret, _ = p.invoke(ctx, name, f)[0].Interface().(workflow.Context)
	return
}

func (p *proxyWorkflowOutbound) ExecuteActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) (ret workflow.Future) {
	ret, _ = p.invoke(ctx, activityType, args)[0].Interface().(workflow.Future)
	return
}

func (p *proxyWorkflowOutbound) ExecuteLocalActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) (ret workflow.Future) {
	ret, _ = p.invoke(ctx, activityType, args)[0].Interface().(workflow.Future)
	return
}

func (p *proxyWorkflowOutbound) ExecuteChildWorkflow(
	ctx workflow.Context,
	childWorkflowType string,
	args ...interface{},
) (ret workflow.ChildWorkflowFuture) {
	ret, _ = p.invoke(ctx, childWorkflowType, args)[0].Interface().(workflow.ChildWorkflowFuture)
	return
}

func (p *proxyWorkflowOutbound) GetInfo(ctx workflow.Context) (ret *workflow.Info) {
	ret, _ = p.invoke(ctx)[0].Interface().(*workflow.Info)
	return
}

func (p *proxyWorkflowOutbound) GetLogger(ctx workflow.Context) (ret log.Logger) {
	ret, _ = p.invoke(ctx)[0].Interface().(log.Logger)
	return
}

func (p *proxyWorkflowOutbound) GetMetricsScope(ctx workflow.Context) (ret tally.Scope) {
	ret, _ = p.invoke(ctx)[0].Interface().(tally.Scope)
	return
}

func (p *proxyWorkflowOutbound) Now(ctx workflow.Context) (ret time.Time) {
	ret, _ = p.invoke(ctx)[0].Interface().(time.Time)
	return
}

func (p *proxyWorkflowOutbound) NewTimer(ctx workflow.Context, d time.Duration) (ret workflow.Future) {
	ret, _ = p.invoke(ctx, d)[0].Interface().(workflow.Future)
	return
}

func (p *proxyWorkflowOutbound) Sleep(ctx workflow.Context, d time.Duration) (err error) {
	err, _ = p.invoke(ctx, d)[0].Interface().(error)
	return
}

func (p *proxyWorkflowOutbound) RequestCancelExternalWorkflow(
	ctx workflow.Context,
	workflowID string,
	runID string,
) (ret workflow.Future) {
	ret, _ = p.invoke(ctx, workflowID, runID)[0].Interface().(workflow.Future)
	return
}

func (p *proxyWorkflowOutbound) SignalExternalWorkflow(
	ctx workflow.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) (ret workflow.Future) {
	ret, _ = p.invoke(ctx, workflowID, runID, signalName, arg)[0].Interface().(workflow.Future)
	return
}

func (p *proxyWorkflowOutbound) UpsertSearchAttributes(
	ctx workflow.Context,
	attributes map[string]interface{},
) (err error) {
	err, _ = p.invoke(ctx, attributes)[0].Interface().(error)
	return
}

func (p *proxyWorkflowOutbound) GetSignalChannel(
	ctx workflow.Context,
	signalName string,
) (ret workflow.ReceiveChannel) {
	ret, _ = p.invoke(ctx, signalName)[0].Interface().(workflow.ReceiveChannel)
	return
}

func (p *proxyWorkflowOutbound) SideEffect(
	ctx workflow.Context,
	f func(ctx workflow.Context) interface{},
) (ret converter.EncodedValue) {
	ret, _ = p.invoke(ctx, f)[0].Interface().(converter.EncodedValue)
	return
}

func (p *proxyWorkflowOutbound) MutableSideEffect(
	ctx workflow.Context,
	id string,
	f func(ctx workflow.Context) interface{},
	equals func(a, b interface{}) bool,
) (ret converter.EncodedValue) {
	ret, _ = p.invoke(ctx, id, f, equals)[0].Interface().(converter.EncodedValue)
	return
}

func (p *proxyWorkflowOutbound) GetVersion(
	ctx workflow.Context,
	changeID string,
	minSupported workflow.Version,
	maxSupported workflow.Version,
) (ret workflow.Version) {
	ret, _ = p.invoke(ctx, changeID, minSupported, maxSupported)[0].Interface().(workflow.Version)
	return
}

func (p *proxyWorkflowOutbound) SetQueryHandler(
	ctx workflow.Context,
	queryType string,
	handler interface{},
) (err error) {
	err, _ = p.invoke(ctx, queryType, handler)[0].Interface().(error)
	return
}

func (p *proxyWorkflowOutbound) IsReplaying(ctx workflow.Context) (ret bool) {
	ret, _ = p.invoke(ctx)[0].Interface().(bool)
	return
}

func (p *proxyWorkflowOutbound) HasLastCompletionResult(ctx workflow.Context) (ret bool) {
	ret, _ = p.invoke(ctx)[0].Interface().(bool)
	return
}

func (p *proxyWorkflowOutbound) GetLastCompletionResult(ctx workflow.Context, d ...interface{}) (err error) {
	err, _ = p.invoke(ctx, d)[0].Interface().(error)
	return
}

func (p *proxyWorkflowOutbound) GetLastError(ctx workflow.Context) (err error) {
	err, _ = p.invoke(ctx)[0].Interface().(error)
	return
}

func (p *proxyWorkflowOutbound) NewContinueAsNewError(
	ctx workflow.Context,
	wfn interface{},
	args ...interface{},
) (err error) {
	err, _ = p.invoke(ctx, wfn, args)[0].Interface().(error)
	return
}

type proxyClientOutbound struct {
	interceptor.ClientOutboundInterceptorBase
	*nextProxy
}

func (p *proxyClientOutbound) ExecuteWorkflow(
	ctx context.Context,
	in *interceptor.ClientExecuteWorkflowInput,
) (ret client.WorkflowRun, err error) {
	vals := p.invoke(ctx, in)
	ret, _ = vals[0].Interface().(client.WorkflowRun)
	err, _ = vals[1].Interface().(error)
	return
}

func (p *proxyClientOutbound) SignalWorkflow(
	ctx context.Context,
	in *interceptor.ClientSignalWorkflowInput,
) (err error) {
	err, _ = p.invoke(ctx, in)[0].Interface().(error)
	return
}

func (p *proxyClientOutbound) SignalWithStartWorkflow(
	ctx context.Context,
	in *interceptor.ClientSignalWithStartWorkflowInput,
) (ret client.WorkflowRun, err error) {
	vals := p.invoke(ctx, in)
	ret, _ = vals[0].Interface().(client.WorkflowRun)
	err, _ = vals[1].Interface().(error)
	return
}

func (p *proxyClientOutbound) CancelWorkflow(
	ctx context.Context,
	in *interceptor.ClientCancelWorkflowInput,
) (err error) {
	err, _ = p.invoke(ctx, in)[0].Interface().(error)
	return
}

func (p *proxyClientOutbound) TerminateWorkflow(
	ctx context.Context,
	in *interceptor.ClientTerminateWorkflowInput,
) (err error) {
	err, _ = p.invoke(ctx, in)[0].Interface().(error)
	return
}

func (p *proxyClientOutbound) QueryWorkflow(
	ctx context.Context,
	in *interceptor.ClientQueryWorkflowInput,
) (ret converter.EncodedValue, err error) {
	vals := p.invoke(ctx, in)
	ret, _ = vals[0].Interface().(converter.EncodedValue)
	err, _ = vals[1].Interface().(error)
	return
}
