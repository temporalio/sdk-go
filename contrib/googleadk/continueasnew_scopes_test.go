package googleadk_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// scopedStateResult is the serializable output of scopedStateWorkflow.
type scopedStateResult struct {
	// SnapshotStateKeys are the sorted keys of the snapshot's State map — it must
	// hold the session-scoped key and none of the prefixed ones.
	SnapshotStateKeys []string
	// SnapshotEventHas{App,User,Temp} report whether any snapshot event's state
	// delta still carries the app:-, user:-, or temp:-scoped probe key.
	SnapshotEventHasApp  bool
	SnapshotEventHasUser bool
	SnapshotEventHasTemp bool
	// SourceEventHasApp reports whether the source service's live history still
	// carries the app:-scoped delta after export — it must, proving ExportSession
	// copied the events rather than stripping them in place.
	SourceEventHasApp bool
	// SnapshotEvents / ImportedEvents count the exported and re-imported history.
	SnapshotEvents int
	ImportedEvents int
	// ImportedNote is the session-scoped state value read back from the imported
	// session — session-scoped state must still round-trip.
	ImportedNote string
	// ImportedHas{App,User,Temp} probe the imported session's merged state view
	// (which includes service-level app/user state) for resurrected keys.
	ImportedHasApp  bool
	ImportedHasUser bool
	ImportedHasTemp bool
	// OtherSessionHasApp probes an unrelated session in the destination service
	// for the app:-scoped key — service-level contamination would leak it there.
	OtherSessionHasApp bool
}

// sessionStateHas reports whether the session's merged state view contains key.
func sessionStateHas(s session.Session, key string) bool {
	for k := range s.State().All() {
		if k == key {
			return true
		}
	}
	return false
}

// eventsDeltaHas reports whether any event in evs carries key in its state delta.
func eventsDeltaHas(evs []*session.Event, key string) bool {
	for _, ev := range evs {
		if ev == nil {
			continue
		}
		if _, ok := ev.Actions.StateDelta[key]; ok {
			return true
		}
	}
	return false
}

// scopedStateWorkflow runs one turn whose tool writes a key in every state scope
// (session, app:, user:, temp:), exports the session, and imports the snapshot
// into a fresh service — the continue-as-new shape — recording where each scope's
// value ends up so the test can assert that only session-scoped state crosses the
// boundary, in both the snapshot's State and its events' state deltas.
func scopedStateWorkflow(ctx workflow.Context) (scopedStateResult, error) {
	adkCtx := googleadk.NewContext(ctx)
	var res scopedStateResult

	// A tool that writes one key per state scope; the writes land in the tool
	// event's Actions.StateDelta and are persisted by the runner.
	scatter, err := funcTool("scatter", func(tctx agent.Context, _ map[string]any) (map[string]any, error) {
		for _, kv := range [...]struct{ k, v string }{
			{"note", "kept"},
			{"app:setting", "stale-app"},
			{"user:pref", "stale-user"},
			{"temp:scratch", "junk"},
		} {
			if serr := tctx.State().Set(kv.k, kv.v); serr != nil {
				return nil, serr
			}
		}
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		return res, err
	}

	// --- One turn against service A that triggers the scatter tool. ---
	svcA := session.InMemoryService()
	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "root assistant",
		Model:       googleadk.NewModel("fake-model"),
		Instruction: "be helpful",
		Tools:       []tool.Tool{scatter},
	})
	if err != nil {
		return res, err
	}
	r, err := runner.New(runner.Config{
		AppName:           "test-app",
		Agent:             root,
		SessionService:    svcA,
		AutoCreateSession: true,
	})
	if err != nil {
		return res, err
	}
	msg := genai.NewContentFromText("scatter the state", genai.RoleUser)
	for _, rerr := range r.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return res, rerr
		}
	}

	// --- Export and inspect the snapshot. ---
	snap, err := googleadk.ExportSession(adkCtx, svcA, "test-app", "user-1", "session-1")
	if err != nil {
		return res, err
	}
	res.SnapshotEvents = len(snap.Events)
	for k := range snap.State {
		res.SnapshotStateKeys = append(res.SnapshotStateKeys, k)
	}
	slices.Sort(res.SnapshotStateKeys)
	res.SnapshotEventHasApp = eventsDeltaHas(snap.Events, "app:setting")
	res.SnapshotEventHasUser = eventsDeltaHas(snap.Events, "user:pref")
	res.SnapshotEventHasTemp = eventsDeltaHas(snap.Events, "temp:scratch")

	// --- Re-read the source session AFTER export: its live history must still
	// carry the app:-scoped delta (export copies, never strips in place). ---
	srcResp, err := svcA.Get(adkCtx, &session.GetRequest{AppName: "test-app", UserID: "user-1", SessionID: "session-1"})
	if err != nil {
		return res, err
	}
	for ev := range srcResp.Session.Events().All() {
		if _, ok := ev.Actions.StateDelta["app:setting"]; ok {
			res.SourceEventHasApp = true
		}
	}

	// --- Import into a fresh service B (as the next run would at its top). ---
	svcB := session.InMemoryService()
	imported, err := googleadk.ImportSession(adkCtx, svcB, snap)
	if err != nil {
		return res, err
	}
	for range imported.Events().All() {
		res.ImportedEvents++
	}
	if v, verr := imported.State().Get("note"); verr == nil {
		if sv, ok := v.(string); ok {
			res.ImportedNote = sv
		}
	}

	// --- Probe the imported session's merged state view for resurrected keys.
	// A fresh Get merges service-level app/user state back in, so any leak into
	// svcB's service-level maps shows up here with the prefix re-added. ---
	impResp, err := svcB.Get(adkCtx, &session.GetRequest{AppName: "test-app", UserID: "user-1", SessionID: "session-1"})
	if err != nil {
		return res, err
	}
	res.ImportedHasApp = sessionStateHas(impResp.Session, "app:setting")
	res.ImportedHasUser = sessionStateHas(impResp.Session, "user:pref")
	res.ImportedHasTemp = sessionStateHas(impResp.Session, "temp:scratch")

	// --- Probe a completely unrelated session in service B: app-scoped leakage
	// is service-wide, so it would contaminate this session too. ---
	if _, cerr := svcB.Create(adkCtx, &session.CreateRequest{AppName: "test-app", UserID: "user-1", SessionID: "session-2"}); cerr != nil {
		return res, cerr
	}
	otherResp, err := svcB.Get(adkCtx, &session.GetRequest{AppName: "test-app", UserID: "user-1", SessionID: "session-2"})
	if err != nil {
		return res, err
	}
	res.OtherSessionHasApp = sessionStateHas(otherResp.Session, "app:setting")

	return res, nil
}

