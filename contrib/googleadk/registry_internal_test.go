package googleadk

// Internal (white-box) tests for worker-side model resolution (resolveModel):
// an explicit Config.Models factory wins, then the application-owned ADK model
// registry, then the built-in zero-config gemini-* fallback — and the package
// must never write into ADK's process-global registry (model.Register panics on
// a duplicate pattern and treats overlapping patterns as ambiguous, so a
// library registration breaks applications that follow upstream's documented
// pattern of registering "^(?i)gemini-.*" themselves).
//
// ORDERING: these tests run in declaration order and share ADK's process-global
// model registry, which has no reset or introspection hook (its whole API is
// Register and NewLLM). TestGeminiRegistrationIsCallerOwned permanently
// registers a gemini pattern, so it is declared LAST. The fake it registers
// cannot leak into other tests: every other gemini test resolves via explicit
// Config.Models factories (which take priority), and TestModelResolvedViaRegistry
// registers its own unique pattern.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/adk/v2/model"
)

// TestGeminiResolvesOutOfTheBox proves gemini-* names resolve with an empty
// Config.Models and no application registry entry, via the built-in fallback.
// Construction-only: with a key in the env the gemini client does no network
// I/O, so this also guards registryHasNoMatch against upstream error-message
// drift (if the no-match wording changed, the fallback would stop firing and
// this test would fail).
func TestGeminiResolvesOutOfTheBox(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "unit-test-key")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")

	a, err := NewActivities(Config{})
	require.NoError(t, err)

	llm, err := a.resolveModel(context.Background(), "gemini-2.0-flash")
	require.NoError(t, err)
	require.NotNil(t, llm)
	require.Equal(t, "gemini-2.0-flash", llm.Name())
}

// TestNonGeminiUnknownNameStillErrors proves the gemini fallback does not
// swallow the registry's no-match error for non-gemini names.
func TestNonGeminiUnknownNameStillErrors(t *testing.T) {
	a, err := NewActivities(Config{})
	require.NoError(t, err)

	_, err = a.resolveModel(context.Background(), "no-such-provider-model-xyzzy")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no registered LLM matches")
}

// TestGeminiRegistrationIsCallerOwned proves the application owns the gemini
// registration: registering upstream's documented "^(?i)gemini-.*" pattern
// after NewActivities must not panic (it did when this package registered the
// identical pattern globally), and the app's factory must then win over the
// built-in fallback. Declared last — the registration is permanent (see the
// file comment).
func TestGeminiRegistrationIsCallerOwned(t *testing.T) {
	a, err := NewActivities(Config{})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		model.Register("^(?i)gemini-.*", func(context.Context, string) (model.LLM, error) {
			return NewFakeModel(TextResponse("app-owned gemini")), nil
		})
	})

	llm, err := a.resolveModel(context.Background(), "gemini-2.0-flash")
	require.NoError(t, err)
	_, ok := llm.(*FakeModel)
	require.True(t, ok, "app-registered gemini factory must win over the built-in fallback")
}
