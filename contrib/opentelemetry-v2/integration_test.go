package opentelemetry_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"

	"go.temporal.io/sdk/client"
	temporalnexus "go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	temporalotel "go.temporal.io/sdk/contrib/opentelemetry-v2"
	"go.temporal.io/sdk/contrib/opentelemetry-v2/internal/spantree"
)

const (
	integrationTaskQueue = "opentelemetry-v2-integration"
	externalWorkflowID   = "externalWorkflowWithSignal"
	nexusEndpointName    = "opentelemetry-v2-integration-endpoint"
	nexusServiceName     = "opentelemetry-v2-integration-service"
	nexusOperationName   = "nexusHandlerWorkflow"
	nexusCancelOpName    = "nexusCancelHandlerWorkflow"
	scheduleID           = "otel-schedule"
)

func activity(ctx context.Context) error {
	_, span := otel.Tracer("activity").Start(ctx, "activity-span")
	defer span.End()
	return nil
}

func localActivity(ctx context.Context) error {
	_, span := otel.Tracer("localActivity").Start(ctx, "local-activity-span")
	defer span.End()
	return nil
}

func externalWorkflowWithSignal(ctx workflow.Context) error {
	_, span := temporalotel.NewTracer(otel.GetTracerProvider(), "externalWorkflowWithSignal").Start(ctx, "external-workflow-with-signal-span")
	defer span.End()

	workflow.GetSignalChannel(ctx, "externalSignal").Receive(ctx, nil)
	return nil
}

func childWorkflowWithSignal(ctx workflow.Context) error {
	_, span := temporalotel.NewTracer(otel.GetTracerProvider(), "childWorkflowWithSignal").Start(ctx, "child-workflow-with-signal-span")
	defer span.End()

	workflow.GetSignalChannel(ctx, "childSignal").Receive(ctx, nil)
	return nil
}

func nexusHandlerWorkflow(ctx workflow.Context, _ nexus.NoValue) (nexus.NoValue, error) {
	_, span := temporalotel.NewTracer(otel.GetTracerProvider(), "workflowWithNexusHandler").Start(ctx, "workflow-with-nexus-handler-span")
	defer span.End()

	return nil, nil
}

func nexusCancelHandlerWorkflow(ctx workflow.Context, _ nexus.NoValue) (nexus.NoValue, error) {
	_, span := temporalotel.NewTracer(otel.GetTracerProvider(), "nexusCancelHandlerWorkflow").Start(ctx, "nexus-cancel-handler-span")
	defer span.End()

	return nil, workflow.Await(ctx, func() bool { return false })
}

func comprehensiveWorkflow(ctx workflow.Context, finalRun bool) error {
	tracer := temporalotel.NewTracer(otel.GetTracerProvider(), "comprehensiveWorkflow")

	_, span := tracer.Start(ctx, "comprehensive-outbound-workflow-span")
	defer span.End()

	if finalRun {
		return nil
	}

	// Queries require unsequenced spans because they run outside history.
	err := workflow.SetQueryHandler(ctx, "getStatus", func() (string, error) {
		queryCtx, span := tracer.StartUnsequenced(ctx, "query-handler-span")
		defer span.End()
		_, child := tracer.StartUnsequenced(queryCtx, "query-handler-child-span")
		defer child.End()
		return "ok", nil
	})
	if err != nil {
		return err
	}

	// Validators are unsequenced; update handlers are sequenced.
	err = workflow.SetUpdateHandlerWithOptions(ctx, "testUpdate",
		func(ctx workflow.Context) error {
			_, span := tracer.Start(ctx, "update-handler-span")
			defer span.End()
			return nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context) error {
				_, span := tracer.StartUnsequenced(ctx, "validate-update-span")
				defer span.End()
				return nil
			},
		})
	if err != nil {
		return err
	}

	// Wait for client operations before exercising the remaining outbound paths.
	workflow.GetSignalChannel(ctx, "proceed").Receive(ctx, nil)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{StartToCloseTimeout: 10 * time.Second})

	err = workflow.ExecuteActivity(ctx, activity).Get(ctx, nil)
	if err != nil {
		return err
	}

	err = workflow.ExecuteLocalActivity(ctx, localActivity).Get(ctx, nil)
	if err != nil {
		return err
	}

	child := workflow.ExecuteChildWorkflow(ctx, childWorkflowWithSignal)
	err = child.SignalChildWorkflow(ctx, "childSignal", nil).Get(ctx, nil)
	if err != nil {
		return err
	}
	if err = child.Get(ctx, nil); err != nil {
		return err
	}

	err = workflow.SignalExternalWorkflow(ctx, externalWorkflowID, "", "externalSignal", nil).Get(ctx, nil)
	if err != nil {
		return err
	}

	nexusClient := workflow.NewNexusClient(nexusEndpointName, nexusServiceName)
	err = nexusClient.ExecuteOperation(ctx, nexusOperationName, nil, workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
	}).Get(ctx, nil)
	if err != nil {
		return err
	}

	// Cancel an active operation to exercise the inbound cancellation handler.
	cancelCtx, cancelNexus := workflow.WithCancel(ctx)
	cancelFut := nexusClient.ExecuteOperation(cancelCtx, nexusCancelOpName, nil, workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
	})
	var cancelExec workflow.NexusOperationExecution
	if err = cancelFut.GetNexusOperationExecution().Get(ctx, &cancelExec); err != nil {
		return err
	}
	cancelNexus()
	// Cancellation is expected.
	_ = cancelFut.Get(ctx, nil)

	return workflow.NewContinueAsNewError(ctx, comprehensiveWorkflow, true)
}

