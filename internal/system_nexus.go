package internal

import "go.temporal.io/sdk/converter"

func getSystemNexusPayloadConverter() converter.DataConverter {
	return converter.GetDefaultDataConverter()
}
