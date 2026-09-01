package internal

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
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
