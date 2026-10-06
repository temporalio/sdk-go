package internal

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// -- BASIC TESTS ----------------------------------------------------

// -- DATA --

// temperature is a struct with no exported fields and its transfer type is a float64.
type temperature struct{ kelvin float64 }

func (temperature) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return converter.NewTransferTypeConverter(
		func(t *temperature) (*float64, error) {
			return &t.kelvin, nil
		},
		func(kelvin *float64, t *temperature) error {
			t.kelvin = *kelvin
			return nil
		},
	)
}

// unencodable always returns an error during transfer-type-encoding.
type unencodable struct{}

var errNoEncoding = errors.New("cannot encode")

func (unencodable) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return converter.NewTransferTypeConverter(
		func(*unencodable) (*string, error) { return nil, errNoEncoding },
		func(*string, *unencodable) error { return nil },
	)
}

// undecodable always returns an error during transfer-type-decoding.
type undecodable struct{}

var errNoDecoding = errors.New("cannot decode")

func (undecodable) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return converter.NewTransferTypeConverter(
		func(*undecodable) (*string, error) {
			encoded := "encoded"
			return &encoded, nil
		},
		func(*string, *undecodable) error { return errNoDecoding },
	)
}

// invalidTransferConverter returns an error when creating the converter.
type invalidTransferConverter struct{}

func (invalidTransferConverter) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return nil, errNoEncoding
}

// mismatchedTransferModel returns a converter for a different model type.
type mismatchedTransferModel struct{}

func (mismatchedTransferModel) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return (temperature{}).TransferTypeConverter()
}

// contextualString has a transfer type converter that looks for [transferContextKey]
// in the context to compute the model and transfer type.
type contextualString string

type transferContextKey struct{}

func (contextualString) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return converter.NewContextualTransferTypeConverter(
		func(ctx context.Context, value *contextualString) (*string, error) {
			label, _ := ctx.Value(transferContextKey{}).(string)
			transferType := fmt.Sprintf("go:%s:%s", label, string(*value))
			return &transferType, nil
		},
		func(ctx context.Context, transferType *string, value *contextualString) error {
			*value = contextualString(fmt.Sprintf("go:%v:%s", ctx.Value(transferContextKey{}), *transferType))
			return nil
		},
		func(ctx Context, value *contextualString) (*string, error) {
			label, _ := ctx.Value(transferContextKey{}).(string)
			transferType := fmt.Sprintf("wf:%s:%s", label, string(*value))
			return &transferType, nil
		},
		func(ctx Context, transferType *string, value *contextualString) error {
			*value = contextualString(fmt.Sprintf("wf:%v:%s", ctx.Value(transferContextKey{}), *transferType))
			return nil
		},
	)
}

// transferEnvelope is a struct that contains a transfer-convertible field,
// but the struct itself has no transfer type converter.
type transferEnvelope struct{ Value contextualString }

// -- TESTS --

func TestTransferAwareDataConverter_PayloadRoundTrip(t *testing.T) {
	t.Parallel()
	dc := makeTransferAware(nil)

	t.Run("scalar transfer values", func(t *testing.T) {
		values := make([]temperature, 10)
		for i := range values {
			values[i] = temperature{kelvin: rand.Float64() * 1_000}
		}

		for _, value := range values {
			payload, err := dc.ToPayload(value)
			require.NoError(t, err)

			var got temperature
			require.NoError(t, dc.FromPayload(payload, &got))
			require.Equal(t, value, got)
		}
	})

	t.Run("values without a transfer type converter", func(t *testing.T) {
		values := make([]string, 10)
		for i := range values {
			values[i] = "plain-" + strconv.FormatUint(rand.Uint64(), 10)
		}

		for _, value := range values {
			payload, err := dc.ToPayload(value)
			require.NoError(t, err)

			want, err := converter.GetDefaultDataConverter().ToPayload(value)
			require.NoError(t, err)
			require.Equal(t, want.GetData(), payload.GetData())

			var got string
			require.NoError(t, dc.FromPayload(payload, &got))
			require.Equal(t, value, got)
		}
	})
}

