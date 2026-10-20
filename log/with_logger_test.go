package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithLogger(t *testing.T) {
	wl := &withLogger{
		keyvals: []any{"p1", 1, "p2", "v2"},
	}
	allKeys := wl.prependKeyvals([]any{"p4", 4})
	assert.Equal(t, []any{"p1", 1, "p2", "v2", "p4", 4}, allKeys)
}

func TestWithLoggerDoesNotOverwriteEarlierKeyvals(t *testing.T) {
	keyvals := make([]any, 2, 4)
	keyvals[0], keyvals[1] = "service", "worker"
	wl := &withLogger{keyvals: keyvals}

	first := wl.prependKeyvals([]any{"request", "first"})
	second := wl.prependKeyvals([]any{"request", "second"})

	assert.Equal(t, []any{"service", "worker", "request", "first"}, first)
	assert.Equal(t, []any{"service", "worker", "request", "second"}, second)
}

func TestWithLoggerSkip(t *testing.T) {
	wl := &withLogger{}
	wl2 := With(wl, "p1", 1, "p2", "v2")
	sl := Skip(wl2, 0)
	wl, _ = sl.(*withLogger)
	assert.Equal(t, []any{"p1", 1, "p2", "v2"}, wl.keyvals)
}
