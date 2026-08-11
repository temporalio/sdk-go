package opentelemetry

import (
	"context"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.opentelemetry.io/otel"

	"go.temporal.io/sdk/workflow"
)

const (
	tracerTestQueryName  = "query"
	tracerTestUpdateName = "update"
	tracerTestSignalName = "gate"

	externalWorkflowID = "externalWorkflowWithSignal"
	nexusEndpointName  = "opentelemetry-v2-integration-endpoint"
	nexusServiceName   = "opentelemetry-v2-integration-service"
	nexusOperationName = "nexusHandlerWorkflow"
	nexusCancelOpName  = "nexusCancelHandlerWorkflow"
)

func validateUpdate(ctx workflow.Context) error {
	tracer := Tracer("validatorTracer")
	_, span := tracer.Start(ctx, "validate start")
	span.End()
	return nil
}

func handleQuery(ctx workflow.Context) (string, error) {
	tracer := Tracer("queryTracer")
	_, span := tracer.Start(ctx, "query start")
	span.End()
	return "ok", nil
}

func handleUpdate(ctx workflow.Context) error {
	tracer := Tracer("updateTracer")
	_, span := tracer.Start(ctx, "update start")
	span.End()
	return nil
}

func tracerWorkflow(ctx workflow.Context, end bool) error {
	err := workflow.SetQueryHandler(ctx, tracerTestQueryName, func() (string, error) {
		return handleQuery(ctx)
	})
	if err != nil {
		return err
	}

	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		tracerTestUpdateName,
		handleUpdate,
		workflow.UpdateHandlerOptions{Validator: validateUpdate},
	)
	if err != nil {
		return err
	}

	if !end {
		workflow.GetSignalChannel(ctx, tracerTestSignalName).Receive(ctx, nil)
	}

	processorTracer := Tracer("processorTracer")
	recorderTracer := Tracer("recorderTracer")

	ctx, beginProcessingSpan := processorTracer.Start(ctx, "process start")
	ctx, recordingResultsSpan := recorderTracer.Start(ctx, "record results")

	beginProcessingSpan.End()
	recordingResultsSpan.End()

	if !end {
		return workflow.NewContinueAsNewError(ctx, tracerWorkflow, true)
	}

	return nil
}

func tracerResetWorkflow(ctx workflow.Context) error {
	processorTracer := Tracer("processorTracer")
	recorderTracer := Tracer("recorderTracer")

	ctx, beginProcessingSpan := processorTracer.Start(ctx, "process start")
	beginProcessingSpan.End()

	workflow.GetSignalChannel(ctx, tracerTestSignalName).Receive(ctx, nil)

	ctx, recordingResultsSpan := recorderTracer.Start(ctx, "record results")
	recordingResultsSpan.End()

	return nil
}

func tracerResetLateSourceWorkflow(ctx workflow.Context) error {
	processorTracer := Tracer("processorTracer")
	ctx, beginProcessingSpan := processorTracer.Start(ctx, "process start")
	beginProcessingSpan.End()

	workflow.GetSignalChannel(ctx, tracerTestSignalName).Receive(ctx, nil)

	recorderTracer := Tracer("recorderTracer")
	ctx, recordingResultsSpan := recorderTracer.Start(ctx, "record results")
	recordingResultsSpan.End()

	return nil
}

func tracerResetDuringSpan(ctx workflow.Context) error {
	processorTracer := Tracer("processorTracer")
	recorderTracer := Tracer("recorderTracer")

	ctx, beginProcessingSpan := processorTracer.Start(ctx, "process start")
	ctx, recordingResultsSpan := recorderTracer.Start(ctx, "record results")

	workflow.GetSignalChannel(ctx, tracerTestSignalName).Receive(ctx, nil)

	beginProcessingSpan.End()
	recordingResultsSpan.End()

	return nil
}

func tracerWorkflowTaskRetry(ctx workflow.Context) error {
	_, span := Tracer("test").Start(ctx, "workflow-task-retry-span")
	span.End()

	panic("intentional workflow task failure")
}

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

func handleComprehensiveQuery(ctx workflow.Context) (string, error) {
	tracer := Tracer("comprehensiveWorkflow")
	ctx, span := tracer.Start(ctx, "query-handler-span")
	defer span.End()
	_, child := tracer.Start(ctx, "query-handler-child-span")
	defer child.End()
	return "ok", nil
}

func handleComprehensiveUpdate(ctx workflow.Context) error {
	tracer := Tracer("comprehensiveWorkflow")
	ctx, span := tracer.Start(ctx, "update-handler-span")
	defer span.End()
	_, child := tracer.Start(ctx, "update-handler-child-span")
	defer child.End()
	return nil
}

func validateComprehensiveUpdate(ctx workflow.Context) error {
	tracer := Tracer("comprehensiveWorkflow")
	ctx, span := tracer.Start(ctx, "validate-update-span")
	defer span.End()
	_, child := tracer.Start(ctx, "validate-update-span-child")
	defer child.End()
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
		return handleComprehensiveQuery(ctx)
	})
	if err != nil {
		return err
	}

	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		"testUpdate",
		handleComprehensiveUpdate,
		workflow.UpdateHandlerOptions{Validator: validateComprehensiveUpdate},
	)
	if err != nil {
		return err
	}

	workflow.GetSignalChannel(ctx, "proceed").Receive(ctx, nil)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{StartToCloseTimeout: 10 * time.Second})

	if err = workflow.ExecuteActivity(ctx, activity).Get(ctx, nil); err != nil {
		return err
	}
	if err = workflow.ExecuteLocalActivity(ctx, localActivity).Get(ctx, nil); err != nil {
		return err
	}

	child := workflow.ExecuteChildWorkflow(ctx, childWorkflowWithSignal)
	if err = child.SignalChildWorkflow(ctx, "childSignal", nil).Get(ctx, nil); err != nil {
		return err
	}
	if err = child.Get(ctx, nil); err != nil {
		return err
	}

	if err = workflow.SignalExternalWorkflow(ctx, externalWorkflowID, "", "externalSignal", nil).Get(ctx, nil); err != nil {
		return err
	}

	nexusClient := workflow.NewNexusClient(nexusEndpointName, nexusServiceName)
	if err = nexusClient.ExecuteOperation(ctx, nexusOperationName, nil, workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
	}).Get(ctx, nil); err != nil {
		return err
	}

	cancelCtx, cancelNexus := workflow.WithCancel(ctx)
	cancelFuture := nexusClient.ExecuteOperation(cancelCtx, nexusCancelOpName, nil, workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
	})
	var cancelExecution workflow.NexusOperationExecution
	if err = cancelFuture.GetNexusOperationExecution().Get(ctx, &cancelExecution); err != nil {
		return err
	}
	cancelNexus()
	// Cancellation is expected.
	_ = cancelFuture.Get(ctx, nil)

	return workflow.NewContinueAsNewError(ctx, comprehensiveWorkflow, true)
}

// standaloneActivity exercises client-root activity spans.
func standaloneActivity(context.Context) error {
	return nil
}

// standaloneWorkflow exercises workflow spans and scheduled starts.
func standaloneWorkflow(workflow.Context) error {
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

	if err := workflow.SetUpdateHandler(ctx, "doUpdate", handleUpdate); err != nil {
		return err
	}

	workflow.GetSignalChannel(ctx, "updateSignal").Receive(ctx, nil)

	return nil
}