func TestTransferAwareDataConverter_PointerValueRoundTrip(t *testing.T) {
	t.Parallel()
	for _, compressed := range []bool{false, true} {
		parent := converter.GetDefaultDataConverter()
		if compressed {
			parent = converter.NewCodecDataConverter(parent, converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
		}
		for _, batch := range []bool{false, true} {
			t.Run(fmt.Sprintf("compressed=%v/batch=%v", compressed, batch), func(t *testing.T) {
				dc := makeTransferAware(parent)
				want := &temperature{kelvin: 300}
				got := temperature{kelvin: 99}
				if batch {
					payloads, err := dc.ToPayloads(want)
					require.NoError(t, err)
					require.NoError(t, dc.FromPayloads(payloads, &got))
				} else {
					payload, err := dc.ToPayload(want)
					require.NoError(t, err)
					require.NoError(t, dc.FromPayload(payload, &got))
				}
				require.Equal(t, *want, got)
			})
		}
	}
}

func TestTransferAwareDataConverter_PayloadsRoundTrip(t *testing.T) {
	t.Parallel()
	dc := DefaultInternalDataConverter

	t.Run("scalar transfer values", func(t *testing.T) {
		values := make([]temperature, 10)
		valuePtrs := make([]any, len(values))
		got := make([]temperature, len(values))
		for i := range values {
			values[i] = temperature{kelvin: rand.Float64() * 1_000}
			valuePtrs[i] = &got[i]
		}

		payloads, err := dc.ToPayloads(sliceToAny(values)...)
		require.NoError(t, err)
		require.NoError(t, dc.FromPayloads(payloads, valuePtrs...))
		require.Equal(t, values, got)
	})

	t.Run("values without a transfer type converter", func(t *testing.T) {
		values := make([]string, 10)
		valuePtrs := make([]any, len(values))
		got := make([]string, len(values))
		for i := range values {
			values[i] = "plain-" + strconv.FormatUint(rand.Uint64(), 10)
			valuePtrs[i] = &got[i]
		}

		payloads, err := dc.ToPayloads(sliceToAny(values)...)
		require.NoError(t, err)
		require.NoError(t, dc.FromPayloads(payloads, valuePtrs...))
		require.Equal(t, values, got)
	})
}

func sliceToAny[T any](values []T) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

func TestTransferAwareDataConverter_MatchesParentForPlainValues(t *testing.T) {
	t.Parallel()
	parent := converter.GetDefaultDataConverter()
	dc := makeTransferAware(parent)

	requireSamePayloads := func(t *testing.T, want, got *commonpb.Payloads) {
		t.Helper()
		require.Len(t, got.GetPayloads(), len(want.GetPayloads()))
		for i, wantPayload := range want.GetPayloads() {
			require.Equal(t, wantPayload.GetData(), got.GetPayloads()[i].GetData(), "payload %d data", i)
			require.Equal(t, wantPayload.GetMetadata(), got.GetPayloads()[i].GetMetadata(), "payload %d metadata", i)
		}
	}

	t.Run("no values", func(t *testing.T) {
		got, err := dc.ToPayloads()
		require.NoError(t, err)
		want, err := parent.ToPayloads()
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("only plain values", func(t *testing.T) {
		values := []any{"plain", 42, []string{"a", "b"}, nil}
		got, err := dc.ToPayloads(values...)
		require.NoError(t, err)
		want, err := parent.ToPayloads(values...)
		require.NoError(t, err)
		requireSamePayloads(t, want, got)
	})

	// Plain values keep their place and their encoding even when a transfer
	// value sits next to them.
	t.Run("plain values mixed with transfer values", func(t *testing.T) {
		got, err := dc.ToPayloads("plain", temperature{kelvin: 300}, 42, temperature{kelvin: 275}, nil)
		require.NoError(t, err)
		want, err := parent.ToPayloads("plain", 300.0, 42, 275.0, nil)
		require.NoError(t, err)
		requireSamePayloads(t, want, got)
	})

	t.Run("decoding only plain values", func(t *testing.T) {
		payloads, err := dc.ToPayloads("plain", 42)
		require.NoError(t, err)

		var (
			gotString string
			gotInt    int
		)
		require.NoError(t, dc.FromPayloads(payloads, &gotString, &gotInt))
		require.Equal(t, "plain", gotString)
		require.Equal(t, 42, gotInt)
	})
}

func TestTransferAwareDataConverter_ConversionErrors(t *testing.T) {
	t.Parallel()
	dc := DefaultInternalDataConverter

	t.Run("encoding one value", func(t *testing.T) {
		_, err := dc.ToPayload(unencodable{})
		require.ErrorIs(t, err, errNoEncoding)
	})

	t.Run("encoding a list", func(t *testing.T) {
		_, err := dc.ToPayloads("plain", unencodable{})
		require.ErrorIs(t, err, errNoEncoding)
	})

	t.Run("decoding one value", func(t *testing.T) {
		payload, err := dc.ToPayload(undecodable{})
		require.NoError(t, err)
		require.ErrorIs(t, dc.FromPayload(payload, &undecodable{}), errNoDecoding)
	})

	t.Run("decoding a list", func(t *testing.T) {
		payloads, err := dc.ToPayloads("plain", undecodable{})
		require.NoError(t, err)

		var got string
		err = dc.FromPayloads(payloads, &got, &undecodable{})
		require.ErrorIs(t, err, errNoDecoding)
	})
}

func TestTransferAwareDataConverter_AbsentBatchPayload(t *testing.T) {
	t.Parallel()
	dc := makeTransferAware(nil)
	payload, err := dc.ToPayload(temperature{kelvin: 300})
	require.NoError(t, err)
	single, batch := temperature{kelvin: 99}, temperature{kelvin: 99}
	var converted temperature
	require.NoError(t, dc.FromPayload(nil, &single))
	require.NoError(t, dc.FromPayloads(&commonpb.Payloads{Payloads: []*commonpb.Payload{nil, payload}}, &batch, &converted))
	require.Equal(t, single, batch)
	require.Equal(t, temperature{kelvin: 300}, converted)
}

func TestTransferAwareDataConverter_PlainNilPointerInput(t *testing.T) {
	t.Parallel()
	parent := converter.GetDefaultDataConverter()
	var input *string
	want, err := parent.ToPayload(input)
	require.NoError(t, err)

	got, err := makeTransferAware(parent).ToPayload(input)
	require.NoError(t, err)
	require.True(t, proto.Equal(want, got))

	wantBatch, err := parent.ToPayloads(input, 300.0, input)
	require.NoError(t, err)
	gotBatch, err := makeTransferAware(parent).ToPayloads(input, temperature{kelvin: 300}, input)
	require.NoError(t, err)
	require.True(t, proto.Equal(wantBatch, gotBatch))
}

func TestTransferAwareDataConverter_ContextDelegation(t *testing.T) {
	t.Parallel()

	t.Run("context-aware parent", func(t *testing.T) {
		dc := makeTransferAware(NewContextAwareDataConverter(converter.GetDefaultDataConverter()))

		ctx := context.WithValue(t.Context(), ContextAwareDataConverterContextKey, "300")
		masked := WithContext(ctx, dc)
		require.NotSame(t, dc, masked)

		payload, err := masked.ToPayload(temperature{kelvin: 300})
		require.NoError(t, err)
		require.Equal(t, "?", string(payload.GetData()))
	})

	// Even when the parent has no use for a context, we hold on to it: transfer
	// converters may still want it.
	t.Run("parent that is not context aware", func(t *testing.T) {
		dc := DefaultInternalDataConverter
		require.NotSame(t, dc, WithContext(t.Context(), dc))
		require.NotSame(t, dc, WithWorkflowContext(Background(), dc))
		// Serialization contexts are only forwarded, so there is nothing to keep.
		require.Same(t, dc, converter.WithDataConverterSerializationContext(dc, converter.WorkflowSerializationContext{}))
	})
}

func TestTransferAwareDataConverter_ConversionContext(t *testing.T) {
	t.Parallel()
	parent := converter.GetDefaultDataConverter()
	ctx := WithValue(Background(), transferContextKey{}, "workflow")
	dc := WithWorkflowContext(ctx, DefaultInternalDataConverter)

	payload, err := dc.ToPayload(contextualString("value"))
	require.NoError(t, err)
	want, err := parent.ToPayload("wf:workflow:value")
	require.NoError(t, err)
	require.Equal(t, want.GetData(), payload.GetData())

	payload, err = parent.ToPayload("value")
	require.NoError(t, err)
	var got contextualString
	require.NoError(t, dc.FromPayload(payload, &got))
	require.Equal(t, contextualString("wf:workflow:value"), got)

	payloads, err := dc.ToPayloads(contextualString("one"), contextualString("two"))
	require.NoError(t, err)
	wants, err := parent.ToPayloads("wf:workflow:one", "wf:workflow:two")
	require.NoError(t, err)
	require.Equal(t, wants.GetPayloads()[0].GetData(), payloads.GetPayloads()[0].GetData())
	require.Equal(t, wants.GetPayloads()[1].GetData(), payloads.GetPayloads()[1].GetData())

	payloads, err = parent.ToPayloads("one", "two")
	require.NoError(t, err)
	var gotOne, gotTwo contextualString
	require.NoError(t, dc.FromPayloads(payloads, &gotOne, &gotTwo))
	require.Equal(t, contextualString("wf:workflow:one"), gotOne)
	require.Equal(t, contextualString("wf:workflow:two"), gotTwo)
}

// -- SDK INTEGRATION TESTS -----------------------------------------------------------

func newTransferTestClient(t *testing.T, dc converter.DataConverter) (*workflowservicemock.MockWorkflowServiceClient, *WorkflowClient) {
	t.Helper()
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	client := NewServiceClient(service, nil, ClientOptions{
		Namespace: "transfer-test", DataConverter: dc,
	})
	return service, client
}

func TestTransferTypesMockClientWorkflowInput(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		workflow any
		args     []any
		wireArgs []any
	}{
		{
			name:     "workflow args use transfer type converters when available",
			workflow: func(Context, string, temperature, temperature, transferEnvelope) error { return nil },
			args:     []any{"plain", temperature{kelvin: 300}, temperature{kelvin: 275}, transferEnvelope{Value: "value"}},
			wireArgs: []any{"plain", 300.0, 275.0, map[string]string{"Value": "value"}},
		},
		{
			name:     "nested values are not transfer-converted",
			workflow: func(Context, transferEnvelope, []contextualString, map[string]contextualString) error { return nil },
			args:     []any{transferEnvelope{Value: "value"}, []contextualString{"value"}, map[string]contextualString{"key": "value"}},
			wireArgs: []any{map[string]string{"Value": "value"}, []string{"value"}, map[string]string{"key": "value"}},
		},
		{
			name:     "client context reaches transfer type converter",
			workflow: func(Context, contextualString) error { return nil },
			args:     []any{contextualString("value")},
			wireArgs: []any{"go:client:value"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dc := converter.GetDefaultDataConverter()
			service, client := newTransferTestClient(t, dc)
			want, err := dc.ToPayloads(tt.wireArgs...)
			require.NoError(t, err)
			service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *workflowservice.StartWorkflowExecutionRequest, _ ...grpc.CallOption) (*workflowservice.StartWorkflowExecutionResponse, error) {
					require.True(t, proto.Equal(want, req.Input), "got input %v, want %v", req.Input, want)
					return &workflowservice.StartWorkflowExecutionResponse{RunId: "run-1"}, nil
				})

			ctx := context.WithValue(t.Context(), transferContextKey{}, "client")
			_, err = client.ExecuteWorkflow(ctx, StartWorkflowOptions{
				ID: "workflow-1", TaskQueue: "transfer-test",
			}, tt.workflow, tt.args...)
			require.NoError(t, err)
		})
	}
}