// standaloneActivity exercises client-root activity spans.
func standaloneActivity(ctx context.Context) error {
	return nil
}

// standaloneWorkflow exercises workflow spans and scheduled starts.
func standaloneWorkflow(ctx workflow.Context) error {
	return nil
}

func signalWithStartTarget(ctx workflow.Context) error {
	_, span := temporalotel.NewTracer(otel.GetTracerProvider(), "signalWithStartTarget").Start(ctx, "signal-with-start-target-span")
	defer span.End()

	workflow.GetSignalChannel(ctx, "startSignal").Receive(ctx, nil)
	return nil
}

func updateTargetWorkflow(ctx workflow.Context) error {
	_, span := temporalotel.NewTracer(otel.GetTracerProvider(), "updateTargetWorkflow").Start(ctx, "update-target-workflow-span")
	defer span.End()

	done := false
	err := workflow.SetUpdateHandler(ctx, "doUpdate", func(ctx workflow.Context) error {
		done = true
		return nil
	})
	if err != nil {
		return err
	}
	return workflow.Await(ctx, func() bool { return done })
}

// nexusOp starts nexusHandlerWorkflow.
var nexusOp = temporalnexus.NewWorkflowRunOperation(
	nexusOperationName,
	nexusHandlerWorkflow,
	func(ctx context.Context, _ nexus.NoValue, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		return client.StartWorkflowOptions{ID: "nexus-handler-" + soo.RequestID}, nil
	},
)

// nexusCancelOp starts nexusCancelHandlerWorkflow.
var nexusCancelOp = temporalnexus.NewWorkflowRunOperation(
	nexusCancelOpName,
	nexusCancelHandlerWorkflow,
	func(ctx context.Context, _ nexus.NoValue, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		return client.StartWorkflowOptions{ID: "nexus-cancel-handler-" + soo.RequestID}, nil
	},
)

// devServer starts a local server or skips the test.
func devServer(t *testing.T) *testsuite.DevServer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Skipf("dev server unavailable (capability skip): %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return srv
}

