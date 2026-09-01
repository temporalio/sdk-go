package converter_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestNewTypedTransferTypeConverterValueTransferType(t *testing.T) {
	transferConverter := converter.NewTypedTransferTypeConverter[customerReference, string](
		func(value customerReference) (string, error) {
			return value.ID, nil
		},
		func(value string) (customerReference, error) {
			return customerReference{ID: value}, nil
		},
	)

	transferValue, err := transferConverter.ToTransferType(customerReference{ID: "value"})
	require.NoError(t, err)
	require.Equal(t, "value", transferValue)

	transferValue, err = transferConverter.ToTransferType(&customerReference{ID: "pointer"})
	require.NoError(t, err)
	require.Equal(t, "pointer", transferValue)

	firstTarget := transferConverter.NewTransferType()
	secondTarget := transferConverter.NewTransferType()
	require.IsType(t, new(string), firstTarget)
	require.NotSame(t, firstTarget, secondTarget)
	*firstTarget.(*string) = "decoded"

	applicationValue, err := transferConverter.FromTransferType(firstTarget)
	require.NoError(t, err)
	require.Equal(t, customerReference{ID: "decoded"}, applicationValue)

	_, err = transferConverter.ToTransferType(struct{}{})
	require.ErrorContains(t, err, "expected application value")
	_, err = transferConverter.FromTransferType(new(int))
	require.ErrorContains(t, err, "expected transfer value")
}

func TestNewTypedTransferTypeConverterPointerTransferType(t *testing.T) {
	type applicationValue struct {
		Value string
	}

	transferConverter := converter.NewTypedTransferTypeConverter[applicationValue, *wrapperspb.StringValue](
		func(value applicationValue) (*wrapperspb.StringValue, error) {
			return wrapperspb.String(value.Value), nil
		},
		func(value *wrapperspb.StringValue) (applicationValue, error) {
			return applicationValue{Value: value.Value}, nil
		},
	)

	transferValue, err := transferConverter.ToTransferType(applicationValue{Value: "encoded"})
	require.NoError(t, err)
	require.Equal(t, wrapperspb.String("encoded"), transferValue)

	transferTarget := transferConverter.NewTransferType()
	require.IsType(t, new(wrapperspb.StringValue), transferTarget)
	transferTarget.(*wrapperspb.StringValue).Value = "decoded"

	applicationResult, err := transferConverter.FromTransferType(transferTarget)
	require.NoError(t, err)
	require.Equal(t, applicationValue{Value: "decoded"}, applicationResult)
}

func TestNewTypedTransferTypeConverterErrors(t *testing.T) {
	toErr := errors.New("to transfer failed")
	fromErr := errors.New("from transfer failed")
	transferConverter := converter.NewTypedTransferTypeConverter[customerReference, string](
		func(customerReference) (string, error) {
			return "", toErr
		},
		func(string) (customerReference, error) {
			return customerReference{}, fromErr
		},
	)

	_, err := transferConverter.ToTransferType(customerReference{})
	require.ErrorIs(t, err, toErr)
	_, err = transferConverter.FromTransferType(new(string))
	require.ErrorIs(t, err, fromErr)

	var nilTo func(customerReference) (string, error)
	var nilFrom func(string) (customerReference, error)
	transferConverter = converter.NewTypedTransferTypeConverter(nilTo, nilFrom)
	_, err = transferConverter.ToTransferType(customerReference{})
	require.ErrorContains(t, err, "nil to-transfer function")
	_, err = transferConverter.FromTransferType(new(string))
	require.ErrorContains(t, err, "nil from-transfer function")
}