func TestTransferTypesMockClientWorkflowResult(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		wireValue any
		resultPtr any
		want      any
	}{
		{"requested model type", 300.0, new(temperature), &temperature{kelvin: 300}},
		{"plain destination", "value", new(string), new("value")},
		{"any destination", 300.0, new(any), new(any(300.0))},
		{"nested values", map[string]string{"Value": "value"}, new(transferEnvelope), &transferEnvelope{Value: "value"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dc := converter.GetDefaultDataConverter()
			service, client := newTransferTestClient(t, dc)
			payloads, err := dc.ToPayloads(tt.wireValue)
			require.NoError(t, err)
			service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
					History: &historypb.History{Events: []*historypb.HistoryEvent{{
						EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
						Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
							WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: payloads},
						},
					}}},
				}, nil)

			ctx := context.WithValue(t.Context(), transferContextKey{}, "client")
			err = client.GetWorkflow(ctx, "workflow-1", "run-1").Get(ctx, tt.resultPtr)
			require.NoError(t, err)
			require.Equal(t, tt.want, tt.resultPtr)
		})
	}
}

// transferExecution's transfer type includes its unexported fields. When encoded with a data
// converter that isn't transfer-aware, the fields disappear.
type transferExecution struct{ workflowID, runID string }

func (transferExecution) TransferTypeConverter() (converter.TransferTypeConverter, error) {
	return converter.NewTransferTypeConverter(
		func(value *transferExecution) (*commonpb.WorkflowExecution, error) {
			return &commonpb.WorkflowExecution{WorkflowId: value.workflowID, RunId: value.runID}, nil
		},
		func(value *commonpb.WorkflowExecution, result *transferExecution) error {
			*result = transferExecution{workflowID: value.GetWorkflowId(), runID: value.GetRunId()}
			return nil
		},
	)
}

