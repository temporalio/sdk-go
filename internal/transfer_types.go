package internal

import (
	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
)

// The default data converter, wrapped so it supports transfer type conversion.
// For values that don't implement [converter.ValueWithTransferTypeConverter], this
// behaves the same as [converter.GetDefaultDataConverter].
var DefaultInternalDataConverter = MakeTransferAware(converter.GetDefaultDataConverter())

// MakeTransferAware wraps dc to support transfer type conversion. A nil dc uses
// converter.GetDefaultDataConverter; an already transfer-aware converter is unchanged.
func MakeTransferAware(dc converter.DataConverter) converter.DataConverter {
	if dc == nil {
		return converter.GetDefaultDataConverter()
	}
	return commonconverter.MakeTransferAware(dc)
}
