# Using the ADK Web Debugger with the Temporal Go Plugin

*A practical guide for developers using `go.temporal.io/sdk/contrib/googleadk` (v0.2.0+) with Google ADK Go (`google.golang.org/adk/v2`).*

---

## What the ADK web debugger is (30 seconds)

Google ADK ships a browser-based **developer UI** ([google/adk-web](https://github.com/google/adk-web)) for building and debugging agents: you chat with your agent in a web page and inspect, turn by turn, the **event stream** (every model request/response, tool call, and state change), **session state**, and **execution traces**. Google is explicit that it is a **development-time tool only** — "not meant for use in production deployments."

**Why it applies to Go, even though you may only have seen `adk web` in Python:** in Python it's a CLI command that discovers your agents. In Go, agents are compiled code, so the debugger ships as a **library you embed in your own binary**. The entire Angular UI is compiled into your executable via `go:embed` — your agent binary *becomes* the debugger:

```go
import (
    "google.golang.org/adk/v2/cmd/launcher"
    "google.golang.org/adk/v2/cmd/launcher/full"
)

l := full.NewLauncher()
err := l.Execute(ctx, config, os.Args[1:])   // config carries your agent
```

```sh
go run . web api webui        # UI at http://localhost:8080/ui/
```

No separate install, no Python, no Node — one Go binary serving the UI (`/ui/`) and the ADK REST API (`/api/`) that drives your agent in-process.

---

## The mental model: two debuggers for two layers

A Temporal-ized ADK agent has two layers, and each has its own inspection tool:

| Layer | Question it answers | Tool | When |
|---|---|---|---|
| **Agent behavior** | "Why did the model call that tool with those arguments?" | ADK web debugger | Development |
| **Durable execution** | "Which model call failed, how many times was it retried, where is the workflow paused?" | Temporal Web UI | Development *and* production |

The integration deliberately keeps your agent **native ADK** — the same `llmagent` tree runs in both layers. You debug the *agent's brain* in the ADK web UI with fast, in-process iteration; you run the *same brain* durably under Temporal, where every model call and tool becomes a retryable, visible Activity.

**One thing does not work out of the box**: pointing the web debugger *directly* at a running Temporal worker. The debugger's "chat" button drives `runner.Run` in its own process; under Temporal, the agent loop runs *inside a Workflow* on a worker. So a given turn executes in one harness or the other — **but the two compose**: Recipe 2 shows how a small proxy agent makes the web UI a live front-end where every chat turn executes durably inside a Temporal Workflow. What you must not do is hand the debugger an agent built with the Temporal seams and expect it to run in-process — that fails immediately and tells you why:

```
googleadk: run context is missing the Temporal workflow bridge; pass
googleadk.NewContext(ctx) as the context to runner.Runner.Run
```

That error is your signal that the agent was built with the Temporal seams (`googleadk.NewModel`, `ActivityAsTool`, `NewMCPToolset`) but is being run outside a workflow.

---

## Recipe 1: One agent, two harnesses (fast iteration)

*Each turn runs in exactly one harness: in-process for debugging, or durably under Temporal. For running every turn under Temporal **while** using the web UI, see Recipe 2.*

Exactly three things differ between "runs under the debugger" and "runs under Temporal" — the model, I/O tools, and MCP toolsets. Everything else (the agent tree, instructions, sub-agents, in-workflow function tools) is byte-for-byte identical. So parameterize your agent constructor on those seams:

```go
// agent.go — shared by both harnesses
func NewAssistant(m model.LLM, tools []tool.Tool, toolsets []tool.Toolset) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "a helpful assistant",
		Model:       m,
		Instruction: "Answer concisely.",
		Tools:       tools,
		Toolsets:    toolsets,
	})
}

// GetWeather is a plain Go function. Under Temporal it is registered as an
// Activity and exposed via googleadk.ActivityAsTool; under the debugger it is
// wrapped as an ordinary ADK functiontool. Same business logic in both.
func GetWeather(ctx context.Context, in GetWeatherInput) (GetWeatherOutput, error) { ... }
```

**Debug harness** (`cmd/debug/main.go`) — direct model, direct tools, embedded web UI:

```go
func main() {
	ctx := context.Background()

	// Real model, called in-process. Reads GEMINI_API_KEY / GOOGLE_API_KEY.
	m, err := gemini.NewModel(ctx, "gemini-2.0-flash", nil)
	if err != nil { log.Fatal(err) }

	// The same GetWeather function, as an in-process ADK tool.
	weather, err := functiontool.New(
		functiontool.Config{Name: "get_weather", Description: "look up weather"},
		func(tctx agent.Context, in GetWeatherInput) (GetWeatherOutput, error) {
			return GetWeather(tctx, in) // agent.Context embeds context.Context
		},
	)
	if err != nil { log.Fatal(err) }

	root, err := NewAssistant(m, []tool.Tool{weather}, nil)
	if err != nil { log.Fatal(err) }

	loader, err := agent.NewSingleLoader(root)
	if err != nil { log.Fatal(err) }

	l := full.NewLauncher()
	if err := l.Execute(ctx, &launcher.Config{
		AgentLoader:    loader,
		SessionService: session.InMemoryService(), // also the default if nil
	}, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
```

```sh
go run ./cmd/debug web api webui
# open http://localhost:8080/ui/  → pick "assistant", chat, inspect events
```

**Production harness** (workflow + worker) — the Temporal seams, exactly as in the [samples](https://github.com/temporalio/samples-go/tree/main/googleadk):

```go
// In the Workflow: Temporal-backed model + tool.
weatherTool, err := googleadk.ActivityAsTool(GetWeather, googleadk.ActivityToolOptions{
	Name: "get_weather", Description: "look up weather",
})
root, err := NewAssistant(googleadk.NewModel("gemini-2.0-flash"), []tool.Tool{weatherTool}, nil)
// ... runner.New(...), then r.Run(googleadk.NewContext(ctx), ...)

// In the worker main: the plugin registers the integration's Activities.
adkPlugin, err := googleadk.NewPlugin(googleadk.Config{ Models: ... })
w := worker.New(c, taskQueue, worker.Options{Plugins: []worker.Plugin{adkPlugin}})
```

Iterate on prompts, tool schemas, and agent structure in the web UI; the identical `NewAssistant` then runs durably in production, with the Temporal UI showing each model call as an `InvokeModel: <model>` Activity and each tool call as `<agent>: <tool>`.

### Gotchas

- **Port mismatch**: the embedded UI's backend URL defaults to `http://localhost:8080/api`. If you pass `web -port 8811`, also pass `webui -api_server_address http://localhost:8811/api`, or the browser SPA will call the wrong backend.
- **Debug tools may need Activity form in prod**: an in-process debugger tool that does I/O (network, disk, clock) must become an `ActivityAsTool` (or MCP) under Temporal — workflows only allow deterministic in-workflow tools. The constructor-parameterization makes that swap explicit.
- **MCP**: under the debugger, use `mcptoolset.New(...)` directly (live session in-process). Under Temporal, `googleadk.NewMCPToolset(...)` + `Config.MCPToolsets` on the plugin. Same `tool.Toolset` interface, same constructor slot.

---

## Recipe 2: The web UI as a live front-end for Temporal-executed agents

Yes — you can have **all agent turns execute durably under the Temporal plugin and still chat with and inspect them through the ADK web UI**. The bridge is a small *proxy agent*: the debugger thinks it's running an agent, but that agent's `Run` sends each user message to a Temporal Workflow (via an Update) and re-emits the events the durable run produced. The web UI gets its full experience — chat transcript, event stream (tool calls included), session browser — while every turn is a real workflow execution with Activities, retries, and history in the Temporal UI.

Two pieces, ~100 lines total:

**Workflow side** — have the chat Workflow's Update handler return the turn's events (the chat sample's `send-message` handler already iterates them; return them instead of just the final text). `[]*session.Event` round-trips through Temporal's JSON converter — that's the same property `SessionSnapshot` relies on:

```go
workflow.SetUpdateHandler(ctx, "send-message",
	func(ctx workflow.Context, msg string) ([]*session.Event, error) {
		var turn []*session.Event
		content := genai.NewContentFromText(msg, genai.RoleUser)
		for ev, err := range r.Run(adkCtx, UserID, SessionID, content, agent.RunConfig{}) {
			if err != nil { return nil, err }
			if ev != nil && !ev.Partial { turn = append(turn, ev) }
		}
		return turn, nil
	})
```

**Debugger side** — a proxy agent built with `agent.New` (the sanctioned way to write a custom agent; the `Agent` interface itself is sealed). One Workflow per debugger session, started lazily on the first turn via Update-with-Start:

```go
proxy, err := agent.New(agent.Config{
	Name:        "assistant", // doubles as the app name in the UI
	Description: "Live view: every turn executes inside a Temporal Workflow",
	Run: func(ictx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			var msg string
			if uc := ictx.UserContent(); uc != nil {
				for _, p := range uc.Parts { msg += p.Text }
			}
			startOp := client.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
				ID:                       "adk-debug-" + ictx.Session().ID(),
				TaskQueue:                chat.TaskQueue,
				WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
			}, chat.ChatWorkflow, chat.ChatInput{})
			handle, err := temporalClient.UpdateWithStartWorkflow(ictx, client.UpdateWithStartWorkflowOptions{
				StartWorkflowOperation: startOp,
				UpdateOptions: client.UpdateWorkflowOptions{
					UpdateName:   "send-message",
					Args:         []any{msg},
					WaitForStage: client.WorkflowUpdateStageCompleted,
				},
			})
			if err != nil { yield(nil, err); return }
			var turn []*session.Event
			if err := handle.Get(ictx, &turn); err != nil { yield(nil, err); return }
			for _, ev := range turn {
				// Re-stamp into this debugger invocation so the UI stores/renders it.
				out := session.NewEvent(ictx, ictx.InvocationID())
				out.Author, out.Branch = ev.Author, ictx.Branch()
				out.LLMResponse, out.Actions = ev.LLMResponse, ev.Actions
				if !yield(out, nil) { return }
			}
		}
	},
})
// then: agent.NewSingleLoader(proxy) → launcher.Config → full.NewLauncher, as in Recipe 1
```

This shape is verified end-to-end (proxy agent → events rendered in chat, persisted for the session browser, streamed per-event over SSE); the ADK runner automatically persists every non-partial event the proxy yields.

### What you get, and the honest limits

- ✅ Chat drives real durable executions; kill the worker mid-turn and the turn *survives* — the durable side retries and the UI gets the answer when it completes. Simultaneously watch the same turn as Activities in the Temporal UI.
- ✅ Event stream and session browser show the durable run's real events (tool calls, function responses, text).
- ⚠️ **Trace/debug tab stays empty** for these turns — it only captures OTel spans from the debugger's own process; the model calls happened on the worker. Use the Temporal UI (Activity inputs/outputs are the full LLM requests/responses) or worker-side tracing for that layer.
- ⚠️ Events arrive at end-of-turn with this simple shape. For live tokens, combine with Recipe 4: yield `Partial: true` events from a `workflowstreams` subscription while the Update is in flight (partials stream to the browser but skip persistence — exactly what you want).
- ⚠️ Long durable turns: the SSE write timeout defaults to 120s (`api -sse-write-timeout`) — raise it if your workflows think longer.
- Gotchas from verification: build events with `session.NewEvent(ictx, ictx.InvocationID())` (fills ID/timestamp); set `Author` explicitly on function-response events (their `role:"user"` content otherwise gets attributed to the human); the proxy's agent name can't be `"user"`; sessions of record live in the workflow — the debugger's session store is just the view.

---

## Recipe 3: Browse *production* conversations in the web UI

The debugger's session browser reads whatever `session.Service` you hand it. The integration's `SessionSnapshot` (from `googleadk.ExportSession`, the same type used for continue-as-new) is plain JSON, and `googleadk.ImportSession` works in **any** process — so you can pull a conversation out of a running workflow and inspect it turn-by-turn in the web UI:

```go
// 1. In the Workflow: expose the session via a Query handler.
workflow.SetQueryHandler(ctx, "session-snapshot", func() (*googleadk.SessionSnapshot, error) {
	return googleadk.ExportSession(adkCtx, svc, appName, userID, sessionID)
})

// 2. In your debug binary, before starting the launcher: fetch and import.
var snap googleadk.SessionSnapshot
resp, _ := temporalClient.QueryWorkflow(ctx, workflowID, "", "session-snapshot")
_ = resp.Get(&snap)

svc := session.InMemoryService()
_, _ = googleadk.ImportSession(ctx, svc, &snap)   // no workflow needed here

// 3. Hand that same svc to launcher.Config.SessionService — the conversation
//    appears in the web UI's session browser (app/user/session IDs preserved).
```

Notes:
- One-way by design (prod → debugger). Don't push debugger sessions back into workflows.
- Snapshots carry session-scoped state and events only; `app:`/`user:`-scoped state is deliberately excluded.
- This gives you *conversation* inspection. Execution traces in the debug tab only exist for runs the debugger executed itself — production spans go to whatever OTel setup your worker has, never to the web UI.

---

## Recipe 4 (advanced): live token streams from production

If the workflow enables streaming (`googleadk.NewModel(name, googleadk.WithStreaming(topic, 0))` + `googleadk.StreamServer(ctx)`), every model chunk is published from the `InvokeModel` Activity to a `workflowstreams` topic. Any process with a Temporal client can tail it:

```go
sub, _ := workflowstreams.NewClient(c, workflowID, workflowstreams.Options{}).
	Subscribe(ctx, workflowstreams.SubscribeOptions{Topics: []string{"chat"}})
for item := range sub.Items() { /* decode model.LLMResponse chunk, print/render */ }
```

That's exactly-once, ordered, and follows continue-as-new — good enough to build a live "watch this production agent think" pane. There's no ready-made adapter feeding this into the ADK web UI today; treat it as CLI/custom-dashboard material.

---

## Quick reference: what works where

| Capability | ADK web UI (Go) | Notes |
|---|---|---|
| Chat with agent, event stream, state inspection | ✅ | Drives `runner.Run` in the debugger's own process |
| Session browsing | ✅ | Reads `launcher.Config.SessionService` — importable from prod (Recipe 3) |
| Trace/graph debug tab | ✅ for debugger-run turns | In-process OTel capture only; prod spans never appear here |
| Eval tab | ❌ | Not implemented in ADK Go (endpoints return 501) |
| Editing session state via UI (PATCH) | ❌ | Not implemented in the Go REST server |
| Chatting through the UI with Temporal-executed turns | ✅ via a proxy agent (Recipe 2) | ~100-line shim; every turn is a durable workflow execution |
| Pointing the UI at a worker with zero code | ❌ | The bridge is the Recipe 2 proxy, not a config flag |
| Production use | ❌ | Google: dev/debug only. Production observability = Temporal UI + your OTel |

**Version notes:** ADK Go v2 (`google.golang.org/adk/v2`, Go 1.25+) provides the launcher/web packages; `contrib/googleadk` v0.2.0 provides `NewPlugin`/`ExportSession`/`ImportSession`. The `full` launcher also gives you a terminal REPL (`go run . console`) if you want agent iteration without a browser.