// TestExportSessionStripsNonSessionScopedEventDeltas proves the snapshot honors
// its scope contract in the event history, not just the State map: app:-, user:-,
// and temp:-scoped entries are stripped from the snapshot events' state deltas
// (without mutating the source session's live events), so importing the snapshot
// cannot resurrect cross-session state — neither on the imported session nor,
// via service-level app state, on an unrelated session in the same service.
func TestExportSessionStripsNonSessionScopedEventDeltas(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(scopedStateWorkflow)
	wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "scatter", map[string]any{}),
				googleadk.TextResponse("done"),
			),
		},
	})

	env.ExecuteWorkflow(scopedStateWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res scopedStateResult
	require.NoError(t, env.GetWorkflowResult(&res))

	// The snapshot's State carries only the session-scoped key.
	assert.Contains(t, res.SnapshotStateKeys, "note")
	assert.NotContains(t, res.SnapshotStateKeys, "app:setting")
	assert.NotContains(t, res.SnapshotStateKeys, "user:pref")
	assert.NotContains(t, res.SnapshotStateKeys, "temp:scratch")

	// The snapshot's event copies carry no non-session-scoped deltas either.
	// (temp: is already trimmed at append by the in-memory service; the snapshot
	// strips it too, guarding custom services that don't.)
	assert.False(t, res.SnapshotEventHasApp, "snapshot events must not carry app:-scoped deltas")
	assert.False(t, res.SnapshotEventHasUser, "snapshot events must not carry user:-scoped deltas")
	assert.False(t, res.SnapshotEventHasTemp, "snapshot events must not carry temp:-scoped deltas")

	// Export copies: the source session's live history is not stripped in place.
	assert.True(t, res.SourceEventHasApp, "export must not mutate the source session's events")

	// The history and session-scoped state still round-trip intact.
	assert.Greater(t, res.SnapshotEvents, 0, "snapshot must capture the conversation history")
	assert.Equal(t, res.SnapshotEvents, res.ImportedEvents, "events must round-trip")
	assert.Equal(t, "kept", res.ImportedNote, "session-scoped state must round-trip")

	// Import resurrects nothing: neither the imported session nor an unrelated
	// session in the destination service sees the stale scoped values.
	assert.False(t, res.ImportedHasApp, "import must not resurrect app:-scoped state")
	assert.False(t, res.ImportedHasUser, "import must not resurrect user:-scoped state")
	assert.False(t, res.ImportedHasTemp, "import must not resurrect temp:-scoped state")
	assert.False(t, res.OtherSessionHasApp, "import must not contaminate other sessions via app state")
}