func TestTransferTypesWorkflowTestEnvironmentRoundTrip(t *testing.T) {
	t.Parallel()
	dc := converter.GetDefaultDataConverter()
	model := transferExecution{workflowID: "workflow-1", runID: "run-1"}

	t.Run("round trip through test workflow environment", func(t *testing.T) {
		var suite WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.SetDataConverter(dc)
		env.ExecuteWorkflow(func(_ Context, input transferExecution) (transferExecution, error) {
			input.runID += ":completed"
			return input, nil
		}, model)
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var got transferExecution
		require.NoError(t, env.GetWorkflowResult(&got))
		require.Equal(t, transferExecution{workflowID: model.workflowID, runID: model.runID + ":completed"}, got)
	})
}

func TestTransferTypesWorkflowTestEnvironmentWorkflowRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		workflow  any
		input     any
		resultPtr any
		want      any
	}{
		{
			name: "model input and result",
			workflow: func(_ Context, input temperature) (temperature, error) {
				return temperature{kelvin: input.kelvin + 10}, nil
			},
			input: temperature{kelvin: 300}, resultPtr: new(temperature), want: &temperature{kelvin: 310},
		},
		{
			name: "nested field is not transfer converted",
			workflow: func(_ Context, input transferEnvelope) (transferEnvelope, error) {
				return input, nil
			},
			input: transferEnvelope{Value: "value"}, resultPtr: new(transferEnvelope), want: &transferEnvelope{Value: "value"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.ExecuteWorkflow(tt.workflow, tt.input)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			require.NoError(t, env.GetWorkflowResult(tt.resultPtr))
			require.Equal(t, tt.want, tt.resultPtr)
		})
	}
}

