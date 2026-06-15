package google_adk_agents_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestGenaiSessionRoundTrip is the cross-boundary serialization guarantee: the
// genai/session-aware DataConverter round-trips the primitives that actually
// cross the Activity boundary (*genai.Content and []*session.Event) without
// loss. Equality is compared on the canonical JSON form to avoid touching the
// proto-backed unexported state inside genai values.
func TestGenaiSessionRoundTrip(t *testing.T) {
	dc := adk.NewDataConverter()

	content := genai.NewContentFromText("what is the weather?", genai.RoleUser)

	ev := session.NewEvent("inv-7")
	ev.ID = "evt-1"
	ev.Author = "weather-agent"
	ev.Timestamp = time.Unix(1_700_000_000, 0).UTC()
	ev.Content = genai.NewContentFromText("it is sunny", genai.RoleModel)
	events := []*session.Event{ev}

	payloads, err := dc.ToPayloads(content, events)
	require.NoError(t, err)
	require.Len(t, payloads.GetPayloads(), 2)

	var gotContent *genai.Content
	var gotEvents []*session.Event
	require.NoError(t, dc.FromPayloads(payloads, &gotContent, &gotEvents))

	require.Equal(t, mustJSON(t, content), mustJSON(t, gotContent), "*genai.Content must round-trip losslessly")
	require.Equal(t, mustJSON(t, events), mustJSON(t, gotEvents), "[]*session.Event must round-trip losslessly")

	// Spot-check the decoded fields directly, not just the JSON blob.
	require.Len(t, gotEvents, 1)
	require.Equal(t, "evt-1", gotEvents[0].ID)
	require.Equal(t, "weather-agent", gotEvents[0].Author)
	require.True(t, containsText(gotEvents, "it is sunny"))
}
