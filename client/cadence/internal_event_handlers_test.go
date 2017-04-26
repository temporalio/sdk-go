package cadence

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