// runScenario returns spans from the full tracing scenario.
func runScenario(t *testing.T, srv *testsuite.DevServer, installPlugin bool, tracerOpts temporalotel.TracerOptions) []sdktrace.ReadOnlySpan {
	recorder := tracetest.NewSpanRecorder()

	processor := sdktrace.WithSpanProcessor(recorder)

	// The global provider records both user and plugin spans.
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(temporalotel.NewTracerProvider(processor))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	clientOptions := client.Options{HostPort: srv.FrontendHostPort()}
	if installPlugin {
		plugin, shutdown, err := temporalotel.NewPlugin(temporalotel.PluginOptions{
			TracerOptions:   tracerOpts,
			ProviderOptions: []sdktrace.TracerProviderOption{processor},
		})
		require.NoError(t, err)
		defer func() { _ = shutdown(context.Background()) }()
		clientOptions.Plugins = []client.Plugin{plugin}
	}

	dialCtx := context.Background()
	c, err := client.DialContext(dialCtx, clientOptions)
	require.NoError(t, err)
	defer c.Close()

	// All client calls share this parent span.
	ctx, clientSpan := otel.Tracer("client").Start(context.Background(), "client-span")

	_, err = c.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: nexusEndpointName,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: "default",
						TaskQueue: integrationTaskQueue,
					},
				},
			},
		},
	})
	require.NoError(t, err)

	w := worker.New(c, integrationTaskQueue, worker.Options{})
	w.RegisterWorkflow(comprehensiveWorkflow)
	w.RegisterWorkflow(childWorkflowWithSignal)
	w.RegisterWorkflow(externalWorkflowWithSignal)
	w.RegisterWorkflow(nexusHandlerWorkflow)
	w.RegisterWorkflow(nexusCancelHandlerWorkflow)
	w.RegisterWorkflow(standaloneWorkflow)
	w.RegisterWorkflow(signalWithStartTarget)
	w.RegisterWorkflow(updateTargetWorkflow)
	w.RegisterActivity(activity)
	w.RegisterActivity(localActivity)
	w.RegisterActivity(standaloneActivity)

	service := nexus.NewService(nexusServiceName)
	require.NoError(t, service.Register(nexusOp, nexusCancelOp))
	w.RegisterNexusService(service)

	require.NoError(t, w.Start())

	// Start the external signal target first.
	external, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        externalWorkflowID,
		TaskQueue: integrationTaskQueue,
	}, externalWorkflowWithSignal)
	require.NoError(t, err)

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "comprehensive-outbound",
		TaskQueue: integrationTaskQueue,
	}, comprehensiveWorkflow, false)
	require.NoError(t, err)

	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   "comprehensive-outbound",
		UpdateName:   "testUpdate",
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err)
	require.NoError(t, updateHandle.Get(ctx, nil))

	val, err := c.QueryWorkflow(ctx, "comprehensive-outbound", "", "getStatus")
	require.NoError(t, err)
	var status string
	require.NoError(t, val.Get(&status))
	require.Equal(t, "ok", status)

	require.NoError(t, c.SignalWorkflow(ctx, "comprehensive-outbound", "", "proceed", nil))

	require.NoError(t, run.Get(ctx, nil))
	require.NoError(t, external.Get(ctx, nil))

	actHandle, err := c.ExecuteActivity(ctx, client.StartActivityOptions{
		ID:                  "otel-standalone-activity-" + uuid.NewString(),
		TaskQueue:           integrationTaskQueue,
		StartToCloseTimeout: 10 * time.Second,
	}, standaloneActivity)
	require.NoError(t, err)
	require.NoError(t, actHandle.Get(ctx, nil))

	standaloneRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "otel-standalone-workflow-" + uuid.NewString(),
		TaskQueue: integrationTaskQueue,
	}, standaloneWorkflow)
	require.NoError(t, err)
	require.NoError(t, standaloneRun.Get(ctx, nil))

	scheduleHandle, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   scheduleID,
		Spec: client.ScheduleSpec{},
		Action: &client.ScheduleWorkflowAction{
			ID:        "otel-schedule-workflow-" + uuid.NewString(),
			Workflow:  standaloneWorkflow,
			TaskQueue: integrationTaskQueue,
		},
	})
	require.NoError(t, err)
	defer func() { _ = scheduleHandle.Delete(context.Background()) }()

	swsRun, err := c.SignalWithStartWorkflow(ctx, "otel-signal-with-start-"+uuid.NewString(),
		"startSignal", nil, client.StartWorkflowOptions{
			TaskQueue: integrationTaskQueue,
		}, signalWithStartTarget)
	require.NoError(t, err)
	require.NoError(t, swsRun.Get(ctx, nil))

	startOp := c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       "otel-update-with-start-" + uuid.NewString(),
		TaskQueue:                integrationTaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, updateTargetWorkflow)
	updateWithStartHandle, err := c.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: startOp,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName:   "doUpdate",
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	require.NoError(t, err)
	require.NoError(t, updateWithStartHandle.Get(ctx, nil))

	clientSpan.End()
	w.Stop()

	return recorder.Ended()
}

