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

func TestMemoryLoggerWithoutWith_With(t *testing.T) {
	logger := NewMemoryLoggerWithoutWith()
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

func TestReplayLogger_Skip(t *testing.T) {
	logger := NewMemoryLogger()
	isReplay, enableLoggingInReplay := false, false

	replayLogger := NewReplayLogger(logger, &isReplay, &enableLoggingInReplay)
	withReplayLogger := log.With(replayLogger, "p1", 1, "p2", "v2")
	withReplayLogger = log.With(withReplayLogger, "p3", float64(3), "p4", true)
	skipLogger := log.Skip(withReplayLogger, 2)
	skipLogger = log.With(skipLogger, "p5", 5, "p6", 6)
	skipLogger.Info("message", "p7", 7)
	assert.Equal(t, "INFO  message p1 1 p2 v2 p3 3 p4 true p5 5 p6 6 p7 7\n", logger.Lines()[0])

	loggerWithoutWith := NewMemoryLoggerWithoutWith()
	replayLogger = NewReplayLogger(loggerWithoutWith, &isReplay, &enableLoggingInReplay)
	withReplayLogger = log.With(replayLogger, "p1", 1, "p2", "v2")
	withReplayLogger = log.With(withReplayLogger, "p3", float64(3), "p4", true)
	skipLogger = log.Skip(withReplayLogger, 2)
	skipLogger = log.With(skipLogger, "p5", 5, "p6", 6)
	skipLogger.Info("message", "p7", 7)
	assert.Equal(t, "INFO  message p1 1 p2 v2 p3 3 p4 true p5 5 p6 6 p7 7\n", loggerWithoutWith.Lines()[0])

}
