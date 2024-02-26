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
	// SearchAttributes represents a collection of typed search attributes. Create with [NewSearchAttributes].
	SearchAttributes = internal.SearchAttributes

	// SearchAttributesUpdate represents a change to SearchAttributes.
	SearchAttributeUpdate = internal.SearchAttributeUpdate

	// SearchAttributeKey represents a typed search attribute key.
	SearchAttributeKey = internal.SearchAttributeKey

	// SearchAttributeKeyString represents a search attribute key for a text attribute type. Create with
	// [NewSearchAttributeKeyString].
	SearchAttributeKeyString = internal.SearchAttributeKeyString

	// SearchAttributeKeyKeyword represents a search attribute key for a keyword attribute type. Create with
	// [NewSearchAttributeKeyKeyword].
	SearchAttributeKeyKeyword = internal.SearchAttributeKeyKeyword

	// SearchAttributeKeyBool represents a search attribute key for a boolean attribute type. Create with
	// [NewSearchAttributeKeyBool].
	SearchAttributeKeyBool = internal.SearchAttributeKeyBool

	// SearchAttributeKeyInt64 represents a search attribute key for a integer attribute type. Create with
	// [NewSearchAttributeKeyInt64].
	SearchAttributeKeyInt64 = internal.SearchAttributeKeyInt64

	// SearchAttributeKeyFloat64 represents a search attribute key for a double attribute type. Create with
	// [NewSearchAttributeKeyFloat64].
	SearchAttributeKeyFloat64 = internal.SearchAttributeKeyFloat64

	// SearchAttributeKeyTime represents a search attribute key for a time attribute type. Create with
	// [NewSearchAttributeKeyTime].
	SearchAttributeKeyTime = internal.SearchAttributeKeyTime

	// SearchAttributeKeyKeywordList represents a search attribute key for a keyword list attribute type. Create with
	// [NewSearchAttributeKeyKeywordList].
	SearchAttributeKeyKeywordList = internal.SearchAttributeKeyKeywordList
)

// NewSearchAttributeKeyString creates a new string-based key.
func NewSearchAttributeKeyString(name string) SearchAttributeKeyString {
	return internal.NewSearchAttributeKeyString(name)
}

// NewSearchAttributeKeyKeyword creates a new keyword-based key.
func NewSearchAttributeKeyKeyword(name string) SearchAttributeKeyKeyword {
	return internal.NewSearchAttributeKeyKeyword(name)
}

// NewSearchAttributeKeyBool creates a new bool-based key.
func NewSearchAttributeKeyBool(name string) SearchAttributeKeyBool {
	return internal.NewSearchAttributeKeyBool(name)
}

// NewSearchAttributeKeyInt64 creates a new int64-based key.
func NewSearchAttributeKeyInt64(name string) SearchAttributeKeyInt64 {
	return internal.NewSearchAttributeKeyInt64(name)
}

// NewSearchAttributeKeyFloat64 creates a new float64-based key.
func NewSearchAttributeKeyFloat64(name string) SearchAttributeKeyFloat64 {
	return internal.NewSearchAttributeKeyFloat64(name)
}

// NewSearchAttributeKeyTime creates a new time-based key.
func NewSearchAttributeKeyTime(name string) SearchAttributeKeyTime {
	return internal.NewSearchAttributeKeyTime(name)
}

// NewSearchAttributeKeyKeywordList creates a new keyword-list-based key.
func NewSearchAttributeKeyKeywordList(name string) SearchAttributeKeyKeywordList {
	return internal.NewSearchAttributeKeyKeywordList(name)
}

// NewSearchAttributes creates a new search attribute collection for the given updates.
func NewSearchAttributes(attributes ...SearchAttributeUpdate) SearchAttributes {
	return internal.NewSearchAttributes(attributes...)
}
