package internal

import (
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/sdk/converter"
)

func FailureToProto(dc converter.DataConverter, value error) (*failurepb.Failure, error) {
	fc := NewDefaultFailureConverter(DefaultFailureConverterOptions{DataConverter: dc})
	return fc.ErrorToFailure(value), nil
}

func FailureFromProto(dc converter.DataConverter, failure *failurepb.Failure) (error, error) {
	fc := NewDefaultFailureConverter(DefaultFailureConverterOptions{DataConverter: dc})
	return fc.FailureToError(failure), nil
}