func TestTransferTypesWorkflowTestEnvironmentActivityRoundTrip(t *testing.T) {
	t.Parallel()
	activity := func(_ context.Context, input temperature, increment float64) (temperature, error) {
		return temperature{kelvin: input.kelvin + increment}, nil
	}
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprintf("local=%t", local), func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterActivity(activity)
			env.ExecuteWorkflow(func(ctx Context) (float64, error) {
				ctx = WithActivityOptions(ctx, ActivityOptions{
					StartToCloseTimeout: time.Minute, RetryPolicy: &RetryPolicy{MaximumAttempts: 1},
				})
				ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
					StartToCloseTimeout: time.Minute, RetryPolicy: &RetryPolicy{MaximumAttempts: 1},
				})
				var future Future
				if local {
					future = ExecuteLocalActivity(ctx, activity, temperature{kelvin: 300}, 42.0)
				} else {
					future = ExecuteActivity(ctx, activity, temperature{kelvin: 300}, 42.0)
				}
				var got temperature
				if err := future.Get(ctx, &got); err != nil {
					return 0, err
				}
				return got.kelvin, nil
			})
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			var got float64
			require.NoError(t, env.GetWorkflowResult(&got))
			require.Equal(t, 342.0, got)
		})
	}
}

func TestTransferTypesWorkflowTestEnvironmentExecutionConversionContext(t *testing.T) {
	t.Parallel()
	t.Run("activity callback", func(t *testing.T) {
		var suite WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.SetWorkerOptions(WorkerOptions{
			BackgroundActivityContext: context.WithValue(t.Context(), transferContextKey{}, "activity"),
		})
		activity := func(context.Context) (contextualString, error) {
			return contextualString("value"), nil
		}
		env.RegisterActivity(activity)
		env.ExecuteWorkflow(func(ctx Context) (string, error) {
			ctx = WithActivityOptions(ctx, ActivityOptions{
				StartToCloseTimeout: time.Minute, RetryPolicy: &RetryPolicy{MaximumAttempts: 1},
			})
			var got string
			err := ExecuteActivity(ctx, activity).Get(ctx, &got)
			return got, err
		})
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var got string
		require.NoError(t, env.GetWorkflowResult(&got))
		require.Equal(t, "go:activity:value", got)
	})

	t.Run("workflow decoding callback", func(t *testing.T) {
		var suite WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		activity := func(context.Context) (string, error) {
			return "value", nil
		}
		env.RegisterActivity(activity)
		env.ExecuteWorkflow(func(ctx Context) (string, error) {
			ctx = WithValue(ctx, transferContextKey{}, "workflow")
			ctx = WithActivityOptions(ctx, ActivityOptions{
				StartToCloseTimeout: time.Minute, RetryPolicy: &RetryPolicy{MaximumAttempts: 1},
			})
			var got contextualString
			err := ExecuteActivity(ctx, activity).Get(ctx, &got)
			return string(got), err
		})
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var got string
		require.NoError(t, env.GetWorkflowResult(&got))
		require.Equal(t, "wf:workflow:value", got)
	})
}

