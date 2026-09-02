package internal

import (
	"go.temporal.io/sdk/converter"
)

// effectiveDataConverter resolves the data converter that SDK code should use,
// given a caller-supplied converter that may be nil.
//
// Every code path that needs a data converter must obtain it here instead of
// reading a caller-supplied field directly or falling back to
// converter.GetDefaultDataConverter on its own. Resolution applies two rules
// that have to stay together:
//
//   - A nil converter falls back to the process-wide default.
//   - The result is wrapped so that transfer type conversion runs before the
//     configured converter. See transferTypeDataConverter.
//
// Wrapping is idempotent, so calling this on an already-resolved converter is
// safe and cheap. Applying only the first rule at a call site is how a code
// path silently loses transfer type conversion, which is why the two rules do
// not appear separately anywhere else.
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
