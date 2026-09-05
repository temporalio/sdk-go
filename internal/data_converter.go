package internal

import (
	"go.temporal.io/sdk/converter"
)

// effectiveDataConverter resolves the data converter that SDK code should use,
// given a caller-supplied converter that may be nil.
//
// Call this where a converter enters the SDK, not where one is read back out.
// Entry points are the public options structs, the workflow and activity
// contexts, and the paths that deliberately fall back to the process-wide
// default. Resolution applies two rules that have to stay together:
//
//   - A nil converter falls back to the process-wide default.
//   - The result is wrapped so that transfer type conversion runs before the
//     configured converter. See transferTypeDataConverter.
//
// Once a resolved converter is stored on a WorkflowClient, worker parameters, a
// WorkflowReplayer, a test environment, or workflow context options, it stays
// resolved. Code that reads one of those fields must use it as is. Resolving
// again is harmless because wrapping is idempotent, but it obscures where the
// converter actually entered.
//
// Applying only the first rule at an entry point is how a code path silently
// loses transfer type conversion, which is why the two rules do not appear
// separately anywhere else.
//
// A small number of call sites intentionally bypass this helper because their
// payloads are not application values, most notably search attributes, which
// the server indexes and must therefore keep their unconverted representation.
// Those sites say so at the point of use.
func effectiveDataConverter(dc converter.DataConverter) converter.DataConverter {
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}
	return wrapTransferTypeDataConverter(dc)
}
