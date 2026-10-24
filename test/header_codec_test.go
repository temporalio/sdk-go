package test_test

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	sdkpb "go.temporal.io/api/sdk/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type strictHeaderSigningCodec struct{ *intTestSigningCodec }

type codecKeysPropagator struct{ workflow.ContextPropagator }

func (codecKeysPropagator) EncodeHeaders() bool { return true }

func (c *strictHeaderSigningCodec) WithSerializationContext(sc converter.SerializationContext) converter.PayloadCodec {
	return &strictHeaderSigningCodec{c.intTestSigningCodec.WithSerializationContext(sc).(*intTestSigningCodec)}
}

func (c *strictHeaderSigningCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	for _, p := range payloads {
		if _, encoded := p.Metadata["ctx-signature"]; encoded {
			return nil, fmt.Errorf("header encoded twice")
		}
	}
	return c.intTestSigningCodec.Encode(payloads)
}

func headerCodecLocalActivity(ctx context.Context) (string, error) {
	return ctx.Value(contextKey(testContextKey1)).(string), nil
}

func headerCodecSignalTarget(ctx workflow.Context) error {
	if err := workflow.SetUpdateHandler(ctx, "header", func(ctx workflow.Context) (string, error) {
		return ctx.Value(contextKey(testContextKey1)).(string), nil
	}); err != nil {
		return err
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return nil
}

func headerCodecWorkflow(ctx workflow.Context, continued bool) ([]string, error) {
	if !continued {
		return nil, workflow.NewContinueAsNewError(ctx, headerCodecWorkflow, true)
	}
	if err := workflow.SetUpdateHandler(ctx, "header", func(ctx workflow.Context) (string, error) {
		return ctx.Value(contextKey(testContextKey1)).(string), nil
	}); err != nil {
		return nil, err
	}
	if err := workflow.SetQueryHandler(ctx, "header", func() (string, error) {
		return ctx.Value(contextKey(testContextKey1)).(string), nil
	}); err != nil {
		return nil, err
	}
	var workflows Workflows
	result, err := workflows.ContextPropagator(ctx, true)
	if err != nil {
		return nil, err
	}
	localCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{StartToCloseTimeout: time.Minute})
	var local string
	if err := workflow.ExecuteLocalActivity(localCtx, headerCodecLocalActivity).Get(ctx, &local); err != nil {
		return nil, err
	}
	targetID := workflow.GetInfo(ctx).WorkflowExecution.ID + "-signal"
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: targetID})
	child := workflow.ExecuteChildWorkflow(childCtx, headerCodecSignalTarget)
	if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return nil, err
	}
	if err := workflow.SignalExternalWorkflow(ctx, targetID, "", "finish", nil).Get(ctx, nil); err != nil {
		return nil, err
	}
	if err := child.Get(ctx, nil); err != nil {
		return nil, err
	}
	workflow.GetSignalChannel(ctx, "client-finish").Receive(ctx, nil)
	return append(result, local), nil
}

