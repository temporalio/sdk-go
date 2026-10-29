package internal

import (
	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
)

// systemNexusDataConverter is a transfer-aware proto binary converter,
// used to encode System Nexus requests and responses to/from the server.
var systemNexusDataConverter = MakeTransferAware(
	converter.NewCompositeDataConverter(converter.NewProtoPayloadConverter()),
)

// MakeTransferAware wraps dc to support transfer type conversion. A nil dc uses
// converter.GetDefaultDataConverter; an already transfer-aware converter is unchanged.
func MakeTransferAware(dc converter.DataConverter) converter.DataConverter {
	if dc == nil {
		return converter.GetDefaultDataConverter()
	}
	return commonconverter.MakeTransferAware(dc)
}
