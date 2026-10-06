package internal

import "go.temporal.io/sdk/converter"

// The default data converter, wrapped so it supports transfer type conversion.
// For values that don't implement [converter.ValueWithTransferTypeConverter], this
// behaves the same as [converter.GetDefaultDataConverter].
var DefaultInternalDataConverter = makeTransferAware(converter.GetDefaultDataConverter())

func makeTransferAware(dc converter.DataConverter) converter.DataConverter {
	return converter.NewTransferAwareDataConverter(dc)
}
