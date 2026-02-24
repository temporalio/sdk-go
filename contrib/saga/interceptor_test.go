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

package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
)

type orderInfo struct {
	ID       string
	IsDelete bool
}

var (
	orders = map[string]*orderInfo{}
	amount = 1
	suite  testsuite.WorkflowTestSuite
)

func init() {
	logger, _ := zap.NewDevelopmentConfig().Build()
	zap.ReplaceGlobals(logger)
}

func createOrder(ctx context.Context, amount int) (string, error) {
	zap.L().Info("enter createOrder")
	id := "abc"
	orders[id] = &orderInfo{
		ID: id,
	}
	return id, nil
}

func deleteOrder(ctx context.Context, id string) error {
	zap.L().Info("enter deleteOrder", zap.String("id", id))
	orders[id].IsDelete = true
	return nil
}

func stockDeduct(ctx context.Context, in int) error {
	zap.L().Info("enter stockDeduct")
	amount -= in
	return nil
}

func stockInc(ctx context.Context, in int) error {
	zap.L().Info("enter stockInc")
	amount += in
	return nil
}

func createPay(ctx context.Context, in int) error {
	return errors.New("must fail")
}

func testConvertor(ctx workflow.Context, f workflow.Future, req interface{}) (rsp interface{}, err error) {
	zap.L().Info("convert", zap.Int("req", req.(int)))
	var id string
	if err := f.Get(ctx, &id); err != nil {
		return nil, err
	}
	return id, nil
}

func testWorkflow(ctx workflow.Context, a int) error {
	zap.L().Debug("enter workflow")
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})
	var id string
	if err := workflow.ExecuteActivity(ctx, "createOrder", a).Get(ctx, &id); err != nil {
		return err
	}
	zap.L().Debug("create order, id:", zap.String("id", id))
	if err := workflow.ExecuteActivity(ctx, "stockDeduct", a).Get(ctx, nil); err != nil {
		return err
	}
	if err := workflow.ExecuteActivity(ctx, "createPay", a).Get(ctx, nil); err != nil {
		return err
	}

	return nil
}

func TestWorkflow(t *testing.T) {
	env := suite.NewTestWorkflowEnvironment()
	intercept, _ := NewInterceptor(InterceptorOptions{
		WorkflowRegistry: map[string]struct{}{
			"testWorkflow": {},
		},
		ActivityRegistry: map[string]CompensateOptions{
			"createOrder": {
				ActivityType: "deleteOrder",
				Convertor:    testConvertor,
			},
			"stockDeduct": {
				ActivityType: "stockInc",
			},
		},
	})
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{intercept}})
	env.RegisterWorkflowWithOptions(testWorkflow, workflow.RegisterOptions{
		Name: "testWorkflow",
	})
	env.RegisterActivityWithOptions(createOrder, activity.RegisterOptions{
		Name: "createOrder",
	})
	env.RegisterActivityWithOptions(deleteOrder, activity.RegisterOptions{
		Name: "deleteOrder",
	})
	env.RegisterActivityWithOptions(stockDeduct, activity.RegisterOptions{
		Name: "stockDeduct",
	})
	env.RegisterActivityWithOptions(stockInc, activity.RegisterOptions{
		Name: "stockInc",
	})
	env.RegisterActivityWithOptions(createPay, activity.RegisterOptions{
		Name: "createPay",
	})

	env.ExecuteWorkflow(testWorkflow, 1)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, 1, len(orders))
	for _, order := range orders {
		require.True(t, order.IsDelete)
	}
	require.Equal(t, 1, amount)
	env.AssertExpectations(t)
}
