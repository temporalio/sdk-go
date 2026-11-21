package test_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type systemNexusWorkerRequest struct{ Value string }

func (systemNexusWorkerRequest) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return commonconverter.NewSystemNexusTransferTypeConverter(
		func(cfg *commonconverter.SystemNexusContext, value *systemNexusWorkerRequest) (*workflowservice.SignalWithStartWorkflowExecutionRequest, error) {
			input, err := cfg.DataConverter.ToPayloads(value.Value)
			return &workflowservice.SignalWithStartWorkflowExecutionRequest{Input: input}, err
		},
		func(cfg *commonconverter.SystemNexusContext, input *workflowservice.SignalWithStartWorkflowExecutionRequest, value *systemNexusWorkerRequest) error {
			return cfg.DataConverter.FromPayloads(input.Input, &value.Value)
		},
	)
}

type rejectNexusSystemEnvelopeCodec struct {
	converter.PayloadCodec
}

func (c rejectNexusSystemEnvelopeCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	for _, payload := range payloads {
		if string(payload.Metadata[proxy.SystemPayloadMetadataKey]) == "true" {
			return nil, fmt.Errorf("user codec received a system envelope")
		}
	}
	return c.PayloadCodec.Encode(payloads)
}

func (c rejectNexusSystemEnvelopeCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	for _, payload := range payloads {
		if string(payload.Metadata[proxy.SystemPayloadMetadataKey]) == "true" {
			return nil, fmt.Errorf("user codec received a system envelope")
		}
	}
	return c.PayloadCodec.Decode(payloads)
}

func TestNexusWorkerSystemPayload(t *testing.T) {
	skipOnCloud(t, cloudRequiresProvisioning, "creates a Nexus endpoint through Operator Service")
	for _, externalStorage := range []bool{false, true} {
		t.Run(fmt.Sprintf("external-storage=%v", externalStorage), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			tc := newTestContext(t, ctx)
			defer tc.client.Close()

			driver := newMemDriver("system-input")
			var storage converter.ExternalStorage
			if externalStorage {
				storage = converter.ExternalStorage{
					Drivers:              []converter.StorageDriver{driver},
					PayloadSizeThreshold: 1,
				}
			}
			callerClient, err := client.NewClientFromExisting(tc.client, client.Options{
				Namespace:       tc.testConfig.Namespace,
				ExternalStorage: storage,
			})
			require.NoError(t, err)
			defer callerClient.Close()
			dc := converter.NewCodecDataConverter(
				converter.GetDefaultDataConverter(),
				rejectNexusSystemEnvelopeCodec{converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true})},
			)
			handlerClient, err := client.NewClientFromExisting(tc.client, client.Options{
				Namespace:       tc.testConfig.Namespace,
				DataConverter:   dc,
				ExternalStorage: storage,
			})
			require.NoError(t, err)
			defer handlerClient.Close()

			// Send a wire-ready envelope from a caller with no codec; only the nested value is encoded.
			nested, err := dc.ToPayloads("workflow-input")
			require.NoError(t, err)
			payload, err := converter.NewProtoPayloadConverter().ToPayload(
				&workflowservice.SignalWithStartWorkflowExecutionRequest{Input: nested},
			)
			require.NoError(t, err)
			payload.Metadata[proxy.SystemPayloadMetadataKey] = []byte("true")
			op := nexus.NewSyncOperation(
				"decode",
				func(_ context.Context, input systemNexusWorkerRequest, _ nexus.StartOperationOptions) (string, error) {
					return "handled-" + input.Value, nil
				},
			)
			service := nexus.NewService("system-input")
			require.NoError(t, service.Register(op))
			handlerWorker := worker.New(handlerClient, tc.taskQueue, worker.Options{})
			handlerWorker.RegisterNexusService(service)
			require.NoError(t, handlerWorker.Start())
			defer handlerWorker.Stop()

			caller := func(ctx workflow.Context) (string, error) {
				var raw converter.RawValue
				err := workflow.NewNexusClient(tc.endpoint, service.Name).ExecuteOperation(
					ctx,
					op.Name(),
					converter.NewRawValue(proto.Clone(payload).(*commonpb.Payload)),
					workflow.NexusOperationOptions{
						ScheduleToCloseTimeout: 20 * time.Second,
					},
				).Get(ctx, &raw)
				if err != nil {
					return "", err
				}
				// The ordinary string result uses the handler's codec, not system envelope conversion.
				var result string
				err = dc.FromPayload(raw.Payload(), &result)
				return result, err
			}
			callerTaskQueue := tc.taskQueue + "-caller"
			callerWorker := worker.New(callerClient, callerTaskQueue, worker.Options{})
			callerWorker.RegisterWorkflow(caller)
			require.NoError(t, callerWorker.Start())
			defer callerWorker.Stop()
			run, err := callerClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				TaskQueue:           callerTaskQueue,
				WorkflowTaskTimeout: time.Second,
			}, caller)
			require.NoError(t, err)
			var result string
			require.NoError(t, run.Get(ctx, &result))
			require.Equal(t, "handled-workflow-input", result)
			stores, retrieves := driver.getStoreCounts()
			if externalStorage {
				require.Positive(t, stores, "nested input should be offloaded")
				require.Positive(t, retrieves, "nested input must be retrieved before decoding")
				driver.mu.Lock()
				storedInput := false
				for _, stored := range driver.data {
					if proto.Equal(stored, nested.Payloads[0]) {
						storedInput = true
						break
					}
				}
				driver.mu.Unlock()
				require.True(t, storedInput, "storage must contain the encoded nested input, not just operation or workflow results")
			} else {
				require.Zero(t, stores)
				require.Zero(t, retrieves)
			}
		})
	}
}
