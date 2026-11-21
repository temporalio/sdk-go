package internal

import (
	"context"
	"errors"
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
	"google.golang.org/protobuf/proto"
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
	const knownType = "temporal.api.common.v1.Payload"
	expectedContext := converter.NexusSerializationContext{
		Service:   "service",
		Operation: "operation",
	}
	for _, test := range []struct {
		name          string
		messageType   string
		encoding      string
		data          []byte
		visitErr      error
		errorType     nexus.HandlerErrorType
		retryBehavior nexus.HandlerErrorRetryBehavior
		message       string
		// legacyMessage is a substring of the failure message in the legacy HandlerError response.
		legacyMessage string
		withContext   bool
	}{
		{
			name:          "unknown type",
			messageType:   "temporal.api.workflowservice.v1.UnknownRequest",
			encoding:      converter.MetadataEncodingProto,
			errorType:     nexus.HandlerErrorTypeInternal,
			retryBehavior: nexus.HandlerErrorRetryBehaviorRetryable,
			message:       `unrecognized system Nexus envelope message type "temporal.api.workflowservice.v1.UnknownRequest"`,
			legacyMessage: `unknown message type "temporal.api.workflowservice.v1.UnknownRequest"`,
			withContext:   true,
		},
		{
			name:          "missing type",
			encoding:      converter.MetadataEncodingProto,
			errorType:     nexus.HandlerErrorTypeBadRequest,
			retryBehavior: nexus.HandlerErrorRetryBehaviorUnspecified,
			message:       "invalid system Nexus operation input",
			legacyMessage: "missing the messageType metadata",
			withContext:   true,
		},
		{
			name:          "invalid type",
			messageType:   "this isn't a valid message type",
			encoding:      converter.MetadataEncodingProto,
			errorType:     nexus.HandlerErrorTypeBadRequest,
			retryBehavior: nexus.HandlerErrorRetryBehaviorUnspecified,
			message:       "invalid system Nexus operation input",
			legacyMessage: "invalid message type",
			withContext:   true,
		},
		{
			name:          "invalid encoding",
			messageType:   knownType,
			encoding:      converter.MetadataEncodingJSON,
			errorType:     nexus.HandlerErrorTypeBadRequest,
			retryBehavior: nexus.HandlerErrorRetryBehaviorUnspecified,
			message:       "invalid system Nexus operation input",
			legacyMessage: "must be encoded as binary/protobuf",
			withContext:   true,
		},
		{
			name:          "malformed protobuf",
			messageType:   knownType,
			encoding:      converter.MetadataEncodingProto,
			data:          []byte{0xff},
			errorType:     nexus.HandlerErrorTypeBadRequest,
			retryBehavior: nexus.HandlerErrorRetryBehaviorUnspecified,
			message:       "invalid system Nexus operation input",
			legacyMessage: "failed to unmarshal system payload",
			withContext:   true,
		},
		{
			// A storage failure inside a valid envelope must not be reported as an invalid envelope.
			name:          "nested storage failure",
			messageType:   "temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest",
			encoding:      converter.MetadataEncodingProto,
			data:          mustMarshalSignalWithStartInput(t),
			visitErr:      errors.New("driver unavailable"),
			errorType:     nexus.HandlerErrorTypeInternal,
			retryBehavior: nexus.HandlerErrorRetryBehaviorUnspecified,
			message:       "failed to retrieve Nexus operation input from external storage: driver unavailable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rpc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			failureConverter := newNexusCapturingFailureConverter()
			dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), nexusInputCodec{
				decode: func([]*commonpb.Payload) ([]*commonpb.Payload, error) {
					t.Fatal("invalid system envelope must not reach the codec")
					return nil, nil
				},
			})
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
				inboundPayloadVisitor: visitorFunc(func(*proxy.VisitPayloadsContext, []*commonpb.Payload) ([]*commonpb.Payload, error) {
					if test.visitErr == nil {
						t.Fatal("invalid system envelope must be rejected before external retrieval")
					}
					return nil, test.visitErr
				}),
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
										converter.MetadataEncoding:     []byte(test.encoding),
										converter.MetadataMessageType:  []byte(test.messageType),
									},
									Data: test.data,
								},
							},
						},
					},
				}
				rpc.EXPECT().RespondNexusTaskFailed(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *workflowservice.RespondNexusTaskFailedRequest, _ ...grpc.CallOption) (*workflowservice.RespondNexusTaskFailedResponse, error) {
						require.Equal(t, task.TaskToken, req.TaskToken)
						require.Equal(t, task.PollerGroupId, req.PollerGroupId)
						if temporalFailureResponses {
							var handlerErr *nexus.HandlerError
							require.ErrorAs(t, GetDefaultFailureConverter().FailureToError(req.Failure), &handlerErr)
							require.Equal(t, test.errorType, handlerErr.Type)
							require.Equal(t, test.retryBehavior, handlerErr.RetryBehavior)
							require.Equal(t, test.message, handlerErr.Message)
						} else {
							//lint:ignore SA1019 verify errors remain actionable on older servers
							handlerErr := req.GetError()
							require.Equal(t, string(test.errorType), handlerErr.GetErrorType())
							require.Equal(t, test.retryBehavior == nexus.HandlerErrorRetryBehaviorRetryable,
								handlerErr.GetRetryBehavior() == enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE)
							require.Contains(t, handlerErr.GetFailure().GetMessage(), test.legacyMessage)
						}
						return &workflowservice.RespondNexusTaskFailedResponse{}, nil
					})
				require.NoError(t, poller.ProcessTask(&nexusTask{task: task}))
			}
			if test.withContext {
				conversion := nexusFailureConversion{context: expectedContext, direction: "encode"}
				require.Equal(t, []nexusFailureConversion{conversion, conversion}, failureConverter.captured())
			}
		})
	}
}

func mustMarshalSignalWithStartInput(t *testing.T) []byte {
	t.Helper()
	data, err := proto.Marshal(&workflowservice.SignalWithStartWorkflowExecutionRequest{
		Input: &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("stored")}}},
	})
	require.NoError(t, err)
	return data
}
