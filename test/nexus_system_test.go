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
	sdkpb "go.temporal.io/api/sdk/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	systemnexus "go.temporal.io/sdk/temporalnexus/system"
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
			req *workflowservicepb.SignalWithStartWorkflowExecutionRequest,
			_ nexus.StartOperationOptions,
		) (*workflowservicepb.SignalWithStartWorkflowExecutionResponse, error) {
			require.Equal(t, "system-nexus-workflow-id", req.GetWorkflowId())
			require.Equal(t, "test-signal", req.GetSignalName())

			for _, payload := range req.GetInput().GetPayloads() {
				require.NotEmpty(t, payload.GetExternalPayloads())
			}
			for _, payload := range req.GetSignalInput().GetPayloads() {
				require.NotEmpty(t, payload.GetExternalPayloads())
			}
			require.NotEmpty(t, req.GetMemo().GetFields()["memo-key"].GetExternalPayloads())
			require.NotEmpty(t, req.GetHeader().GetFields()["header-key"].GetExternalPayloads())
			require.NotEmpty(t, req.GetUserMetadata().GetSummary().GetExternalPayloads())
			require.NotEmpty(t, req.GetUserMetadata().GetDetails().GetExternalPayloads())

			searchAttr := req.GetSearchAttributes().GetIndexedFields()["custom-key"]
			require.Empty(t, searchAttr.GetExternalPayloads())
			require.NotContains(t, searchAttr.GetMetadata(), "test-codec")

			return &workflowservicepb.SignalWithStartWorkflowExecutionResponse{
				RunId: "system-nexus-workflow-id-run",
			}, nil
		},
	)))
	handlerWorker.RegisterNexusService(service)

	require.NoError(t, callerWorker.Start())
	defer callerWorker.Stop()
	require.NoError(t, handlerWorker.Start())
	defer handlerWorker.Stop()

	run, err := callerClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "system-nexus-" + uuid.NewString(),
		TaskQueue: callerTaskQueue,
	}, systemNexusSignalWithStartWorkflow, handlerTC.endpoint)
	require.NoError(t, err)

	var result string
	require.NoError(t, run.Get(ctx, &result))
	require.Equal(t, "system-nexus-workflow-id-run", result)
	require.GreaterOrEqual(t, codec.EncodeCount(), 6)

	driver.mu.Lock()
	defer driver.mu.Unlock()
	var storedPayloadData [][]byte
	for _, payload := range driver.data {
		require.Equal(t, []byte("true"), payload.GetMetadata()["test-codec"])
		storedPayloadData = append(storedPayloadData, payload.GetData())
	}
	require.NotEmpty(t, storedPayloadData)
	require.Contains(t, storedPayloadData, []byte(`"workflow-input"`))
	require.Contains(t, storedPayloadData, []byte(`"signal-input"`))
	require.Contains(t, storedPayloadData, []byte(`"memo-value"`))
	require.Contains(t, storedPayloadData, []byte(`"header-value"`))
	require.Contains(t, storedPayloadData, []byte(`"summary-value"`))
	require.Contains(t, storedPayloadData, []byte(`"details-value"`))
}

func systemNexusSignalWithStartWorkflow(ctx workflow.Context, endpoint string) (string, error) {
	nexusClient := workflow.NewNexusClient(endpoint, systemnexus.WorkflowService.ServiceName)
	fut := nexusClient.ExecuteOperation(
		ctx,
		systemnexus.WorkflowService.SignalWithStartWorkflowExecution,
		&workflowservicepb.SignalWithStartWorkflowExecutionRequest{
			Namespace:  "default",
			WorkflowId: "system-nexus-workflow-id",
			SignalName: "test-signal",
			Input: &commonpb.Payloads{Payloads: []*commonpb.Payload{
				jsonStringPayload("workflow-input"),
			}},
			SignalInput: &commonpb.Payloads{Payloads: []*commonpb.Payload{
				jsonStringPayload("signal-input"),
			}},
			Memo: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{
					"memo-key": jsonStringPayload("memo-value"),
				},
			},
			Header: &commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"header-key": jsonStringPayload("header-value"),
				},
			},
			UserMetadata: &sdkpb.UserMetadata{
				Summary: jsonStringPayload("summary-value"),
				Details: jsonStringPayload("details-value"),
			},
			SearchAttributes: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"custom-key": jsonStringPayload("search-attribute-value"),
				},
			},
		},
		workflow.NexusOperationOptions{},
	)

	var result *workflowservicepb.SignalWithStartWorkflowExecutionResponse
	if err := fut.Get(ctx, &result); err != nil {
		return "", err
	}
	return result.GetRunId(), nil
}

func jsonStringPayload(value string) *commonpb.Payload {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON),
		},
		Data: data,
	}
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
