package converter_test

import (
	"fmt"

	"go.temporal.io/sdk/converter"
)

type customerReference struct {
	ID string
}

var customerReferenceConverterInstance = converter.NewTypedTransferTypeConverter[customerReference, string](
	func(value customerReference) (string, error) {
		return value.ID, nil
	},
	func(value string) (customerReference, error) {
		return customerReference{ID: value}, nil
	},
)

func (customerReference) TransferTypeConverter() converter.TransferTypeConverter {
	return customerReferenceConverterInstance
}

func ExampleTransferTypeConvertible() {
	var source converter.TransferTypeConvertible = &customerReference{ID: "customer-123"}
	transferConverter := source.TransferTypeConverter()

	transferValue, err := transferConverter.ToTransferType(source)
	if err != nil {
		panic(err)
	}
	fmt.Println(transferValue)

	transferTarget := transferConverter.NewTransferType()
	*transferTarget.(*string) = "customer-456"
	applicationValue, err := transferConverter.FromTransferType(transferTarget)
	if err != nil {
		panic(err)
	}
	fmt.Println(applicationValue.(customerReference).ID)

	// Output:
	// customer-123
	// customer-456
}
