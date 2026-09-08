package internal

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
)

type capturedNexusSerializationCall struct {
	params    ExecuteNexusOperationParams
	started   func(string, error)
	completed func(*commonpb.Payload, error)
}

type captureNexusSerializationEnv struct {
	WorkflowEnvironment
	calls []*capturedNexusSerializationCall
}

func (e *captureNexusSerializationEnv) ExecuteNexusOperation(
	params ExecuteNexusOperationParams,
	completed func(*commonpb.Payload, error),
	started func(string, error),
) int64 {
	e.calls = append(e.calls, &capturedNexusSerializationCall{
		params:    params,
		started:   started,
		completed: completed,
	})
	return int64(len(e.calls))
}

func TestNexusSerializationContextInputAndResultIsolation(t *testing.T) {
	testEnv := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	testEnv.SetDataConverter(converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&serCtxSigningCodec{},
	))
	interceptor, ctx, err := newWorkflowContext(testEnv.impl, testEnv.impl.GetRegistry().interceptors)
	require.NoError(t, err)
	capture := &captureNexusSerializationEnv{WorkflowEnvironment: interceptor.env}
	interceptor.env = capture

	operationRef := mockOperationReference{name: "typed-operation", inputType: reflect.TypeFor[string]()}
	var results [2]string
	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		first := NewNexusClient("endpoint-a", "service-a").ExecuteOperation(
			ctx, operationRef, "input-a", NexusOperationOptions{},
		)
		second := NewNexusClient("endpoint-b", "service-b").ExecuteOperation(
			ctx, "string-operation", "input-b", NexusOperationOptions{},
		)

		// Complete in reverse order to prove that each future retains the
		// converter selected for its own operation.
		for i := len(capture.calls) - 1; i >= 0; i-- {
			call := capture.calls[i]
			payload, encodeErr := call.params.dataConverter.ToPayload("result-" + call.params.operation)
			if encodeErr != nil {
				panic(encodeErr)
			}
			call.started("token-"+call.params.operation, nil)
			call.completed(payload, nil)
		}

		if getErr := first.Get(ctx, &results[0]); getErr != nil {
			panic(getErr)
		}
		if getErr := second.Get(ctx, &results[1]); getErr != nil {
			panic(getErr)
		}
	}, func() bool { return false })
	d.interceptor = interceptor
	defer d.Close()

	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.Len(t, capture.calls, 2)

	expected := []converter.NexusSerializationContext{
		{Endpoint: "endpoint-a", Service: "service-a", Operation: "typed-operation"},
		{Endpoint: "endpoint-b", Service: "service-b", Operation: "string-operation"},
	}
	for i, call := range capture.calls {
		require.Equal(t, expected[i].Operation, call.params.operation)
		require.Equal(
			t,
			expected[i].Endpoint+":"+expected[i].Service+":"+expected[i].Operation,
			string(call.params.input.Metadata["ctx-signature"]),
		)
		var input string
		require.NoError(t, call.params.dataConverter.FromPayload(call.params.input, &input))
		require.Equal(t, []string{"input-a", "input-b"}[i], input)
	}
	require.Equal(t, [2]string{"result-typed-operation", "result-string-operation"}, results)
}

type nexusFailureConversion struct {
	context   converter.SerializationContext
	direction string
}

type nexusCapturingFailureConverter struct {
	converter.FailureConverter
	mu          *sync.Mutex
	conversions *[]nexusFailureConversion
	context     converter.SerializationContext
}

func newNexusCapturingFailureConverter() *nexusCapturingFailureConverter {
	conversions := make([]nexusFailureConversion, 0)
	return &nexusCapturingFailureConverter{
		FailureConverter: GetDefaultFailureConverter(),
		mu:               &sync.Mutex{},
		conversions:      &conversions,
	}
}

