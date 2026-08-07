package opentelemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor/tracing"
	temporalnexus "go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	integrationTaskQueue      = "opentelemetry-v2-integration"
	externalWorkflowID        = "externalWorkflowWithSignal"
	nexusEndpointName         = "opentelemetry-v2-integration-endpoint"
	nexusServiceName          = "opentelemetry-v2-integration-service"
	nexusOperationName        = "nexusHandlerWorkflow"
	nexusCancelOpName         = "nexusCancelHandlerWorkflow"
	scheduleID                = "otel-schedule"
	comprehensiveWorkflowID   = "comprehensive-outbound"
	comprehensiveUpdateID     = "comprehensive-update"
	updateWithStartWorkflowID = "otel-update-with-start"
	updateWithStartUpdateID   = "comprehensive-update-with-start"
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
	_, span := Tracer("externalWorkflowWithSignal").Start(ctx, "external-workflow-with-signal-span")
	defer span.End()

	workflow.GetSignalChannel(ctx, "externalSignal").Receive(ctx, nil)
	return nil
}

func childWorkflowWithSignal(ctx workflow.Context) error {
	_, span := Tracer("childWorkflowWithSignal").Start(ctx, "child-workflow-with-signal-span")
	defer span.End()

	workflow.GetSignalChannel(ctx, "childSignal").Receive(ctx, nil)
	return nil
}

func nexusHandlerWorkflow(ctx workflow.Context, _ nexus.NoValue) (nexus.NoValue, error) {
	_, span := Tracer("workflowWithNexusHandler").Start(ctx, "workflow-with-nexus-handler-span")
	defer span.End()

	return nil, nil
}

func nexusCancelHandlerWorkflow(ctx workflow.Context, _ nexus.NoValue) (nexus.NoValue, error) {
	_, span := Tracer("nexusCancelHandlerWorkflow").Start(ctx, "nexus-cancel-handler-span")
	defer span.End()

	return nil, workflow.Await(ctx, func() bool { return false })
}

func comprehensiveWorkflow(ctx workflow.Context, finalRun bool) error {
	tracer := Tracer("comprehensiveWorkflow")

	_, span := tracer.Start(ctx, "comprehensive-outbound-workflow-span")
	defer span.End()

	if finalRun {
		return nil
	}

	err := workflow.SetQueryHandler(ctx, "getStatus", func() (string, error) {
		queryCtx, span := tracer.Start(ctx, "query-handler-span")
		defer span.End()
		_, child := tracer.Start(queryCtx, "query-handler-child-span")
		defer child.End()
		return "ok", nil
	})
	if err != nil {
		return err
	}

	err = workflow.SetUpdateHandlerWithOptions(ctx, "testUpdate",
		func(uctx workflow.Context) error {
			updateCtx, span := tracer.Start(uctx, "update-handler-span")
			defer span.End()
			_, child := tracer.Start(updateCtx, "update-handler-child-span")
			defer child.End()
			return nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context) error {
				validateCtx, span := tracer.Start(ctx, "validate-update-span")
				defer span.End()
				_, child := tracer.Start(validateCtx, "validate-update-span-child")
				defer child.End()
				return nil
			},
		})
	if err != nil {
		return err
	}

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
	_, span := Tracer("signalWithStartTarget").Start(ctx, "signal-with-start-target-span")
	defer span.End()

	workflow.GetSignalChannel(ctx, "startSignal").Receive(ctx, nil)
	return nil
}

func updateTargetWorkflow(ctx workflow.Context) error {
	_, span := Tracer("updateTargetWorkflow").Start(ctx, "update-target-workflow-span")
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

type integrationTestSuite struct {
	otelTestSuite
}

func TestIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(integrationTestSuite))
}

