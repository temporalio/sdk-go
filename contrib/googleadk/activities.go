package googleadk

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/tool"
)

// geminiNameRE recognizes the same "gemini-" name family as upstream ADK's
// documented registry pattern ("^(?i)gemini-.*"). It is matched locally by
// resolveModel and never written into ADK's process-global model registry:
// upstream documents that registry as opt-in and application-owned, so a
// library registration there would panic the application's own model.Register
// of the same pattern (in either order) and turn overlapping app patterns into
// runtime ambiguity errors.
var geminiNameRE = regexp.MustCompile(`^(?i)gemini-`)

// registryHasNoMatch reports whether err is model.NewLLM's zero-matches error.
// Upstream exports no sentinel for it (a bare fmt.Errorf), so this matches the
// message; the pinned adk/v2 version in go.mod plus TestGeminiResolvesOutOfTheBox
// guard against wording drift.
func registryHasNoMatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no registered LLM matches")
}

// ModelFactory reconstructs a model.LLM worker-side from the model name carried
// in LLMRequest.Model. API keys and other credentials are captured in the
// closure and therefore live only on the worker — they are never serialized
// into Activity inputs.
//
// IMPORTANT: disable the model SDK's own client-side retries inside the factory.
// The InvokeModel Activity already runs under Temporal's RetryPolicy, so leaving
// SDK retries enabled means a transient failure is retried twice over (SDK
// attempts × Temporal attempts) — inflating latency and cost and blurring the
// retry contract. Temporal should be the single source of truth for retries.
type ModelFactory func(ctx context.Context, modelName string) (model.LLM, error)

// MCPFactory constructs a live MCP tool.Toolset worker-side. The real
// mcptoolset.New(...) — which holds a network session and spawns goroutines —
// lives here, behind the Activity boundary, never in the workflow.
//
// Lifecycle contract: the factory runs at most once per toolset name per
// Activities value (a construction failure is retried on the next call). Its
// result is cached and shared by all subsequent ListMcpTools / CallMcpTool
// calls, so it MUST be safe for concurrent use (mcptoolset.New toolsets are:
// their connection is mutex-guarded and self-healing). Keep the factory a
// cheap, non-blocking constructor — the live session should open lazily, as
// mcptoolset.New's does — because a single lock serializes construction across
// all toolsets on the worker; do not call back into this Activities value from
// inside a factory. ctx is only valid for the duration of the constructing
// call; mcptoolset.New ignores it — its MCP session opens lazily with each
// request's own context. If the returned toolset implements `Close() error`,
// Activities.Close closes it at worker shutdown.
type MCPFactory func(ctx context.Context) (tool.Toolset, error)

// Config declares the worker-side registries the Activities consult when a
// model, tool or MCP call crosses the Activity boundary. Construct it once at
// worker start and hand it to NewActivities.
type Config struct {
	// Models maps a model name (the string the user sets on their model.LLM and
	// that ADK copies into LLMRequest.Model) to a factory that rebuilds it
	// worker-side. The InvokeModel Activity looks the factory up by name. It is
	// optional: when a name is absent here, InvokeModel falls back to ADK's
	// name-based model registry (model.NewLLM) — which stays application-owned;
	// this package never registers into it — and gemini-* names the registry
	// does not know resolve via a built-in zero-config Gemini fallback. Use
	// Models to supply custom credentials, disable SDK retries, or override
	// what the fallbacks would otherwise build.
	Models map[string]ModelFactory

	// MCPToolsets maps the logical name passed to NewMCPToolset to a factory that
	// builds the live stateless MCP toolset worker-side. The ListMcpTools and
	// CallMcpTool Activities use it. Each factory runs at most once per name —
	// its toolset is cached on the Activities value and shared across calls; see
	// MCPFactory for the full lifecycle contract.
	MCPToolsets map[string]MCPFactory
}

// Activities holds the worker-side registries and implements the Temporal
// Activities dispatched from the workflow side: InvokeModel (by TemporalModel),
// and ListMcpTools / CallMcpTool (by the MCP proxy). Ordinary function tools run
// in-workflow and need no registration here; ActivityAsTool dispatches the
// user's own already-registered activity by name. Build one with NewActivities
// and register it with Register.
type Activities struct {
	models map[string]ModelFactory
	mcp    map[string]MCPFactory

	// mcpMu guards mcpCache and mcpClosed. A factory runs at most once per
	// toolset name; the resulting toolset is cached and shared by every
	// subsequent Activity call, so its lazily-opened MCP session (and, for
	// CommandTransport, the server subprocess) is bounded to one per worker
	// rather than leaked per call. mcpClosed makes Close terminal: a late or
	// retried MCP Activity must not silently repopulate the cache with a
	// toolset nothing will ever close.
	mcpMu     sync.Mutex
	mcpCache  map[string]tool.Toolset
	mcpClosed bool
}