func (c *nexusCapturingFailureConverter) WithSerializationContext(
	ctx converter.SerializationContext,
) converter.FailureConverter {
	return &nexusCapturingFailureConverter{
		FailureConverter: c.FailureConverter,
		mu:               c.mu,
		conversions:      c.conversions,
		context:          ctx,
	}
}

func (c *nexusCapturingFailureConverter) record(direction string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.conversions = append(*c.conversions, nexusFailureConversion{
		context:   c.context,
		direction: direction,
	})
}

func (c *nexusCapturingFailureConverter) ErrorToFailure(err error) *failurepb.Failure {
	c.record("encode")
	return c.FailureConverter.ErrorToFailure(err)
}

func (c *nexusCapturingFailureConverter) FailureToError(failure *failurepb.Failure) error {
	c.record("decode")
	return c.FailureConverter.FailureToError(failure)
}

func (c *nexusCapturingFailureConverter) captured() []nexusFailureConversion {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]nexusFailureConversion, len(*c.conversions))
	copy(result, *c.conversions)
	return result
}

func TestNexusSerializationContextFailureConverterInTestEnvironment(t *testing.T) {
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	failureConverter := newNexusCapturingFailureConverter()
	env.SetFailureConverter(failureConverter)
	op := nexus.NewOperationReference[string, string]("failed-operation")
	env.OnNexusOperation("failure-service", op, "input", mock.Anything).
		Return(nil, errors.New("handler failed"))

	env.ExecuteWorkflow(func(ctx Context) error {
		return NewNexusClient("failure-endpoint", "failure-service").
			ExecuteOperation(ctx, op, "input", NexusOperationOptions{}).
			Get(ctx, nil)
	})
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())

	expected := converter.NexusSerializationContext{
		Endpoint:  "failure-endpoint",
		Service:   "failure-service",
		Operation: "failed-operation",
	}
	var found bool
	for _, conversion := range failureConverter.captured() {
		if conversion.direction == "decode" && conversion.context == expected {
			found = true
			break
		}
	}
	require.True(t, found, "Nexus failure should be decoded with its operation context")
}

func TestStandaloneNexusSerializationContextInputAndResult(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&serCtxSigningCodec{},
	)
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	expectedContext := converter.NexusSerializationContext{
		Endpoint:  "standalone-endpoint",
		Service:   "standalone-service",
		Operation: "standalone-operation",
	}
	resultPayload, err := converter.WithDataConverterSerializationContext(
		dataConverter,
		expectedContext,
	).ToPayload("standalone-result")
	require.NoError(t, err)

	service.EXPECT().
		StartNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			request *workflowservice.StartNexusOperationExecutionRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.StartNexusOperationExecutionResponse, error) {
			require.Equal(t, "standalone-endpoint:standalone-service:standalone-operation", string(request.Input.Metadata["ctx-signature"]))
			require.Empty(t, request.UserMetadata.GetSummary().Metadata["ctx-signature"])
			return &workflowservice.StartNexusOperationExecutionResponse{RunId: "standalone-run-id"}, nil
		})
	service.EXPECT().
		PollNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollNexusOperationExecutionResponse{
			Outcome: &workflowservice.PollNexusOperationExecutionResponse_Result{
				Result: resultPayload,
			},
		}, nil)

	nexusClient, err := client.NewNexusClient(ClientNexusClientOptions{
		Endpoint: expectedContext.Endpoint,
		Service:  expectedContext.Service,
	})
	require.NoError(t, err)
	handle, err := nexusClient.ExecuteOperation(
		t.Context(),
		expectedContext.Operation,
		"standalone-input",
		ClientStartNexusOperationOptions{
			ID:      "standalone-operation-id",
			Summary: "standalone-summary",
		},
	)
	require.NoError(t, err)

	var result string
	require.NoError(t, handle.Get(t.Context(), &result))
	require.Equal(t, "standalone-result", result)
}

