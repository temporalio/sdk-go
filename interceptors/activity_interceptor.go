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

package interceptors

import "go.temporal.io/sdk/internal"

type (
	// ActivityInterceptor is used to create a single link in the interceptor
	// chain.
	ActivityInterceptor = internal.ActivityInterceptor

	// ActivityInboundCallsInterceptor is an interface that can be implemented to
	// intercept calls to activities from the workflow. Use
	// ActivityInboundCallsInterceptorBase as a base struct for implementations
	// that do not want to implement every method. Interceptor implementation
	// should forward calls to the next in the interceptor chain. All code in the
	// interceptor is executed with the context given to an activity. Therefore,
	// calls such as activity.GetInfo will work properly.
	ActivityInboundCallsInterceptor = internal.ActivityInboundCallsInterceptor

	// ActivityInboundCallsInterceptorBase is a noop implementation of
	// ActivityInboundCallsInterceptor that just forwards requests to the next
	// link in an interceptor chain. To be used as base implementation of
	// interceptors.
	ActivityInboundCallsInterceptorBase = internal.ActivityInboundCallsInterceptorBase
)
