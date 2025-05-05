package interceptortest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

var testWorkflowStartTime = time.Date(1969, 7, 20, 20, 17, 0, 0, time.UTC)

type testUpdateCallbacks struct {
	AcceptImpl   func()
	RejectImpl   func(err error)
	CompleteImpl func(success interface{}, err error)
}

// Accept implements internal.UpdateCallbacks.
func (t *testUpdateCallbacks) Accept() {
}

// Complete implements internal.UpdateCallbacks.
func (t *testUpdateCallbacks) Complete(success interface{}, err error) {
}

// Reject implements internal.UpdateCallbacks.
func (t *testUpdateCallbacks) Reject(err error) {
}

// TestTracer is an interceptor.Tracer that returns finished spans.
type TestTracer interface {
	interceptor.Tracer
	FinishedSpans() []*SpanInfo
	SpanName(options *interceptor.TracerStartSpanOptions) string
}

// SpanInfo is information about a span.
type SpanInfo struct {
	Name     string
	Children []*SpanInfo
}

// Span creates a SpanInfo.
func Span(name string, children ...*SpanInfo) *SpanInfo {
	return &SpanInfo{Name: name, Children: children}
}

// RunTestWorkflow executes a test workflow with a tracing interceptor.
func RunTestWorkflow(t *testing.T, tracer interceptor.Tracer) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(testActivity)
	env.RegisterActivity(testActivityLocal)
	env.RegisterWorkflow(testWorkflow)
	env.RegisterWorkflow(testWorkflowChild)
	env.RegisterWorkflow(testWaitForCancelWorkflow)
	op := temporalnexus.NewWorkflowRunOperation("op", testWaitForCancelWorkflow, func(ctx context.Context, input nexus.NoValue, options nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		return client.StartWorkflowOptions{
			ID: options.RequestID,
		}, nil
	})
	service := nexus.NewService("test")
	service.MustRegister(op)
	env.RegisterNexusService(service)

	// Set tracer interceptor
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{interceptor.NewTracingInterceptor(tracer)},
	})

	env.SetStartTime(testWorkflowStartTime)

	// Send an update
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("testUpdate", "updateID", &testUpdateCallbacks{
			RejectImpl: func(err error) {
			},
			AcceptImpl: func() {
			},
			CompleteImpl: func(interface{}, error) {
			},
		})
	}, 0*time.Second)

	// Exec
	env.ExecuteWorkflow(testWorkflow)

	// Confirm result
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result []string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, []string{"work", "act", "act-local", "work-child", "act", "act-local", "nexus-op"}, result)

	// Query workflow
	val, err := env.QueryWorkflow("my-query", nil)
	require.NoError(t, err)
	var queryResp string
	require.NoError(t, val.Get(&queryResp))
	require.Equal(t, "query-response", queryResp)
}

func RunTestWorkflowWithError(t *testing.T, tracer interceptor.Tracer) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(testWorkflowWithError)

	// Set tracer interceptor
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{interceptor.NewTracingInterceptor(tracer)},
	})

	env.SetStartTime(testWorkflowStartTime)

	// Exec
	env.ExecuteWorkflow(testWorkflowWithError)

	// Confirm result
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func AssertSpanPropagation(t *testing.T, tracer TestTracer) {

	require.Equal(t, []*SpanInfo{
		Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "ValidateUpdate", Name: "testUpdate"})),
		Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "HandleUpdate", Name: "testUpdate"}),
			Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartActivity", Name: "testActivity"}),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunActivity", Name: "testActivity"}))),
			Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartActivity", Name: "testActivityLocal"}),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunActivity", Name: "testActivityLocal"})))),
		// This is the workflow that gets started from the nexus workflow run operation.
		Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunWorkflow", Name: "testWaitForCancelWorkflow"})),
		Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunWorkflow", Name: "testWorkflow"}),
			Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartActivity", Name: "testActivity"}),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunActivity", Name: "testActivity"}))),
			Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartActivity", Name: "testActivityLocal"}),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunActivity", Name: "testActivityLocal"}))),
			Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartChildWorkflow", Name: "testWorkflowChild"}),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunWorkflow", Name: "testWorkflowChild"}),
					Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartActivity", Name: "testActivity"}),
						Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunActivity", Name: "testActivity"}))),
					Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartActivity", Name: "testActivityLocal"}),
						Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunActivity", Name: "testActivityLocal"}))))),
			Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "SignalChildWorkflow", Name: "my-signal"}),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "HandleSignal", Name: "my-signal"}))),
			Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "StartNexusOperation", Name: "test/op"}),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunStartNexusOperationHandler", Name: "test/op"})),
				Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "RunCancelNexusOperationHandler", Name: "test/op"})),
			)),
		Span(tracer.SpanName(&interceptor.TracerStartSpanOptions{Operation: "HandleQuery", Name: "my-query"})),
	}, tracer.FinishedSpans())
}