func TestTransferTypes_DataConverterWrapping(t *testing.T) {
	t.Parallel()
	value := temperature{kelvin: 300}
	payloads, err := DefaultInternalDataConverter.ToPayloads(value)
	require.NoError(t, err)

	t.Run("workflow replayer", func(t *testing.T) {
		replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{
			DataConverter: converter.GetDefaultDataConverter(),
		})
		require.NoError(t, err)
		replayer.workflowExecutionResults["workflow-1"] = payloads

		var got temperature
		require.NoError(t, replayer.GetWorkflowResult("workflow-1", &got))
		require.Equal(t, value, got)
	})

	t.Run("workflow context", func(t *testing.T) {
		ctx := WithDataConverter(Background(), converter.GetDefaultDataConverter())
		dc := GetDataConverterFromWorkflowContext(ctx)

		var got temperature
		require.NoError(t, dc.FromPayloads(payloads, &got))
		require.Equal(t, value, got)
	})
}

// -- VALIDATION TESTS --

func TestNewContextualTransferTypeConverter_RejectsPointerTypes(t *testing.T) {
	t.Parallel()
	tc, err := converter.NewContextualTransferTypeConverter[*temperature, float64](nil, nil, nil, nil)
	require.Nil(t, tc)
	require.EqualError(t, err, "transfer type converter: model type must not be a pointer, got *internal.temperature")
	tc, err = converter.NewContextualTransferTypeConverter[temperature, *float64](nil, nil, nil, nil)
	require.Nil(t, tc)
	require.EqualError(t, err, "transfer type converter: transfer type must not be a pointer, got *float64")
}

func TestTransferTypeConverter_InvalidTypes(t *testing.T) {
	t.Parallel()
	dc := converter.NewTransferAwareDataConverter(nil)
	payload, err := converter.GetDefaultDataConverter().ToPayload(300.0)
	require.NoError(t, err)
	payloads := &commonpb.Payloads{Payloads: []*commonpb.Payload{payload}}

	for _, tt := range []struct {
		name string
		dc   converter.DataConverter
	}{
		{name: "Go context", dc: WithContext(t.Context(), dc)},
		{name: "workflow context", dc: WithWorkflowContext(Background(), dc)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value := mismatchedTransferModel{}
			_, err := tt.dc.ToPayload(value)
			require.ErrorContains(t, err, "want value of type internal.temperature or *internal.temperature, got internal.mismatchedTransferModel")
			_, err = tt.dc.ToPayloads(value)
			require.ErrorContains(t, err, "want value of type internal.temperature or *internal.temperature, got internal.mismatchedTransferModel")
			require.ErrorContains(t, tt.dc.FromPayload(payload, &value), "want value of type *internal.temperature, got *internal.mismatchedTransferModel")
			require.ErrorContains(t, tt.dc.FromPayloads(payloads, &value), "want value of type *internal.temperature, got *internal.mismatchedTransferModel")
		})
	}
}

func TestTransferAwareDataConverter_DiscoveryError(t *testing.T) {
	t.Parallel()
	dc := makeTransferAware(nil)
	_, err := dc.ToPayload(invalidTransferConverter{})
	require.ErrorIs(t, err, errNoEncoding)
	_, err = dc.ToPayloads(invalidTransferConverter{})
	require.ErrorIs(t, err, errNoEncoding)
	payload, err := converter.GetDefaultDataConverter().ToPayload("value")
	require.NoError(t, err)
	var value invalidTransferConverter
	require.ErrorIs(t, dc.FromPayload(payload, &value), errNoEncoding)
	require.ErrorIs(t, dc.FromPayloads(&commonpb.Payloads{Payloads: []*commonpb.Payload{payload}}, &value), errNoEncoding)
}
