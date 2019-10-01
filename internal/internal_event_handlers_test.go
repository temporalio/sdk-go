// Copyright (c) 2017 Uber Technologies, Inc.
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
	"encoding/json"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/zap"
)

func TestReplayAwareLogger(t *testing.T) {
	temp, err := ioutil.TempFile("", "cadence-client-test")
	require.NoError(t, err, "Failed to create temp file.")
	defer os.Remove(temp.Name())
	config := zap.NewProductionConfig()
	config.OutputPaths = []string{temp.Name()}
	config.EncoderConfig.TimeKey = "" // no timestamps in tests

	isReplay, enableLoggingInReplay := false, false
	logger, err := config.Build()
	require.NoError(t, err, "Failed to create logger.")
	logger = logger.WithOptions(zap.WrapCore(wrapLogger(&isReplay, &enableLoggingInReplay)))

	logger.Info("normal info")

	isReplay = true
	logger.Info("replay info") // this log should be suppressed

	isReplay, enableLoggingInReplay = false, true
	logger.Info("normal2 info")

	isReplay = true
	logger.Info("replay2 info")

	logger.Sync()

	byteContents, err := ioutil.ReadAll(temp)
	require.NoError(t, err, "Couldn't read log contents from temp file.")
	logs := string(byteContents)

	require.True(t, strings.Contains(logs, "normal info"), "normal info should show")
	require.False(t, strings.Contains(logs, "replay info"), "replay info should not show")
	require.True(t, strings.Contains(logs, "normal2 info"), "normal2 info should show")
	require.True(t, strings.Contains(logs, "replay2 info"), "replay2 info should show")
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
	env := &workflowEnvironmentImpl{
		dataConverter: getDefaultDataConverter(),
	}
	testDecodeValueHelper(t, env)
}

func TestDecodedValue_WithDataConverter(t *testing.T) {
	env := &workflowEnvironmentImpl{
		dataConverter: newTestDataConverter(),
	}
	testDecodeValueHelper(t, env)
}

func Test_DecodedValuePtr(t *testing.T) {
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
	_, err := validateAndSerializeSearchAttributes(nil)
	require.EqualError(t, err, "search attributes is empty")

	attr := map[string]interface{}{
		"JustKey": make(chan int),
	}
	_, err = validateAndSerializeSearchAttributes(attr)
	require.EqualError(t, err, "encode search attribute [JustKey] error: json: unsupported type: chan int")

	attr = map[string]interface{}{
		"key": 1,
	}
	searchAttr, err := validateAndSerializeSearchAttributes(attr)
	require.NoError(t, err)
	require.Equal(t, 1, len(searchAttr.IndexedFields))
	var resp int
	json.Unmarshal(searchAttr.IndexedFields["key"], &resp)
	require.Equal(t, 1, resp)
}

func Test_UpsertSearchAttributes(t *testing.T) {
	env := &workflowEnvironmentImpl{
		decisionsHelper: newDecisionsHelper(),
		workflowInfo:    GetWorkflowInfo(createRootTestContext()),
	}
	err := env.UpsertSearchAttributes(nil)
	require.Error(t, err)

	err = env.UpsertSearchAttributes(map[string]interface{}{
		CadenceChangeVersion: []string{"change2-1", "change1-1"}},
	)
	require.NoError(t, err)
	_, ok := env.decisionsHelper.decisions[makeDecisionID(decisionTypeUpsertSearchAttributes, "change2-1")]
	require.True(t, ok)
	require.Equal(t, int32(0), env.counterID)

	err = env.UpsertSearchAttributes(map[string]interface{}{"key": 1})
	require.NoError(t, err)
	require.Equal(t, int32(1), env.counterID)
}

func Test_MergeSearchAttributes(t *testing.T) {
	tests := []struct {
		name     string
		current  *s.SearchAttributes
		upsert   *s.SearchAttributes
		expected *s.SearchAttributes
	}{
		{
			name:     "currentIsNil",
			current:  nil,
			upsert:   &s.SearchAttributes{},
			expected: &s.SearchAttributes{},
		},
		{
			name:     "currentIsEmpty",
			current:  &s.SearchAttributes{IndexedFields: make(map[string][]byte)},
			upsert:   &s.SearchAttributes{},
			expected: &s.SearchAttributes{},
		},
		{
			name: "normalMerge",
			current: &s.SearchAttributes{
				IndexedFields: map[string][]byte{
					"CustomIntField":     []byte(`1`),
					"CustomKeywordField": []byte(`keyword`),
				},
			},
			upsert: &s.SearchAttributes{
				IndexedFields: map[string][]byte{
					"CustomIntField":  []byte(`2`),
					"CustomBoolField": []byte(`true`),
				},
			},
			expected: &s.SearchAttributes{
				IndexedFields: map[string][]byte{
					"CustomIntField":     []byte(`2`),
					"CustomKeywordField": []byte(`keyword`),
					"CustomBoolField":    []byte(`true`),
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := mergeSearchAttributes(test.current, test.upsert)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_GetChangeVersion(t *testing.T) {
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
		t.Run(test.name, func(t *testing.T) {
			result := getChangeVersion(test.changeID, test.version)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_GetChangeVersions(t *testing.T) {
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
		t.Run(test.name, func(t *testing.T) {
			result := getChangeVersions(test.changeID, test.version, test.existingChangeVersions)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_CreateSearchAttributesForChangeVersion(t *testing.T) {
	result := createSearchAttributesForChangeVersion("cid", 1, map[string]Version{})
	val, ok := result["CadenceChangeVersion"]
	require.True(t, ok, "Remember to update related key on server side")
	require.Equal(t, []string{"cid-1"}, val)
}