func TestStandaloneNexusSerializationContextFailure(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	failureConverter := newNexusCapturingFailureConverter()
	client := NewServiceClient(service, nil, ClientOptions{FailureConverter: failureConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	expectedContext := converter.NexusSerializationContext{
		Endpoint:  "failure-endpoint",
		Service:   "failure-service",
		Operation: "failure-operation",
	}
	failure := GetDefaultFailureConverter().ErrorToFailure(
		NewApplicationError("operation failed", "FailureType", true, nil),
	)
	service.EXPECT().
		StartNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.StartNexusOperationExecutionResponse{RunId: "failure-run-id"}, nil)
	service.EXPECT().
		PollNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollNexusOperationExecutionResponse{
			Outcome: &workflowservice.PollNexusOperationExecutionResponse_Failure{
				Failure: failure,
			},
		}, nil)

	nexusClient, err := client.NewNexusClient(ClientNexusClientOptions{
		Endpoint: expectedContext.Endpoint,
		Service:  expectedContext.Service,
	})
	require.NoError(t, err)
	handle, err := nexusClient.ExecuteOperation(
		t.Context(),
		expectedContext.Operation,
		"input",
		ClientStartNexusOperationOptions{ID: "failure-operation-id"},
	)
	require.NoError(t, err)
	require.Error(t, handle.Get(t.Context(), nil))
	require.Equal(t, []nexusFailureConversion{{
		context:   expectedContext,
		direction: "decode",
	}}, failureConverter.captured())
}

func TestStandaloneNexusSerializationContextDetachedHandle(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&serCtxSigningCodec{},
	)
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	expectedContext := converter.NexusSerializationContext{
		Endpoint:  "detached-endpoint",
		Service:   "detached-service",
		Operation: "detached-operation",
	}
	resultPayload, err := converter.WithDataConverterSerializationContext(
		dataConverter,
		expectedContext,
	).ToPayload("detached-result")
	require.NoError(t, err)

	service.EXPECT().
		DescribeNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeNexusOperationExecutionResponse{
			Info: &nexuspb.NexusOperationExecutionInfo{
				OperationId: "detached-operation-id",
				RunId:       "resolved-run-id",
				Endpoint:    expectedContext.Endpoint,
				Service:     expectedContext.Service,
				Operation:   expectedContext.Operation,
			},
		}, nil)
	service.EXPECT().
		PollNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			request *workflowservice.PollNexusOperationExecutionRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.PollNexusOperationExecutionResponse, error) {
			require.Equal(t, "resolved-run-id", request.RunId)
			return &workflowservice.PollNexusOperationExecutionResponse{
				Outcome: &workflowservice.PollNexusOperationExecutionResponse_Result{
					Result: resultPayload,
				},
			}, nil
		})

	handle := client.GetNexusOperationHandle(ClientGetNexusOperationHandleOptions{
		OperationID: "detached-operation-id",
	})
	var result string
	require.NoError(t, handle.Get(t.Context(), &result))
	require.Equal(t, "detached-result", result)
	require.Empty(t, handle.GetRunID())
}

func TestStandaloneNexusSerializationContextUseExisting(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&serCtxSigningCodec{},
	)
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	existingContext := converter.NexusSerializationContext{
		Endpoint:  "existing-endpoint",
		Service:   "existing-service",
		Operation: "existing-operation",
	}
	resultPayload, err := converter.WithDataConverterSerializationContext(
		dataConverter,
		existingContext,
	).ToPayload("existing-result")
	require.NoError(t, err)

	service.EXPECT().
		StartNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.StartNexusOperationExecutionResponse{
			RunId:   "existing-run-id",
			Started: false,
		}, nil)
	service.EXPECT().
		DescribeNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeNexusOperationExecutionResponse{
			Info: &nexuspb.NexusOperationExecutionInfo{
				OperationId: "existing-operation-id",
				RunId:       "existing-run-id",
				Endpoint:    existingContext.Endpoint,
				Service:     existingContext.Service,
				Operation:   existingContext.Operation,
			},
		}, nil)
	service.EXPECT().
		PollNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollNexusOperationExecutionResponse{
			Outcome: &workflowservice.PollNexusOperationExecutionResponse_Result{
				Result: resultPayload,
			},
		}, nil)

	nexusClient, err := client.NewNexusClient(ClientNexusClientOptions{
		Endpoint: "requested-endpoint",
		Service:  "requested-service",
	})
	require.NoError(t, err)
	handle, err := nexusClient.ExecuteOperation(
		t.Context(),
		"requested-operation",
		"input",
		ClientStartNexusOperationOptions{
			ID:               "existing-operation-id",
			IDConflictPolicy: enumspb.NEXUS_OPERATION_ID_CONFLICT_POLICY_USE_EXISTING,
		},
	)
	require.NoError(t, err)

	var result string
	require.NoError(t, handle.Get(t.Context(), &result))
	require.Equal(t, "existing-result", result)
}

