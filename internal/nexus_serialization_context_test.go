package internal

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	activitypb "go.temporal.io/api/activity/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestNexusBackedWorkflowStartSerializationContext(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	expectedContext := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, request *workflowservice.StartWorkflowExecutionRequest, _ ...grpc.CallOption) (*workflowservice.StartWorkflowExecutionResponse, error) {
			require.Equal(t, nexusSerializationContextToProto(expectedContext), request.GetPropagatedNexusSerializationContext())
			require.Equal(t, "endpoint:service:operation", string(request.GetInput().GetPayloads()[0].GetMetadata()["ctx-signature"]))
			return &workflowservice.StartWorkflowExecutionResponse{RunId: "run-id"}, nil
		})

	ctx := ContextWithNexusOperationContext(t.Context(), &NexusOperationContext{nexusSerializationContext: expectedContext})
	_, err := client.ExecuteWorkflow(ctx, StartWorkflowOptions{ID: "workflow-id", TaskQueue: "task-queue"}, "workflow", "input")
	require.NoError(t, err)
}

func TestNexusBackedActivityStartSerializationContext(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	expectedContext := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	service.EXPECT().StartActivityExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, request *workflowservice.StartActivityExecutionRequest, _ ...grpc.CallOption) (*workflowservice.StartActivityExecutionResponse, error) {
			require.Equal(t, nexusSerializationContextToProto(expectedContext), request.GetPropagatedNexusSerializationContext())
			require.Equal(t, "endpoint:service:operation", string(request.GetInput().GetPayloads()[0].GetMetadata()["ctx-signature"]))
			return &workflowservice.StartActivityExecutionResponse{RunId: "run-id"}, nil
		})
	resultPayloads, err := converter.WithDataConverterSerializationContext(dataConverter, expectedContext).ToPayloads("result")
	require.NoError(t, err)
	service.EXPECT().PollActivityExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollActivityExecutionResponse{
			Outcome: &activitypb.ActivityExecutionOutcome{
				Value: &activitypb.ActivityExecutionOutcome_Result{Result: resultPayloads},
			},
		}, nil)
	inputPayloads, err := converter.WithDataConverterSerializationContext(dataConverter, expectedContext).ToPayloads("input")
	require.NoError(t, err)
	service.EXPECT().DescribeActivityExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeActivityExecutionResponse{
			Info: &activitypb.ActivityExecutionInfo{
				ActivityId: "activity-id", ActivityType: &commonpb.ActivityType{Name: "activity"},
				TaskQueue: "task-queue", SearchAttributes: &commonpb.SearchAttributes{},
			},
			Input: inputPayloads,
			Outcome: &activitypb.ActivityExecutionOutcome{
				Value: &activitypb.ActivityExecutionOutcome_Result{Result: resultPayloads},
			},
		}, nil)

	ctx := ContextWithNexusOperationContext(t.Context(), &NexusOperationContext{nexusSerializationContext: expectedContext})
	handle, err := client.ExecuteActivity(ctx, ClientStartActivityOptions{
		ID: "activity-id", TaskQueue: "task-queue", ScheduleToCloseTimeout: time.Minute,
	}, "activity", "input")
	require.NoError(t, err)
	var result string
	require.NoError(t, handle.Get(t.Context(), &result))
	require.Equal(t, "result", result)
	description, err := handle.Describe(t.Context(), ClientDescribeActivityOptions{IncludeInput: true, IncludeOutcome: true})
	require.NoError(t, err)
	var input string
	require.NoError(t, description.GetInput(&input))
	require.Equal(t, "input", input)
	require.NoError(t, description.GetResult(&result))
	require.Equal(t, "result", result)
}

