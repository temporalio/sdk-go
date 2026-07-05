# Getting started

Run a Google ADK agent durably on Temporal with the `google_adk_agents` plugin.

> **ADK v2 preview.** This branch targets **ADK-Go v2** (`google.golang.org/adk/v2`).
> For ADK v1.x, use the `add-google-adk-agents-contrib` branch instead.

This is a **preview from branches**, so the setup uses local checkouts plus `replace`
directives:

- the plugin lives on the **`add-google-adk-agents-contrib-v2`** branch of
  `temporalio/sdk-go` (not yet a tagged release);
- it depends on the **`temporal-integration-v2`** branch of
  `github.com/DABH/adk-go` — an adk-go **v2** fork carrying a few not-yet-upstreamed
  seams (the `platform.TaskRunner` seam, `tool/toolutils.PackTool`, and the
  `model.Register`/`NewLLM` registry).

Everything else is a normal released dependency: `go.temporal.io/sdk` v1.45.0 and
`go.temporal.io/sdk/contrib/workflowstreams` v0.1.1.

> **Why a local checkout of adk-go (not a pinned version)?** The fork keeps the
> upstream `google.golang.org/adk/v2` module path, and Go cannot pull a `/v2` module
> from a fork via a pseudo-version. So the seams are consumed through a **local
> `replace`** until they land on upstream `google/adk-go`, at which point the replace
> disappears entirely.

## Prerequisites

- Go 1.25+
- The `temporal` CLI for a local dev server (`brew install temporal`)
- A Gemini API key: <https://aistudio.google.com/apikey>

## 1. Get the sources (as siblings)

```bash
git clone -b add-google-adk-agents-contrib-v2 https://github.com/temporalio/sdk-go.git
git clone -b temporal-integration-v2 https://github.com/DABH/adk-go.git
```

Clone them side by side: the plugin's `replace google.golang.org/adk/v2 => ../../../adk-go`
expects `adk-go/` as a sibling of `sdk-go/`.

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

// adk-go's v2 seams aren't upstream yet, and a /v2 fork can't be pinned by
// pseudo-version — point this at your local adk-go checkout from step 1.
replace google.golang.org/adk/v2 => /path/to/adk-go
```

Then run `go mod tidy`.

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

The `replace`s exist only because this is a branch preview. Once the adk-go v2 seams
land on upstream `google/adk-go` (and a release is cut) and the plugin is tagged in
`temporalio/sdk-go`, the replaces disappear and consuming it is a plain
`go get go.temporal.io/sdk/contrib/google_adk_agents` against `google.golang.org/adk/v2`.
