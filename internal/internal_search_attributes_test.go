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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSearchAttributes(t *testing.T) {
	t.Parallel()
	sa := NewSearchAttributes()
	boolKey := NewSearchAttributeKeyBool("boolKey")

	require.False(t, sa.ContainsKey(boolKey))
	boolValue, ok := sa.GetBool(boolKey)
	require.False(t, boolValue)
	require.False(t, ok)

	require.NotNil(t, sa.GetUntypedValues())
	require.Equal(t, 0, len(sa.GetUntypedValues()))
	require.Equal(t, 0, sa.Size())

	stringKey := NewSearchAttributeKeyString("stringKey")
	keywordKey := NewSearchAttributeKeyword("keywordKey")
	intKey := NewSearchAttributeKeyInt64("intKey")
	floatKey := NewSearchAttributeKeyFloat64("floatKey")
	timeKey := NewSearchAttributeKeyTime("timeKey")
	keywordListKey := NewSearchAttributeKeywordList("keywordListKey")

	now := time.Now()
	sa = NewSearchAttributes(
		boolKey.ValueSet(true),
		stringKey.ValueSet("string"),
		keywordKey.ValueSet("keyword"),
		intKey.ValueSet(10),
		floatKey.ValueSet(5.4),
		timeKey.ValueSet(now),
		keywordListKey.ValueSet([]string{"value1", "value2"}),
	)
	require.True(t, sa.ContainsKey(boolKey))
	boolValue, ok = sa.GetBool(boolKey)
	require.True(t, boolValue)
	require.True(t, ok)

	require.True(t, sa.ContainsKey(stringKey))
	stringValue, ok := sa.GetString(stringKey)
	require.Equal(t, "string", stringValue)
	require.True(t, ok)

	require.True(t, sa.ContainsKey(keywordKey))
	keywordValue, ok := sa.GetKeyword(keywordKey)
	require.Equal(t, "keyword", keywordValue)
	require.True(t, ok)

	require.True(t, sa.ContainsKey(intKey))
	intValue, ok := sa.GetInt(intKey)
	require.Equal(t, int64(10), intValue)
	require.True(t, ok)

	require.True(t, sa.ContainsKey(floatKey))
	floatValue, ok := sa.GetFloat(floatKey)
	require.Equal(t, float64(5.4), floatValue)
	require.True(t, ok)

	require.True(t, sa.ContainsKey(timeKey))
	timeValue, ok := sa.GetTime(timeKey)
	require.Equal(t, now, timeValue)
	require.True(t, ok)

	require.True(t, sa.ContainsKey(keywordListKey))
	keywordListValue, ok := sa.GetKeywordList(keywordListKey)
	require.Equal(t, []string{"value1", "value2"}, keywordListValue)
	require.True(t, ok)

	require.NotNil(t, sa.GetUntypedValues())
	require.Equal(t, 7, len(sa.GetUntypedValues()))
	require.Equal(t, 7, sa.Size())
	// Verify copy and delete behaviors
	stringKey2 := NewSearchAttributeKeyString("otherStringKey")

	sa = NewSearchAttributes(
		sa.Copy(),
		boolKey.ValueUnset(),
		stringKey2.ValueSet("other string"),
	)

	require.False(t, sa.ContainsKey(boolKey))
	boolValue, ok = sa.GetBool(boolKey)
	require.False(t, boolValue)
	require.False(t, ok)

	require.True(t, sa.ContainsKey(stringKey))
	stringValue, ok = sa.GetString(stringKey)
	require.True(t, ok)
	require.Equal(t, "string", stringValue)

	require.True(t, sa.ContainsKey(stringKey2))
	stringValue, ok = sa.GetString(stringKey2)
	require.Equal(t, "other string", stringValue)
	require.True(t, ok)

	require.True(t, sa.ContainsKey(keywordKey))
	keywordValue, ok = sa.GetKeyword(keywordKey)
	require.Equal(t, "keyword", keywordValue)
	require.True(t, ok)

	require.NotNil(t, sa.GetUntypedValues())
	require.Equal(t, 7, len(sa.GetUntypedValues()))
	require.Equal(t, 7, sa.Size())
}

func TestSearchAttributesKeyWordList(t *testing.T) {
	t.Parallel()
	kw := NewSearchAttributeKeywordList("keywordList")
	kv := []string{"keyword1", "keyword2", "keyword3"}
	sa := NewSearchAttributes(kw.ValueSet(kv))
	// Modify the list and verify it doesn't change the search attribute
	kv[0] = ""
	require.Equal(t, 1, sa.Size())
	values, ok := sa.GetKeywordList(kw)
	require.True(t, ok)
	require.Equal(t, []string{"keyword1", "keyword2", "keyword3"}, values)
	// Modify the return value and verify it doesn't change the
	// underlying value in the search attribute
	values[0] = ""
	values2, ok := sa.GetKeywordList(kw)
	require.True(t, ok)
	require.Equal(t, []string{"keyword1", "keyword2", "keyword3"}, values2)
}

func TestSearchAttributesDeepCopy(t *testing.T) {
	t.Parallel()
	key1 := NewSearchAttributeKeyString("stringKey1")
	key2 := NewSearchAttributeKeywordList("keywordList")
	keywordListValue := []string{"keyword1", "keyword2", "keyword3"}
	sa := NewSearchAttributes(
		key1.ValueSet("value"),
		key2.ValueSet(keywordListValue),
		NewSearchAttributeKeyString("stringKey2").ValueSet("value"),
		NewSearchAttributeKeyString("stringKey3").ValueSet("value"),
	)
	// Modify the untyped map and show it doesn't effect the search attribute
	untypedValues := sa.GetUntypedValues()
	untypedValues[key1] = "new value"
	value, ok := sa.GetString(key1)
	require.True(t, ok)
	require.Equal(t, "value", value)
	// Test with a slice as well
	keywordList := untypedValues[key2].([]string)
	keywordList[0] = ""
	keywordListSA, ok := sa.GetKeywordList(key2)
	require.True(t, ok)
	require.Equal(t, []string{"keyword1", "keyword2", "keyword3"}, keywordListSA)
}
