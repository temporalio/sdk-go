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
	"errors"
	"time"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/multierr"
	"go.uber.org/zap"
)

const (
	malformedActivity = "args of saga activity != 1"
)

var (
	defaultActivityOpts = workflow.ActivityOptions{
		ScheduleToStartTimeout: 1 * time.Minute,
		StartToCloseTimeout:    5 * time.Minute,
	}
)

type (
	// CompensateOptions is options for compensate.
	CompensateOptions struct {
		// ActivityType is the name of compensate activity.
		ActivityType string
		// ActivityOptions is the activity execute options, local activity is not supported.
		ActivityOptions *workflow.ActivityOptions
		// Convertor optional. Convert req & response to request for compensate activity.
		// currently, activity func is not available for worker, so decode futures should be done by developer.
		Convertor func(ctx workflow.Context, f workflow.Future, args interface{}) (interface{}, error)
	}

	//InterceptorOptions is options for saga interceptor.
	InterceptorOptions struct {
		// WorkflowRegistry names for workflow to be treated as Saga transaction.
		WorkflowRegistry map[string]struct{}
		// ActivityRegistry Action -> CompensateAction, key is activity type for action.
		ActivityRegistry map[string]CompensateOptions
	}

	sagaInterceptor struct {
		interceptor.WorkerInterceptorBase
		options InterceptorOptions
	}

	workflowInboundInterceptor struct {
		interceptor.WorkflowInboundInterceptorBase
		root *sagaInterceptor
		ctx  sagaContext
	}
	workflowOutboundInterceptor struct {
		interceptor.WorkflowOutboundInterceptorBase
		root *sagaInterceptor
		ctx  *sagaContext
	}

	action struct {
		ActivityType string
		Future       workflow.Future
		Arg          interface{}
	}

	sagaContext struct {
		Actions []*action
	}
)

// NewInterceptor creates an interceptor for execute in Saga patterns.
// when workflow fails, registered compensate activities will be executed automatically.
// NOTE: action&compensate activity has only one arg,
// like func(ctx context.Context, arg interface{}) (interface{}, error),
// or func(ctx context.Context, arg interface{}) error.
func NewInterceptor(options InterceptorOptions) (interceptor.WorkerInterceptor, error) {
	return &sagaInterceptor{options: options}, nil
}

func (s *sagaInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	if _, ok := s.options.WorkflowRegistry[workflow.GetInfo(ctx).WorkflowType.Name]; !ok {
		return next
	}

	workflow.GetLogger(ctx).Debug("intercept saga workflow")
	i := &workflowInboundInterceptor{root: s}
	i.Next = next
	return i
}

func (w *workflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	i := &workflowOutboundInterceptor{root: w.root, ctx: &w.ctx}
	i.Next = outbound
	return w.Next.Init(i)
}

func (w *workflowInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (ret interface{}, err error) {
	workflow.GetLogger(ctx).Debug("intercept ExecuteWorkflow")
	ret, wferr := w.Next.ExecuteWorkflow(ctx, in)
	if wferr == nil || len(w.ctx.Actions) == 0 {
		return ret, wferr
	}

	ctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	for i := len(w.ctx.Actions) - 1; i >= 0; i-- {
		a := w.ctx.Actions[i]
		opt, ok := w.root.options.ActivityRegistry[a.ActivityType]
		if !ok {
			workflow.GetLogger(ctx).Warn("action in history has no compensate activity",
				"activity_type", a.ActivityType)
			continue
		}

		// only compensate action with success
		if err := a.Future.Get(ctx, nil); err != nil {
			continue
		}

		// add opts if not config
		activityOpts := opt.ActivityOptions
		if activityOpts == nil {
			activityOpts = &defaultActivityOpts
		}
		ctx = workflow.WithActivityOptions(ctx, *activityOpts)

		// use arg in action as default for compensate
		arg := a.Arg
		if opt.Convertor != nil {
			arg, err = opt.Convertor(ctx, a.Future, arg)
			if err != nil {
				workflow.GetLogger(ctx).Error("failed to convert to compensate req", zap.Error(err))
				return ret, multierr.Append(wferr, err)
			}
		}

		if err := workflow.ExecuteActivity(ctx, opt.ActivityType, arg).Get(ctx, nil); err != nil {
			return ret, multierr.Append(wferr, err)
		}
	}
	return ret, wferr
}

func (w *workflowOutboundInterceptor) ExecuteActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	workflow.GetLogger(ctx).Debug("intercept ExecuteActivity")
	f := w.Next.ExecuteActivity(ctx, activityType, args...)
	if _, ok := w.root.options.ActivityRegistry[activityType]; ok {
		workflow.GetLogger(ctx).Debug("save action future", "activity_type", activityType)
		if len(args) != 1 {
			f, set := workflow.NewFuture(ctx)
			set.SetError(errors.New(malformedActivity))
			return f
		}
		w.ctx.Actions = append(w.ctx.Actions, &action{
			ActivityType: activityType,
			Future:       f,
			Arg:          args[0],
		})
	}

	return f
}

func (w *workflowOutboundInterceptor) ExecuteLocalActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	workflow.GetLogger(ctx).Debug("intercept ExecuteLocalActivity")
	f := w.Next.ExecuteLocalActivity(ctx, activityType, args...)
	if _, ok := w.root.options.ActivityRegistry[activityType]; ok {
		workflow.GetLogger(ctx).Debug("save action future", "activity_type", activityType)
		if len(args) != 1 {
			f, set := workflow.NewFuture(ctx)
			set.SetError(errors.New(malformedActivity))
			return f
		}
		w.ctx.Actions = append(w.ctx.Actions, &action{
			ActivityType: activityType,
			Future:       f,
			Arg:          args[0],
		})
	}

	return f
}
