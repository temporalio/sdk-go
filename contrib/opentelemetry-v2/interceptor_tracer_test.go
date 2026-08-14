package opentelemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor/tracing"
	temporalnexus "go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
)

const (
	ctxTimeout                = 30 * time.Second
	integrationTaskQueue      = "opentelemetry-v2-integration"
	scheduleID                = "otel-schedule"
	comprehensiveWorkflowID   = "comprehensive-outbound"
	comprehensiveUpdateID     = "comprehensive-update"
	updateWithStartWorkflowID = "otel-update-with-start"
	updateWithStartUpdateID   = "comprehensive-update-with-start"
	unservedTaskQueue         = "opentelemetry-v2-unserved"
	terminateReason           = "otel-terminate-reason"
)

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

func (s *integrationTestSuite) runScenario(pluginOpts PluginOptions) []sdktrace.ReadOnlySpan {
	recorder := s.newSpanRecorder()

	plugin, err := NewPlugin(pluginOpts)
	s.Require().NoError(err)

	c := s.newDevServerClient(client.Options{
		Plugins: []client.Plugin{plugin},
	})

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
	w.RegisterWorkflow(asyncCompletionWorkflow)
	w.RegisterActivity(activity)
	w.RegisterActivity(asyncCompletionActivity)
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

	asyncRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "otel-async-completion-" + uuid.NewString(),
		TaskQueue: integrationTaskQueue,
	}, asyncCompletionWorkflow)
	s.Require().NoError(err)

	tokenCtx, cancelToken := context.WithTimeout(ctx, ctxTimeout)
	defer cancelToken()
	var taskToken []byte
	select {
	case taskToken = <-taskTokenCh:
	case <-tokenCtx.Done():
		s.Require().Fail("timed out waiting for activity task token")
	}
	s.Require().NoError(c.CompleteActivity(ctx, taskToken, nil, nil))
	s.Require().NoError(asyncRun.Get(ctx, nil))

	cancelTarget, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "otel-cancel-target-" + uuid.NewString(),
		TaskQueue: unservedTaskQueue,
	}, unservedWorkflow)
	s.Require().NoError(err)
	s.Require().NoError(c.CancelWorkflow(ctx, cancelTarget.GetID(), ""))

	terminateTarget, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "otel-terminate-target-" + uuid.NewString(),
		TaskQueue: unservedTaskQueue,
	}, unservedWorkflow)
	s.Require().NoError(err)
	s.Require().NoError(c.TerminateWorkflow(ctx, terminateTarget.GetID(), "", terminateReason))

	_, err = c.DescribeWorkflow(ctx, comprehensiveWorkflowID, "")
	s.Require().NoError(err)

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

	updateWithStartRun, err := startOp.Get(ctx)
	s.Require().NoError(err)
	s.Require().NoError(c.SignalWorkflow(ctx, updateWithStartRun.GetID(), "", "updateSignal", nil))
	s.Require().NoError(updateWithStartRun.Get(ctx, nil))

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
		{"StartWorkflowUpdate:testUpdate", comprehensiveWorkflowID, comprehensiveUpdateID},
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

func (s *integrationTestSuite) requireResultPendingErrorNotRecorded(spans []sdktrace.ReadOnlySpan) {
	s.T().Helper()
	runSpan := s.requireSpanNamed(spans, "RunActivity:asyncCompletionActivity")
	s.Require().Equal(codes.Unset, runSpan.Status().Code)
	s.Require().Empty(runSpan.Events())
}

// fullTree is the span tree with Temporal spans enabled.
var fullTree = []string{
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
	"      SignalChildWorkflow:childSignal",
	"        HandleSignal:childSignal",
	"      SignalExternalWorkflow:externalSignal",
	"        HandleSignal:externalSignal",
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
	// Cancels the backing nexusCancelHandlerWorkflow, not the unserved target.
	"          CancelWorkflow",
	// Continue-as-new links the outbound, continued-run, and user spans.
	"      ContinueAsNew:comprehensiveWorkflow",
	"        RunWorkflow:comprehensiveWorkflow",
	"          comprehensive-outbound-workflow-span",
	"      comprehensive-outbound-workflow-span",
	// Update user spans follow their current inbound operation.
	"  StartWorkflowUpdate:testUpdate",
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
	"  StartWorkflow:asyncCompletionWorkflow",
	"    RunWorkflow:asyncCompletionWorkflow",
	"      StartActivity:asyncCompletionActivity",
	"        RunActivity:asyncCompletionActivity",
	// Cancel, terminate, and describe target the unserved workflows.
	"  StartWorkflow:unservedWorkflow",
	"  CancelWorkflow",
	"  StartWorkflow:unservedWorkflow",
	"  TerminateWorkflow",
	"  DescribeWorkflow",
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
	"      update start",
	"    RunWorkflow:updateTargetWorkflow",
	"      update-target-workflow-span",
	"  SignalWorkflow:updateSignal",
	"    HandleSignal:updateSignal",
}

