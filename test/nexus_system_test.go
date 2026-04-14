package test_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	systemnexus "go.temporal.io/api/workflowservice/v1/workflowservicenexus/json"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

func TestSystemNexusDefersOuterEnvelopeEncoding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	var suiteBase ConfigAndClientSuiteBase
	require.NoError(t, suiteBase.InitConfigAndNamespace())
	if suiteBase.client != nil {
		defer suiteBase.client.Close()
	}

	handlerTC := newTestContext(t, ctx)
	driver := newMemDriver("system-nexus")
	codec := &rejectOuterSystemNexusCodec{}

	callerClient, err := client.DialContext(ctx, client.Options{
		HostPort:  handlerTC.testConfig.ServiceAddr,
		Namespace: handlerTC.testConfig.Namespace,
		Logger:    ilog.NewDefaultLogger(),
		DataConverter: converter.NewCodecDataConverter(
			converter.GetDefaultDataConverter(),
			codec,
		),
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{driver},
			PayloadSizeThreshold: 1,
		},
		ConnectionOptions:       client.ConnectionOptions{TLS: handlerTC.testConfig.TLS},
		WorkerHeartbeatInterval: -1,
	})
	require.NoError(t, err)
	defer callerClient.Close()

	callerTaskQueue := "sdk-go-system-nexus-caller-" + uuid.NewString()
	callerWorker := worker.New(callerClient, callerTaskQueue, worker.Options{})
	callerWorker.RegisterWorkflow(systemNexusSignalWithStartWorkflow)

	handlerWorker := worker.New(handlerTC.client, handlerTC.taskQueue, worker.Options{})
	service := nexus.NewService(systemnexus.WorkflowService.ServiceName)
	require.NoError(t, service.Register(nexus.NewSyncOperation(
		systemnexus.WorkflowService.SignalWithStartWorkflowExecution.Name(),
		func(
			_ context.Context,
			req systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput,
			_ nexus.StartOperationOptions,
		) (systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionOutput, error) {
			require.Equal(t, "system-nexus-workflow-id", req.WorkflowID)
			require.Equal(t, "test-signal", req.SignalName)
			requirePayloadJSONReference(t, req.Input.Payloads[0])
			requirePayloadJSONReference(t, req.SignalInput.Payloads[0])
			requirePayloadJSONReference(t, req.Memo.Fields["memo-key"])
			requirePayloadJSONReference(t, req.UserMetadata.Summary)
			requirePayloadJSONReference(t, req.UserMetadata.Details)
			require.Equal(t, "search-attribute-value", req.SearchAttributes.IndexedFields["custom-key"])

			return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionOutput{
				RunID: "system-nexus-workflow-id-run",
			}, nil
		},
	)))
	handlerWorker.RegisterNexusService(service)

	systemEndpointSpec := &nexuspb.EndpointSpec{
		// TODO: Switch this back to "__temporal_system" once the server supports
		// reserved system endpoint names for Nexus endpoint registration/routing.
		Name: "temporal-system",
		Target: &nexuspb.EndpointTarget{
			Variant: &nexuspb.EndpointTarget_Worker_{
				Worker: &nexuspb.EndpointTarget_Worker{
					Namespace: handlerTC.testConfig.Namespace,
					TaskQueue: handlerTC.taskQueue,
				},
			},
		},
	}
	existingEndpoints, err := handlerTC.client.OperatorService().ListNexusEndpoints(ctx, &operatorservice.ListNexusEndpointsRequest{
		Name: systemEndpointSpec.Name,
	})
	require.NoError(t, err)
	if len(existingEndpoints.Endpoints) == 0 {
		_, err = handlerTC.client.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
			Spec: systemEndpointSpec,
		})
		require.NoError(t, err)
	} else {
		_, err = handlerTC.client.OperatorService().UpdateNexusEndpoint(ctx, &operatorservice.UpdateNexusEndpointRequest{
			Id:      existingEndpoints.Endpoints[0].Id,
			Version: existingEndpoints.Endpoints[0].Version,
			Spec:    systemEndpointSpec,
		})
		require.NoError(t, err)
	}

	require.NoError(t, callerWorker.Start())
	defer callerWorker.Stop()
	require.NoError(t, handlerWorker.Start())
	defer handlerWorker.Stop()

	run, err := callerClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "system-nexus-" + uuid.NewString(),
		TaskQueue: callerTaskQueue,
	}, systemNexusSignalWithStartWorkflow)
	require.NoError(t, err)

	var result string
	require.NoError(t, run.Get(ctx, &result))
	require.Equal(t, "system-nexus-workflow-id-run", result)
	require.GreaterOrEqual(t, codec.EncodeCount(), 5)

	driver.mu.Lock()
	defer driver.mu.Unlock()
	var storedPayloadData [][]byte
	for _, payload := range driver.data {
		storedPayloadData = append(storedPayloadData, payload.GetData())
	}
	require.NotEmpty(t, storedPayloadData)
	require.Contains(t, storedPayloadData, []byte(`"workflow-input"`))
	require.Contains(t, storedPayloadData, []byte(`"signal-input"`))
	require.Contains(t, storedPayloadData, []byte(`"memo-value"`))
	require.Contains(t, storedPayloadData, []byte(`"summary-value"`))
	require.Contains(t, storedPayloadData, []byte(`"details-value"`))
}

