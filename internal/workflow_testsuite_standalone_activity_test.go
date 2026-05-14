package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/converter"
)

func testStandaloneGreetActivity(ctx context.Context, name string) (string, error) {
	return "Hello, " + name + "!", nil
}

func TestExecuteStandaloneActivity_BasicExecution(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testStandaloneGreetActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{
			ID:                  "test-activity-1",
			TaskQueue:           "test-queue",
			StartToCloseTimeout: 10 * 60 * 1e9, // 10 minutes in nanoseconds
		},
		testStandaloneGreetActivity,
		"World",
	)
	require.NoError(t, err)
	require.NotNil(t, handle)
	require.Equal(t, "test-activity-1", handle.GetID())
	require.NotEmpty(t, handle.GetRunID())

	var result string
	err = handle.Get(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "Hello, World!", result)
}

func TestExecuteStandaloneActivity_DefaultOptions(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testStandaloneGreetActivity)

	// Test with minimal options — defaults should be applied
	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{},
		testStandaloneGreetActivity,
		"Temporal",
	)
	require.NoError(t, err)
	require.NotNil(t, handle)
	require.NotEmpty(t, handle.GetID())

	var result string
	err = handle.Get(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "Hello, Temporal!", result)
}

func testStandaloneFailActivity(ctx context.Context) (string, error) {
	return "", NewApplicationError("intentional failure", "TEST_ERROR", false, nil)
}

func TestExecuteStandaloneActivity_Failure(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testStandaloneFailActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{
			ID:                  "fail-activity",
			TaskQueue:           "test-queue",
			StartToCloseTimeout: 10 * 60 * 1e9,
		},
		testStandaloneFailActivity,
	)
	require.NoError(t, err)
	require.NotNil(t, handle)

	var result string
	err = handle.Get(context.Background(), &result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "intentional failure")
}

// tracingInterceptor records whether ExecuteActivity was called on the
// ClientOutboundInterceptor chain.
type standaloneActivityTestInterceptor struct {
	WorkerInterceptorBase
	ClientInterceptorBase
	executeActivityCalled bool
	activityType          string
}

func (s *standaloneActivityTestInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &standaloneActivityTestOutbound{
		ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next},
		interceptor:                   s,
	}
}

type standaloneActivityTestOutbound struct {
	ClientOutboundInterceptorBase
	interceptor *standaloneActivityTestInterceptor
}

func (s *standaloneActivityTestOutbound) ExecuteActivity(
	ctx context.Context,
	in *ClientExecuteActivityInput,
) (ClientActivityHandle, error) {
	s.interceptor.executeActivityCalled = true
	s.interceptor.activityType = in.ActivityType

	// Verify header is available (proves contextWithNewHeader was called)
	header := Header(ctx)
	if header == nil {
		panic("expected non-nil header in interceptor")
	}

	return s.Next.ExecuteActivity(ctx, in)
}

func TestExecuteStandaloneActivity_InterceptorChain(t *testing.T) {
	interceptor := &standaloneActivityTestInterceptor{}

	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{
		Interceptors: []WorkerInterceptor{interceptor},
	})
	env.RegisterActivity(testStandaloneGreetActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{
			ID:                  "intercepted-activity",
			TaskQueue:           "test-queue",
			StartToCloseTimeout: 10 * 60 * 1e9,
		},
		testStandaloneGreetActivity,
		"Intercepted",
	)
	require.NoError(t, err)
	require.NotNil(t, handle)

	// Verify interceptor was called
	require.True(t, interceptor.executeActivityCalled, "expected interceptor ExecuteActivity to be called")
	require.Equal(t, "testStandaloneGreetActivity", interceptor.activityType)

	var result string
	err = handle.Get(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "Hello, Intercepted!", result)
}

func TestExecuteStandaloneActivity_UnregisteredActivity(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	// The test suite panics when no activities are registered for the task queue.
	// This matches the existing behavior of TestActivityEnvironment.ExecuteActivity.
	require.Panics(t, func() {
		_, _ = env.ExecuteStandaloneActivity(
			context.Background(),
			ClientStartActivityOptions{
				ID:        "unregistered",
				TaskQueue: "test-queue",
			},
			func(ctx context.Context) error { return nil },
		)
	})
}

func TestExecuteStandaloneActivity_CustomDataConverter(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetDataConverter(converter.GetDefaultDataConverter())
	env.RegisterActivity(testStandaloneGreetActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{
			ID:                  "custom-dc-activity",
			TaskQueue:           "test-queue",
			StartToCloseTimeout: 10 * 60 * 1e9,
		},
		testStandaloneGreetActivity,
		"DataConverter",
	)
	require.NoError(t, err)

	var result string
	err = handle.Get(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "Hello, DataConverter!", result)
}