func (s *integrationTestSuite) runScenario(
	srv *testsuite.DevServer,
	pluginOpts PluginOptions,
) []sdktrace.ReadOnlySpan {
	recorder := s.newSpanRecorder()

	plugin, err := NewPlugin(pluginOpts)
	s.Require().NoError(err)

	clientOptions := client.Options{
		HostPort: srv.FrontendHostPort(),
		Plugins:  []client.Plugin{plugin},
	}

	dialCtx := context.Background()
	c, err := client.DialContext(dialCtx, clientOptions)
	s.Require().NoError(err)
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
	s.Require().NoError(err)

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
	s.Require().NoError(service.Register(nexusOp, nexusCancelOp))
	w.RegisterNexusService(service)

	s.Require().NoError(w.Start())

	// Start the external signal target first.
	external, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        externalWorkflowID,
		TaskQueue: integrationTaskQueue,
	}, externalWorkflowWithSignal)
	s.Require().NoError(err)

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        comprehensiveWorkflowID,
		TaskQueue: integrationTaskQueue,
	}, comprehensiveWorkflow, false)
	s.Require().NoError(err)

	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		UpdateID:     comprehensiveUpdateID,
		WorkflowID:   comprehensiveWorkflowID,
		UpdateName:   "testUpdate",
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	s.Require().NoError(err)
	s.Require().NoError(updateHandle.Get(ctx, nil))

	val, err := c.QueryWorkflow(ctx, comprehensiveWorkflowID, "", "getStatus")
	s.Require().NoError(err)
	var status string
	s.Require().NoError(val.Get(&status))
	s.Require().Equal("ok", status)

	s.Require().NoError(c.SignalWorkflow(ctx, comprehensiveWorkflowID, "", "proceed", nil))

	s.Require().NoError(run.Get(ctx, nil))
	s.Require().NoError(external.Get(ctx, nil))

	actHandle, err := c.ExecuteActivity(ctx, client.StartActivityOptions{
		ID:                  "otel-standalone-activity-" + uuid.NewString(),
		TaskQueue:           integrationTaskQueue,
		StartToCloseTimeout: 10 * time.Second,
	}, standaloneActivity)
	s.Require().NoError(err)
	s.Require().NoError(actHandle.Get(ctx, nil))

	standaloneRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "otel-standalone-workflow-" + uuid.NewString(),
		TaskQueue: integrationTaskQueue,
	}, standaloneWorkflow)
	s.Require().NoError(err)
	s.Require().NoError(standaloneRun.Get(ctx, nil))

	scheduleHandle, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   scheduleID,
		Spec: client.ScheduleSpec{},
		Action: &client.ScheduleWorkflowAction{
			ID:        "otel-schedule-workflow-" + uuid.NewString(),
			Workflow:  standaloneWorkflow,
			TaskQueue: integrationTaskQueue,
		},
	})
	s.Require().NoError(err)
	defer func() { _ = scheduleHandle.Delete(context.Background()) }()

	swsRun, err := c.SignalWithStartWorkflow(ctx, "otel-signal-with-start-"+uuid.NewString(),
		"startSignal", nil, client.StartWorkflowOptions{
			TaskQueue: integrationTaskQueue,
		}, signalWithStartTarget)
	s.Require().NoError(err)
	s.Require().NoError(swsRun.Get(ctx, nil))

	startOp := c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       updateWithStartWorkflowID,
		TaskQueue:                integrationTaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, updateTargetWorkflow)
	updateWithStartHandle, err := c.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: startOp,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateID:     updateWithStartUpdateID,
			WorkflowID:   updateWithStartWorkflowID,
			UpdateName:   "doUpdate",
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	s.Require().NoError(err)
	s.Require().NoError(updateWithStartHandle.Get(ctx, nil))

	clientSpan.End()
	w.Stop()

	return recorder.Ended()
}

func (s *integrationTestSuite) requireUpdateIDs(spans []sdktrace.ReadOnlySpan) {
	s.T().Helper()
	for _, expected := range []struct {
		spanName   string
		workflowID string
		updateID   string
	}{
		{"UpdateWorkflow:testUpdate", comprehensiveWorkflowID, comprehensiveUpdateID},
		{"UpdateWithStartWorkflow:doUpdate", updateWithStartWorkflowID, updateWithStartUpdateID},
	} {
		span := s.requireSpanNamed(spans, expected.spanName)
		s.Require().Equal(expected.workflowID, s.requireSpanAttribute(span, "temporalWorkflowID").AsString())
		s.Require().Equal(expected.updateID, s.requireSpanAttribute(span, "temporalUpdateID").AsString())
	}
}

func (s *integrationTestSuite) requireContinueAsNewErrorNotRecorded(spans []sdktrace.ReadOnlySpan) {
	s.T().Helper()
	runSpan := s.requireSpanNamed(spans, "RunWorkflow:comprehensiveWorkflow")
	s.Require().Equal(codes.Unset, runSpan.Status().Code)
	s.Require().Empty(runSpan.Events())
}

