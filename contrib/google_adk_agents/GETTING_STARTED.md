# Getting started

Run a Google ADK agent durably on Temporal with the `google_adk_agents` plugin.

> **ADK v2.** This branch targets **ADK-Go v2** (`google.golang.org/adk/v2`).
> For ADK v1.x, use the `add-google-adk-agents-contrib` branch instead.

This is a **branch preview** — the plugin lives on the
**`add-google-adk-agents-contrib-v2`** branch of `temporalio/sdk-go` and is not
yet a tagged release, so consuming it needs one `replace` pointing at your local
checkout.

Its dependencies all resolve normally: `go.temporal.io/sdk` v1.45.0,
`go.temporal.io/sdk/contrib/workflowstreams` v0.1.1, and
`google.golang.org/adk/v2`. The plugin consumes a few ADK seams (the
`platform.TaskRunner` seam, `tool/toolutils.PackTool`, and the
`model.Register`/`NewLLM` registry) that merged to upstream `google/adk-go` after
its v2.0.0 release, so `go.mod` pins `adk/v2` to a `main`-branch pseudo-version
until a release ships that includes them. No adk-go fork or checkout is required.

## Prerequisites

- Go 1.25+
- The `temporal` CLI for a local dev server (`brew install temporal`)
- A Gemini API key: <https://aistudio.google.com/apikey>

## 1. Get the plugin source

```bash
git clone -b add-google-adk-agents-contrib-v2 https://github.com/temporalio/sdk-go.git
```

The plugin's `go.mod` already pins `google.golang.org/adk/v2` to the upstream
`main`-branch commit that carries the seams it needs, so `go build` / `go run`
fetch it automatically — no adk-go checkout required.

## 2a. Fastest path: run the bundled example

```bash
temporal server start-dev                                   # terminal 1
export GEMINI_API_KEY=...                                   # terminal 2
cd sdk-go/contrib/google_adk_agents/examples/weatheragent
go run .
```

The agent answers a weather question after calling a `get_weather` tool — the model
call and the tool call each run as a Temporal Activity, with the ADK runner loop
executing durably inside the workflow.

## 2b. Use the plugin in your own application

Add to your application's `go.mod`:

```go
require go.temporal.io/sdk/contrib/google_adk_agents v0.0.0-00010101000000-000000000000

// The plugin is still on a branch (no release tag yet): point it at your local
// sdk-go checkout from step 1.
replace go.temporal.io/sdk/contrib/google_adk_agents => /path/to/sdk-go/contrib/google_adk_agents
```

Then run `go mod tidy`. `google.golang.org/adk/v2` resolves normally from the
plugin's pinned `main`-branch pseudo-version — no adk-go replace needed.

Wire the plugin the way [`examples/weatheragent/main.go`](examples/weatheragent/main.go)
does:

1. Build your agent the normal ADK way — `llmagent.New(...)` with your tools.
2. Add the plugin to your runner: `googleadk.Plugin(googleadk.Options{...})` in
   `runner.Config.PluginConfig.Plugins`.
3. Pass the bridged context to the runner: `r.Run(googleadk.NewContext(ctx), ...)`.
4. On the worker, register `googleadk.NewActivities(googleadk.Config{Models: ..., Tools: ...})`,
   where your real model (with credentials) and tool handlers live — these run
   worker-side, behind the Activity boundary. `Config.Models` is optional for Gemini
   (the plugin registers a `gemini-*` factory with the adk model registry on setup);
   disable the model SDK's own retries in any `ModelFactory` so Temporal owns retries.

## 3. Run

```bash
temporal server start-dev
export GEMINI_API_KEY=...
go run .
```

## When this gets simpler

The remaining `replace` exists only because the plugin is still on a branch. Once
it is tagged in `temporalio/sdk-go` — and the `adk/v2` pin moves from a
`main`-branch pseudo-version to a tagged release that includes the seams —
consuming it is a plain `go get go.temporal.io/sdk/contrib/google_adk_agents`.