func TestNexusBackedWorkflowDetachedHandleResultSerializationContext(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	nexusContext := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	result, err := converter.WithDataConverterSerializationContext(dataConverter, nexusContext).ToPayloads("result")
	require.NoError(t, err)
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
			History:                             &historypb.History{},
			NextPageToken:                       []byte("next-page"),
			PropagatedNexusSerializationContext: nexusSerializationContextToProto(nexusContext),
		}, nil)
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
			require.Equal(t, []byte("next-page"), request.GetNextPageToken())
			return &workflowservice.GetWorkflowExecutionHistoryResponse{
				History: &historypb.History{Events: []*historypb.HistoryEvent{{
					EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
						WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: result},
					},
				}}},
			}, nil
		})

	handle := client.GetWorkflow(t.Context(), "workflow-id", "run-id")
	var decoded string
	require.NoError(t, handle.Get(t.Context(), &decoded))
	require.Equal(t, "result", decoded)
}

func TestNexusBackedWorkflowDetachedHandleFailureSerializationContext(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	failureConverter := newNexusCapturingFailureConverter()
	client := NewServiceClient(service, nil, ClientOptions{FailureConverter: failureConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	context := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	failure := GetDefaultFailureConverter().ErrorToFailure(NewApplicationError("failed", "FailureType", true, nil))
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
			PropagatedNexusSerializationContext: nexusSerializationContextToProto(context),
			History: &historypb.History{Events: []*historypb.HistoryEvent{{
				EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
					WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{Failure: failure},
				},
			}}},
		}, nil)

	handle := client.GetWorkflow(t.Context(), "workflow-id", "run-id")
	require.Error(t, handle.Get(t.Context(), nil))
	require.Equal(t, []nexusFailureConversion{{context: context, direction: "decode"}}, failureConverter.captured())
}

func TestNexusBackedActivityDetachedHandleResultSerializationContext(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	context := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	result, err := converter.WithDataConverterSerializationContext(dataConverter, context).ToPayloads("result")
	require.NoError(t, err)
	service.EXPECT().PollActivityExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollActivityExecutionResponse{
			PropagatedNexusSerializationContext: nexusSerializationContextToProto(context),
			Outcome: &activitypb.ActivityExecutionOutcome{
				Value: &activitypb.ActivityExecutionOutcome_Result{Result: result},
			},
		}, nil)

	handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id", RunID: "run-id"})
	var decoded string
	require.NoError(t, handle.Get(t.Context(), &decoded))
	require.Equal(t, "result", decoded)
}

