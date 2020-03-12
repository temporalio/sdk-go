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

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/uber-go/tally"
	"google.golang.org/grpc"
)

type (
	scopeInterceptor struct {
		scope       tally.Scope
		childScopes map[string]tally.Scope
		mutex       sync.Mutex
	}
)

func NewScopeInterceptor(scope tally.Scope) grpc.UnaryClientInterceptor {
	scopeInterceptor := &scopeInterceptor{scope: scope, childScopes: make(map[string]tally.Scope)}
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		scope := scopeInterceptor.getOperationScope(method)
		err := invoker(ctx, method, req, reply, cc, opts...)
		scope.handleError(err)
		return err
	}
}

func (w *scopeInterceptor) getOperationScope(method string) *operationScope {
	scopeName := w.convertMethodToScope(method)
	scope := w.getScope(scopeName)
	scope.Counter(TemporalRequest).Inc(1)

	return &operationScope{scope: scope, startTime: time.Now()}
}

func (w *scopeInterceptor) convertMethodToScope(method string) string {
	// method is something like "/workflowservice.WorkflowService/RegisterDomain"
	methodStart := strings.LastIndex(method, "/") + 1
	return TemporalMetricsPrefix + method[methodStart:]
}

func (w *scopeInterceptor) getScope(scopeName string) tally.Scope {
	// TODO: maps are not thread-safe even for read. Should we pre-build child scopes instead of getting lock on every request?
	w.mutex.Lock()
	defer w.mutex.Unlock()
	scope, ok := w.childScopes[scopeName]
	if ok {
		return scope
	}
	scope = w.scope.SubScope(scopeName)
	w.childScopes[scopeName] = scope
	return scope
}