func formatSpanTree(spans []sdktrace.ReadOnlySpan) []string {
	var tree []string
	var walk func(trace.SpanID, int)
	walk = func(parentID trace.SpanID, depth int) {
		for _, span := range spans {
			if span.Parent().SpanID() != parentID {
				continue
			}
			tree = append(tree, strings.Repeat("  ", depth)+span.Name())
			walk(span.SpanContext().SpanID(), depth+1)
		}
	}
	walk(trace.SpanID{}, 0)
	return tree
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
	"      query-handler-span",
	"        query-handler-child-span",
	"      StartActivity:activity",
	"        RunActivity:activity",
	"          activity-span",
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
	// Update user spans follow their current inbound operation.
	"  UpdateWorkflow:testUpdate",
	"    ValidateUpdate:testUpdate",
	"      validate-update-span",
	"        validate-update-span-child",
	"    HandleUpdate:testUpdate",
	"      update-handler-span",
	"        update-handler-child-span",
	// Ambient read-only state reparents captured query contexts.
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
	"    HandleSignal:startSignal",
	"    RunWorkflow:signalWithStartTarget",
	"      signal-with-start-target-span",
	// Update-with-start links validation, execution, worker, and user spans.
	"  UpdateWithStartWorkflow:doUpdate",
	"    ValidateUpdate:doUpdate",
	"    HandleUpdate:doUpdate",
	"    RunWorkflow:updateTargetWorkflow",
	"      update-target-workflow-span",
}

// disabledTree omits signal, query, and update spans. Their user spans become
// roots, while SignalWithStartWorkflow remains.
var disabledTree = []string{
	// Re-rooted after update parents are dropped.
	"validate-update-span",
	"  validate-update-span-child",
	"update-handler-span",
	"  update-handler-child-span",
	// Re-rooted after UpdateWithStartWorkflow is dropped.
	"RunWorkflow:updateTargetWorkflow",
	"  update-target-workflow-span",
	"client-span",
	"  StartWorkflow:externalWorkflowWithSignal",
	"    RunWorkflow:externalWorkflowWithSignal",
	"      external-workflow-with-signal-span",
	"  StartWorkflow:comprehensiveWorkflow",
	"    RunWorkflow:comprehensiveWorkflow",
	"      query-handler-span",
	"        query-handler-child-span",
	"      StartActivity:activity",
	"        RunActivity:activity",
	"          activity-span",
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
	// Headers link standalone StartActivity and RunActivity spans.
	"  StartActivity:standaloneActivity",
	"    RunActivity:standaloneActivity",
	"  StartWorkflow:standaloneWorkflow",
	"    RunWorkflow:standaloneWorkflow",
	"  CreateSchedule:" + scheduleID,
	// SignalWithStartWorkflow remains.
	"  SignalWithStartWorkflow:signalWithStartTarget",
	"    RunWorkflow:signalWithStartTarget",
	"      signal-with-start-target-span",
}

func (s *integrationTestSuite) TestComprehensive() {
	s.Run("full", func() {
		spans := s.runScenario(s.devServer(), PluginOptions{})
		s.Require().Equal(fullTree, formatSpanTree(spans))
		s.requireUniqueSpanIDs(spans)
		s.requireUpdateIDs(spans)
		s.requireContinueAsNewErrorNotRecorded(spans)
	})

	s.Run("all-disabled", func() {
		spans := s.runScenario(s.devServer(), PluginOptions{
			TracerOptions: tracing.TracerOptions{
				DisableSignalTracing: true,
				DisableQueryTracing:  true,
				DisableUpdateTracing: true,
			},
			DisableBaggage: true,
		})
		s.Require().Equal(disabledTree, formatSpanTree(spans))
		s.requireUniqueSpanIDs(spans)
		s.requireContinueAsNewErrorNotRecorded(spans)
	})
}

func (s *integrationTestSuite) TestWorkflowTaskRetryReusesSpanID() {
	recorder := s.newSpanRecorder()

	plugin, err := NewPlugin(PluginOptions{})
	s.Require().NoError(err)

	srv := s.devServer()
	c, err := client.DialContext(context.Background(), client.Options{
		HostPort: srv.FrontendHostPort(),
		Plugins:  []client.Plugin{plugin},
	})
	s.Require().NoError(err)
	defer c.Close()

	shouldPanic := true
	workflowTaskRetrySpanWorkflow := func(ctx workflow.Context) error {
		_, span := Tracer("test").Start(ctx, "workflow-task-retry-span")
		span.End()

		if shouldPanic {
			shouldPanic = false
			panic("intentional workflow task failure")
		}
		return nil
	}

	taskQueue := "opentelemetry-v2-workflow-task-retry-" + uuid.NewString()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(workflowTaskRetrySpanWorkflow)
	s.Require().NoError(w.Start())
	defer w.Stop()

	run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        "opentelemetry-v2-workflow-task-retry-" + uuid.NewString(),
		TaskQueue: taskQueue,
	}, workflowTaskRetrySpanWorkflow)
	s.Require().NoError(err)
	s.Require().NoError(run.Get(context.Background(), nil))

	var spanIDs []trace.SpanID
	for _, span := range recorder.Ended() {
		if span.Name() == "workflow-task-retry-span" {
			spanIDs = append(spanIDs, span.SpanContext().SpanID())
		}
	}
	s.Require().Len(spanIDs, 2)
	s.Require().Equal(spanIDs[0], spanIDs[1])
}