func TestNexusBackedActivityDetachedHandleFailureSerializationContext(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	failureConverter := newNexusCapturingFailureConverter()
	client := NewServiceClient(service, nil, ClientOptions{FailureConverter: failureConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	context := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	failure := GetDefaultFailureConverter().ErrorToFailure(NewApplicationError("failed", "FailureType", true, nil))
	service.EXPECT().PollActivityExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollActivityExecutionResponse{
			PropagatedNexusSerializationContext: nexusSerializationContextToProto(context),
			Outcome: &activitypb.ActivityExecutionOutcome{
				Value: &activitypb.ActivityExecutionOutcome_Failure{Failure: failure},
			},
		}, nil)

	handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id", RunID: "run-id"})
	require.Error(t, handle.Get(t.Context(), nil))
	require.Equal(t, []nexusFailureConversion{{context: context, direction: "decode"}}, failureConverter.captured())
}

func TestNexusBackedWorkflowExecutorInputAndResultSerializationContext(t *testing.T) {
	nexusContext := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	wfInfo := &WorkflowInfo{
		Namespace:                           "namespace",
		WorkflowExecution:                   WorkflowExecution{ID: "workflow-id", RunID: "run-id"},
		WorkflowType:                        WorkflowType{Name: "workflow"},
		TaskQueueName:                       "task-queue",
		propagatedNexusSerializationContext: &nexusContext,
	}
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	eventHandler := newWorkflowExecutionEventHandler(
		wfInfo, nil, ilog.NewNopLogger(), false, metrics.NopHandler, newRegistry(),
		dataConverter, GetDefaultFailureConverter(), nil, 0, nil, nil,
	).(*workflowExecutionEventHandlerImpl)
	_, ctx, err := newWorkflowContext(eventHandler.workflowEnvironmentImpl, nil)
	require.NoError(t, err)
	input, err := converter.WithDataConverterSerializationContext(dataConverter, nexusContext).ToPayloads("input")
	require.NoError(t, err)
	executor := &workflowExecutor{
		workflowType: "workflow",
		fn: func(_ Context, value string) (string, error) {
			require.Equal(t, "input", value)
			return "result", nil
		},
	}
	result, err := executor.Execute(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "endpoint:service:operation", string(result.GetPayloads()[0].GetMetadata()["ctx-signature"]))
	var decoded string
	require.NoError(t, converter.WithDataConverterSerializationContext(dataConverter, nexusContext).FromPayloads(result, &decoded))
	require.Equal(t, "result", decoded)
}

func TestNexusBackedWorkflowCompletionFailureSerializationContext(t *testing.T) {
	nexusContext := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	wfInfo := &WorkflowInfo{
		Namespace:                           "namespace",
		WorkflowExecution:                   WorkflowExecution{ID: "workflow-id", RunID: "run-id"},
		WorkflowType:                        WorkflowType{Name: "workflow"},
		propagatedNexusSerializationContext: &nexusContext,
	}
	failureConverter := newNexusCapturingFailureConverter()
	eventHandler := newWorkflowExecutionEventHandler(
		wfInfo, nil, ilog.NewNopLogger(), false, metrics.NopHandler, newRegistry(),
		converter.GetDefaultDataConverter(), failureConverter, nil, 0, nil, nil,
	).(*workflowExecutionEventHandlerImpl)
	wth := &workflowTaskHandlerImpl{
		namespace:        "namespace",
		failureConverter: failureConverter,
		metricsHandler:   metrics.NopHandler,
	}
	completion := wth.completeWorkflow(
		eventHandler,
		&workflowservice.PollWorkflowTaskQueueResponse{
			WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflow-id", RunId: "run-id"},
		},
		&workflowExecutionContextImpl{workflowInfo: wfInfo, err: errors.New("failed")},
		nil, nil, false,
	)
	request, ok := completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	require.True(t, ok)
	require.Len(t, request.Commands, 1)
	require.NotNil(t, request.Commands[0].GetFailWorkflowExecutionCommandAttributes())
	require.Equal(t, []nexusFailureConversion{{context: nexusContext, direction: "encode"}}, failureConverter.captured())
}

func TestNexusBackedActivityWorkerSerializationContext(t *testing.T) {
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	context := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	task := &workflowservice.PollActivityTaskQueueResponse{
		WorkflowNamespace:                   "namespace",
		ActivityType:                        &commonpb.ActivityType{Name: "activity"},
		PropagatedNexusSerializationContext: nexusSerializationContextToProto(context),
	}
	ctx, err := WithActivityTask(t.Context(), task, "task-queue", nil, ilog.NewNopLogger(), metrics.NopHandler,
		dataConverter, nil, nil, nil, nil)
	require.NoError(t, err)
	payload, err := getActivityEnv(ctx).dataConverter.ToPayload("result")
	require.NoError(t, err)
	require.Equal(t, "endpoint:service:operation", string(payload.GetMetadata()["ctx-signature"]))
}

func TestNexusBackedActivityCompletionFailureSerializationContext(t *testing.T) {
	nexusContext := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	failureConverter := newNexusCapturingFailureConverter()
	registry := newRegistry()
	registry.RegisterActivityWithOptions(func(context.Context) error {
		return errors.New("failed")
	}, RegisterActivityOptions{Name: "activity"})
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	ath := &activityTaskHandlerImpl{
		client:           NewServiceClient(service, nil, ClientOptions{}),
		namespace:        "namespace",
		logger:           ilog.NewNopLogger(),
		metricsHandler:   metrics.NopHandler,
		registry:         registry,
		dataConverter:    converter.GetDefaultDataConverter(),
		failureConverter: failureConverter,
	}
	now := timestamppb.Now()
	completion, err := ath.Execute("task-queue", &workflowservice.PollActivityTaskQueueResponse{
		TaskToken:                           []byte("task-token"),
		WorkflowNamespace:                   "namespace",
		ActivityId:                          "activity-id",
		ActivityRunId:                       "run-id",
		ActivityType:                        &commonpb.ActivityType{Name: "activity"},
		ScheduledTime:                       now,
		StartedTime:                         now,
		ScheduleToCloseTimeout:              durationpb.New(time.Minute),
		StartToCloseTimeout:                 durationpb.New(time.Minute),
		PropagatedNexusSerializationContext: nexusSerializationContextToProto(nexusContext),
	})
	require.NoError(t, err)
	request, ok := completion.response.(*workflowservice.RespondActivityTaskFailedRequest)
	require.True(t, ok)
	require.Equal(t, "failed", request.GetFailure().GetMessage())
	require.Equal(t, []nexusFailureConversion{{context: nexusContext, direction: "encode"}}, failureConverter.captured())
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

func TestNexusTaskHandlerSerializationContextInputAndSyncResult(t *testing.T) {
	expectedContext := converter.NexusSerializationContext{
		Endpoint:  "handler-endpoint",
		Service:   "handler-service",
		Operation: "handler-operation",
	}
	dataConverter := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&serCtxSigningCodec{},
	)
	contextualDataConverter := converter.WithDataConverterSerializationContext(dataConverter, expectedContext)
	inputPayload, err := contextualDataConverter.ToPayload("handler-input")
	require.NoError(t, err)

	operation := nexus.NewSyncOperation(
		expectedContext.Operation,
		func(_ context.Context, input string, _ nexus.StartOperationOptions) (string, error) {
			require.Equal(t, "handler-input", input)
			return "handler-result", nil
		},
	)
	service := nexus.NewService(expectedContext.Service)
	require.NoError(t, service.Register(operation))
	registry := nexus.NewServiceRegistry()
	require.NoError(t, registry.Register(service))
	registry.Use(nexusMiddleware(nil))
	handler, err := registry.NewHandler()
	require.NoError(t, err)

	taskHandler := newNexusTaskHandler(
		handler,
		"identity",
		"namespace",
		"task-queue",
		nil,
		dataConverter,
		GetDefaultFailureConverter(),
		ilog.NewNopLogger(),
		metrics.NopHandler,
		newRegistry(),
	)
	completed, failed, err := taskHandler.Execute(&workflowservice.PollNexusTaskQueueResponse{
		TaskToken: []byte("task-token"),
		Request: &nexuspb.Request{
			Endpoint: expectedContext.Endpoint,
			Variant: &nexuspb.Request_StartOperation{
				StartOperation: &nexuspb.StartOperationRequest{
					Service:   expectedContext.Service,
					Operation: expectedContext.Operation,
					Payload:   inputPayload,
				},
			},
		},
	})
	require.NoError(t, err)
	require.Nil(t, failed)

	resultPayload := completed.GetResponse().GetStartOperation().GetSyncSuccess().GetPayload()
	require.Equal(t, "handler-endpoint:handler-service:handler-operation", string(resultPayload.Metadata["ctx-signature"]))
	var result string
	require.NoError(t, contextualDataConverter.FromPayload(resultPayload, &result))
	require.Equal(t, "handler-result", result)
}

func TestNexusTaskHandlerFailureSerializationContext(t *testing.T) {
	expectedContext := converter.NexusSerializationContext{
		Endpoint:  "handler-endpoint",
		Service:   "handler-service",
		Operation: "handler-operation",
	}
	inputPayload, err := converter.GetDefaultDataConverter().ToPayload("handler-input")
	require.NoError(t, err)

	tests := []struct {
		name             string
		err              error
		expectCompletion bool
	}{
		{
			name:             "operation failure",
			err:              nexus.NewOperationFailedErrorf("operation failed"),
			expectCompletion: true,
		},
		{
			name: "handler failure",
			err: &nexus.HandlerError{
				Type:  nexus.HandlerErrorTypeBadRequest,
				Cause: errors.New("handler failed"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failureConverter := newNexusCapturingFailureConverter()
			operation := nexus.NewSyncOperation(
				expectedContext.Operation,
				func(context.Context, string, nexus.StartOperationOptions) (string, error) {
					return "", test.err
				},
			)
			service := nexus.NewService(expectedContext.Service)
			require.NoError(t, service.Register(operation))
			registry := nexus.NewServiceRegistry()
			require.NoError(t, registry.Register(service))
			registry.Use(nexusMiddleware(nil))
			handler, err := registry.NewHandler()
			require.NoError(t, err)

			taskHandler := newNexusTaskHandler(
				handler,
				"identity",
				"namespace",
				"task-queue",
				nil,
				converter.GetDefaultDataConverter(),
				failureConverter,
				ilog.NewNopLogger(),
				metrics.NopHandler,
				newRegistry(),
			)
			completed, failed, err := taskHandler.Execute(&workflowservice.PollNexusTaskQueueResponse{
				TaskToken: []byte("task-token"),
				Request: &nexuspb.Request{
					Endpoint: expectedContext.Endpoint,
					Capabilities: &nexuspb.Request_Capabilities{
						TemporalFailureResponses: true,
					},
					Variant: &nexuspb.Request_StartOperation{
						StartOperation: &nexuspb.StartOperationRequest{
							Service:   expectedContext.Service,
							Operation: expectedContext.Operation,
							Payload:   inputPayload,
						},
					},
				},
			})
			require.NoError(t, err)
			if test.expectCompletion {
				require.NotNil(t, completed)
				require.Nil(t, failed)
			} else {
				require.Nil(t, completed)
				require.NotNil(t, failed)
			}
			require.Equal(t, []nexusFailureConversion{{
				context:   expectedContext,
				direction: "encode",
			}}, failureConverter.captured())
		})
	}
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

func TestStandaloneNexusSerializationContextUseExisting(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		&serCtxSigningCodec{},
	)
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	requestContext := converter.NexusSerializationContext{
		Endpoint:  "requested-endpoint",
		Service:   "requested-service",
		Operation: "requested-operation",
	}
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
		PollNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollNexusOperationExecutionResponse{
			PropagatedNexusSerializationContext: nexusSerializationContextToProto(existingContext),
			Outcome: &workflowservice.PollNexusOperationExecutionResponse_Result{
				Result: resultPayload,
			},
		}, nil)

	nexusClient, err := client.NewNexusClient(ClientNexusClientOptions{
		Endpoint: requestContext.Endpoint,
		Service:  requestContext.Service,
	})
	require.NoError(t, err)
	handle, err := nexusClient.ExecuteOperation(
		t.Context(),
		requestContext.Operation,
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

func TestStandaloneNexusSerializationContextDetachedHandle(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	client := NewServiceClient(service, nil, ClientOptions{DataConverter: dataConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	context := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	resultPayload, err := converter.WithDataConverterSerializationContext(dataConverter, context).ToPayload("result")
	require.NoError(t, err)
	service.EXPECT().PollNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollNexusOperationExecutionResponse{
			PropagatedNexusSerializationContext: nexusSerializationContextToProto(context),
			Outcome: &workflowservice.PollNexusOperationExecutionResponse_Result{
				Result: resultPayload,
			},
		}, nil)

	handle := client.GetNexusOperationHandle(ClientGetNexusOperationHandleOptions{OperationID: "operation-id", RunID: "run-id"})
	var result string
	require.NoError(t, handle.Get(t.Context(), &result))
	require.Equal(t, "result", result)
}

func TestStandaloneNexusSerializationContextDetachedHandleFailure(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	failureConverter := newNexusCapturingFailureConverter()
	client := NewServiceClient(service, nil, ClientOptions{FailureConverter: failureConverter})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	context := converter.NexusSerializationContext{Endpoint: "endpoint", Service: "service", Operation: "operation"}
	failure := GetDefaultFailureConverter().ErrorToFailure(NewApplicationError("failed", "FailureType", true, nil))
	service.EXPECT().PollNexusOperationExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollNexusOperationExecutionResponse{
			PropagatedNexusSerializationContext: nexusSerializationContextToProto(context),
			Outcome: &workflowservice.PollNexusOperationExecutionResponse_Failure{
				Failure: failure,
			},
		}, nil)

	handle := client.GetNexusOperationHandle(ClientGetNexusOperationHandleOptions{OperationID: "operation-id", RunID: "run-id"})
	require.Error(t, handle.Get(t.Context(), nil))
	require.Equal(t, []nexusFailureConversion{{context: context, direction: "decode"}}, failureConverter.captured())
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