// queryUpdateSignalDisabledTree is fullTree without the signal, query, and
// update Temporal spans. Everything below a removed span reattaches to the
// nearest surviving ancestor.
var queryUpdateSignalDisabledTree = []string{
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
	// Cancels the backing nexusCancelHandlerWorkflow, not the unserved target.
	"          CancelWorkflow",
	// Continue-as-new links the outbound, continued-run, and user spans.
	"      ContinueAsNew:comprehensiveWorkflow",
	"        RunWorkflow:comprehensiveWorkflow",
	"          comprehensive-outbound-workflow-span",
	"      comprehensive-outbound-workflow-span",
	// Update user spans reattach to the client span.
	"  validate-update-span",
	"    validate-update-span-child",
	"  update-handler-span",
	"    update-handler-child-span",
	// Headers link standalone StartActivity and RunActivity spans.
	"  StartActivity:standaloneActivity",
	"    RunActivity:standaloneActivity",
	"  StartWorkflow:standaloneWorkflow",
	"    RunWorkflow:standaloneWorkflow",
	"  StartWorkflow:asyncCompletionWorkflow",
	"    RunWorkflow:asyncCompletionWorkflow",
	"      StartActivity:asyncCompletionActivity",
	"        RunActivity:asyncCompletionActivity",
	// Cancel, terminate, and describe target the unserved workflows.
	"  StartWorkflow:unservedWorkflow",
	"  CancelWorkflow",
	"  StartWorkflow:unservedWorkflow",
	"  TerminateWorkflow",
	"  DescribeWorkflow",
	"  CreateSchedule:" + scheduleID,
	// Signal-with-start and update-with-start still propagate to the workflows
	// they start.
	"  RunWorkflow:signalWithStartTarget",
	"    signal-with-start-target-span",
	"  update start",
	"  RunWorkflow:updateTargetWorkflow",
	"    update-target-workflow-span",
}

// noTemporalSpansTree is fullTree without any Temporal spans. Only user spans
// remain, and each reattaches to the nearest surviving ancestor.
var noTemporalSpansTree = []string{
	"client-span",
	"  validate-update-span",
	"    validate-update-span-child",
	"  update-handler-span",
	"    update-handler-child-span",
	"  query-handler-span",
	"    query-handler-child-span",
	"  activity-span",
	"  local-activity-span",
	"  child-workflow-with-signal-span",
	"  external-workflow-with-signal-span",
	"  workflow-with-nexus-handler-span",
	"  nexus-cancel-handler-span",
	// Continue-as-new emits the user span once per run.
	"  comprehensive-outbound-workflow-span",
	"  comprehensive-outbound-workflow-span",
	"  signal-with-start-target-span",
	"  update start",
	"  update-target-workflow-span",
}

func (s *integrationTestSuite) TestComprehensive() {
	s.Run("with-temporal-spans", func() {
		spans := s.runScenario(PluginOptions{TracerOptions: tracing.TracerOptions{AddTemporalSpans: true}})
		s.Require().Equal(fullTree, s.formatSpanTree(spans))
		s.requireUniqueSpanIDs(spans)
		s.requireUpdateIDs(spans)
		s.requireContinueAsNewErrorNotRecorded(spans)
		s.requireResultPendingErrorNotRecorded(spans)
	})

	s.Run("with-temporal-spans-query-update-signal-disabled", func() {
		spans := s.runScenario(PluginOptions{
			TracerOptions: tracing.TracerOptions{
				AddTemporalSpans:     true,
				DisableSignalTracing: true,
				DisableQueryTracing:  true,
				DisableUpdateTracing: true,
			},
		})
		s.Require().Equal(queryUpdateSignalDisabledTree, s.formatSpanTree(spans))
		s.requireUniqueSpanIDs(spans)
		s.requireContinueAsNewErrorNotRecorded(spans)
		s.requireResultPendingErrorNotRecorded(spans)
	})

	s.Run("no-temporal-spans", func() {
		spans := s.runScenario(PluginOptions{})
		s.Require().Equal(noTemporalSpansTree, s.formatSpanTree(spans))
		s.requireUniqueSpanIDs(spans)
	})
}