func (ts *IntegrationTestSuite) TestHeaderCodec() {
	recorder := tracetest.NewSpanRecorder()
	tracer := trace.NewTracerProvider(trace.WithSpanProcessor(recorder)).Tracer("header-codec")
	tracing, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{Tracer: tracer, EncodeHeaders: true})
	ts.Require().NoError(err)
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
		&strictHeaderSigningCodec{&intTestSigningCodec{}},
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	storage := newMemDriver("headers")
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = dc
		options.ContextPropagators = []workflow.ContextPropagator{codecKeysPropagator{NewKeysPropagator([]string{testContextKey1})}}
		options.Interceptors = []interceptor.ClientInterceptor{tracing}
		options.ExternalStorage = converter.ExternalStorage{
			Drivers: []converter.StorageDriver{storage}, PayloadSizeThreshold: 1,
		}
	})
	ts.Require().NoError(err)
	defer c.Close()
	queue := "header-codec-" + uuid.NewString()
	w := worker.New(c, queue, worker.Options{})
	w.RegisterWorkflow(headerCodecWorkflow)
	w.RegisterWorkflow(headerCodecSignalTarget)
	w.RegisterWorkflow(ts.workflows.ContextPropagator)
	w.RegisterActivity(ts.activities.PropagateActivity)
	w.RegisterActivity(headerCodecLocalActivity)
	ts.Require().NoError(w.Start())
	defer w.Stop()
	ctx, span := tracer.Start(context.WithValue(ts.T().Context(), contextKey(testContextKey1), "header-value"), "caller")
	defer span.End()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: uuid.NewString(), TaskQueue: queue}, headerCodecWorkflow, false)
	ts.Require().NoError(err)
	startedRunID := run.GetRunID()
	update, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: run.GetID(), UpdateName: "header", WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	ts.Require().NoError(err)
	var value string
	ts.Require().NoError(update.Get(ctx, &value))
	ts.Equal("header-value", value)
	bound := converter.WithDataConverterSerializationContext(dc,
		converter.WorkflowSerializationContext{Namespace: ts.config.Namespace, WorkflowID: run.GetID()})
	raw, err := bound.ToPayload("raw-value")
	ts.Require().NoError(err)
	propagated, err := bound.ToPayload("header-value")
	ts.Require().NoError(err)
	queryHeader := &commonpb.Header{Fields: map[string]*commonpb.Payload{
		"raw": raw, testContextKey1: propagated,
	}}
	originalHeader := proto.Clone(queryHeader)
	query, err := c.QueryWorkflowWithOptions(ctx, &client.QueryWorkflowWithOptionsRequest{
		WorkflowID: run.GetID(), QueryType: "header", Header: queryHeader,
	})
	ts.Require().NoError(err)
	ts.Require().NoError(query.QueryResult.Get(&value))
	ts.Equal("header-value", value)
	ts.True(proto.Equal(originalHeader, queryHeader))
	ts.Require().NoError(c.SignalWorkflow(ctx, run.GetID(), "", "client-finish", nil))
	var values []string
	ts.Require().NoError(run.Get(ctx, &values))
	ts.Equal([]string{"header-value", "activity_header-value", "child_header-value", "child_activity_header-value", "header-value"}, values)
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		DataConverter:      dc,
		ContextPropagators: []workflow.ContextPropagator{codecKeysPropagator{NewKeysPropagator([]string{testContextKey1})}},
		ExternalStorage:    converter.ExternalStorage{Drivers: []converter.StorageDriver{storage}},
	})
	ts.Require().NoError(err)
	replayer.RegisterWorkflow(headerCodecWorkflow)
	ts.Require().NoError(replayer.ReplayWorkflowExecution(ctx, c.WorkflowService(), nil, ts.config.Namespace,
		workflow.Execution{ID: run.GetID(), RunID: run.GetRunID()}))
	start := c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID: uuid.NewString(), TaskQueue: queue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, headerCodecSignalTarget)
	multiUpdate, err := c.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName: "header", WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	ts.Require().NoError(err)
	ts.Require().NoError(multiUpdate.Get(ctx, &value))
	ts.Equal("header-value", value)
	multiRun, err := start.Get(ctx)
	ts.Require().NoError(err)
	ts.Require().NoError(c.SignalWorkflow(ctx, multiRun.GetID(), "", "finish", nil))
	ts.Require().NoError(multiRun.Get(ctx, nil))
	ts.NotEmpty(recorder.Ended())
	history := c.GetWorkflowHistory(ctx, run.GetID(), startedRunID, false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	started, err := history.Next()
	ts.Require().NoError(err)
	headerReference := started.GetWorkflowExecutionStartedEventAttributes().GetHeader().GetFields()["_tracer-data"]
	ts.Require().NotNil(headerReference)
	var reference sdkpb.ExternalStorageReference
	ts.Require().NoError(converter.GetDefaultDataConverter().FromPayload(headerReference, &reference))
	ts.Equal(storage.Name(), reference.GetDriverName())
	storage.mu.Lock()
	stored := storage.data[reference.GetClaimData()["key"]]
	storage.mu.Unlock()
	ts.Require().NotNil(stored)
	ts.Equal(run.GetID(), string(stored.Metadata["ctx-signature"]))
	ts.Equal("binary/zlib", string(stored.Metadata[converter.MetadataEncoding]))
}
