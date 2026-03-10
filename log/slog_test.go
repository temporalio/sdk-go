//go:build go1.21

package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func addCallerDepth(log Logger) {
	log.Info("calling AddCallerDepth")
}

func TestTemporalSlogAdapter(t *testing.T) {
	var buf bytes.Buffer
	th := slog.NewJSONHandler(&buf, &slog.HandlerOptions{AddSource: true, Level: slog.LevelDebug})
	sl := NewStructuredLogger(slog.New(th))

	sl.Info("info log", "Key", "Value")
	addCallerDepth(sl)
	addCallerDepth(Skip(sl, 1))
	With(sl, "WithKey", "WithValue").Debug("With")
	sl.Debug("debug log", "Key", "Value", "OtherKey", "OtherValue")

	// Parse logs
	var ms []map[string]any
	for _, line := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		err := json.Unmarshal(line, &m)
		require.NoError(t, err)
		ms = append(ms, m)
	}

	expectedLogs := []map[string]any{
		{
			"level": "INFO",
			"Key":   "Value",
			"msg":   "info log",
			"source": map[string]string{
				"file":     "log/slog_test.go",
				"function": "go.temporal.io/sdk/log.TestTemporalSlogAdapter",
				"line":     "",
			},
			"time": "",
		},
		{
			"level": "INFO",
			"msg":   "calling AddCallerDepth",
			"source": map[string]string{
				"file":     "log/slog_test.go",
				"function": "go.temporal.io/sdk/log.addCallerDepth",
				"line":     "",
			},
			"time": "",
		},
		{
			"level": "INFO",
			"msg":   "calling AddCallerDepth",
			"source": map[string]string{
				"file":     "log/slog_test.go",
				"function": "go.temporal.io/sdk/log.TestTemporalSlogAdapter",
				"line":     "",
			},
			"time": "",
		},
		{
			"level":   "DEBUG",
			"WithKey": "WithValue",
			"msg":     "With",
			"source": map[string]string{
				"file":     "log/slog_test.go",
				"function": "go.temporal.io/sdk/log.TestTemporalSlogAdapter",
				"line":     "",
			},
			"time": "",
		},
		{
			"level":    "DEBUG",
			"Key":      "Value",
			"OtherKey": "OtherValue",
			"msg":      "debug log",
			"source": map[string]string{
				"file":     "log/slog_test.go",
				"function": "go.temporal.io/sdk/log.TestTemporalSlogAdapter",
				"line":     "",
			},
			"time": "",
		},
	}
	// Compare expected vs actual logs. We don't do a strict comparison because
	// we know some fields will be different
	require.Equal(t, len(expectedLogs), len(ms))
	for i, expectedLog := range expectedLogs {
		actualLog := ms[i]
		require.Equal(t, len(expectedLog), len(actualLog))
		for k, v := range expectedLog {
			if k == slog.TimeKey {
				// Skip time because we know it will be different
				require.Contains(t, actualLog, slog.TimeKey)
			} else if k == slog.SourceKey {
				require.Contains(t, actualLog, slog.SourceKey)
				expectedSource := v.(map[string]string)
				actualSource := actualLog[slog.SourceKey].(map[string]any)
				require.True(t, strings.HasSuffix(actualSource["file"].(string), expectedSource["file"]))
				require.Equal(t, expectedSource["function"], actualSource["function"])
				// Skip line to make the test less annoying to maintain
				require.Contains(t, actualSource, "line")
			} else {
				require.Equal(t, v, actualLog[k])
			}
		}
	}
}