func testWorkflowWithError(_ workflow.Context) error {
	return errors.New("ignore me")
}

func testWorkflow(ctx workflow.Context) ([]string, error) {
	var updateRan bool
	err := workflow.SetUpdateHandler(ctx, "testUpdate", func(ctx workflow.Context) (string, error) {
		defer func() { updateRan = true }()
		_, err := workflowInternal(ctx, false)
		if err != nil {
			return "", err
		}
		return "updateID", nil
	})
	if err != nil {
		return nil, err
	}
	err = workflow.Await(ctx, func() bool { return updateRan })
	if err != nil {
		return nil, err
	}
	// Run code
	ret, err := workflowInternal(ctx, false)
	if err != nil {
		return nil, err
	}

	// Run child
	if err == nil {
		var temp []string
		fut := workflow.ExecuteChildWorkflow(ctx, testWorkflowChild)
		// Signal and get result
		if err := fut.SignalChildWorkflow(ctx, "my-signal", nil).Get(ctx, nil); err != nil {
			return nil, fmt.Errorf("failed signaling child: %w", err)
		}
		if err := fut.Get(ctx, &temp); err != nil {
			return nil, fmt.Errorf("failed running child: %w", err)
		}
		ret = append(ret, temp...)
	}

	nc := workflow.NewNexusClient("test-endpoint", "test")
	opCtx, cancel := workflow.WithCancel(ctx)
	defer cancel()
	fut := nc.ExecuteOperation(opCtx, "op", nil, workflow.NexusOperationOptions{})
	if err := fut.GetNexusOperationExecution().Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("failed starting nexus operation: %w", err)
	}

	cancel()
	if err := fut.Get(ctx, nil); err == nil || !errors.As(err, new(*temporal.CanceledError)) {
		return nil, fmt.Errorf("expected nexus operation to fail with a canceled error, got: %w", err)
	}
	ret = append(ret, "nexus-op")

	return append([]string{"work"}, ret...), nil
}

func testWaitForCancelWorkflow(ctx workflow.Context, input nexus.NoValue) (nexus.NoValue, error) {
	return nil, workflow.Await(ctx, func() bool { return false })
}

func testWorkflowChild(ctx workflow.Context) (ret []string, err error) {
	ret, err = workflowInternal(ctx, true)
	return append([]string{"work-child"}, ret...), err
}

func workflowInternal(ctx workflow.Context, waitSignal bool) (ret []string, err error) {
	// Add signal and query handling
	if waitSignal {
		workflow.GetSignalChannel(ctx, "my-signal").Receive(ctx, nil)
	}
	err = workflow.SetQueryHandler(ctx, "my-query", func() (string, error) { return "query-response", nil })
	if err != nil {
		return
	}

	// Exec normal activity
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	var temp []string
	err = workflow.ExecuteActivity(ctx, testActivity).Get(ctx, &temp)
	ret = append(ret, temp...)

	// Exec local activity
	if err == nil {
		ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{StartToCloseTimeout: 10 * time.Second})
		temp = nil
		err = workflow.ExecuteLocalActivity(ctx, testActivityLocal).Get(ctx, &temp)
		ret = append(ret, temp...)
	}

	return
}

func testActivity(ctx context.Context) ([]string, error) {
	return []string{"act"}, nil
}

func testActivityLocal(ctx context.Context) ([]string, error) {
	return []string{"act-local"}, nil
}
