package google_adk_agents

import (
	"context"
	"fmt"
	"sync"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// AgentRunner bundles the native adk-go objects a turn Activity needs. An
// AgentFactory builds one per turn. The SessionService MUST be the same
// instance passed to runner.Config.SessionService, and AppName MUST match
// runner.Config.AppName, so the plugin can seed prior events and read the
// post-turn snapshot from the runner's own session store.
type AgentRunner struct {
	// Runner is the native adk-go runner (runner.New).
	Runner *runner.Runner
	// SessionService is the session store the Runner was built with.
	SessionService session.Service
	// AppName matches the runner's app name; defaults to the agent name.
	AppName string
	// ExternalSessionService should be set true when SessionService is a
	// database/vertexai store rather than the in-memory one, so the plugin
	// can warn about double durability (see Options.WarnOnExternalSessionService).
	ExternalSessionService bool
	// RequiresStatefulMCP marks an agent that needs a long-lived MCP session
	// spanning turns. That is unsupported (each turn is an independent
	// Activity process); the plugin fails the turn non-retryably. Build a
	// fresh McpToolset per turn in the factory instead.
	RequiresStatefulMCP bool
}

// AgentFactory reconstructs a named agent's native adk-go runner worker-side,
// once per turn. This is where the user writes 100% native ADK
// (gemini.NewModel, llmagent.New, tools, runner.New) and where API keys live.
// It is never serialized and never crosses the Activity boundary.
type AgentFactory func(ctx context.Context) (*AgentRunner, error)

// AgentRegistry maps agent names to factories, supporting multiple agent
// instances on one worker.
type AgentRegistry struct {
	mu        sync.RWMutex
	factories map[string]AgentFactory
}

// NewAgentRegistry returns an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{factories: make(map[string]AgentFactory)}
}

// Register adds a factory under a unique, non-empty name. It panics on an
// empty name, a nil factory, or a duplicate registration — all programmer
// errors that should fail at worker setup.
func (r *AgentRegistry) Register(name string, factory AgentFactory) {
	if name == "" {
		panic("google_adk_agents: agent name must not be empty")
	}
	if factory == nil {
		panic(fmt.Sprintf("google_adk_agents: nil factory for agent %q", name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[name]; exists {
		panic(fmt.Sprintf("google_adk_agents: agent %q already registered", name))
	}
	r.factories[name] = factory
}

// factory looks up a registered factory.
func (r *AgentRegistry) factory(name string) (AgentFactory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[name]
	return f, ok
}

// Names returns the registered agent names.
func (r *AgentRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	return names
}

// RegisterActivities registers RunTurnActivity and RunTurnStreamingActivity on
// the worker, bound to the registry and options. The activities are
// registered under ActivityNameRunTurn and ActivityNameRunTurnStreaming so the
// workflow can dispatch them by name. Panics if opts fails validation.
func RegisterActivities(w worker.Worker, reg *AgentRegistry, opts Options) {
	if err := opts.Validate(); err != nil {
		panic(err)
	}
	a := NewActivities(reg, opts)
	w.RegisterActivityWithOptions(a.RunTurnActivity, activity.RegisterOptions{Name: ActivityNameRunTurn})
	w.RegisterActivityWithOptions(a.RunTurnStreamingActivity, activity.RegisterOptions{Name: ActivityNameRunTurnStreaming})
}

// RegisterWorkflow registers AgentSessionWorkflow on the worker. Call it on
// any worker that should host agent sessions.
func RegisterWorkflow(w worker.Worker) {
	w.RegisterWorkflow(AgentSessionWorkflow)
}

// ActivityAsTool wraps an existing Temporal-activity-shaped Go function
// (func(context.Context, TArgs) (TResults, error)) as an adk-go tool, so users
// who already have @activity.defn functions can expose them to an agent
// without re-declaring them as plain callables.
//
// Because the whole agent runs inside one turn Activity, the wrapped function
// executes in-process during the turn (adk-go's ToolContext is a
// context.Context); it is not scheduled as a nested Temporal Activity. Build
// the tool inside your AgentFactory and pass it to llmagent.Config.Tools.
func ActivityAsTool[TArgs, TResults any](
	name, description string,
	fn func(ctx context.Context, args TArgs) (TResults, error),
) (tool.Tool, error) {
	if fn == nil {
		return nil, fmt.Errorf("google_adk_agents: ActivityAsTool requires a non-nil function")
	}
	return functiontool.New(
		functiontool.Config{Name: name, Description: description},
		func(tc adkagent.ToolContext, args TArgs) (TResults, error) {
			// adkagent.ToolContext embeds context.Context.
			return fn(tc, args)
		},
	)
}
