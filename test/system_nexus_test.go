package test_test

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

// Below we declare a SignalWithStart "model type" whose transfer type is
// SignalWithStartWorkflowExecutionRequest, and a SignalWithStartResponse
// model type whose transfer type is SignalWithStartWorkflowExecutionResponse.
// Soon these mock fixtures will be replaced with real model types that we
// expose to the user. For now we use them to test the workflow system
// nexus execution path end-to-end.

const (
	systemNexusWorkflowService = "temporal.api.workflowservice.v1.WorkflowService"
	systemNexusSignalWithStart = "SignalWithStartWorkflowExecution"
)

type signalWithStartModel struct {
	Namespace    string
	WorkflowID   string
	WorkflowType string
	TaskQueue    string
	SignalName   string
	Input        string
}

func init() {
	// Here we tell the system nexus data converter what serialization
	// context it should use when encoding a signalWithStartModel.
	internal.UpdateNexusOperationRegistry(map[internal.NexusOperationKey]internal.NexusOperationRegistryEntry{
		{Service: systemNexusWorkflowService, Operation: systemNexusSignalWithStart}: {
			SerializationContext: func(value any) converter.SerializationContext {
				model := value.(signalWithStartModel)
				return converter.WorkflowSerializationContext{
					Namespace:  model.Namespace,
					WorkflowID: model.WorkflowID,
				}
			},
		},
	})
}

func (signalWithStartModel) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return commonconverter.NewSystemNexusTransferTypeConverter(
		// This is the transfer type encoder that turns signalWithStartModel
		// into a SignalWithStartWorkflowExecutionRequest proto. As part of
		// that encoding step, we encode the arguments (model.Input) using
		// the callee's serialization context.
		func(cfg *commonconverter.SystemNexusConfig, model *signalWithStartModel) (*workflowservice.SignalWithStartWorkflowExecutionRequest, error) {
			input, err := cfg.DataConverter.ToPayloads(model.Input)
			if err != nil {
				return nil, err
			}
			return &workflowservice.SignalWithStartWorkflowExecutionRequest{
				Namespace:    model.Namespace,
				WorkflowId:   model.WorkflowID,
				WorkflowType: &commonpb.WorkflowType{Name: model.WorkflowType},
				TaskQueue:    &taskqueuepb.TaskQueue{Name: model.TaskQueue},
				SignalName:   model.SignalName,
				Input:        input,
			}, nil
		},
		// Transfer type decoder
		func(*commonconverter.SystemNexusConfig, *workflowservice.SignalWithStartWorkflowExecutionRequest, *signalWithStartModel) error {
			return errors.New("signal-with-start test request is encode-only")
		},
	)
}

type signalWithStartResponseModel struct {
	RunID string
}

func (signalWithStartResponseModel) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return commonconverter.NewSystemNexusTransferTypeConverter(
		// Transfer type encoder
		func(_ *commonconverter.SystemNexusConfig, model *signalWithStartResponseModel) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
			return &workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: model.RunID}, nil
		},
		// This is transfer type decoder that turns a SignalWithStartWorkflowExecutionResponse proto
		// into a signalWithStartResponseModel.
		func(_ *commonconverter.SystemNexusConfig, response *workflowservice.SignalWithStartWorkflowExecutionResponse, model *signalWithStartResponseModel) error {
			model.RunID = response.GetRunId()
			return nil
		},
	)
}

// TestSystemNexusSignalWithStartSerializationContext invokes SignalWithStart from
// a caller workflow using system nexus. We verify that the call went through and
// that the arguments were encoded using the callee's serialization context.
func (ts *IntegrationTestSuite) TestSystemNexusSignalWithStart() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	caller := "system-nexus-proto-caller"
	callee := "system-nexus-proto-callee"
	taskQueue := ts.taskQueueName + "-system-nexus-proto"
	id := uuid.NewString()
	callerID := caller + id
	calleeID := callee + id

	callerWorkflow := func(ctx workflow.Context, targetID, taskQueue string) (string, error) {
		request := signalWithStartModel{
			Namespace:    workflow.GetInfo(ctx).Namespace,
			WorkflowID:   targetID,
			WorkflowType: callee,
			TaskQueue:    taskQueue,
			SignalName:   "greeting",
			Input:        "started",
		}
		var response signalWithStartResponseModel
		err := internal.NewSystemNexusClient(systemNexusWorkflowService).
			ExecuteOperation(ctx, systemNexusSignalWithStart, request, workflow.NexusOperationOptions{}).
			Get(ctx, &response)
		return response.RunID, err
	}
	calleeWorkflow := func(_ workflow.Context, input string) (string, error) {
		return input, nil
	}

	c, w := ts.newSerCtxClientAndWorker(taskQueue)
	defer c.Close()
	w.RegisterWorkflowWithOptions(callerWorkflow, workflow.RegisterOptions{Name: caller})
	w.RegisterWorkflowWithOptions(calleeWorkflow, workflow.RegisterOptions{Name: callee})
	ts.NoError(w.Start())
	defer w.Stop()

	callerRun, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       callerID,
		TaskQueue:                taskQueue,
		WorkflowExecutionTimeout: 20 * time.Second,
	}, caller, calleeID, taskQueue)
	ts.NoError(err)

	var targetRunID string
	ts.NoError(callerRun.Get(ctx, &targetRunID))
	ts.NotEmpty(targetRunID)

	var result string
	ts.NoError(c.GetWorkflow(ctx, calleeID, targetRunID).Get(ctx, &result))
	ts.Equal("started", result)

	history := c.GetWorkflowHistory(ctx, callerID, callerRun.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var scheduledInput *commonpb.Payload
	for history.HasNext() {
		event, err := history.Next()
		ts.NoError(err)
		if attrs := event.GetNexusOperationScheduledEventAttributes(); attrs != nil {
			scheduledInput = attrs.GetInput()
			break
		}
	}
	ts.NotNil(scheduledInput, "caller history must contain the system Nexus request")
	var request workflowservice.SignalWithStartWorkflowExecutionRequest
	ts.NoError(proto.Unmarshal(scheduledInput.GetData(), &request))
	ts.Len(request.GetInput().GetPayloads(), 1)
	ts.Equal(calleeID, string(request.GetInput().GetPayloads()[0].Metadata["ctx-signature"]))
}
