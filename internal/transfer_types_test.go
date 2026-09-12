package internal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// -- DATA -----------------------------------------------------------

type temperature struct{ kelvin float64 }

var _ ValueWithTransferConverter = temperature{}

var temperatureConverter = NewTransferConverter(
	func(t temperature) (float64, error) { return t.kelvin, nil },
	func(kelvin float64, t *temperature) error {
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
	func(u userRef) (userRefTransfer, error) { return userRefTransfer{ID: u.id}, nil },
	func(t userRefTransfer, u *userRef) error {
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
		func(unencodable) (string, error) { return "", errNoEncoding },
		func(string, *unencodable) error { return nil },
	)
}

var errNoDecoding = errors.New("cannot decode")

// undecodable always returns an error when decoding the transfer type
type undecodable struct{}

var _ ValueWithTransferConverter = undecodable{}

func (undecodable) TransferConverter() TransferConverter {
	return NewTransferConverter(
		func(undecodable) (string, error) { return "encoded", nil },
		func(string, *undecodable) error { return errNoDecoding },
	)
}

// countingDataConverter records which decode methods its wrapper calls.
type countingDataConverter struct {
	converter.DataConverter
	fromPayloadCalls  int
	fromPayloadsCalls int
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

	t.Run("scalar transfer value", func(t *testing.T) {
		payload, err := dc.ToPayload(temperature{kelvin: 300})
		require.NoError(t, err)
		require.Equal(t, "300", string(payload.GetData()))

		var got temperature
		require.NoError(t, dc.FromPayload(payload, &got))
		require.Equal(t, temperature{kelvin: 300}, got)
	})

	t.Run("struct transfer value", func(t *testing.T) {
		payload, err := dc.ToPayload(userRef{id: "u-1", cache: "Ada"})
		require.NoError(t, err)
		require.Equal(t, `{"ID":"u-1"}`, string(payload.GetData()))

		var got userRef
		require.NoError(t, dc.FromPayload(payload, &got))
		require.Equal(t, userRef{id: "u-1"}, got)
	})

	t.Run("value without a transfer converter", func(t *testing.T) {
		payload, err := dc.ToPayload("plain")
		require.NoError(t, err)

		want, err := converter.GetDefaultDataConverter().ToPayload("plain")
		require.NoError(t, err)
		require.Equal(t, want.GetData(), payload.GetData())

		var got string
		require.NoError(t, dc.FromPayload(payload, &got))
		require.Equal(t, "plain", got)
	})
}

func TestTransferAwareDataConverter_PayloadsRoundTrip(t *testing.T) {
	t.Parallel()
	dc := defaultTransferAwareDataConverter()

	payloads, err := dc.ToPayloads(temperature{kelvin: 300}, "plain", userRef{id: "u-1"})
	require.NoError(t, err)

	var (
		gotTemperature temperature
		gotString      string
		gotUser        userRef
	)
	require.NoError(t, dc.FromPayloads(payloads, &gotTemperature, &gotString, &gotUser))
	require.Equal(t, temperature{kelvin: 300}, gotTemperature)
	require.Equal(t, "plain", gotString)
	require.Equal(t, userRef{id: "u-1"}, gotUser)
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

// A missing payload leaves the value alone, the same way the underlying data
// converters do.
func TestTransferAwareDataConverter_MissingPayloads(t *testing.T) {
	t.Parallel()
	dc := defaultTransferAwareDataConverter()

	t.Run("nil payload", func(t *testing.T) {
		got := temperature{kelvin: 42}
		require.NoError(t, dc.FromPayload(nil, &got))
		require.Equal(t, temperature{kelvin: 42}, got)
	})

	t.Run("nil payloads", func(t *testing.T) {
		got := temperature{kelvin: 42}
		require.NoError(t, dc.FromPayloads(nil, &got))
		require.Equal(t, temperature{kelvin: 42}, got)
	})

	t.Run("fewer payloads than values", func(t *testing.T) {
		payloads, err := dc.ToPayloads(temperature{kelvin: 300})
		require.NoError(t, err)

		gotFirst := temperature{kelvin: 42}
		gotSecond := temperature{kelvin: 42}
		require.NoError(t, dc.FromPayloads(payloads, &gotFirst, &gotSecond))
		require.Equal(t, temperature{kelvin: 300}, gotFirst)
		require.Equal(t, temperature{kelvin: 42}, gotSecond)
	})

	t.Run("more payloads than values", func(t *testing.T) {
		payloads, err := dc.ToPayloads(temperature{kelvin: 300}, temperature{kelvin: 400})
		require.NoError(t, err)

		var got temperature
		require.NoError(t, dc.FromPayloads(payloads, &got))
		require.Equal(t, temperature{kelvin: 300}, got)
	})
}

func TestTransferAwareDataConverter_ContextDelegation(t *testing.T) {
	t.Parallel()

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
		require.Same(t, dc, WithContext(context.Background(), dc))
	})
}
