# Google ADK (adk-go) plugin for the Temporal Go SDK

Make a [Google Agent Development Kit](https://github.com/google/adk-go) agent
durable, recoverable, and replay-safe by running it under a Temporal Go worker.

You keep writing **native adk-go** — `gemini.NewModel`, `llmagent.New`, your
tools and sub-agents, `runner.New`. This plugin runs each conversational turn
inside a Temporal Activity, orchestrated by a durable workflow that owns the
conversation state, retries transient model failures, pauses for
human-in-the-loop tool confirmation, streams partial output, and
continue-as-news long conversations. No agent, tool, or model rewrite.

## Install

```bash
go get go.temporal.io/sdk/contrib/google_adk_agents
```

The module pins adk-go to a commit on `main` that exposes the deterministic
clock/UUID seams (`platform.WithTimeProvider` / `platform.WithUUIDProvider`).

## Turn-as-activity model (read this first)

The Temporal Go SDK runs workflow code on a single cooperative coroutine
dispatcher: native goroutines, channels, `time.Sleep`, and `context.Context`
network I/O are non-deterministic and **disallowed** inside a workflow. adk-go's
flow uses all of them (parallel tool execution, channel-based streaming, sleep
backoff, real network calls). The two runtimes cannot be interleaved.

So this plugin does **not** reimplement ADK's loop inside the workflow. It runs
one complete `runner.Run` turn inside a single Temporal Activity
("turn-as-activity"), and a durable `AgentSessionWorkflow` orchestrates the
turns and owns the durable session snapshot. Consequences:

- Durability and retry granularity are **per turn**, not per LLM call. A
  mid-turn failure re-runs the whole turn (re-issuing that turn's model and
  tool calls).
- Workflow replay determinism comes from Temporal recording each turn
  Activity's result in history. The determinism seams additionally give
  **reproducible ADK event IDs and timestamps across Activity retries and
  continue-as-new**, so a retried turn re-emits identical event identity.

## Hello world

A single `package main` that registers a native ADK agent, starts a worker, and
drives one durable turn. Set `GOOGLE_API_KEY` and run a Temporal dev server
(`temporal server start-dev`).

```go
package main

import (
	"context"
	"log"
	"os"

	"go.temporal.io/sdk/client"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/worker"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	agentName = "assistant"
	taskQueue = "adk-agents"
)

// newAssistant is 100% native adk-go: it builds the model, agent, and runner.
// API keys live here, in the worker process — never in workflow or Activity
// inputs. The plugin calls this once per turn from inside the turn Activity.
func newAssistant() adk.AgentFactory {
	return func(ctx context.Context) (*adk.AgentRunner, error) {
		m, err := gemini.NewModel(ctx, "gemini-2.0-flash", &genai.ClientConfig{
			APIKey:  os.Getenv("GOOGLE_API_KEY"),
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return nil, err
		}
		ag, err := llmagent.New(llmagent.Config{
			Name:        agentName,
			Description: "a helpful assistant",
			Model:       m,
		})
		if err != nil {
			return nil, err
		}
		svc := session.InMemoryService()
		r, err := runner.New(runner.Config{AppName: agentName, Agent: ag, SessionService: svc})
		if err != nil {
			return nil, err
		}
		return &adk.AgentRunner{Runner: r, SessionService: svc, AppName: agentName}, nil
	}
}

func main() {
	ctx := context.Background()

	// Pass the plugin's data converter so genai/session types round-trip
	// losslessly and stay readable in workflow history.
	c, err := client.Dial(client.Options{DataConverter: adk.NewDataConverter()})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	reg := adk.NewAgentRegistry()
	reg.Register(agentName, newAssistant())

	w := worker.New(c, taskQueue, worker.Options{})
	adk.RegisterActivities(w, reg, adk.Options{})
	adk.RegisterWorkflow(w)
	if err := w.Start(); err != nil {
		log.Fatal(err)
	}
	defer w.Stop()

	// Start a durable session and send one turn, awaiting its result.
	in := adk.NewAgentSessionInput(adk.Options{}, agentName, agentName, "user-1", "session-1")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, adk.AgentSessionWorkflow, in)
	if err != nil {
		log.Fatal(err)
	}

	handle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   run.GetID(),
		UpdateName:   adk.UpdateSendMessageAndWait,
		WaitForStage: client.WorkflowUpdateStageCompleted,
		Args: []interface{}{adk.TurnRequest{
			Message: genai.NewContentFromText("Say hello in one sentence.", genai.RoleUser),
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	var res adk.TurnResult
	if err := handle.Get(ctx, &res); err != nil {
		log.Fatal(err)
	}

	for _, ev := range res.Events {
		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" {
					log.Printf("%s: %s", ev.Author, p.Text)
				}
			}
		}
	}
}
```

The plugin is propagated to the worker automatically because the worker is built
from the same client. You drive the session from any client (CLI, HTTP handler,
another workflow) via signals, queries, and updates — see below.

## What this plugin gives you

- **Durable multi-turn sessions** — `AgentSessionWorkflow` owns the transcript;
  a worker crash mid-turn resumes the turn, not the whole conversation.
- **Drive sessions with Temporal primitives**:
  - `SignalSendMessage` (`"send_message"`) — enqueue a turn, fire-and-forget.
  - `UpdateSendMessageAndWait` (`"send_message_and_wait"`) — send a turn and
    await its `TurnResult`; the validator rejects an empty message at the
    workflow boundary.
  - `QueryConversation` (`"conversation"`) — the full `[]*session.Event`
    transcript.
- **Human-in-the-loop tool confirmation** mapped onto ADK's native
  `tool/toolconfirmation` pause/resume — see below.
- **Token streaming** (SSE) surfaced to the workflow — see below.
- **Continue-as-new** for long conversations — see below.
- **Reproducible event identity** via `WithDeterministicProviders`, installed
  inside the turn Activity and seeded from workflow-deterministic values.
- **Lossless, readable serialization** via `NewDataConverter` for `*genai.Content`
  and `[]*session.Event`.
- **Reuse existing Temporal activities as tools** with
  `ActivityAsTool[Args, Results](name, description, fn)` — wrap an
  activity-shaped function as an adk-go `tool.Tool` in your factory and pass it
  to `llmagent.Config.Tools`. (Because the agent runs inside one turn Activity,
  the wrapped function executes in-process during the turn — it is not scheduled
  as a nested Activity.)
- **Network-free testing** with `MockModel` (see [Testing](#testing)).

## Human-in-the-loop confirmation

When a tool calls `ctx.RequestConfirmation(...)`, ADK ends the turn awaiting a
user function-response. The plugin surfaces the pending request and resumes ADK's
own confirmation path when the human decides:

```go
// 1. A UI polls for what needs approval.
val, _ := c.QueryWorkflow(ctx, run.GetID(), "", adk.QueryPendingConfirmations)
var pending []adk.ConfirmationRequest
_ = val.Get(&pending) // each carries FunctionCallID, ToolName, Hint, Payload

// 2. The human approves; ADK resumes on the next turn.
h, _ := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
	WorkflowID:   run.GetID(),
	UpdateName:   adk.UpdateConfirm, // or fire-and-forget: adk.SignalConfirm
	WaitForStage: client.WorkflowUpdateStageCompleted,
	Args: []interface{}{adk.ToolConfirmationDecision{
		FunctionCallID: pending[0].FunctionCallID,
		ToolName:       pending[0].ToolName,
		Confirmed:      true,
	}},
})
var res adk.TurnResult
_ = h.Get(ctx, &res)
```

A rejected confirmation surfaces as an `ApplicationError` of type
`ADKConfirmationRejected`.

## Streaming

Set `Settings.Streaming = true` on the `AgentSessionInput` to route turns
through `RunTurnStreamingActivity`. The Activity forwards coalesced partial text
to the workflow as `StreamChunk` signals; the workflow accumulates them and
exposes them through the `QueryPendingStream` (`"pending_stream"`) query while
the turn is in flight. Tune coalescing with `Options.StreamingBatchInterval`
(default 200ms) and the signal name with `Options.StreamingSignalName`.

```go
in := adk.NewAgentSessionInput(adk.Options{}, agentName, agentName, "user-1", "session-1")
in.Settings.Streaming = true
// ... ExecuteWorkflow(in), then poll QueryPendingStream for []adk.StreamChunk.
```

Partial chunks are a live view of the in-flight turn; they reset at the start of
each streaming turn. The authoritative record is always the `TurnResult.Events`
transcript. Bidirectional/audio streaming (`Runner.RunLive`) is **not** supported
in v1.

## Continue-as-new and what state carries

`AgentSessionWorkflow` continue-as-news when the first `CANThreshold` is
crossed — `MaxTurns` (default 250) or `MaxSessionBytes` (default 2 MiB of
JSON-encoded snapshot) — and only at a clean boundary (no in-flight turn, no
pending confirmation).

**Carried across the boundary** (in `ResumeState`):

- the durable transcript (`PriorEvents`, i.e. the accumulated
  `[]*session.Event`),
- the ADK session state map (`SessionState`),
- the turn counter (`TurnCount`), so the threshold does not reset.

**NOT carried** (intentionally):

- in-flight turns — continue-as-new waits for the active turn to finish first,
- accumulated streaming `StreamChunk`s — they are a per-turn live view and are
  reset,
- any live ADK object — the next run's factory rebuilds the runner and replays
  `PriorEvents` into a fresh session.

Because each turn Activity replays `PriorEvents` into the session it builds, the
resumed run sees the full conversation. If your factory uses an **external**
session service (database/vertexai) rather than the in-memory one, the
Temporal-owned snapshot and your external store can diverge ("double
durability"); set `AgentRunner.ExternalSessionService = true` and
`Options.WarnOnExternalSessionService = true` to get a warning, and treat one
store as the source of truth.

## MCP tools

MCP toolsets are supported **natively and statefully within a turn**: build a
fresh `mcptoolset.McpToolset` inside your `AgentFactory` and pass it to
`llmagent.Config.Tools`. It connects, lists, and calls tools entirely inside the
turn Activity, so full tool schemas never cross the wire. A **long-lived MCP
session spanning turns is not supported** — each turn is an independent Activity
process. If a factory sets `AgentRunner.RequiresStatefulMCP = true`, the turn
fails non-retryably (`ADKNonRetryable`) by design.

## Error classification

Turn failures are surfaced as `temporal.ApplicationError` with a stable `Type`
you can branch on (never string-match the message):

| Type | Meaning | Retried |
| --- | --- | --- |
| `ADKAgentError` | transient model/network failure | yes (per `RetryPolicy`) |
| `ADKNonRetryable` | config/programming error (unknown agent, bad factory, unsupported feature) | no |
| `ADKConfirmationRejected` | human rejected a tool confirmation | no |

Temporal owns turn-level retry via `ActivityOptions.RetryPolicy`; ADK's own
in-call retries are left at their defaults. A turn that ultimately fails fails
the workflow with the classified type as the outermost `ApplicationError`.

## Composing with other plugins

This plugin configures the worker through the standard `RegisterActivities` /
`RegisterWorkflow` calls and a data converter you pass to the client. To compose
with another integration (for example `contrib/opentelemetry`):

- Register both sets of activities/workflows on the same worker.
- Add your interceptors via `worker.Options.Interceptors` and
  `client.Options.Interceptors` as usual.
- If you already use a custom `DataConverter`, wrap `NewDataConverter`'s payload
  behavior into yours rather than passing two converters — only one converter is
  used per client.

## Per-agent tuning

Give thinking models or long-tool agents longer timeouts than a chat agent:

```go
opts := adk.Options{
	DefaultActivityOptions: adk.ActivityOptions{StartToCloseTimeout: 2 * time.Minute},
	ActivityOptions: map[string]adk.ActivityOptions{
		"researcher": {
			StartToCloseTimeout: 15 * time.Minute,
			SummaryFunc:         func(in adk.TurnInput) string { return "research: " + in.AgentName },
		},
	},
}
```

Every turn Activity sets a `Summary` (the agent name by default, or
`SummaryFunc`'s output) so it is identifiable in the Temporal UI.

## Testing

`MockModel` is a deterministic, network-free `model.LLM` for unit-testing
workflows and factories without calling a billed model API:

```go
func newTestAgent() adk.AgentFactory {
	return func(_ context.Context) (*adk.AgentRunner, error) {
		m := adk.NewMockModel("mock", genai.NewContentFromText("hi", genai.RoleModel))
		ag, _ := llmagent.New(llmagent.Config{Name: "chat", Model: m})
		svc := session.InMemoryService()
		r, _ := runner.New(runner.Config{AppName: "chat", Agent: ag, SessionService: svc})
		return &adk.AgentRunner{Runner: r, SessionService: svc, AppName: "chat"}, nil
	}
}
```

Drive it through `testsuite.WorkflowTestSuite` (register a stub `RunTurnActivity`
under `adk.ActivityNameRunTurn`) or the turn Activity directly.

## Limitations

- Durability is **per turn**, not per LLM call (see the turn-as-activity model).
- No bidirectional/audio streaming (`Runner.RunLive`) in v1; SSE token streaming
  only.
- MCP is stateless per turn; no cross-turn MCP session.
- Sub-agents, transfers, and `AgentTool` run in-process inside the turn Activity
  (no child workflow per sub-agent in v1).
- An external ADK session service can diverge from the Temporal-owned snapshot;
  pick one source of truth.
