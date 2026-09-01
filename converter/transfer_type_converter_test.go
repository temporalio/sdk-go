package converter_test

import (
	"fmt"

	"go.temporal.io/sdk/converter"
)

type customerReference struct {
	ID string
}

func (customerReference) TransferTypeConverter() converter.TransferTypeConverter {
	return customerReferenceTransferTypeConverter{}
}

type customerReferenceTransferTypeConverter struct{}

func (customerReferenceTransferTypeConverter) NewTransferType() any {
	return new(string)
}

func (customerReferenceTransferTypeConverter) ToTransferType(value any) (any, error) {
	switch value := value.(type) {
	case customerReference:
		return value.ID, nil
	case *customerReference:
		return value.ID, nil
	default:
		return nil, fmt.Errorf("expected customerReference or *customerReference, got %T", value)
	}
}

func (customerReferenceTransferTypeConverter) FromTransferType(value any) (any, error) {
	id, ok := value.(*string)
	if !ok {
		return nil, fmt.Errorf("expected *string, got %T", value)
	}
	return customerReference{ID: *id}, nil
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
