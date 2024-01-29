// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

import "go.temporal.io/sdk/internal"

type (
	// SearchAttributes represents a collection of typed search attributes
	SearchAttributes = internal.SearchAttributes

	// SearchAttributesUpdate represents a change to SearchAttributes
	SearchAttributeUpdate = internal.SearchAttributeUpdate

	// SearchAttributeKey represents a typed search attribute key.
	SearchAttributeKey = internal.SearchAttributeKey

	// SearchAttributeKeyString represents a search attribute key for a text attribute type
	SearchAttributeKeyString = internal.SearchAttributeKeyString

	// SearchAttributeKeyString represents a search attribute key for a keyword attribute type
	SearchAttributeKeyword = internal.SearchAttributeKeyword

	// SearchAttributeKeyBool represents a search attribute key for a boolean attribute type
	SearchAttributeKeyBool = internal.SearchAttributeKeyBool

	// SearchAttributeKeyInt64 represents a search attribute key for a integer attribute type
	SearchAttributeKeyInt64 = internal.SearchAttributeKeyInt64

	// SearchAttributeKeyFloat64 represents a search attribute key for a double attribute type
	SearchAttributeKeyFloat64 = internal.SearchAttributeKeyFloat64

	// SearchAttributeKeyTime represents a search attribute key for a time attribute type
	SearchAttributeKeyTime = internal.SearchAttributeKeyTime

	// SearchAttributeKeywordList represents a search attribute key for a keyword list attribute type
	SearchAttributeKeywordList = internal.SearchAttributeKeywordList
)

func NewSearchAttributeKeyString(name string) SearchAttributeKeyString {
	return internal.NewSearchAttributeKeyString(name)
}

func NewSearchAttributeKeyword(name string) SearchAttributeKeyword {
	return internal.NewSearchAttributeKeyword(name)
}

func NewSearchAttributeKeyBool(name string) SearchAttributeKeyBool {
	return internal.NewSearchAttributeKeyBool(name)
}

func NewSearchAttributeKeyInt64(name string) SearchAttributeKeyInt64 {
	return internal.NewSearchAttributeKeyInt64(name)
}

func NewSearchAttributeKeyFloat64(name string) SearchAttributeKeyFloat64 {
	return internal.NewSearchAttributeKeyFloat64(name)
}

func NewSearchAttributeKeyTime(name string) SearchAttributeKeyTime {
	return internal.NewSearchAttributeKeyTime(name)
}

func NewSearchAttributeKeywordList(name string) SearchAttributeKeywordList {
	return internal.NewSearchAttributeKeywordList(name)
}

func NewSearchAttributes(attributes ...SearchAttributeUpdate) SearchAttributes {
	return internal.NewSearchAttributes(attributes...)
}