// fullTree is the span tree with all tracing enabled.
var fullTree = []string{
	// Client operations share one root span.
	"client-span",
	// The external workflow links client, worker, and user spans.
	"  StartWorkflow:externalWorkflowWithSignal",
	"    RunWorkflow:externalWorkflowWithSignal",
	"      external-workflow-with-signal-span",
	// Headers link outbound spans to downstream inbound spans.
	"  StartWorkflow:comprehensiveWorkflow",
	"    RunWorkflow:comprehensiveWorkflow",
	"      StartActivity:activity",
	"        RunActivity:activity",
	"          activity-span",
	"      query-handler-span",
	"        query-handler-child-span",
	"      StartActivity:localActivity",
	"        RunActivity:localActivity",
	"          local-activity-span",
	"      StartChildWorkflow:childWorkflowWithSignal",
	"        RunWorkflow:childWorkflowWithSignal",
	"          child-workflow-with-signal-span",
	"      SignalChildWorkflow:childSignal",
	"        HandleSignal:childSignal",
	"      SignalExternalWorkflow:externalSignal",
	"        HandleSignal:externalSignal",
	"      StartNexusOperation:" + nexusServiceName + "/nexusHandlerWorkflow",
	"        RunStartNexusOperationHandler:" + nexusServiceName + "/nexusHandlerWorkflow",
	"          StartWorkflow:nexusHandlerWorkflow",
	"            RunWorkflow:nexusHandlerWorkflow",
	"              workflow-with-nexus-handler-span",
	// Start and cancellation handlers share the outbound operation parent.
	"      StartNexusOperation:" + nexusServiceName + "/nexusCancelHandlerWorkflow",
	"        RunStartNexusOperationHandler:" + nexusServiceName + "/nexusCancelHandlerWorkflow",
	"          StartWorkflow:nexusCancelHandlerWorkflow",
	"            RunWorkflow:nexusCancelHandlerWorkflow",
	"              nexus-cancel-handler-span",
	"        RunCancelNexusOperationHandler:" + nexusServiceName + "/nexusCancelHandlerWorkflow",
	// Continue-as-new links the outbound, continued-run, and user spans.
	"      ContinueAsNew:comprehensiveWorkflow",
	"        RunWorkflow:comprehensiveWorkflow",
	"          comprehensive-outbound-workflow-span",
	"      comprehensive-outbound-workflow-span",
	// Query user spans inherit the captured workflow context, not HandleQuery.
	"  UpdateWorkflow:testUpdate",
	"    ValidateUpdate:testUpdate",
	"      validate-update-span",
	"    HandleUpdate:testUpdate",
	"      update-handler-span",
	"  QueryWorkflow:getStatus",
	"    HandleQuery:getStatus",
	"  SignalWorkflow:proceed",
	"    HandleSignal:proceed",
	// Headers link standalone StartActivity and RunActivity spans.
	"  StartActivity:standaloneActivity",
	"    RunActivity:standaloneActivity",
	"  StartWorkflow:standaloneWorkflow",
	"    RunWorkflow:standaloneWorkflow",
	"  CreateSchedule:" + scheduleID,
	// Signal-with-start links client, worker, signal, and user spans.
	"  SignalWithStartWorkflow:signalWithStartTarget",
	"    RunWorkflow:signalWithStartTarget",
	"      signal-with-start-target-span",
	"    HandleSignal:startSignal",
	// Update-with-start links validation, execution, worker, and user spans.
	"  UpdateWithStartWorkflow:doUpdate",
	"    ValidateUpdate:doUpdate",
	"    HandleUpdate:doUpdate",
	"    RunWorkflow:updateTargetWorkflow",
	"      update-target-workflow-span",
}

