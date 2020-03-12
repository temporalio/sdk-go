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