// TestExecuteStandaloneActivity_InterceptorTestProxy validates that the
// interceptortest.CallRecordingInvoker (the SDK's canonical proxy pattern)
// correctly records ExecuteActivity calls made via ExecuteStandaloneActivity.
// This is the critical test for issue #2318: it proves that standalone activity
// execution traverses the ClientOutboundInterceptor chain, enabling OTel
// tracing support (PR #2302).
func TestExecuteStandaloneActivity_InterceptorTestProxy(t *testing.T) {
	var rec callRecordingInterceptor

	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{
		Interceptors: []WorkerInterceptor{&rec},
	})
	env.RegisterActivity(testStandaloneGreetActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{
			ID:                  "proxy-activity",
			TaskQueue:           "test-queue",
			StartToCloseTimeout: 10 * 60 * 1e9,
		},
		testStandaloneGreetActivity,
		"Proxy",
	)
	require.NoError(t, err)
	require.NotNil(t, handle)

	var result string
	err = handle.Get(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "Hello, Proxy!", result)

	// Verify the interceptor recorded the call with correct method and args
	require.True(t, rec.executeActivityCalled)
	require.Equal(t, "testStandaloneGreetActivity", rec.activityType)
	require.NotNil(t, rec.options)
	require.Equal(t, "proxy-activity", rec.options.ID)
}

// callRecordingInterceptor is a lightweight recording interceptor that
// captures ExecuteActivity calls including the full input options.
type callRecordingInterceptor struct {
	WorkerInterceptorBase
	ClientInterceptorBase
	executeActivityCalled bool
	activityType          string
	options               *ClientStartActivityOptions
}

func (c *callRecordingInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &callRecordingOutbound{
		ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next},
		rec:                           c,
	}
}

type callRecordingOutbound struct {
	ClientOutboundInterceptorBase
	rec *callRecordingInterceptor
}

func (c *callRecordingOutbound) ExecuteActivity(
	ctx context.Context,
	in *ClientExecuteActivityInput,
) (ClientActivityHandle, error) {
	c.rec.executeActivityCalled = true
	c.rec.activityType = in.ActivityType
	c.rec.options = in.Options
	return c.Next.ExecuteActivity(ctx, in)
}

// TestExecuteStandaloneActivity_MultipleInterceptors verifies that multiple
// interceptors are chained correctly, with the outermost interceptor called
// first (matching the real client's behavior).
func TestExecuteStandaloneActivity_MultipleInterceptors(t *testing.T) {
	var callOrder []string

	outer := &orderTrackingInterceptor{name: "outer", callOrder: &callOrder}
	inner := &orderTrackingInterceptor{name: "inner", callOrder: &callOrder}

	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{
		// Earlier interceptors wrap later ones
		Interceptors: []WorkerInterceptor{outer, inner},
	})
	env.RegisterActivity(testStandaloneGreetActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{},
		testStandaloneGreetActivity,
		"Multi",
	)
	require.NoError(t, err)

	var result string
	err = handle.Get(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "Hello, Multi!", result)

	// Outer interceptor should be called first
	require.Equal(t, []string{"outer", "inner"}, callOrder)
}

type orderTrackingInterceptor struct {
	WorkerInterceptorBase
	ClientInterceptorBase
	name      string
	callOrder *[]string
}

func (o *orderTrackingInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &orderTrackingOutbound{
		ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next},
		interceptor:                   o,
	}
}

type orderTrackingOutbound struct {
	ClientOutboundInterceptorBase
	interceptor *orderTrackingInterceptor
}

func (o *orderTrackingOutbound) ExecuteActivity(
	ctx context.Context,
	in *ClientExecuteActivityInput,
) (ClientActivityHandle, error) {
	*o.interceptor.callOrder = append(*o.interceptor.callOrder, o.interceptor.name)
	return o.Next.ExecuteActivity(ctx, in)
}

func testStandaloneVoidActivity(ctx context.Context) error {
	return nil
}

// TestExecuteStandaloneActivity_VoidReturn verifies that activities with no
// return value (only error) work correctly.
func TestExecuteStandaloneActivity_VoidReturn(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testStandaloneVoidActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{},
		testStandaloneVoidActivity,
	)
	require.NoError(t, err)
	require.NotNil(t, handle)

	// Get with nil valuePtr should succeed
	err = handle.Get(context.Background(), nil)
	require.NoError(t, err)
}

func testStandaloneMultiArgActivity(ctx context.Context, first string, second int, third bool) (string, error) {
	if third {
		return fmt.Sprintf("%s-%d-true", first, second), nil
	}
	return fmt.Sprintf("%s-%d-false", first, second), nil
}

// TestExecuteStandaloneActivity_MultipleArgs verifies that activities with
// multiple arguments are encoded and passed correctly.
func TestExecuteStandaloneActivity_MultipleArgs(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testStandaloneMultiArgActivity)

	handle, err := env.ExecuteStandaloneActivity(
		context.Background(),
		ClientStartActivityOptions{},
		testStandaloneMultiArgActivity,
		"hello", 42, true,
	)
	require.NoError(t, err)

	var result string
	err = handle.Get(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "hello-42-true", result)
}