// noPluginTree contains only user spans, without interceptor links.
var noPluginTree = []string{
	// The in-process client span has no children.
	"client-span",
	"external-workflow-with-signal-span",
	"comprehensive-outbound-workflow-span",
	"comprehensive-outbound-workflow-span",
	"query-handler-span",
	"  query-handler-child-span",
	"activity-span",
	"local-activity-span",
	"child-workflow-with-signal-span",
	"workflow-with-nexus-handler-span",
	"nexus-cancel-handler-span",
	"validate-update-span",
	"update-handler-span",
	"signal-with-start-target-span",
	"update-target-workflow-span",
}

// disabledTree omits signal, query, and update spans. Their user spans become
// roots, while SignalWithStartWorkflow remains.
var disabledTree = []string{
	"client-span",
	"  StartWorkflow:externalWorkflowWithSignal",
	"    RunWorkflow:externalWorkflowWithSignal",
	"      external-workflow-with-signal-span",
	"  StartWorkflow:comprehensiveWorkflow",
	"    RunWorkflow:comprehensiveWorkflow",
	"      StartActivity:activity",
	"        RunActivity:activity",
	"          activity-span",
	"      query-handler-span",
	"        query-handler-child-span",
	"      StartActivity:localActivity",
	"        RunActivity:localActivity",
	"          local-activity-span",
	"      StartChildWorkflow:childWorkflowWithSignal",
	"        RunWorkflow:childWorkflowWithSignal",
	"          child-workflow-with-signal-span",
	"      StartNexusOperation:" + nexusServiceName + "/nexusHandlerWorkflow",
	"        RunStartNexusOperationHandler:" + nexusServiceName + "/nexusHandlerWorkflow",
	"          StartWorkflow:nexusHandlerWorkflow",
	"            RunWorkflow:nexusHandlerWorkflow",
	"              workflow-with-nexus-handler-span",
	"      StartNexusOperation:" + nexusServiceName + "/nexusCancelHandlerWorkflow",
	"        RunStartNexusOperationHandler:" + nexusServiceName + "/nexusCancelHandlerWorkflow",
	"          StartWorkflow:nexusCancelHandlerWorkflow",
	"            RunWorkflow:nexusCancelHandlerWorkflow",
	"              nexus-cancel-handler-span",
	"        RunCancelNexusOperationHandler:" + nexusServiceName + "/nexusCancelHandlerWorkflow",
	"      ContinueAsNew:comprehensiveWorkflow",
	"        RunWorkflow:comprehensiveWorkflow",
	"          comprehensive-outbound-workflow-span",
	"      comprehensive-outbound-workflow-span",
	// Re-rooted after update parents are dropped.
	"validate-update-span",
	"update-handler-span",
	"  StartActivity:standaloneActivity",
	"    RunActivity:standaloneActivity",
	"  StartWorkflow:standaloneWorkflow",
	"    RunWorkflow:standaloneWorkflow",
	"  CreateSchedule:" + scheduleID,
	// SignalWithStartWorkflow remains.
	"  SignalWithStartWorkflow:signalWithStartTarget",
	"    RunWorkflow:signalWithStartTarget",
	"      signal-with-start-target-span",
	// Re-rooted after UpdateWithStartWorkflow is dropped.
	"RunWorkflow:updateTargetWorkflow",
	"  update-target-workflow-span",
}

func TestComprehensive(t *testing.T) {
	worker.SetStickyWorkflowCacheSize(0)
	t.Cleanup(worker.PurgeStickyWorkflowCache)

	t.Run("full", func(t *testing.T) {
		spans := runScenario(t, devServer(t), true, temporalotel.TracerOptions{})
		require.ElementsMatch(t, fullTree, spantree.Format(spans))
	})

	t.Run("no-plugin", func(t *testing.T) {
		spans := runScenario(t, devServer(t), false, temporalotel.TracerOptions{})
		require.ElementsMatch(t, noPluginTree, spantree.Format(spans))
	})

	t.Run("all-disabled", func(t *testing.T) {
		spans := runScenario(t, devServer(t), true, temporalotel.TracerOptions{
			DisableSignalTracing: true,
			DisableQueryTracing:  true,
			DisableUpdateTracing: true,
			DisableBaggage:       true,
		})
		require.ElementsMatch(t, disabledTree, spantree.Format(spans))
	})
}
