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
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

func Test_DecodedValue(t *testing.T) {
	equals := func(a, b interface{}) bool {
		ao := a.(ActivityOptions)
		bo := b.(ActivityOptions)
		return ao.TaskList == bo.TaskList
	}
	value := ActivityOptions{TaskList: "test-tasklist"}
	blob := encodeValue(value)
	isEqual := isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	value.TaskList = "value-changed"
	isEqual = isEqualValue(value, blob, equals)
	require.False(t, isEqual)
}

func Test_DecodedValuePtr(t *testing.T) {
	equals := func(a, b interface{}) bool {
		ao := a.(*ActivityOptions)
		bo := b.(*ActivityOptions)
		return ao.TaskList == bo.TaskList
	}
	value := &ActivityOptions{TaskList: "test-tasklist"}
	blob := encodeValue(value)
	isEqual := isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	value.TaskList = "value-changed"
	isEqual = isEqualValue(value, blob, equals)
	require.False(t, isEqual)
}

func Test_DecodedValueNil(t *testing.T) {
	equals := func(a, b interface{}) bool {
		return a == nil && b == nil
	}
	// newValue is nil, old value is nil
	var value interface{}
	blob := encodeValue(value)
	isEqual := isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	// newValue is nil, oldValue is not nil
	blob = encodeValue("any-non-nil-value")
	isEqual = isEqualValue(value, blob, equals)
	require.False(t, isEqual)

	// newValue is not nil, oldValue is nil
	blob = encodeValue(nil)
	isEqual = isEqualValue("non-nil-value", blob, equals)
	require.False(t, isEqual)
}
