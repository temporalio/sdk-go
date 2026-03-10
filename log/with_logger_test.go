package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithLogger(t *testing.T) {
	wl := &withLogger{
		keyvals: []interface{}{"p1", 1, "p2", "v2"},
	}
	allKeys := wl.prependKeyvals([]interface{}{"p4", 4})
	assert.Equal(t, []interface{}{"p1", 1, "p2", "v2", "p4", 4}, allKeys)
}

func TestWithLoggerSkip(t *testing.T) {
	wl := &withLogger{}
	wl2 := With(wl, "p1", 1, "p2", "v2")
	sl := Skip(wl2, 0)
	wl, _ = sl.(*withLogger)
	assert.Equal(t, []interface{}{"p1", 1, "p2", "v2"}, wl.keyvals)
}
