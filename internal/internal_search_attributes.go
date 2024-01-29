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

package internal

import (
	"reflect"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
)

type (
	// SearchAttributes represents a collection of typed search attributes
	SearchAttributes struct {
		untypedValue map[SearchAttributeKey]interface{}
	}

	// SearchAttributeUpdate represents a change to SearchAttributes
	SearchAttributeUpdate func(*SearchAttributes)

	// SearchAttributeKey represents a typed search attribute key.
	SearchAttributeKey interface {
		// GetName of the search attribute.
		GetName() string
		// GetValueType of the search attribute.
		GetValueType() enumspb.IndexedValueType
		// GetReflectType of the search attribute.
		GetReflectType() reflect.Type
	}

	baseSearchAttributeKey struct {
		name        string
		valueType   enumspb.IndexedValueType
		reflectType reflect.Type
	}

	// SearchAttributeKeyString represents a search attribute key for a text attribute type
	SearchAttributeKeyString struct {
		baseSearchAttributeKey
	}

	// SearchAttributeKeyString represents a search attribute key for a keyword attribute type
	SearchAttributeKeyword struct {
		baseSearchAttributeKey
	}

	// SearchAttributeKeyBool represents a search attribute key for a boolean attribute type
	SearchAttributeKeyBool struct {
		baseSearchAttributeKey
	}

	// SearchAttributeKeyInt64 represents a search attribute key for a integer attribute type
	SearchAttributeKeyInt64 struct {
		baseSearchAttributeKey
	}

	// SearchAttributeKeyFloat64 represents a search attribute key for a float attribute type
	SearchAttributeKeyFloat64 struct {
		baseSearchAttributeKey
	}

	// SearchAttributeKeyTime represents a search attribute key for a date time attribute type
	SearchAttributeKeyTime struct {
		baseSearchAttributeKey
	}

	// SearchAttributeKeywordList represents a search attribute key for a list of keyword attribute type
	SearchAttributeKeywordList struct {
		baseSearchAttributeKey
	}
)

func (bk baseSearchAttributeKey) GetName() string {
	return bk.name
}

func (bk baseSearchAttributeKey) GetValueType() enumspb.IndexedValueType {
	return bk.valueType
}

func (bk baseSearchAttributeKey) GetReflectType() reflect.Type {
	return bk.reflectType
}

func NewSearchAttributeKeyString(name string) SearchAttributeKeyString {
	return SearchAttributeKeyString{
		baseSearchAttributeKey: baseSearchAttributeKey{
			name:        name,
			valueType:   enumspb.INDEXED_VALUE_TYPE_TEXT,
			reflectType: reflect.TypeOf(""),
		},
	}
}

func (k SearchAttributeKeyString) ValueSet(value string) SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = value
	}
}

func (k SearchAttributeKeyString) ValueUnset() SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = nil
	}
}

func NewSearchAttributeKeyword(name string) SearchAttributeKeyword {
	return SearchAttributeKeyword{
		baseSearchAttributeKey: baseSearchAttributeKey{
			name:        name,
			valueType:   enumspb.INDEXED_VALUE_TYPE_KEYWORD,
			reflectType: reflect.TypeOf(""),
		},
	}
}

func (k SearchAttributeKeyword) ValueSet(value string) SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = value
	}
}

func (k SearchAttributeKeyword) ValueUnset() SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = nil
	}
}

func NewSearchAttributeKeyBool(name string) SearchAttributeKeyBool {
	return SearchAttributeKeyBool{
		baseSearchAttributeKey: baseSearchAttributeKey{
			name:        name,
			valueType:   enumspb.INDEXED_VALUE_TYPE_BOOL,
			reflectType: reflect.TypeOf(false),
		},
	}
}

func (k SearchAttributeKeyBool) ValueSet(value bool) SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = value
	}
}

func (k SearchAttributeKeyBool) ValueUnset() SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = nil
	}
}

func NewSearchAttributeKeyInt64(name string) SearchAttributeKeyInt64 {
	return SearchAttributeKeyInt64{
		baseSearchAttributeKey: baseSearchAttributeKey{
			name:        name,
			valueType:   enumspb.INDEXED_VALUE_TYPE_INT,
			reflectType: reflect.TypeOf(int64(0)),
		},
	}
}

func (k SearchAttributeKeyInt64) ValueSet(value int64) SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = value
	}
}

func (k SearchAttributeKeyInt64) ValueUnset() SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = nil
	}
}

func NewSearchAttributeKeyFloat64(name string) SearchAttributeKeyFloat64 {
	return SearchAttributeKeyFloat64{
		baseSearchAttributeKey: baseSearchAttributeKey{
			name:        name,
			valueType:   enumspb.INDEXED_VALUE_TYPE_DOUBLE,
			reflectType: reflect.TypeOf(float64(0)),
		},
	}
}