func TestStandaloneNexusSerializationContextDescribeFailures(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	failureConverter := newNexusCapturingFailureConverter()
	client := NewServiceClient(service, nil, ClientOptions{FailureConverter: failureConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	expectedContext := converter.NexusSerializationContext{
		Endpoint:  "describe-endpoint",
		Service:   "describe-service",
		Operation: "describe-operation",
	}
	failure := GetDefaultFailureConverter().ErrorToFailure(
		NewApplicationError("attempt failed", "AttemptFailureType", true, nil),
	)
	service.EXPECT().
		DescribeNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeNexusOperationExecutionResponse{
			Info: &nexuspb.NexusOperationExecutionInfo{
				OperationId:        "describe-operation-id",
				RunId:              "describe-run-id",
				Endpoint:           expectedContext.Endpoint,
				Service:            expectedContext.Service,
				Operation:          expectedContext.Operation,
				LastAttemptFailure: failure,
				CancellationInfo: &nexuspb.NexusOperationExecutionCancellationInfo{
					LastAttemptFailure: failure,
				},
			},
		}, nil)

	handle := client.GetNexusOperationHandle(ClientGetNexusOperationHandleOptions{
		OperationID: "describe-operation-id",
		RunID:       "describe-run-id",
	})
	description, err := handle.Describe(t.Context(), ClientDescribeNexusOperationOptions{})
	require.NoError(t, err)
	require.Error(t, description.GetLastAttemptFailure())
	require.Error(t, description.CancellationInfo.GetLastAttemptFailure())
	require.Equal(t, []nexusFailureConversion{
		{context: expectedContext, direction: "decode"},
		{context: expectedContext, direction: "decode"},
	}, failureConverter.captured())
}

type nexusEventFailureConverter struct {
	err                 error
	failureToErrorCalls int
}

func (c *nexusEventFailureConverter) ErrorToFailure(error) *failurepb.Failure {
	return nil
}

func (c *nexusEventFailureConverter) FailureToError(*failurepb.Failure) error {
	c.failureToErrorCalls++
	return c.err
}

func TestNexusOperationFailureEventsUseScheduledFailureConverter(t *testing.T) {
	tests := []struct {
		name          string
		cancelRequest bool
		event         func(int64, *failurepb.Failure) *historypb.HistoryEvent
	}{
		{
			name: "failed",
			event: func(scheduledEventID int64, failure *failurepb.Failure) *historypb.HistoryEvent {
				return &historypb.HistoryEvent{
					EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_FAILED,
					Attributes: &historypb.HistoryEvent_NexusOperationFailedEventAttributes{
						NexusOperationFailedEventAttributes: &historypb.NexusOperationFailedEventAttributes{
							ScheduledEventId: scheduledEventID,
							Failure:          failure,
						},
					},
				}
			},
		},
		{
			name: "canceled",
			event: func(scheduledEventID int64, failure *failurepb.Failure) *historypb.HistoryEvent {
				return &historypb.HistoryEvent{
					EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCELED,
					Attributes: &historypb.HistoryEvent_NexusOperationCanceledEventAttributes{
						NexusOperationCanceledEventAttributes: &historypb.NexusOperationCanceledEventAttributes{
							ScheduledEventId: scheduledEventID,
							Failure:          failure,
						},
					},
				}
			},
		},
		{
			name: "timed out",
			event: func(scheduledEventID int64, failure *failurepb.Failure) *historypb.HistoryEvent {
				return &historypb.HistoryEvent{
					EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_TIMED_OUT,
					Attributes: &historypb.HistoryEvent_NexusOperationTimedOutEventAttributes{
						NexusOperationTimedOutEventAttributes: &historypb.NexusOperationTimedOutEventAttributes{
							ScheduledEventId: scheduledEventID,
							Failure:          failure,
						},
					},
				}
			},
		},
		{
			name:          "cancel request failed",
			cancelRequest: true,
			event: func(scheduledEventID int64, failure *failurepb.Failure) *historypb.HistoryEvent {
				return &historypb.HistoryEvent{
					EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCEL_REQUEST_FAILED,
					Attributes: &historypb.HistoryEvent_NexusOperationCancelRequestFailedEventAttributes{
						NexusOperationCancelRequestFailedEventAttributes: &historypb.NexusOperationCancelRequestFailedEventAttributes{
							ScheduledEventId: scheduledEventID,
							Failure:          failure,
						},
					},
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commandsHelper := newCommandsHelper()
			commandsHelper.setCurrentWorkflowTaskStartedEventID(1)
			selectedErr := errors.New("selected Nexus failure converter")
			selectedConverter := &nexusEventFailureConverter{err: selectedErr}
			fallbackConverter := &nexusEventFailureConverter{err: errors.New("fallback failure converter")}
			env := &workflowEnvironmentImpl{
				commandsHelper:   commandsHelper,
				dataConverter:    converter.GetDefaultDataConverter(),
				failureConverter: fallbackConverter,
				logger:           ilog.NewNopLogger(),
			}

			var callbackErr error
			seq := env.ExecuteNexusOperation(ExecuteNexusOperationParams{
				client:           NewNexusClient("endpoint", "service"),
				operation:        "operation",
				options:          NexusOperationOptions{CancellationType: NexusOperationCancellationTypeWaitRequested},
				failureConverter: selectedConverter,
			}, func(_ *commonpb.Payload, err error) {
				callbackErr = err
			}, nil)

			commandsHelper.getCommands(true)
			const scheduledEventID = 10
			commandsHelper.handleNexusOperationScheduled(&historypb.HistoryEvent{
				EventId:   scheduledEventID,
				EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED,
			})
			weh := &workflowExecutionEventHandlerImpl{workflowEnvironmentImpl: env}
			if test.cancelRequest {
				env.RequestCancelNexusOperation(seq)
				commandsHelper.getCommands(true)
				require.NoError(t, weh.handleNexusOperationCancelRequested(&historypb.HistoryEvent{
					EventType: enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCEL_REQUESTED,
					Attributes: &historypb.HistoryEvent_NexusOperationCancelRequestedEventAttributes{
						NexusOperationCancelRequestedEventAttributes: &historypb.NexusOperationCancelRequestedEventAttributes{
							ScheduledEventId: scheduledEventID,
						},
					},
				}))
				require.NoError(t, weh.handleNexusOperationCancelRequestDelivered(
					test.event(scheduledEventID, &failurepb.Failure{Message: "failure"}),
				))
			} else {
				require.NoError(t, weh.handleNexusOperationCompleted(
					test.event(scheduledEventID, &failurepb.Failure{Message: "failure"}),
				))
			}

			require.ErrorIs(t, callbackErr, selectedErr)
			require.Equal(t, 1, selectedConverter.failureToErrorCalls)
			require.Zero(t, fallbackConverter.failureToErrorCalls)
		})
	}
}
