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

package interceptor

import (
	"go.temporal.io/sdk/internal"
)

type Interceptor = internal.Interceptor
type InterceptorBase = internal.InterceptorBase

type WorkerInterceptor = internal.WorkerInterceptor
type WorkerInterceptorBase = internal.WorkerInterceptorBase

type ActivityInboundInterceptor = internal.ActivityInboundInterceptor
type ActivityInboundInterceptorBase = internal.ActivityInboundInterceptorBase
type ExecuteActivityInput = internal.ExecuteActivityInput

type ActivityOutboundInterceptor = internal.ActivityOutboundInterceptor
type ActivityOutboundInterceptorBase = internal.ActivityOutboundInterceptorBase

type WorkflowInboundInterceptor = internal.WorkflowInboundInterceptor
type WorkflowInboundInterceptorBase = internal.WorkflowInboundInterceptorBase
type ExecuteWorkflowInput = internal.ExecuteWorkflowInput
type HandleSignalInput = internal.HandleSignalInput
type HandleQueryInput = internal.HandleQueryInput

type WorkflowOutboundInterceptor = internal.WorkflowOutboundInterceptor
type WorkflowOutboundInterceptorBase = internal.WorkflowOutboundInterceptorBase

type ClientInterceptor = internal.ClientInterceptor
type ClientInterceptorBase = internal.ClientInterceptorBase

type ClientOutboundInterceptor = internal.ClientOutboundInterceptor
type ClientOutboundInterceptorBase = internal.ClientOutboundInterceptorBase