func (k SearchAttributeKeyFloat64) ValueSet(value float64) SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = value
	}
}

func (k SearchAttributeKeyFloat64) ValueUnset() SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = nil
	}
}

func NewSearchAttributeKeyTime(name string) SearchAttributeKeyTime {
	return SearchAttributeKeyTime{
		baseSearchAttributeKey: baseSearchAttributeKey{
			name:        name,
			valueType:   enumspb.INDEXED_VALUE_TYPE_DATETIME,
			reflectType: reflect.TypeOf(time.Time{}),
		},
	}
}

func (k SearchAttributeKeyTime) ValueSet(value time.Time) SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = value
	}
}

func (k SearchAttributeKeyTime) ValueUnset() SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = nil
	}
}

func NewSearchAttributeKeywordList(name string) SearchAttributeKeywordList {
	return SearchAttributeKeywordList{
		baseSearchAttributeKey: baseSearchAttributeKey{
			name:        name,
			valueType:   enumspb.INDEXED_VALUE_TYPE_KEYWORD_LIST,
			reflectType: reflect.TypeOf([]string{}),
		},
	}
}

func (k SearchAttributeKeywordList) ValueSet(values []string) SearchAttributeUpdate {
	listCopy := append([]string(nil), values...)
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = listCopy
	}
}

func (k SearchAttributeKeywordList) ValueUnset() SearchAttributeUpdate {
	return func(sa *SearchAttributes) {
		sa.untypedValue[k] = nil
	}
}

func NewSearchAttributes(attributes ...SearchAttributeUpdate) SearchAttributes {
	sa := SearchAttributes{
		untypedValue: make(map[SearchAttributeKey]interface{}),
	}
	for _, attr := range attributes {
		attr(&sa)
	}
	// remove nil values
	for key, value := range sa.untypedValue {
		if value == nil {
			delete(sa.untypedValue, key)
		}
	}
	return sa
}

func (sa *SearchAttributes) GetString(key SearchAttributeKeyString) (string, bool) {
	value, ok := sa.untypedValue[key]
	if !ok {
		return "", false
	}
	return value.(string), true
}

func (sa *SearchAttributes) GetKeyword(key SearchAttributeKeyword) (string, bool) {
	value, ok := sa.untypedValue[key]
	if !ok {
		return "", false
	}
	return value.(string), true
}

func (sa *SearchAttributes) GetBool(key SearchAttributeKeyBool) (bool, bool) {
	value, ok := sa.untypedValue[key]
	if !ok {
		return false, false
	}
	return value.(bool), true
}

func (sa *SearchAttributes) GetInt(key SearchAttributeKeyInt64) (int64, bool) {
	value, ok := sa.untypedValue[key]
	if !ok {
		return 0, false
	}
	return value.(int64), true
}

func (sa *SearchAttributes) GetFloat(key SearchAttributeKeyFloat64) (float64, bool) {
	value, ok := sa.untypedValue[key]
	if !ok {
		return 0.0, false
	}
	return value.(float64), true
}

func (sa *SearchAttributes) GetTime(key SearchAttributeKeyTime) (time.Time, bool) {
	value, ok := sa.untypedValue[key]
	if !ok {
		return time.Time{}, false
	}
	return value.(time.Time), true
}

func (sa *SearchAttributes) GetKeywordList(key SearchAttributeKeywordList) ([]string, bool) {
	value, ok := sa.untypedValue[key]
	if !ok {
		return nil, false
	}
	result := value.([]string)
	// Return a copy to prevent caller from mutating the underlying value
	return append([]string(nil), result...), true
}

func (sa *SearchAttributes) ContainsKey(key SearchAttributeKey) bool {
	_, ok := sa.untypedValue[key]
	return ok
}

func (sa *SearchAttributes) Size() int {
	return len(sa.untypedValue)
}

func (sa *SearchAttributes) GetUntypedValues() map[SearchAttributeKey]interface{} {
	untypedValueCopy := make(map[SearchAttributeKey]interface{}, len(sa.untypedValue))
	for key, value := range sa.untypedValue {
		switch v := value.(type) {
		case []string:
			untypedValueCopy[key] = append([]string(nil), v...)
		default:
			untypedValueCopy[key] = v
		}
	}
	return untypedValueCopy
}

func (sa *SearchAttributes) Copy() SearchAttributeUpdate {
	return func(s *SearchAttributes) {
		untypedValues := sa.GetUntypedValues()
		for key, value := range untypedValues {
			s.untypedValue[key] = value
		}
	}
}