func systemNexusSignalWithStartWorkflow(ctx workflow.Context) (string, error) {
	fut := workflow.SignalWithStartWorkflow(
		ctx,
		"system-nexus-workflow-id",
		"test-signal",
		"signal-input",
		workflow.StartWorkflowOptions{
			TaskQueue: "test-task-queue",
			Memo: map[string]interface{}{
				"memo-key": "memo-value",
			},
			SearchAttributes: map[string]interface{}{
				"custom-key": "search-attribute-value",
			},
			StaticSummary: "summary-value",
			StaticDetails: "details-value",
		},
		systemNexusTargetWorkflow,
		"workflow-input",
	)

	var exec workflow.Execution
	if err := fut.Get(ctx, &exec); err != nil {
		return "", err
	}
	return exec.RunID, nil
}

func systemNexusTargetWorkflow(ctx workflow.Context, input string) error {
	return nil
}

type rejectOuterSystemNexusCodec struct {
	mu          sync.Mutex
	encodeCount int
}

func (c *rejectOuterSystemNexusCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	encoded := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		if looksLikeSystemNexusEnvelope(payload) {
			return nil, fmt.Errorf("outer system nexus envelope should not be codec encoded")
		}
		cloned := proto.Clone(payload).(*commonpb.Payload)
		if cloned.Metadata == nil {
			cloned.Metadata = make(map[string][]byte, 1)
		}
		cloned.Metadata["test-codec"] = []byte("true")
		encoded[i] = cloned
	}

	c.mu.Lock()
	c.encodeCount += len(payloads)
	c.mu.Unlock()
	return encoded, nil
}

func (c *rejectOuterSystemNexusCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	for _, payload := range payloads {
		if looksLikeSystemNexusEnvelope(payload) {
			return nil, fmt.Errorf("outer system nexus envelope should not be codec decoded")
		}
	}
	return payloads, nil
}

func (c *rejectOuterSystemNexusCodec) EncodeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encodeCount
}

func looksLikeSystemNexusEnvelope(payload *commonpb.Payload) bool {
	var value map[string]any
	if err := json.Unmarshal(payload.GetData(), &value); err != nil {
		return false
	}
	_, hasNamespace := value["namespace"]
	_, hasWorkflowID := value["workflowId"]
	_, hasSignalName := value["signalName"]
	return hasNamespace && hasWorkflowID && hasSignalName
}

func requirePayloadJSONReference(t *testing.T, value any) {
	t.Helper()
	payloadMap, ok := value.(map[string]any)
	require.True(t, ok)
	_, hasExternalPayloads := payloadMap["externalPayloads"]
	require.True(t, hasExternalPayloads)
}
