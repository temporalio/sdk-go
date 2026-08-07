# Google ADK agents — Temporal integration (Go)

Make a [Google ADK](https://google.github.io/adk-docs/) (`adk-go`) agent **durable
and replay-safe under Temporal** without rewriting the agent. The agent's
orchestration loop runs inside a Temporal Workflow; each **LLM call** becomes a
durable Temporal Activity, and any **tool** that does I/O opts into an Activity
too — so calls are retried, timed-out, visible in the Temporal UI, and replayable.

You keep building agents the native ADK way — `llmagent.New(...)` with a
`model.LLM`, `tool.Tool`s / `tool.Toolset`s and `SubAgents`, wrapped in
`runner.New(...)` and driven by `r.Run(...)`. You change two things:

1. Use `googleadk.NewModel("<model-name>")` as your agent's `Model`. It is a
   `model.LLM` whose calls are dispatched to the `InvokeModel` Activity; the real
   model is reconstructed **worker-side** (never in the workflow).
2. Pass the bridged context from `googleadk.NewContext(workflowCtx)` to `r.Run`,
   which installs Temporal-deterministic time / UUID / task-fan-out providers.

Tools run **in-workflow by default** — the idiomatic Temporal model: your workflow
is deterministic, and anything that touches the network, clock, or disk goes
through an Activity. Opt a tool into an Activity with `googleadk.ActivityAsTool`,
or use `googleadk.NewMCPToolset` for MCP. The real model and any activity/MCP tool
handlers live worker-side in the registry declared by `googleadk.Config` and
wired onto the worker by `googleadk.NewPlugin(...)`.

## Add to your project

From your application's Go module, run:

```sh
go get go.temporal.io/sdk/contrib/googleadk@latest
```

```go
import "go.temporal.io/sdk/contrib/googleadk"
```

This package depends on the deterministic ADK `platform` seams
(`WithTimeProvider`, `WithUUIDProvider`, `WithTaskRunner`), `tool/toolutils.PackTool`,
and the `model.NewLLM` registry lookup from upstream `google.golang.org/adk/v2`
(the registry itself stays application-owned; this package never registers into it).
These merged after the latest tagged ADK release (v2.0.0), so `go.mod` pins
`adk/v2` to a `main`-branch pseudo-version for now; it reverts to an ordinary
tagged version once a release ships that includes them.

## Module versioning

The Google ADK integration is released as a separate Go module from the core
Temporal Go SDK. See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Samples

Runnable end-to-end samples live in
[temporalio/samples-go](https://github.com/temporalio/samples-go/tree/main/googleadk).

## Hello world

Two halves: the **worker** registers the real model handler as an Activity; the
**workflow** builds a vanilla ADK agent and drives it.

```go
package main

import (
	"context"
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"go.temporal.io/sdk/contrib/googleadk"
)

const taskQueue = "adk"

// AgentWorkflow runs a native ADK agent. The model call inside r.Run is
// dispatched to a Temporal Activity by googleadk.NewModel.
func AgentWorkflow(ctx workflow.Context, question string) (string, error) {
	// The model is a TemporalModel: in-workflow it only carries the model name;
	// the real gemini model is reconstructed worker-side by the ModelFactory.
	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "a helpful assistant",
		Model:       googleadk.NewModel("gemini-2.0-flash"),
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
	})
	if err != nil {
		return "", err
	}

	// NewContext bridges the workflow.Context into the context ADK reads its
	// determinism/executor seams from. Pass it straight to Run.
	adkCtx := googleadk.NewContext(ctx)
	msg := genai.NewContentFromText(question, genai.RoleUser)

	var answer string
	for ev, err := range r.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if err != nil {
			return "", err
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

	// Worker-side registry, wired as a worker plugin: the real model lives here.
	// API keys are captured in the factory closure and never cross the Activity
	// boundary. The plugin registers the Activities at worker start and closes
	// cached MCP toolsets at worker stop.
	adkPlugin, err := googleadk.NewPlugin(googleadk.Config{
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

	w := worker.New(c, taskQueue, worker.Options{Plugins: []worker.Plugin{adkPlugin}})
	w.RegisterWorkflow(AgentWorkflow)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatal(err)
	}
}
```

`Config.Models` is optional: when a model name is absent, `InvokeModel` falls
back to ADK's model registry (`model.NewLLM`), which stays application-owned —
this package never registers into it, so your own `model.Register` calls are
honored — and `gemini-*` names the registry does not know resolve via a
built-in zero-config Gemini fallback (a `nil` config reads `GEMINI_API_KEY` /
`GOOGLE_API_KEY` worker-side). Supply a factory to inject credentials, disable
the model SDK's own retries (see below), or override the fallbacks.

## What you get

- **Every LLM turn is a durable Activity.** `NewModel(name)` returns a `model.LLM`
  that dispatches to the `InvokeModel` Activity. The workflow only ships the model
  **name**; the Activity reconstructs the model from your `ModelFactory`.
- **Deterministic tools, in-workflow by default.** Ordinary `functiontool.New(...)`
  tools run on Temporal's deterministic dispatcher inside the workflow — no
  Activity overhead, and their session-state mutations (`ctx.State().Set`,
  `ctx.Actions()`) propagate normally.
- **Opt a tool into an Activity when it does I/O.** `ActivityAsTool(myActivity,
  ...)` exposes an existing `func(context.Context, TArgs) (TResults, error)`
  Temporal activity to the agent as a tool (parameter schema inferred from
  `TArgs`); its `Run` dispatches the activity. Register the same activity on the
  worker as usual.
- **MCP, statelessly.** `NewMCPToolset(...)` is a workflow-side proxy: it lists
  remote tools (full declarations, including parameters) via `ListMcpTools` and
  executes calls via `CallMcpTool`. The live, stateful `mcptoolset.New(...)` runs
  worker-side, never in the workflow. Your `MCPFactory` runs at most once per
  toolset name — the toolset is cached and shared across calls — and the plugin
  closes any cached toolset that implements `Close() error` at worker stop
  automatically; with manual wiring, call `Activities.Close` yourself after
  `worker.Run` returns.
- **Deterministic by construction.** `NewContext` binds ADK's `platform.Now` to
  `workflow.Now`, `platform.NewUUID` to a deterministic seeded generator, and
  `platform.RunTasks` to a `workflow.Go` fan-out, so the agent loop replays
  deterministically. Concurrent tool fan-out is on by default; use
  `NewContext(ctx, googleadk.WithSequentialToolFanout())` for a serial fallback.
- **Typed failures.** Model / tool / MCP failures surface as Temporal
  `ApplicationError`s tagged `googleadk.ModelError` / `.ToolError` / `.McpError`.
  Classify with `IsNonRetryable(err)`; never string-match. Upstream HTTP status
  drives retryability (`408`/`409`/`429`/`5xx` retryable, other `4xx` not).
- **Disable model-SDK retries in your `ModelFactory`.** `InvokeModel` already runs
  under Temporal's `RetryPolicy`; leaving the model client's own retries on retries
  a transient failure twice over. Let Temporal own retries.
- **Test without a live LLM.** `testing.go` ships `FakeModel`, `FakeMCPServer`, and
  `TextResponse` / `FunctionCallResponse` so you can unit-test workflows with no
  network. The test environments do not run plugins; register the Activities
  directly with `NewActivities` + `Register` (as this repo's own tests do).

> **Determinism note.** Because plain tools run in-workflow, their code must be
> deterministic and replay-safe — no direct network, clock, randomness, or
> goroutines. Anything that isn't belongs in an `ActivityAsTool` (or an MCP tool),
> where it runs worker-side under Temporal's retry/timeout policy.

## Human-in-the-loop (HITL) tool confirmation

A tool that needs approval calls ADK's `ctx.RequestConfirmation(hint, payload)`.
ADK records the request and emits a function call named `adk_request_confirmation`,
ending the turn. Because the tool runs in-workflow, the request lands in the
workflow's own event actions. MCP tools participate too: a confirmation the
worker-side tool requests (e.g. via `mcptoolset`'s `RequireConfirmation` option)
is tunneled back across the Activity boundary and re-recorded workflow-side, so
the agent pauses the same way. Drive it from your workflow like this:

```go
for {
	var events []*session.Event
	for ev, err := range r.Run(adkCtx, userID, sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			return err
		}
		events = append(events, ev)
	}

	pending := googleadk.PendingConfirmations(events)
	if len(pending) == 0 {
		break // done
	}

	// Ask the human. Deliver decisions via a Temporal signal or update.
	var decisions []googleadk.ConfirmationDecision
	for _, p := range pending {
		var d googleadk.ConfirmationDecision // {FunctionCallID, Confirmed}
		// e.g. workflow.GetSignalChannel(ctx, googleadk.ConfirmationSignalName).Receive(ctx, &d)
		decisions = append(decisions, d)
	}

	// Resume: re-run with the confirmation responses.
	msg = googleadk.ConfirmationResponse(decisions...)
}
```

`PendingConfirmations` exposes each pending call's `OriginalCall` and `Hint` for
your UI; `ConfirmationResponse` builds the resume message ADK expects.

## Continue-as-new (long conversations)

A conversation's history lives in the ADK session. To keep a workflow's history
bounded, snapshot the session and continue-as-new:

```go
if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
	snap, err := googleadk.ExportSession(adkCtx, svc, appName, userID, sessionID)
	if err != nil {
		return err
	}
	return workflow.NewContinueAsNewError(ctx, AgentWorkflow, snap /* + next input */)
}
```

On the next run, rebuild the session before driving the agent:

```go
svc := session.InMemoryService()
if snap != nil {
	if _, err := googleadk.ImportSession(adkCtx, svc, snap); err != nil {
		return err
	}
}
```

`SessionSnapshot` is JSON-serializable (session-scoped state + full event
history); every value in session state and every tool result must be
JSON-encodable. App/user-scoped state (managed across sessions by the session
service) is not carried — use a durable session service for that.

## Streaming

`NewModel(name, googleadk.WithStreaming(topic, 0))` drives the model in streaming
mode: the `InvokeModel` Activity calls the model with `stream=true`, heartbeats,
and **publishes each chunk** to a per-run
[`workflowstreams`](https://pkg.go.dev/go.temporal.io/sdk/contrib/workflowstreams)
topic for external (UI) consumers, then returns the aggregated final response into
the workflow so replay stays deterministic.

Call `googleadk.StreamServer(ctx)` once near the top of the workflow that drives
`r.Run`, and set `agent.RunConfig{StreamingMode: agent.StreamingModeSSE}`:

```go
func StreamingAgentWorkflow(ctx workflow.Context, q string) (string, error) {
	if err := googleadk.StreamServer(ctx); err != nil { // required when streaming
		return "", err
	}
	topic := "run-" + workflow.GetInfo(ctx).WorkflowExecution.ID
	root, _ := llmagent.New(llmagent.Config{
		Model: googleadk.NewModel("gemini-2.0-flash", googleadk.WithStreaming(topic, 0)),
		// ...
	})
	// ... build runner, set agent.RunConfig{StreamingMode: agent.StreamingModeSSE}, drive r.Run as above
}
```

External consumers read chunks with `workflowstreams.NewClient(c, workflowID, ...).Subscribe(...)`.
The bidirectional `RunLive` path (hard-coded goroutines/channels) is **not** supported.

## Composing with other plugins

This integration's plugin only registers its Activities at worker start and
closes cached MCP toolsets at worker stop — no interceptors, no data converter —
so it composes with other entries in `worker.Options.Plugins` (e.g. interceptor-
or converter-based plugins like `sdk-go/contrib/opentelemetry`) without conflict.
On the ADK side, add other ADK plugins to `runner.PluginConfig.Plugins` as usual.
ADK emits its own OpenTelemetry spans; register your tracing interceptor on the
worker as usual.

## Supported & not-yet-supported

- **Supported:** single- and multi-agent (`SubAgents`) trees, in-workflow function
  tools, `ActivityAsTool`, stateless MCP, Gemini built-in tools (executed
  server-side inside `InvokeModel`), HITL tool confirmation, continue-as-new state
  carry, the in-memory session service, and SSE streaming.
- **Not yet:** `RunLive` (bidirectional streaming), sub-agent-as-child-workflow,
  live memory/artifact tools that require in-workflow network I/O, and DB/Vertex
  session services. These raise or are documented rather than silently degrading.
