// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package temporal

import (
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
)

type (
	// DefaultFailureConverterOptions are optional parameters for DefaultFailureConverter creation.
	DefaultFailureConverterOptions = internal.DefaultFailureConverterOptions

	// DefaultFailureConverter seralizes errors with the option to encode common parameters under Failure.EncodedAttributes.
	DefaultFailureConverter        = internal.DefaultFailureConverter
)

// NewDefaultFailureConverter creates new instance of DefaultFailureConverter.
func NewDefaultFailureConverter(opt DefaultFailureConverterOptions) *DefaultFailureConverter {
	return internal.NewDefaultFailureConverter(opt)
}

// GetDefaultDataConverter returns the default failure converter used by Temporal.
func GetDefaultFailureConverter() converter.FailureConverter {
	return internal.GetDefaultFailureConverter()
}
