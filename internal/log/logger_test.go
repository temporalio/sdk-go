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

package log

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.temporal.io/sdk/log"
)

func TestMemoryLogger_With(t *testing.T) {
	logger := NewMemoryLogger()
	withLogger := log.With(logger, "p1", 1, "p2", "v2")
	withLogger.Info("message", "p3", float64(3))
	logger.Info("message2", "p4", 4)

	assert.Equal(t, "INFO  message p1 1 p2 v2 p3 3\n", logger.Lines()[0])
	assert.Equal(t, "INFO  message2 p4 4\n", logger.Lines()[1])
}

func TestDefaultLogger_With(t *testing.T) {
	logger := NewDefaultLogger()

	logger = logger.With("p1", 1, "p2", "v2").(*DefaultLogger)
	assert.Equal(t, "p1 1 p2 v2", logger.globalKeyvals)

	logger = logger.With("p3", 3, "p4", "v4").(*DefaultLogger)
	assert.Equal(t, "p1 1 p2 v2 p3 3 p4 v4", logger.globalKeyvals)
}

func TestReplayLogger(t *testing.T) {
	logger := NewMemoryLogger()

	isReplay, enableLoggingInReplay := false, false
	replayLogger := NewReplayLogger(logger, &isReplay, &enableLoggingInReplay)

	replayLogger.Info("normal info")

	isReplay = true
	replayLogger.Info("replay info") // this log should be suppressed

	isReplay, enableLoggingInReplay = false, true
	replayLogger.Info("normal2 info")

	isReplay = true
	replayLogger.Info("replay2 info")

	assert.Len(t, logger.Lines(), 3) // ensures "replay info" wasn't just misspelled
	assert.Contains(t, logger.Lines(), "INFO  normal info\n")
	assert.NotContains(t, logger.Lines(), "INFO  replay info\n")
	assert.Contains(t, logger.Lines(), "INFO  normal2 info\n")
	assert.Contains(t, logger.Lines(), "INFO  replay2 info\n")
}

func TestReplayLogger_With(t *testing.T) {
	logger := NewMemoryLogger()
	isReplay, enableLoggingInReplay := false, false
	replayLogger := NewReplayLogger(logger, &isReplay, &enableLoggingInReplay)
	withReplayLogger := log.With(replayLogger, "p1", 1, "p2", "v2")
	withReplayLogger.Info("message", "p3", float64(3))
	logger.Info("message2", "p4", 4)

	withReplayLogger.Info("message")
	assert.Equal(t, "INFO  message p1 1 p2 v2 p3 3\n", logger.Lines()[0])
	assert.Equal(t, "INFO  message2 p4 4\n", logger.Lines()[1])
}
