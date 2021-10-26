package opentracing_test

import (
	"context"
	"testing"
	"time"

	"github.com/opentracing/opentracing-go/mocktracer"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/opentracing"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestSpanPropagation(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(Activity)
	env.RegisterWorkflow(Workflow)
	env.RegisterWorkflow(WorkflowChild)

	// Create tracer
	tracer := mocktracer.New()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{opentracing.NewInterceptor(tracer)},
	})

	// Exec
	env.ExecuteWorkflow(Workflow)

	// Confirm result
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result []string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, []string{"work", "act", "act-local", "work-child", "act", "act-local"}, result)

	// Check span tree
	require.Equal(t, []*span{
		sp("RunWorkflow:Workflow",
			sp("StartActivity:Activity",
				sp("RunActivity:Activity")),
			sp("StartActivity:ActivityLocal",
				sp("RunActivity:ActivityLocal")),
			sp("StartChildWorkflow:WorkflowChild",
				sp("RunWorkflow:WorkflowChild",
					sp("StartActivity:Activity",
						sp("RunActivity:Activity")),
					sp("StartActivity:ActivityLocal",
						sp("RunActivity:ActivityLocal"))))),
	}, spanChildren(tracer.FinishedSpans(), 0))
}

type span struct {
	name     string
	children []*span
}

func sp(name string, children ...*span) *span { return &span{name, children} }

func spanChildren(spans []*mocktracer.MockSpan, parentID int) (ret []*span) {
	for _, s := range spans {
		if s.ParentID == parentID {
			ret = append(ret, &span{name: s.OperationName, children: spanChildren(spans, s.SpanContext.SpanID)})
		}
	}
	return
}

func Workflow(ctx workflow.Context) ([]string, error) {
	// Run code
	ret, err := workflowInternal(ctx)

	// Run child
	if err == nil {
		var temp []string
		err = workflow.ExecuteChildWorkflow(ctx, WorkflowChild).Get(ctx, &temp)
		ret = append(ret, temp...)
	}

	return append([]string{"work"}, ret...), err
}

func WorkflowChild(ctx workflow.Context) (ret []string, err error) {
	ret, err = workflowInternal(ctx)
	return append([]string{"work-child"}, ret...), err
}

func workflowInternal(ctx workflow.Context) (ret []string, err error) {
	// Exec normal activity
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	var temp []string
	err = workflow.ExecuteActivity(ctx, Activity).Get(ctx, &temp)
	ret = append(ret, temp...)

	// Exec local activity
	if err == nil {
		ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{StartToCloseTimeout: 10 * time.Second})
		temp = nil
		err = workflow.ExecuteLocalActivity(ctx, ActivityLocal).Get(ctx, &temp)
		ret = append(ret, temp...)
	}

	return
}

func Activity(ctx context.Context) ([]string, error) {
	return []string{"act"}, nil
}

func ActivityLocal(ctx context.Context) ([]string, error) {
	return []string{"act-local"}, nil
}
