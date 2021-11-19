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

package interceptortest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestProxy(t *testing.T) {
	// Just a sanity check to make sure proxy works

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProxyWorkflow)
	env.RegisterActivity(ProxyActivity)

	// Set recorder
	var rec interceptortest.CallRecordingInvoker
	proxy := interceptortest.NewProxy(&rec)
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{proxy}})

	// Exec
	env.ExecuteWorkflow(ProxyWorkflow, "World")

	// Confirm result
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "Hello, World", result)

	// Make sure expected methods are present
	calls := rec.Calls()
	getCall := func(qualifiedMethod string) *interceptortest.RecordedCall {
		for _, call := range calls {
			if call.Interface.Name()+"."+call.Method.Name == qualifiedMethod {
				return call
			}
		}
		return nil
	}
	require.NotNil(t, getCall("ActivityInboundInterceptor.Init"))
	call := getCall("ActivityInboundInterceptor.ExecuteActivity")
	require.NotNil(t, call)
	require.Equal(t, "World", call.Args[1].Interface().(*interceptor.ExecuteActivityInput).Args[0])
	call = getCall("ActivityOutboundInterceptor.GetInfo")
	require.NotNil(t, call)
	require.Equal(t, "ProxyActivity", call.Results[0].Interface().(activity.Info).ActivityType.Name)
}

func ProxyWorkflow(ctx workflow.Context, suffix string) (ret string, err error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	err = workflow.ExecuteActivity(ctx, ProxyActivity, suffix).Get(ctx, &ret)
	return
}

func ProxyActivity(ctx context.Context, suffix string) (string, error) {
	activity.GetInfo(ctx)
	return "Hello, " + suffix, nil
}
