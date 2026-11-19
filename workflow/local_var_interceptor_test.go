package workflow_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	localVarQueryName    = "local-var"
	localVarSetSignal    = "set-local-var"
	localVarFinishSignal = "finish"
)

type localVarInterceptorContextKey struct{}

func localVarFromInterceptorContext(ctx workflow.Context) workflow.LocalVar[string] {
	return ctx.Value(localVarInterceptorContextKey{}).(workflow.LocalVar[string])
}

type executeLocalVarInterceptor struct {
	interceptor.WorkerInterceptorBase
	interceptValue string
	executeValue   string
}

type executeLocalVarInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	control *executeLocalVarInterceptor
	local   workflow.LocalVar[string]
}

func (i *executeLocalVarInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	local := workflow.NewLocalVar[string](ctx)
	i.interceptValue = local.Get(ctx)
	local.Set(ctx, "set-in-intercept")
	return &executeLocalVarInboundInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
		control:                        i,
		local:                          local,
	}
}

func (i *executeLocalVarInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (any, error) {
	i.control.executeValue = i.local.Get(ctx)
	i.local.Set(ctx, "set-in-execute")
	return i.Next.ExecuteWorkflow(workflow.WithValue(ctx, localVarInterceptorContextKey{}, i.local), in)
}

type signalLocalVarInterceptor struct {
	interceptor.WorkerInterceptorBase
	beforeValue string
	afterValue  string
}

type signalLocalVarInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	control *signalLocalVarInterceptor
	local   workflow.LocalVar[string]
}

func (i *signalLocalVarInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	return &signalLocalVarInboundInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
		control:                        i,
		local:                          workflow.NewLocalVar[string](ctx),
	}
}

func (i *signalLocalVarInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (any, error) {
	return i.Next.ExecuteWorkflow(workflow.WithValue(ctx, localVarInterceptorContextKey{}, i.local), in)
}

func (i *signalLocalVarInboundInterceptor) HandleSignal(
	ctx workflow.Context,
	in *interceptor.HandleSignalInput,
) error {
	if in.SignalName == localVarSetSignal {
		i.control.beforeValue = i.local.Get(ctx)
		i.local.Set(ctx, "set-in-signal")
		i.control.afterValue = i.local.Get(ctx)
	}
	return i.Next.HandleSignal(ctx, in)
}

type queryLocalVarInterceptor struct {
	interceptor.WorkerInterceptorBase
	queryRead string
}

type queryLocalVarInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	control *queryLocalVarInterceptor
	local   workflow.LocalVar[string]
}

func (i *queryLocalVarInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	return &queryLocalVarInboundInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
		control:                        i,
		local:                          workflow.NewLocalVar[string](ctx),
	}
}

func (i *queryLocalVarInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (any, error) {
	return i.Next.ExecuteWorkflow(workflow.WithValue(ctx, localVarInterceptorContextKey{}, i.local), in)
}

func (i *queryLocalVarInboundInterceptor) HandleQuery(
	ctx workflow.Context,
	in *interceptor.HandleQueryInput,
) (any, error) {
	i.control.queryRead = i.local.Get(ctx)
	return i.Next.HandleQuery(ctx, in)
}

func TestLocalVarExecuteWorkflowInterceptorValueIsVisibleToWorkflowCode(t *testing.T) {
	workflowInterceptor := &executeLocalVarInterceptor{}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{workflowInterceptor},
	})

	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(localVarQueryName)
		require.NoError(t, err)

		var queried string
		require.NoError(t, value.Get(&queried))
		require.Equal(t, "set-in-execute", queried)

		env.SignalWorkflow(localVarFinishSignal, nil)
	}, time.Hour)

	env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		local := localVarFromInterceptorContext(ctx)
		if err := workflow.SetQueryHandler(ctx, localVarQueryName, func() (string, error) {
			return local.Get(ctx), nil
		}); err != nil {
			return "", err
		}

		workflow.GetSignalChannel(ctx, localVarFinishSignal).Receive(ctx, nil)
		return local.Get(ctx), nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "set-in-execute", result)
	require.Equal(t, "", workflowInterceptor.interceptValue)
	require.Equal(t, "set-in-intercept", workflowInterceptor.executeValue)
}
func TestLocalVarSignalInterceptorWriteSurvivesDefaultSignalHandling(t *testing.T) {
	signalInterceptor := &signalLocalVarInterceptor{}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{signalInterceptor},
	})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(localVarSetSignal, nil)
	}, time.Hour)
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(localVarQueryName)
		require.NoError(t, err)

		var queried string
		require.NoError(t, value.Get(&queried))
		require.Equal(t, "set-in-signal", queried)

		env.SignalWorkflow(localVarFinishSignal, nil)
	}, 2*time.Hour)

	env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		local := localVarFromInterceptorContext(ctx)
		if err := workflow.SetQueryHandler(ctx, localVarQueryName, func() (string, error) {
			return local.Get(ctx), nil
		}); err != nil {
			return "", err
		}

		workflow.GetSignalChannel(ctx, localVarSetSignal).Receive(ctx, nil)
		workflow.GetSignalChannel(ctx, localVarFinishSignal).Receive(ctx, nil)
		return local.Get(ctx), nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "set-in-signal", result)
	require.Equal(t, "", signalInterceptor.beforeValue)
	require.Equal(t, "set-in-signal", signalInterceptor.afterValue)
}

func TestLocalVarQueryInterceptorCanReadWorkflowScopedValue(t *testing.T) {
	queryInterceptor := &queryLocalVarInterceptor{}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{queryInterceptor},
	})

	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(localVarQueryName)
		require.NoError(t, err)

		var queried string
		require.NoError(t, value.Get(&queried))
		require.Equal(t, "set-in-workflow", queried)
		require.Equal(t, "set-in-workflow", queryInterceptor.queryRead)

		env.SignalWorkflow(localVarFinishSignal, nil)
	}, time.Hour)

	env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		local := localVarFromInterceptorContext(ctx)
		local.Set(ctx, "set-in-workflow")

		if err := workflow.SetQueryHandler(ctx, localVarQueryName, func() (string, error) {
			return local.Get(ctx), nil
		}); err != nil {
			return "", err
		}

		workflow.GetSignalChannel(ctx, localVarFinishSignal).Receive(ctx, nil)
		return local.Get(ctx), nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "set-in-workflow", result)
}