// NewActivities validates cfg and returns the worker-side Activities. It returns
// an error if a registered factory is nil, so misconfiguration surfaces at
// worker start rather than mid-run inside an Activity.
func NewActivities(cfg Config) (*Activities, error) {
	a := &Activities{
		models:   make(map[string]ModelFactory, len(cfg.Models)),
		mcp:      make(map[string]MCPFactory, len(cfg.MCPToolsets)),
		mcpCache: make(map[string]tool.Toolset),
	}
	for name, f := range cfg.Models {
		if f == nil {
			return nil, fmt.Errorf("googleadk: nil ModelFactory registered for model %q", name)
		}
		a.models[name] = f
	}
	for name, f := range cfg.MCPToolsets {
		if f == nil {
			return nil, fmt.Errorf("googleadk: nil MCPFactory registered for toolset %q", name)
		}
		a.mcp[name] = f
	}
	return a, nil
}

// resolveModel reconstructs the worker-side model.LLM for a model name, in
// priority order: an explicit Config.Models factory wins; otherwise the
// application-owned ADK model registry (model.NewLLM) is honored; and when the
// registry simply has no match for a gemini-* name, a built-in zero-config
// Gemini fallback applies (nil config reads GEMINI_API_KEY / GOOGLE_API_KEY
// worker-side). The fallback is gated on the registry's no-match error only: a
// failing app-registered gemini factory, or an app-caused pattern ambiguity,
// surfaces unchanged rather than being masked by ambient-credential Gemini.
func (a *Activities) resolveModel(ctx context.Context, name string) (model.LLM, error) {
	if factory, ok := a.models[name]; ok {
		return factory(ctx, name)
	}
	llm, err := model.NewLLM(ctx, name)
	switch {
	case err == nil:
		return llm, nil
	case geminiNameRE.MatchString(name) && registryHasNoMatch(err):
		return gemini.NewModel(ctx, name, nil)
	default:
		return nil, err
	}
}

// activityRegistry is the registration surface registerAll needs. Both
// worker.Registry and the plugin run-context registry
// (temporal.SimplePluginRunContextBeforeOptions.Registry) satisfy it.
type activityRegistry interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}

// registerAll wires InvokeModel, ListMcpTools and CallMcpTool onto r under
// their stable Activity names.
func (a *Activities) registerAll(r activityRegistry) {
	r.RegisterActivityWithOptions(a.InvokeModel, activity.RegisterOptions{Name: InvokeModelActivityName})
	r.RegisterActivityWithOptions(a.ListMcpTools, activity.RegisterOptions{Name: ListMcpToolsActivityName})
	r.RegisterActivityWithOptions(a.CallMcpTool, activity.RegisterOptions{Name: CallMcpToolActivityName})
}

// Register wires InvokeModel, ListMcpTools and CallMcpTool onto the worker under
// their stable Activity names. NewPlugin is the standard wiring — it performs
// this registration at worker start; Register remains for the workflow/activity
// test environments (which construct no real worker, so plugins do not run
// there) and manual setups.
func (a *Activities) Register(r worker.Registry) {
	a.registerAll(r)
}

// Close closes every cached MCP toolset that implements `Close() error`,
// joining any failures. Call it after the worker stops (defer acts.Close()) so
// long-lived MCP sessions — and, for CommandTransport, their server
// subprocesses — shut down with the worker. It is idempotent and terminal:
// once closed, MCP Activities on this value fail (non-retryably) rather than
// silently rebuilding toolsets nothing would ever close. The default worker
// stop does not wait for in-flight Activities, so set a nonzero
// worker.Options.WorkerStopTimeout when using closable toolsets to let them
// drain before Close runs. Toolsets without a Close method (including ADK v2's
// mcptoolset at the pinned version) cannot be closed and end with the worker
// process.
func (a *Activities) Close() error {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	a.mcpClosed = true
	var errs []error
	for name, ts := range a.mcpCache {
		c, ok := ts.(interface{ Close() error })
		if !ok {
			continue
		}
		if err := c.Close(); err != nil {
			errs = append(errs, fmt.Errorf("googleadk: close MCP toolset %q: %w", name, err))
		}
	}
	clear(a.mcpCache)
	return errors.Join(errs...)
}
