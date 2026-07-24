package interceptor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestIdempotencyKeyGeneration(t *testing.T) {
	i := &tracingWorkflowInboundInterceptor{
		root: nil,
		info: &workflow.Info{
			Namespace: "default",
			WorkflowExecution: workflow.Execution{
				ID:    "foo",
				RunID: "bar",
			},
		},
	}

	// Verify that each call to get an idempotency key results in a deterministic and different result.
	//
	// XXX(jlegrone): Any change to the idempotency key format in this test constitutes a breaking
	//                change to the SDK. This is because tracers that were using the old idempotency
	//                key format will generate new IDs for the same spans after updating, and that
	//                will lead to disconnected traces for workflows still running at the time of the
	//                SDK upgrade.
	//
	//                If possible, changing the idempotency keys should be avoided. Otherwise at a
	//                minimum, a description of this upgrade behavior should be included in the next
	//                SDK version's release notes.
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:1")
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:2")
}

type recordingTracer struct {
	BaseTracer
	options []TracerStartSpanOptions
}

func (r *recordingTracer) Options() TracerOptions {
	return TracerOptions{
		SpanContextKey: "recording-tracer",
		HeaderKey:      "recording-tracer",
	}
}

func (r *recordingTracer) UnmarshalSpan(map[string]string) (TracerSpanRef, error) {
	return nil, nil
}

func (r *recordingTracer) MarshalSpan(TracerSpan) (map[string]string, error) {
	return nil, nil
}

func (r *recordingTracer) SpanFromContext(context.Context) TracerSpan {
	return nil
}

func (r *recordingTracer) ContextWithSpan(ctx context.Context, span TracerSpan) context.Context {
	return ctx
}

func (r *recordingTracer) StartSpan(options *TracerStartSpanOptions) (TracerSpan, error) {
	r.options = append(r.options, *options)
	return recordingSpan{}, nil
}

type recordingSpan struct{}

func (recordingSpan) Finish(*TracerFinishSpanOptions) {}

type recordingActivityInbound struct {
	ActivityInboundInterceptorBase
}

func (recordingActivityInbound) Init(ActivityOutboundInterceptor) error {
	return nil
}

func (recordingActivityInbound) ExecuteActivity(context.Context, *ExecuteActivityInput) (interface{}, error) {
	return "activity-result", nil
}

func testRunWorkflowWorkerClockWorkflow(workflow.Context) error {
	return nil
}

func TestRunWorkflowSpanUsesWorkerClock(t *testing.T) {
	tracer := &recordingTracer{}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	serverWorkflowStartTime := time.Now().Add(time.Hour)
	env.SetStartTime(serverWorkflowStartTime)
	env.RegisterWorkflow(testRunWorkflowWorkerClockWorkflow)
	env.SetWorkerOptions(worker.Options{
		Interceptors: []WorkerInterceptor{NewTracingInterceptor(tracer)},
	})

	beforeRunWorkflow := time.Now()
	env.ExecuteWorkflow(testRunWorkflowWorkerClockWorkflow)
	afterRunWorkflow := time.Now()

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Len(t, tracer.options, 1)
	require.Equal(t, "RunWorkflow", tracer.options[0].Operation)
	require.False(t, tracer.options[0].Time.Before(beforeRunWorkflow))
	require.False(t, tracer.options[0].Time.After(afterRunWorkflow))
	require.True(t, tracer.options[0].Time.Before(serverWorkflowStartTime))
}

func TestRunActivitySpanUsesWorkerClock(t *testing.T) {
	tracer := &recordingTracer{}
	root := NewTracingInterceptor(tracer).(*tracingInterceptor)
	inbound := &tracingActivityInboundInterceptor{
		ActivityInboundInterceptorBase: ActivityInboundInterceptorBase{Next: &recordingActivityInbound{}},
		root:                           root,
	}

	serverStartedTime := time.Now().Add(time.Hour)
	ctx, err := internal.WithActivityTask(
		context.Background(),
		&workflowservice.PollActivityTaskQueueResponse{
			ActivityId:             "activity-id",
			ActivityType:           &commonpb.ActivityType{Name: "activity-type"},
			ScheduledTime:          timestamppb.New(time.Now()),
			StartedTime:            timestamppb.New(serverStartedTime),
			StartToCloseTimeout:    durationpb.New(time.Minute),
			WorkflowExecution:      &commonpb.WorkflowExecution{WorkflowId: "workflow-id", RunId: "run-id"},
			WorkflowNamespace:      "default",
			WorkflowType:           &commonpb.WorkflowType{Name: "workflow-type"},
			ScheduleToCloseTimeout: durationpb.New(time.Minute),
		},
		"task-queue",
		nil,
		ilog.NewNopLogger(),
		metrics.NopHandler,
		converter.GetDefaultDataConverter(),
		nil,
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)

	beforeRunActivity := time.Now()
	ret, err := inbound.ExecuteActivity(ctx, &ExecuteActivityInput{})
	afterRunActivity := time.Now()

	require.NoError(t, err)
	require.Equal(t, "activity-result", ret)
	require.Len(t, tracer.options, 1)
	require.Equal(t, "RunActivity", tracer.options[0].Operation)
	require.False(t, tracer.options[0].Time.Before(beforeRunActivity))
	require.False(t, tracer.options[0].Time.After(afterRunActivity))
	require.True(t, tracer.options[0].Time.Before(serverStartedTime))
}
