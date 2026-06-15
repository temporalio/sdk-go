package google_adk_agents

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPartialStreamerCoalesce: with a batch interval longer than the turn, all
// added text is coalesced and surfaces as a single Done chunk on flush. This is
// the StreamingBatchInterval coalescing knob exercised at the unit level.
func TestPartialStreamerCoalesce(t *testing.T) {
	var chunks []StreamChunk
	s := newPartialStreamer(time.Hour, func(c StreamChunk) { chunks = append(chunks, c) })

	s.add("a")
	s.add("b")
	s.add("c")
	require.Empty(t, chunks, "a long interval must buffer until flush")

	s.flush()
	require.Len(t, chunks, 1)
	require.Equal(t, "abc", chunks[0].Text)
	require.True(t, chunks[0].Done)
	require.Equal(t, 0, chunks[0].Index)
}

// TestPartialStreamerZeroInterval: a zero interval emits a chunk per add, and
// the concatenation of emitted text reconstructs the full stream. Index is
// monotonic so the consumer can order partials.
func TestPartialStreamerZeroInterval(t *testing.T) {
	var chunks []StreamChunk
	s := newPartialStreamer(0, func(c StreamChunk) { chunks = append(chunks, c) })

	s.add("x")
	s.add("y")
	require.GreaterOrEqual(t, len(chunks), 2, "zero interval emits per add")

	var combined string
	for i, c := range chunks {
		combined += c.Text
		if i > 0 {
			require.Greater(t, c.Index, chunks[i-1].Index, "chunk index must be monotonic")
		}
	}
	require.Equal(t, "xy", combined)
}
