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

package interceptors

import (
	"go.temporal.io/temporal/internal"
)

type (
	// WorkflowInterceptor is used to create a single link in the interceptor chain
	WorkflowInterceptor = internal.WorkflowInterceptor

	// WorkflowCallsInterceptor is an interface that can be implemented to intercept calls to the workflow function
	// as well calls done by the workflow code.
	// Use worker.WorkflowInterceptorBase as a base struct for implementations that do not want to implement every method.
	// Interceptor implementation must forward calls to the next in the interceptor chain.
	// All code in the interceptor is executed in the workflow.Context of a workflow. So all the rules and restrictions
	// that apply to the workflow code should be obeyed by the interceptor implementation.
	// Use workflow.IsReplaying(ctx) to filter out duplicated calls.
	WorkflowCallsInterceptor = internal.WorkflowCallsInterceptor

	// WorkflowInterceptorBase is a noop implementation of WorkflowCallsInterceptor that just forwards requests
	// to the next link in an interceptor chain. To be used as base implementation of interceptors.
	WorkflowInterceptorBase = internal.WorkflowInterceptorBase
)
