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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/temporal-proto/common"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestReplayAwareLogger(t *testing.T) {
	t.Parallel()
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core, zap.Development())

	isReplay, enableLoggingInReplay := false, false
	logger = logger.WithOptions(zap.WrapCore(wrapLogger(&isReplay, &enableLoggingInReplay)))

	logger.Info("normal info")

	isReplay = true
	logger.Info("replay info") // this log should be suppressed

	isReplay, enableLoggingInReplay = false, true
	logger.Info("normal2 info")

	isReplay = true
	logger.Info("replay2 info")

	var messages []string
	for _, log := range observed.AllUntimed() {
		messages = append(messages, log.Message)
	}
	assert.Len(t, messages, 3) // ensures "replay info" wasn't just misspelled
	assert.Contains(t, messages, "normal info")
	assert.NotContains(t, messages, "replay info")
	assert.Contains(t, messages, "normal2 info")
	assert.Contains(t, messages, "replay2 info")
}

func testDecodeValueHelper(t *testing.T, env *workflowEnvironmentImpl) {
	equals := func(a, b interface{}) bool {
		ao := a.(ActivityOptions)
		bo := b.(ActivityOptions)
		return ao.TaskList == bo.TaskList
	}
	value := ActivityOptions{TaskList: "test-tasklist"}
	blob := env.encodeValue(value)
	isEqual := env.isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	value.TaskList = "value-changed"
	isEqual = env.isEqualValue(value, blob, equals)
	require.False(t, isEqual)
}

func TestDecodedValue(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: getDefaultDataConverter(),
	}
	testDecodeValueHelper(t, env)
}

func TestDecodedValueWithDataConverter(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: newTestDataConverter(),
	}
	testDecodeValueHelper(t, env)
}

func Test_DecodedValuePtr(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: getDefaultDataConverter(),
	}
	equals := func(a, b interface{}) bool {
		ao := a.(*ActivityOptions)
		bo := b.(*ActivityOptions)
		return ao.TaskList == bo.TaskList
	}
	value := &ActivityOptions{TaskList: "test-tasklist"}
	blob := env.encodeValue(value)
	isEqual := env.isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	value.TaskList = "value-changed"
	isEqual = env.isEqualValue(value, blob, equals)
	require.False(t, isEqual)
}

func Test_DecodedValueNil(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: getDefaultDataConverter(),
	}
	equals := func(a, b interface{}) bool {
		return a == nil && b == nil
	}
	// newValue is nil, old value is nil
	var value interface{}
	blob := env.encodeValue(value)
	isEqual := env.isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	// newValue is nil, oldValue is not nil
	blob = env.encodeValue("any-non-nil-value")
	isEqual = env.isEqualValue(value, blob, equals)
	require.False(t, isEqual)

	// newValue is not nil, oldValue is nil
	blob = env.encodeValue(nil)
	isEqual = env.isEqualValue("non-nil-value", blob, equals)
	require.False(t, isEqual)
}

func Test_ValidateAndSerializeSearchAttributes(t *testing.T) {
	t.Parallel()
	_, err := validateAndSerializeSearchAttributes(nil)
	require.EqualError(t, err, "search attributes is empty")

	attr := map[string]interface{}{
		"JustKey": make(chan int),
	}
	_, err = validateAndSerializeSearchAttributes(attr)
	require.EqualError(t, err, "encode search attribute [JustKey] error: unable to encode to JSON: json: unsupported type: chan int")

	attr = map[string]interface{}{
		"key": 1,
	}
	searchAttr, err := validateAndSerializeSearchAttributes(attr)
	require.NoError(t, err)
	require.Equal(t, 1, len(searchAttr.IndexedFields))
	var resp int
	_ = DefaultDataConverter.FromPayload(searchAttr.IndexedFields["key"], &resp)
	require.Equal(t, 1, resp)
}

func Test_UpsertSearchAttributes(t *testing.T) {
	t.Parallel()
	helper := newDecisionsHelper()
	env := &workflowEnvironmentImpl{
		decisionsHelper: helper,
		workflowInfo:    GetWorkflowInfo(createRootTestContext()),
	}
	helper.setCurrentDecisionStartedEventID(4)
	err := env.UpsertSearchAttributes(nil)
	require.Error(t, err)

	err = env.UpsertSearchAttributes(map[string]interface{}{
		TemporalChangeVersion: []string{"change2-1", "change1-1"}},
	)
	require.NoError(t, err)
	_, ok := env.decisionsHelper.decisions[makeDecisionID(decisionTypeUpsertSearchAttributes, "change2-1")]
	require.True(t, ok)
	require.Equal(t, int64(7), env.GenerateSequence())

	err = env.UpsertSearchAttributes(map[string]interface{}{"key": 1})
	require.NoError(t, err)
	require.Equal(t, int64(8), env.GenerateSequence())
}

func Test_MergeSearchAttributes(t *testing.T) {
	t.Parallel()

	encodeString := func(str string) *commonpb.Payload {
		payload, _ := DefaultDataConverter.ToPayload(str)
		return payload
	}

	tests := []struct {
		name     string
		current  *commonpb.SearchAttributes
		upsert   *commonpb.SearchAttributes
		expected *commonpb.SearchAttributes
	}{
		{
			name:     "currentIsNil",
			current:  nil,
			upsert:   &commonpb.SearchAttributes{},
			expected: nil,
		},
		{
			name:     "currentIsEmpty",
			current:  &commonpb.SearchAttributes{IndexedFields: make(map[string]*commonpb.Payload)},
			upsert:   &commonpb.SearchAttributes{},
			expected: nil,
		},
		{
			name: "normalMerge",
			current: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"CustomIntField":     encodeString(`1`),
					"CustomKeywordField": encodeString(`keyword`),
				},
			},
			upsert: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"CustomIntField":  encodeString(`2`),
					"CustomBoolField": encodeString(`true`),
				},
			},
			expected: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"CustomIntField":     encodeString(`2`),
					"CustomKeywordField": encodeString(`keyword`),
					"CustomBoolField":    encodeString(`true`),
				},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := mergeSearchAttributes(test.current, test.upsert)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_GetChangeVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		changeID string
		version  Version
		expected string
	}{
		{
			name:     "default",
			changeID: "cid",
			version:  DefaultVersion,
			expected: "cid--1",
		},
		{
			name:     "normal_case",
			changeID: "cid",
			version:  1,
			expected: "cid-1",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := getChangeVersion(test.changeID, test.version)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_GetChangeVersions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		changeID               string
		version                Version
		existingChangeVersions map[string]Version
		expected               []string
	}{
		{
			name:                   "single_change_id",
			changeID:               "cid",
			version:                1,
			existingChangeVersions: map[string]Version{},
			expected:               []string{"cid-1"},
		},
		{
			name:     "multi_change_ids",
			changeID: "cid2",
			version:  1,
			existingChangeVersions: map[string]Version{
				"cid": 1,
			},
			expected: []string{"cid2-1", "cid-1"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := getChangeVersions(test.changeID, test.version, test.existingChangeVersions)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_CreateSearchAttributesForChangeVersion(t *testing.T) {
	t.Parallel()
	result := createSearchAttributesForChangeVersion("cid", 1, map[string]Version{})
	val, ok := result["TemporalChangeVersion"]
	require.True(t, ok, "Remember to update related key on server side")
	require.Equal(t, []string{"cid-1"}, val)
}
