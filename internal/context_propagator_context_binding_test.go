package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

type propagationBindingConverter struct {
	converter.DataConverter
	sc converter.SerializationContext
}

func (d *propagationBindingConverter) WithSerializationContext(sc converter.SerializationContext) converter.DataConverter {
	return &propagationBindingConverter{DataConverter: d.DataConverter, sc: sc}
}

func (d *propagationBindingConverter) WithContext(ctx context.Context) converter.DataConverter {
	return &propagationBoundConverter{DataConverter: d.DataConverter, ctx: ctx, sc: d.sc}
}

func (d *propagationBindingConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	return &propagationBoundConverter{DataConverter: d.DataConverter, ctx: ctx, sc: d.sc}
}

// Deliberately does not implement ContextAware: binding is allowed to be terminal.
type propagationBoundConverter struct {
	converter.DataConverter
	ctx any
	sc  converter.SerializationContext
}

type propagationBindingExpectation struct {
	ctx any
	sc  converter.SerializationContext
}

func (d *propagationBoundConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if expected, ok := valuePtr.(*propagationBindingExpectation); ok {
		if d.ctx != expected.ctx {
			return fmt.Errorf("header converter is bound to an outbound or pre-extraction context")
		}
		if d.sc != expected.sc {
			return fmt.Errorf("header serialization context = %#v, want %#v", d.sc, expected.sc)
		}
		return nil
	}
	return d.DataConverter.FromPayload(payload, valuePtr)
}

type propagationBindingPropagator struct {
	keysPropagator
	dc converter.DataConverter
}

func (p *propagationBindingPropagator) WithDataConverter(dc converter.DataConverter) ContextPropagator {
	bound := *p
	bound.dc = dc
	return &bound
}

func (p *propagationBindingPropagator) check(ctx any, sc converter.SerializationContext) error {
	payload, err := converter.GetDefaultDataConverter().ToPayload("binding")
	if err != nil {
		return err
	}
	return p.dc.FromPayload(payload, &propagationBindingExpectation{ctx: ctx, sc: sc})
}

func (p *propagationBindingPropagator) Extract(ctx context.Context, _ HeaderReader) (context.Context, error) {
	info := GetActivityInfo(ctx)
	return ctx, p.check(ctx, converter.ActivitySerializationContext{
		Namespace: info.WorkflowNamespace, WorkflowID: info.WorkflowExecution.ID,
		WorkflowType: info.WorkflowType.Name, ActivityType: info.ActivityType.Name,
		TaskQueue: info.TaskQueue, IsLocal: info.IsLocalActivity,
	})
}

func (p *propagationBindingPropagator) ExtractToWorkflow(ctx Context, _ HeaderReader) (Context, error) {
	info := GetWorkflowInfo(ctx)
	return ctx, p.check(ctx, converter.WorkflowSerializationContext{
		Namespace: info.Namespace, WorkflowID: info.WorkflowExecution.ID,
	})
}

func TestContextPropagatorInboundConverterBinding(t *testing.T) {
	for _, operation := range []string{"activity", "local-activity", "child"} {
		t.Run(operation, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetDataConverter(&propagationBindingConverter{DataConverter: converter.GetDefaultDataConverter()})
			env.SetContextPropagators([]ContextPropagator{
				&keysPropagator{keys: []string{"tenant"}},
				&propagationBindingPropagator{},
			})
			tenant, err := converter.GetDefaultDataConverter().ToPayload("initial")
			require.NoError(t, err)
			env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"tenant": tenant}})
			env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "parent"})
			activity := func(ctx context.Context) (string, error) {
				return ctx.Value(contextKey("tenant")).(string), nil
			}
			child := func(ctx Context) (string, error) {
				return ctx.Value(contextKey("tenant")).(string), nil
			}
			env.RegisterActivity(activity)
			env.RegisterWorkflow(child)
			env.ExecuteWorkflow(func(ctx Context) (string, error) {
				ctx = WithValue(ctx, contextKey("tenant"), "outbound")
				var result string
				var err error
				switch operation {
				case "activity":
					ctx = WithActivityOptions(ctx, ActivityOptions{
						StartToCloseTimeout: time.Minute, RetryPolicy: &RetryPolicy{MaximumAttempts: 1},
					})
					err = ExecuteActivity(ctx, activity).Get(ctx, &result)
				case "local-activity":
					ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
						StartToCloseTimeout: time.Minute, RetryPolicy: &RetryPolicy{MaximumAttempts: 1},
					})
					err = ExecuteLocalActivity(ctx, activity).Get(ctx, &result)
				case "child":
					ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{WorkflowID: "child"})
					err = ExecuteChildWorkflow(ctx, child).Get(ctx, &result)
				}
				return result, err
			})
			require.NoError(t, env.GetWorkflowError())
			var result string
			require.NoError(t, env.GetWorkflowResult(&result))
			require.Equal(t, "outbound", result)
		})
	}
}

func TestContextPropagatorLocalActivityHandlerConverterBinding(t *testing.T) {
	root := &propagationBindingConverter{DataConverter: converter.GetDefaultDataConverter()}
	sc := converter.ActivitySerializationContext{
		Namespace: "namespace", WorkflowID: "workflow", WorkflowType: "workflow-type",
		ActivityType: "activity", TaskQueue: "task-queue", IsLocal: true,
	}
	outbound := WithWorkflowContext(Background(), converter.WithDataConverterSerializationContext(root, sc))
	tenant, err := converter.GetDefaultDataConverter().ToPayload("propagated")
	require.NoError(t, err)
	params := ExecuteLocalActivityParams{
		ExecuteLocalActivityOptions: ExecuteLocalActivityOptions{StartToCloseTimeout: time.Minute},
		ActivityType:                sc.ActivityType,
		ActivityFn: func(ctx context.Context) (string, error) {
			return ctx.Value(contextKey("tenant")).(string), nil
		},
		WorkflowInfo: &WorkflowInfo{
			Namespace: sc.Namespace, WorkflowExecution: WorkflowExecution{ID: sc.WorkflowID},
			WorkflowType: WorkflowType{Name: sc.WorkflowType}, TaskQueueName: sc.TaskQueue,
		},
		DataConverter: outbound,
		Header:        &commonpb.Header{Fields: map[string]*commonpb.Payload{"tenant": tenant}},
		Attempt:       1,
		ScheduledTime: time.Now(),
	}
	handler := localActivityTaskHandler{
		backgroundContext: t.Context(),
		dataConverter:     root,
		metricsHandler:    metrics.NopHandler,
		logger:            ilog.NewNopLogger(),
		contextPropagators: []ContextPropagator{
			&keysPropagator{keys: []string{"tenant"}},
			&propagationBindingPropagator{},
		},
	}
	result := handler.executeLocalActivityTask(newLocalActivityTask(params, func(*LocalActivityResultWrapper) {}, "1"))
	require.NoError(t, result.err)
	var decoded string
	require.NoError(t, outbound.FromPayloads(result.result, &decoded))
	require.Equal(t, "propagated", decoded)
}
