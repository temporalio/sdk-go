# Getting started

Run a Google ADK agent durably on Temporal with the `google_adk_agents` plugin.

This is a **preview from branches**, so the setup needs two `replace` directives:

- the plugin lives on the **`add-google-adk-agents-contrib`** branch of
  `temporalio/sdk-go` (not yet a tagged release);
- it depends on the **`temporal-integration`** branch of
  `github.com/DABH/adk-go` — an adk-go fork with a few not-yet-upstreamed seams —
  pulled in by a single `replace`.

Everything else is a normal released dependency: `go.temporal.io/sdk` v1.45.0 (which
ships the workflow-streams namespace fix the plugin needs) and
`go.temporal.io/sdk/contrib/workflowstreams` v0.1.1.

## Prerequisites

- Go 1.25+
- The `temporal` CLI for a local dev server (`brew install temporal`)
- A Gemini API key: <https://aistudio.google.com/apikey>

## 1. Get the plugin source

```bash
git clone -b add-google-adk-agents-contrib https://github.com/temporalio/sdk-go.git
```

(A direct `go get` of the module at the branch does not resolve cleanly — it's a
sub-module of a multi-module repo with no release tag yet — so use a local checkout
plus a `replace`, below.)

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

// adk-go's seams aren't upstream yet, so pull them from the fork. This is the only
// non-local replace you need — go.temporal.io/sdk (v1.45.0) and workflowstreams
// (v0.1.1) resolve as normal released modules.
replace google.golang.org/adk => github.com/DABH/adk-go v1.4.1-0.20260618195158-4a566e4ba2a0
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
   worker-side, behind the Activity boundary. `Config.Models` is optional for
   providers the adk model registry already knows (e.g. gemini); disable the model
   SDK's own retries in any `ModelFactory` so Temporal owns retries.

## 3. Run

```bash
temporal server start-dev
export GEMINI_API_KEY=...
go run .
```

## When this gets simpler

The two `replace`s exist only because this is a branch preview. Once the adk-go
seams land on upstream `google/adk-go` (and a release is cut) and the plugin is
tagged in `temporalio/sdk-go`, both replaces disappear and consuming it is a plain
`go get go.temporal.io/sdk/contrib/google_adk_agents`.
