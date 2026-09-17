package otlpworker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFirstNonEmptyEnv(t *testing.T) {
	env := map[string]string{
		"FIRST":  "first-value",
		"SECOND": "second-value",
		"EMPTY":  "",
	}
	getenv := func(name string) string { return env[name] }

	t.Run("explicit wins", func(t *testing.T) {
		require.Equal(t, "explicit",
			FirstNonEmptyEnv("explicit", getenv, []string{"FIRST"}, "fallback"))
	})

	t.Run("first non-empty environment variable in order", func(t *testing.T) {
		require.Equal(t, "first-value",
			FirstNonEmptyEnv("", getenv, []string{"FIRST", "SECOND"}, "fallback"))
		require.Equal(t, "second-value",
			FirstNonEmptyEnv("", getenv, []string{"MISSING", "SECOND"}, "fallback"))
	})

	t.Run("empty environment values are ignored", func(t *testing.T) {
		require.Equal(t, "second-value",
			FirstNonEmptyEnv("", getenv, []string{"EMPTY", "SECOND"}, "fallback"))
	})

	t.Run("fallback when nothing set", func(t *testing.T) {
		require.Equal(t, "fallback",
			FirstNonEmptyEnv("", getenv, []string{"MISSING", "EMPTY"}, "fallback"))
	})
}
