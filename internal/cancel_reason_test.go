package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func recordCancellationReason(ctx Context) (string, error) {
	ctx.Done().Receive(ctx, nil)
	return GetCancellationReason(ctx), nil
}

func TestCancellationReason_FromTestEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel func(env *TestWorkflowEnvironment)
		want   string
	}{
		{
			name:   "with reason",
			cancel: func(env *TestWorkflowEnvironment) { env.CancelWorkflowWithReason("because") },
			want:   "because",
		},
		{
			name:   "without reason",
			cancel: func(env *TestWorkflowEnvironment) { env.CancelWorkflow() },
			want:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(recordCancellationReason)
			env.RegisterDelayedCallback(func() { tc.cancel(env) }, time.Millisecond)

			env.ExecuteWorkflow(recordCancellationReason)

			require.True(t, env.IsWorkflowCompleted())
			var reason string
			require.NoError(t, env.GetWorkflowResult(&reason))
			require.Equal(t, tc.want, reason)
		})
	}
}

func selfCancelWithReason(ctx Context) (string, error) {
	info := GetWorkflowInfo(ctx)
	RequestCancelExternalWorkflowWithOptions(ctx, RequestCancelExternalWorkflowOptions{
		WorkflowID: info.WorkflowExecution.ID,
		RunID:      info.WorkflowExecution.RunID,
		Reason:     "because",
	})
	ctx.Done().Receive(ctx, nil)
	return GetCancellationReason(ctx), nil
}

func TestCancellationReason_FromRequestCancelExternalWorkflowWithOptions(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(selfCancelWithReason)

	env.ExecuteWorkflow(selfCancelWithReason)

	require.True(t, env.IsWorkflowCompleted())
	var reason string
	require.NoError(t, env.GetWorkflowResult(&reason))
	require.Equal(t, "because", reason)
}

func cancelChildThroughContext(ctx Context) (string, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{WorkflowID: "child-1", WaitForCancellation: true})
	var reason string
	err := ExecuteChildWorkflow(ctx, recordCancellationReason).Get(ctx, &reason)
	return reason, err
}

func TestCancellationReason_ChildCancelCarriesNoInventedReason(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(cancelChildThroughContext)
	env.RegisterWorkflow(recordCancellationReason)
	env.RegisterDelayedCallback(func() { env.CancelWorkflowWithReason("because") }, time.Millisecond)

	env.ExecuteWorkflow(cancelChildThroughContext)

	require.True(t, env.IsWorkflowCompleted())
	var reason string
	require.NoError(t, env.GetWorkflowResult(&reason))
	require.Empty(t, reason)
}

type cancelCountingInterceptor struct {
	WorkflowOutboundInterceptorBase
	legacyCalls  *int
	optionsCalls *int
}

func (i *cancelCountingInterceptor) RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future {
	*i.legacyCalls++
	return i.Next.RequestCancelExternalWorkflow(ctx, workflowID, runID)
}

func (i *cancelCountingInterceptor) RequestCancelExternalWorkflowWithOptions(ctx Context, options RequestCancelExternalWorkflowOptions) Future {
	*i.optionsCalls++
	return i.Next.RequestCancelExternalWorkflowWithOptions(ctx, options)
}

func (i *cancelCountingInterceptor) GetCancellationReason(ctx Context) string {
	return "intercepted"
}

type cancelCountingWorkerInterceptor struct {
	WorkerInterceptorBase
	legacyCalls  *int
	optionsCalls *int
}

func (i *cancelCountingWorkerInterceptor) InterceptWorkflow(ctx Context, next WorkflowInboundInterceptor) WorkflowInboundInterceptor {
	return &cancelCountingInboundInterceptor{
		WorkflowInboundInterceptorBase: WorkflowInboundInterceptorBase{Next: next},
		legacyCalls:                    i.legacyCalls,
		optionsCalls:                   i.optionsCalls,
	}
}

type cancelCountingInboundInterceptor struct {
	WorkflowInboundInterceptorBase
	legacyCalls  *int
	optionsCalls *int
}

func (i *cancelCountingInboundInterceptor) Init(outbound WorkflowOutboundInterceptor) error {
	return i.Next.Init(&cancelCountingInterceptor{
		WorkflowOutboundInterceptorBase: WorkflowOutboundInterceptorBase{Next: outbound},
		legacyCalls:                     i.legacyCalls,
		optionsCalls:                    i.optionsCalls,
	})
}

func cancelBothWays(ctx Context) (string, error) {
	info := GetWorkflowInfo(ctx)
	RequestCancelExternalWorkflow(ctx, "other-1", "")
	RequestCancelExternalWorkflowWithOptions(ctx, RequestCancelExternalWorkflowOptions{
		WorkflowID: info.WorkflowExecution.ID,
		Reason:     "because",
	})
	ctx.Done().Receive(ctx, nil)
	return GetCancellationReason(ctx), nil
}

func TestCancellationReason_InterceptorSeesBothPaths(t *testing.T) {
	var legacyCalls, optionsCalls int

	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{Interceptors: []WorkerInterceptor{
		&cancelCountingWorkerInterceptor{legacyCalls: &legacyCalls, optionsCalls: &optionsCalls},
	}})
	env.RegisterWorkflow(cancelBothWays)
	env.OnRequestCancelExternalWorkflow(mock.Anything, "other-1", mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(cancelBothWays)

	require.True(t, env.IsWorkflowCompleted())
	require.Equal(t, 1, legacyCalls, "legacy cancel must reach the legacy interceptor method")
	require.Equal(t, 1, optionsCalls, "options cancel must reach the options interceptor method")

	var reason string
	require.NoError(t, env.GetWorkflowResult(&reason))
	require.Equal(t, "intercepted", reason, "GetCancellationReason must go through the interceptor chain")
}

func cancelChildByIDWithReason(ctx Context) (string, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{WorkflowID: "child-by-id", WaitForCancellation: true})
	var reason string
	err := ExecuteChildWorkflow(ctx, recordCancellationReason).Get(ctx, &reason)
	return reason, err
}

func TestCancellationReason_CancelWorkflowByIDWithReason(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(cancelChildByIDWithReason)
	env.RegisterWorkflow(recordCancellationReason)
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflowByIDWithReason("child-by-id", "", "because")
	}, time.Millisecond)

	env.ExecuteWorkflow(cancelChildByIDWithReason)

	require.True(t, env.IsWorkflowCompleted())
	var reason string
	require.NoError(t, env.GetWorkflowResult(&reason))
	require.Equal(t, "because", reason)
}

func cancelExternalWithReason(ctx Context) error {
	return RequestCancelExternalWorkflowWithOptions(ctx, RequestCancelExternalWorkflowOptions{
		WorkflowID: "external-1",
		Reason:     "because",
	}).Get(ctx, nil)
}

func TestCancellationReason_MockMatchesReason(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(cancelExternalWithReason)
	env.OnRequestCancelExternalWorkflowWithOptions(mock.Anything, "external-1", mock.Anything, "because").Return(nil).Once()

	env.ExecuteWorkflow(cancelExternalWithReason)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
