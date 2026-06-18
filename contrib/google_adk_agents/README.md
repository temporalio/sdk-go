# Google ADK agents — Temporal plugin (Go)

Make a [Google ADK](https://google.github.io/adk-docs/) (`adk-go`) agent **durable
and replay-safe under Temporal** without rewriting the agent. Every LLM call and
every tool call becomes a Temporal Activity (retried, timed-out, visible in the
Temporal UI, replayable), while the agent's orchestration loop runs inside a
Temporal Workflow.

You keep building agents the native ADK way — `llmagent.New(...)` with a
`model.LLM`, `tool.Tool`s / `tool.Toolset`s and `SubAgents`, wrapped in
`runner.New(...)` and driven by `r.Run(...)`. You add exactly two things:

1. the ADK `*plugin.Plugin` from `googleadk.Plugin(...)` on your `runner.Config`; and
2. the bridged context from `googleadk.NewContext(workflowCtx)` passed to `r.Run`.

The real model and tool handlers run **worker-side**, behind the Activity
boundary, from a registry you build with `googleadk.NewActivities(...)`.

## Install

```go
import googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
```

This package depends on the deterministic ADK `platform` seams
(`WithTimeProvider`, `WithUUIDProvider`, `WithTaskRunner`) that currently live
only on the `add-platform-task-runner-seam` branch of
`github.com/DABH/adk-go`. The module's `go.mod` pins them with a `replace`
directive:

```
replace google.golang.org/adk => github.com/DABH/adk-go <pseudo-version>
```

## Hello world

Two halves: the **worker** registers the real model/tool handlers as Activities;
the **workflow** builds a vanilla ADK agent and drives it through the plugin.
Note the plugin is passed on **one side only** — the Temporal SDK propagates a
client plugin to every worker built from that client, so do not also pass it to
`Worker(...)`.

```go
package main

import (
	"context"
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

const taskQueue = "adk"

// AgentWorkflow runs a native ADK agent. Every model/tool call inside r.Run is
// short-circuited into a Temporal Activity by the plugin.
func AgentWorkflow(ctx workflow.Context, question string) (string, error) {
	pl, err := googleadk.Plugin(googleadk.Options{TaskQueue: taskQueue})
	if err != nil {
		return "", err
	}

	// Build the agent the ordinary ADK way. In-workflow the model only needs to
	// report the model name: the plugin intercepts the call and the real gemini
	// model is reconstructed worker-side by the ModelFactory (see main).
	// NewFakeModel().WithName is a lightweight, deterministic name-carrier for the
	// workflow side, so no real model client is constructed inside the workflow.
	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "a helpful assistant",
		Model:       googleadk.NewFakeModel().WithName("gemini-2.0-flash"),
		Instruction: "Answer concisely.",
	})
	if err != nil {
		return "", err
	}

	r, err := runner.New(runner.Config{
		AppName:           "hello",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
		PluginConfig:      runner.PluginConfig{Plugins: []*plugin.Plugin{pl}},
	})
	if err != nil {
		return "", err
	}

	// NewContext bridges the workflow.Context into the context ADK reads its
	// determinism/executor seams from. Pass it straight to Run.
	adkCtx := googleadk.NewContext(ctx)
	msg := genai.NewContentFromText(question, genai.RoleUser)

	var answer string
	for ev, rerr := range r.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return "", rerr
		}
		if ev != nil && ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p != nil && p.Text != "" {
					answer = p.Text
				}
			}
		}
	}
	return answer, nil
}

func main() {
	c, err := client.Dial(client.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(AgentWorkflow)

	// Worker-side registry: the real model lives here. API keys are captured in
	// the factory closure and never cross the Activity boundary.
	acts, err := googleadk.NewActivities(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"gemini-2.0-flash": func(ctx context.Context, name string) (model.LLM, error) {
				// nil config reads GEMINI_API_KEY / GOOGLE_API_KEY from the env, worker-side.
				return gemini.NewModel(ctx, name, nil)
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	acts.Register(w)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatal(err)
	}
}
```

## What this plugin gives you

- **Every LLM turn is a durable Activity.** `Plugin(...)` installs ADK's
  `BeforeModelCallback`, which short-circuits the in-workflow model call into the
  `InvokeModel` Activity. The workflow only ever ships a model **name**; the
  Activity reconstructs the model from your `ModelFactory`. Underlying-SDK retries
  should be disabled in the factory so Temporal's retry policy is the single
  source of truth.
- **Every tool call is a durable Activity.** `BeforeToolCallback` routes
  `functiontool.New(...)` tools through `CallTool` and MCP tools through
  `CallMcpTool`. ADK's pure control tools (`transfer_to_agent`, `exit_loop`) run
  in-workflow so their action mutations are not lost across the boundary.
- **Deterministic by construction.** `NewContext` binds ADK's `platform.Now` to
  `workflow.Now`, `platform.NewUUID` to a deterministic seeded generator, and
  `platform.RunTasks` to a `workflow.Go` fan-out — so the agent loop replays
  deterministically. No determinism wiring leaks into your agent code.
- **Concurrent tool fan-out.** When one LLM turn emits several tool calls they run
  as concurrent durable Activities by default. Use
  `NewContext(ctx, googleadk.WithSequentialToolFanout())` for the fully
  determinism-safe serial fallback.
- **MCP, statelessly.** `NewMCPToolset(...)` is a workflow-side proxy: it lists
  remote tools (full declarations, **including parameters**) via `ListMcpTools`
  and executes calls via `CallMcpTool`. The live, stateful `mcptoolset.New(...)`
  runs worker-side, never in the workflow.
- **Your existing Activities as tools.** `ActivityAsTool(myActivity, ...)` exposes
  a `func(context.Context, TArgs) (TResults, error)` Temporal activity to the agent
  as a tool, with the parameter schema inferred from `TArgs` — no re-declaring.
- **Typed failures.** Model/tool/MCP failures surface as Temporal
  `ApplicationError`s tagged `google_adk_agents.ModelError` /`.ToolError` /
  `.McpError`. Classify them with `IsNonRetryable(err)`; never string-match.
  Upstream HTTP status drives retryability (`429`/`5xx` retryable, other `4xx`
  not).
- **Per-model timeouts and UI summaries.** `Options.PerModelTimeouts` gives
  thinking models a longer budget; every model/tool Activity carries a `summary`
  for the Temporal UI (`Options.SummaryFn`, default = the ADK agent name).
- **Test without a live LLM.** `testing.go` ships `FakeModel`, `FakeMCPServer`,
  and `TextResponse` / `FunctionCallResponse` so you can unit-test your workflows
  through the plugin with no network.

## Streaming

Set `Options.StreamingTopic` to drive the model in streaming mode: the
`InvokeModel` Activity calls the model with `stream=true`, heartbeats, and
**publishes each chunk** to a per-run topic for external (UI) consumers via
[`workflowstreams`](https://pkg.go.dev/go.temporal.io/sdk/contrib/workflowstreams),
then returns the aggregated final response into the workflow so replay stays
deterministic.

Streaming has one extra workflow-side requirement: call `googleadk.StreamServer(ctx)`
once near the top of the workflow that drives `r.Run`, so the published chunks
have somewhere to land:

```go
func StreamingAgentWorkflow(ctx workflow.Context, q string) (string, error) {
	if err := googleadk.StreamServer(ctx); err != nil { // required when streaming
		return "", err
	}
	pl, _ := googleadk.Plugin(googleadk.Options{TaskQueue: taskQueue, StreamingTopic: "run-" + workflow.GetInfo(ctx).WorkflowExecution.ID})
	// ... build agent + runner, set agent.RunConfig{StreamingMode: agent.StreamingModeSSE}, drive r.Run as above
}
```

External consumers read chunks with `workflowstreams.NewClient(c, workflowID, ...).Subscribe(...)`.
The bidirectional `RunLive` path (hard-coded goroutines/channels) is **not**
supported.

## Composing with other plugins

`googleadk.Plugin(...)` is an ADK `*plugin.Plugin`; add other ADK plugins to the
same `runner.PluginConfig.Plugins` slice. On the Temporal side, the model/tool
Activities use the default JSON data converter and ship no client/worker
interceptor, so this plugin composes with Temporal interceptor- or
converter-based plugins (e.g. `sdk-go/contrib/opentelemetry`) without conflict.
ADK emits its own OpenTelemetry spans; register your tracing interceptor on the
worker as usual.

## Determinism & limits

- **Supported:** single- and multi-agent (`SubAgents`) trees, `functiontool`,
  stateless MCP, Gemini built-in tools (executed server-side inside `InvokeModel`),
  pure control tools, the in-memory session service, and SSE streaming.
- **Not in v1:** `RunLive` (bidi), agent-as-tool / sub-agent-as-child-workflow,
  live memory/artifact tools, HITL tool confirmation, continue-as-new state carry,
  and DB/Vertex session services (network I/O in-workflow). These raise or are
  documented rather than silently degrading.
- The state map handed to a tool Activity is an **immutable view**; mutations
  inside an Activity do not propagate back into the workflow.
