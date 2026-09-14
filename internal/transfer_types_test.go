package internal

import (
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// -- DATA -----------------------------------------------------------

type temperature struct{ kelvin float64 }

var _ ValueWithTransferConverter = temperature{}

var temperatureConverter = NewTransferConverter(
	func(_ context.Context, t temperature) (float64, error) { return t.kelvin, nil },
	func(_ context.Context, kelvin float64, t *temperature) error {
		t.kelvin = kelvin
		return nil
	},
)

func (temperature) TransferConverter() TransferConverter {
	return temperatureConverter
}

type userRef struct {
	id    string
	cache string
}

var _ ValueWithTransferConverter = userRef{}

type userRefTransfer struct{ ID string }

var userRefConverter = NewTransferConverter(
	func(_ context.Context, u userRef) (userRefTransfer, error) { return userRefTransfer{ID: u.id}, nil },
	func(_ context.Context, t userRefTransfer, u *userRef) error {
		u.id = t.ID
		return nil
	},
)

func (userRef) TransferConverter() TransferConverter { return userRefConverter }

// unencodable always returns an error when encoding the value into a transfer value
type unencodable struct{}

var _ ValueWithTransferConverter = unencodable{}

var errNoEncoding = errors.New("cannot encode")

func (unencodable) TransferConverter() TransferConverter {
	return NewTransferConverter(
		func(context.Context, unencodable) (string, error) { return "", errNoEncoding },
		func(context.Context, string, *unencodable) error { return nil },
	)
}

var errNoDecoding = errors.New("cannot decode")

// undecodable always returns an error when decoding the transfer type
type undecodable struct{}

var _ ValueWithTransferConverter = undecodable{}

func (undecodable) TransferConverter() TransferConverter {
	return NewTransferConverter(
		func(context.Context, undecodable) (string, error) { return "encoded", nil },
		func(context.Context, string, *undecodable) error { return errNoDecoding },
	)
}

type transferContextKey struct{}

type contextualString string

var contextualStringConverter = NewTransferConverter(
	func(ctx context.Context, value contextualString) (string, error) {
		return ctx.Value(transferContextKey{}).(string) + string(value), nil
	},
	func(ctx context.Context, transferValue string, value *contextualString) error {
		*value = contextualString(transferValue[len(ctx.Value(transferContextKey{}).(string)):])
		return nil
	},
)

func (contextualString) TransferConverter() TransferConverter {
	return contextualStringConverter
}

// countingDataConverter records which decode methods its wrapper calls.
type countingDataConverter struct {
	converter.DataConverter
	fromPayloadCalls  int
	fromPayloadsCalls int
}

type nilReturningSerializationContextDataConverter struct {
	converter.DataConverter
}

func (*nilReturningSerializationContextDataConverter) WithSerializationContext(
	converter.SerializationContext,
) converter.DataConverter {
	return nil
}

func (dc *countingDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	dc.fromPayloadCalls++
	return dc.DataConverter.FromPayload(payload, valuePtr)
}

func (dc *countingDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	dc.fromPayloadsCalls++
	return dc.DataConverter.FromPayloads(payloads, valuePtrs...)
}

func defaultTransferAwareDataConverter() *transferAwareDataConverter {
	return makeTransferAware(converter.GetDefaultDataConverter())
}

// -- TESTS -----------------------------------------------------------

func TestTransferAwareDataConverter_PayloadRoundTrip(t *testing.T) {
	t.Parallel()
	dc := defaultTransferAwareDataConverter()

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

	t.Run("struct transfer values", func(t *testing.T) {
		values := make([]userRef, 10)
		for i := range values {
			values[i] = userRef{
				id:    "u-" + strconv.FormatUint(rand.Uint64(), 10),
				cache: "cache-" + strconv.FormatUint(rand.Uint64(), 10),
			}
		}

		for _, value := range values {
			payload, err := dc.ToPayload(value)
			require.NoError(t, err)

			var got userRef
			require.NoError(t, dc.FromPayload(payload, &got))
			require.Equal(t, userRef{id: value.id}, got)
		}
	})

	t.Run("values without a transfer converter", func(t *testing.T) {
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

func TestTransferAwareDataConverter_PointerValuePanics(t *testing.T) {
	t.Parallel()
	dc := defaultTransferAwareDataConverter()
	value := &temperature{kelvin: 300}

	require.Implements(t, (*ValueWithTransferConverter)(nil), value)
	require.Panics(t, func() {
		_, _ = dc.ToPayload(value)
	})
}

func TestTransferAwareDataConverter_PayloadsRoundTrip(t *testing.T) {
	t.Parallel()
	dc := defaultTransferAwareDataConverter()

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

	t.Run("struct transfer values", func(t *testing.T) {
		values := make([]userRef, 10)
		want := make([]userRef, len(values))
		valuePtrs := make([]any, len(values))
		got := make([]userRef, len(values))
		for i := range values {
			values[i] = userRef{
				id:    "u-" + strconv.FormatUint(rand.Uint64(), 10),
				cache: "cache-" + strconv.FormatUint(rand.Uint64(), 10),
			}
			want[i] = userRef{id: values[i].id}
			valuePtrs[i] = &got[i]
		}

		payloads, err := dc.ToPayloads(sliceToAny(values)...)
		require.NoError(t, err)
		require.NoError(t, dc.FromPayloads(payloads, valuePtrs...))
		require.Equal(t, want, got)
	})

	t.Run("values without a transfer converter", func(t *testing.T) {
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
		got, err := dc.ToPayloads("plain", temperature{kelvin: 300}, 42, userRef{id: "u-1"}, nil)
		require.NoError(t, err)
		want, err := parent.ToPayloads("plain", 300.0, 42, userRefTransfer{ID: "u-1"}, nil)
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
	dc := defaultTransferAwareDataConverter()

	t.Run("encoding one value", func(t *testing.T) {
		_, err := dc.ToPayload(unencodable{})
		require.ErrorIs(t, err, errNoEncoding)
	})

	t.Run("encoding a list", func(t *testing.T) {
		_, err := dc.ToPayloads("plain", unencodable{})
		require.ErrorIs(t, err, errNoEncoding)
		require.Contains(t, err.Error(), "values[1]")
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
		require.Contains(t, err.Error(), "payload item 1")
	})
}

func TestTransferAwareDataConverter_ContextDelegation(t *testing.T) {
	t.Parallel()

	t.Run("transfer converter", func(t *testing.T) {
		dc := makeTransferAware(converter.GetDefaultDataConverter())
		ctx := context.WithValue(context.Background(), transferContextKey{}, "context:")
		contextualDC := dc.WithContext(ctx)

		payload, err := contextualDC.ToPayload(contextualString("value"))
		require.NoError(t, err)
		var got contextualString
		require.NoError(t, contextualDC.FromPayload(payload, &got))
		require.Equal(t, contextualString("value"), got)

		payloads, err := contextualDC.ToPayloads(contextualString("one"), contextualString("two"))
		require.NoError(t, err)
		var gotOne, gotTwo contextualString
		require.NoError(t, contextualDC.FromPayloads(payloads, &gotOne, &gotTwo))
		require.Equal(t, contextualString("one"), gotOne)
		require.Equal(t, contextualString("two"), gotTwo)
	})

	t.Run("context-aware parent", func(t *testing.T) {
		dc := makeTransferAware(NewContextAwareDataConverter(converter.GetDefaultDataConverter()))

		ctx := context.WithValue(context.Background(), ContextAwareDataConverterContextKey, "300")
		masked := WithContext(ctx, dc)
		require.NotSame(t, dc, masked)

		payload, err := masked.ToPayload(temperature{kelvin: 300})
		require.NoError(t, err)
		require.Equal(t, "?", string(payload.GetData()))
	})

	t.Run("parent that is not context aware", func(t *testing.T) {
		dc := defaultTransferAwareDataConverter()
		require.NotSame(t, dc, WithContext(context.Background(), dc))
	})

	t.Run("serialization context parent returning nil", func(t *testing.T) {
		dc := makeTransferAware(&nilReturningSerializationContextDataConverter{
			DataConverter: converter.GetDefaultDataConverter(),
		})
		require.PanicsWithValue(
			t,
			"DataConverterWithSerializationContext.WithSerializationContext must not return nil",
			func() {
				dc.WithSerializationContext(converter.WorkflowSerializationContext{})
			},
		)
	})
}
