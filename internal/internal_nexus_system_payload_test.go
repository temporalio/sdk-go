package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

func TestNexusTaskHandlerSystemPayloadSerializationContext(t *testing.T) {
	nexusContext := converter.NexusSerializationContext{
		Endpoint:  "endpoint",
		Service:   "service",
		Operation: "operation",
	}
	dc := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&serCtxSigningCodec{},
		newSystemEnvelopeRejectingCodec(),
	)
	boundDC := converter.WithDataConverterSerializationContext(dc, nexusContext)
	payload, err := commonconverter.WithDataConverterSystemNexusContext(systemNexusDataConverter, boundDC).
		ToPayload(systemNexusModel{Value: "input"})
	require.NoError(t, err)

	operation := nexus.NewSyncOperation(
		nexusContext.Operation,
		func(_ context.Context, input systemNexusModel, _ nexus.StartOperationOptions) (string, error) {
			return input.Value, nil
		},
	)
	service := nexus.NewService(nexusContext.Service)
	require.NoError(t, service.Register(operation))
	reg := nexus.NewServiceRegistry()
	require.NoError(t, reg.Register(service))
	reg.Use(nexusMiddleware(nil))
	handler, err := reg.NewHandler()
	require.NoError(t, err)
	taskHandler := newNexusTaskHandler(
		handler,
		"identity",
		"namespace",
		"task-queue",
		nil,
		dc,
		GetDefaultFailureConverter(),
		ilog.NewNopLogger(),
		metrics.NopHandler,
		newRegistry(),
	)

	completed, failed, err := taskHandler.Execute(&workflowservice.PollNexusTaskQueueResponse{
		TaskToken: []byte("token"),
		Request: &nexuspb.Request{
			Endpoint: nexusContext.Endpoint,
			Variant: &nexuspb.Request_StartOperation{
				StartOperation: &nexuspb.StartOperationRequest{
					Service:   nexusContext.Service,
					Operation: nexusContext.Operation,
					Payload:   payload,
				},
			},
		},
	})
	require.NoError(t, err)
	require.Nil(t, failed)
	require.NotNil(t, completed)
	output := completed.GetResponse().GetStartOperation().GetSyncSuccess().GetPayload()
	require.NotNil(t, output)
	require.NotContains(t, output.Metadata, proxy.SystemPayloadMetadataKey)
	var result string
	require.NoError(t, boundDC.FromPayload(output, &result))
	require.Equal(t, "input", result)
}

func TestNexusPollerInvalidSystemPayload(t *testing.T) {
	expectedContext := converter.NexusSerializationContext{
		Service:   "service",
		Operation: "operation",
	}
	for _, messageType := range []string{"temporal.api.workflowservice.v1.UnknownRequest", ""} {
		for _, withCodec := range []bool{false, true} {
			for _, withVisitor := range []bool{false, true} {
				t.Run(fmt.Sprintf("type=%s/codec=%v/visitor=%v", messageType, withCodec, withVisitor), func(t *testing.T) {
					rpc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
					failureConverter := newNexusCapturingFailureConverter()
					dc := converter.GetDefaultDataConverter()
					if withCodec {
						dc = converter.NewCodecDataConverter(dc, nexusInputCodec{
							decode: func([]*commonpb.Payload) ([]*commonpb.Payload, error) {
								t.Fatal("unknown system envelope must not reach the codec")
								return nil, nil
							},
						})
					}
					handler := newNexusTaskHandler(
						nil, // Invalid envelopes must be rejected before operation dispatch.
						"identity",
						"namespace",
						"task-queue",
						&WorkflowClient{workflowService: rpc},
						dc,
						failureConverter,
						ilog.NewNopLogger(),
						metrics.NopHandler,
						newRegistry(),
					)
					poller := &nexusTaskPoller{
						basePoller:        basePoller{metricsHandler: metrics.NopHandler},
						taskHandler:       handler,
						backgroundContext: t.Context(),
					}
					if withVisitor {
						poller.inboundPayloadVisitor = visitorFunc(func(*proxy.VisitPayloadsContext, []*commonpb.Payload) ([]*commonpb.Payload, error) {
							t.Fatal("unknown system envelope must be rejected before external retrieval")
							return nil, nil
						})
					}
					for _, temporalFailureResponses := range []bool{false, true} {
						task := &workflowservice.PollNexusTaskQueueResponse{
							TaskToken:     []byte("token"),
							PollerGroupId: "poller-group",
							Request: &nexuspb.Request{
								ScheduledTime: timestamppb.Now(),
								Capabilities: &nexuspb.Request_Capabilities{
									TemporalFailureResponses: temporalFailureResponses,
								},
								Variant: &nexuspb.Request_StartOperation{
									StartOperation: &nexuspb.StartOperationRequest{
										Service:   expectedContext.Service,
										Operation: expectedContext.Operation,
										Payload: &commonpb.Payload{
											Metadata: map[string][]byte{
												proxy.SystemPayloadMetadataKey: []byte("true"),
												converter.MetadataEncoding:     []byte(converter.MetadataEncodingProto),
												converter.MetadataMessageType:  []byte(messageType),
											},
										},
									},
								},
							},
						}
						rpc.EXPECT().RespondNexusTaskFailed(gomock.Any(), gomock.Any(), gomock.Any()).
							DoAndReturn(func(_ context.Context, req *workflowservice.RespondNexusTaskFailedRequest, _ ...grpc.CallOption) (*workflowservice.RespondNexusTaskFailedResponse, error) {
								require.Equal(t, task.TaskToken, req.TaskToken)
								require.Equal(t, task.PollerGroupId, req.PollerGroupId)
								var message string
								if temporalFailureResponses {
									var handlerErr *nexus.HandlerError
									require.ErrorAs(t, GetDefaultFailureConverter().FailureToError(req.Failure), &handlerErr)
									require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
									require.Equal(t, nexus.HandlerErrorRetryBehaviorRetryable, handlerErr.RetryBehavior)
									message = handlerErr.Message
								} else {
									//lint:ignore SA1019 verify errors remain actionable on older servers
									handlerErr := req.GetError()
									require.Equal(t, string(nexus.HandlerErrorTypeInternal), handlerErr.GetErrorType())
									require.Equal(t, enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE, handlerErr.GetRetryBehavior())
									message = handlerErr.GetFailure().GetMessage()
								}
								require.Contains(t, message, "unrecognized system Nexus envelope message type")
								require.NotContains(t, message, "external storage")
								return &workflowservice.RespondNexusTaskFailedResponse{}, nil
							})
						require.NoError(t, poller.ProcessTask(&nexusTask{task: task}))
					}
					require.Equal(t, []nexusFailureConversion{
						{
							context:   expectedContext,
							direction: "encode",
						},
						{
							context:   expectedContext,
							direction: "encode",
						},
					}, failureConverter.captured())
				})
			}
		}
	}
}
