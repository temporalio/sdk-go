# toolregistry

LLM tool-calling primitives for Temporal activities — define tools once, use with
Anthropic or OpenAI.

## Before you start

A Temporal Activity is a function that Temporal monitors and retries automatically on failure. Temporal streams progress between retries via heartbeats — that's the mechanism `RunWithSession` uses to resume a crashed LLM conversation mid-turn.

`RunToolLoop` works standalone in any async function — no Temporal server needed. Add `RunWithSession` only when you need crash-safe resume inside a Temporal activity.

`RunWithSession` requires a running Temporal worker — it reads and writes heartbeat state from the active activity context. Use `RunToolLoop` standalone for scripts, one-off jobs, or any code that runs outside a Temporal worker.

New to Temporal? → https://docs.temporal.io/develop

**Python or TypeScript user?** Those SDKs also ship framework-level integrations (`openai_agents`, `google_adk_agents`, `langgraph`, `@temporalio/ai-sdk`) for teams already using a specific agent framework. ToolRegistry is the equivalent story for direct Anthropic/OpenAI calls, and shares the same API surface across all six Temporal SDKs.

## Install

```bash
go get go.temporal.io/sdk/contrib/toolregistry
```

Install the LLM client SDK separately:

```bash
go get github.com/anthropics/anthropic-sdk-go   # Anthropic
go get github.com/openai/openai-go              # OpenAI
```

## Quickstart

Tool definitions use [JSON Schema](https://json-schema.org/understanding-json-schema/) for `InputSchema`. The quickstart uses a single string field; for richer schemas refer to the JSON Schema docs.

```go
import "go.temporal.io/sdk/contrib/toolregistry"

func AnalyzeActivity(ctx context.Context, prompt string) ([]string, error) {
    var issues []string
    reg := toolregistry.NewToolRegistry()
    reg.Register(toolregistry.ToolDef{
        Name:        "flag_issue",
        Description: "Flag a problem found in the analysis",
        InputSchema: map[string]any{
            "type":       "object",
            "properties": map[string]any{"description": map[string]any{"type": "string"}},
            "required":   []string{"description"},
        },
    }, func(inp map[string]any) (string, error) {
        issues = append(issues, inp["description"].(string))
        return "recorded", nil // this string is sent back to the LLM as the tool result
    })

    cfg := toolregistry.AnthropicConfig{APIKey: os.Getenv("ANTHROPIC_API_KEY")}
    provider := toolregistry.NewAnthropicProvider(cfg, reg,
        "You are a code reviewer. Call flag_issue for each problem you find.")

    // RunToolLoop returns the full conversation history; capture or discard as needed.
    if _, err := toolregistry.RunToolLoop(ctx, provider, reg, "" /* system prompt: "" defers to provider default */, prompt); err != nil {
        return nil, err
    }
    return issues, nil
}
```

### Selecting a model

The default model is `"claude-sonnet-4-6"` (Anthropic) or `"gpt-4o"` (OpenAI). Override with the `Model` field:

```go
cfg := toolregistry.AnthropicConfig{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
    Model:  "claude-3-5-sonnet-20241022",
}
provider := toolregistry.NewAnthropicProvider(cfg, reg, "system prompt")
```

Model IDs are defined by the provider — see Anthropic or OpenAI docs for current names.

### OpenAI

```go
cfg := toolregistry.OpenAIConfig{APIKey: os.Getenv("OPENAI_API_KEY")}
provider := toolregistry.NewOpenAIProvider(cfg, reg, "system prompt")
if _, err := toolregistry.RunToolLoop(ctx, provider, reg, "", prompt); err != nil {
    return nil, err
}
```

## Crash-safe agentic sessions

For multi-turn LLM conversations that must survive activity retries, use
`RunWithSession`. It saves conversation history via `activity.RecordHeartbeat`
on every turn and restores it automatically on retry.

```go
import (
    "context"
    "os"
    "go.temporal.io/sdk/contrib/toolregistry"
)

func LongAnalysisActivity(ctx context.Context, prompt string) ([]map[string]any, error) {
    var issues []map[string]any

    err := toolregistry.RunWithSession(ctx, func(ctx context.Context, s *toolregistry.AgenticSession) error {
        reg := toolregistry.NewToolRegistry()
        reg.Register(toolregistry.ToolDef{
            Name: "flag", Description: "...",
            InputSchema: map[string]any{"type": "object"},
        }, func(inp map[string]any) (string, error) {
            s.Issues = append(s.Issues, inp) // s.Issues is []map[string]any
            return "ok", nil
        })

        cfg := toolregistry.AnthropicConfig{APIKey: os.Getenv("ANTHROPIC_API_KEY")}
        provider := toolregistry.NewAnthropicProvider(cfg, reg, "...")
        if err := s.RunToolLoop(ctx, provider, reg, "...", prompt); err != nil {
            return err
        }
        issues = s.Issues // capture after loop completes
        return nil
    })
    return issues, err
}
```

## Testing without an API key

```go
import "go.temporal.io/sdk/contrib/toolregistry"

func TestAnalyze(t *testing.T) {
    reg := toolregistry.NewToolRegistry()
    reg.Register(toolregistry.ToolDef{Name: "flag", Description: "d",
        InputSchema: map[string]any{}},
        func(inp map[string]any) (string, error) { return "ok", nil })

    provider := toolregistry.NewMockProvider([]toolregistry.MockResponse{
        toolregistry.ToolCall("flag", map[string]any{"description": "stale API"}),
        toolregistry.Done("analysis complete"),
    }).WithRegistry(reg)

    msgs, err := toolregistry.RunToolLoop(context.Background(), provider, reg, "sys", "analyze")
    require.NoError(t, err)
    require.Greater(t, len(msgs), 2)
}
```

## Integration testing with real providers

To run the integration tests against live Anthropic and OpenAI APIs:

```bash
RUN_INTEGRATION_TESTS=1 \
  ANTHROPIC_API_KEY=sk-ant-... \
  OPENAI_API_KEY=sk-proj-... \
  go test ./contrib/toolregistry/ -run Integration -v
```

Tests skip automatically when `RUN_INTEGRATION_TESTS` is unset. Real API calls
incur billing — expect a few cents per full test run.

## Storing application results

`s.Issues` accumulates application-level results during the tool loop.
Elements are serialized to JSON inside each heartbeat checkpoint — they must be
plain maps/dicts with JSON-serializable values. A non-serializable value raises
a non-retryable `ApplicationError` at heartbeat time rather than silently losing
data on the next retry.

### Storing typed results

Convert your domain type to a plain dict at the tool-call site and back after
the session:

```go
type Issue struct {
    Type string `json:"type"`
    File string `json:"file"`
}

// Inside tool handler:
s.Issues = append(s.Issues, map[string]any{"type": "smell", "file": "foo.go"})

// After session:
var issues []Issue
for _, raw := range s.Issues {
    data, _ := json.Marshal(raw)
    var issue Issue
    _ = json.Unmarshal(data, &issue)
    issues = append(issues, issue)
}
```

## Per-turn LLM timeout

Individual LLM calls inside the tool loop are unbounded by default. A hung HTTP
connection holds the activity open until Temporal's `ScheduleToCloseTimeout`
fires — potentially many minutes. Set a per-turn timeout on the provider client:

```go
import "github.com/anthropics/anthropic-sdk-go/option"

cfg := toolregistry.AnthropicConfig{
    APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
    Options: []option.RequestOption{option.WithRequestTimeout(30 * time.Second)},
}
provider := toolregistry.NewAnthropicProvider(cfg, reg, "system prompt")
// provider now enforces 30s per turn
```

Recommended timeouts:

| Model type | Recommended |
|---|---|
| Standard (Claude 3.x, GPT-4o) | 30 s |
| Reasoning (o1, o3, extended thinking) | 300 s |

## MCP integration

`FromMCPTools` converts a slice of MCP tool descriptors into a populated registry.
Handlers default to no-ops that return an empty string; override them with `Register`
after construction.

```go
// mcpTools is []MCPTool — populate from your MCP client.
reg := toolregistry.FromMCPTools(mcpTools)

// Override specific handlers before running the loop.
reg.Register(toolregistry.ToolDef{Name: "read_file", /* ... */},
    func(inp map[string]any) (string, error) {
        return readFile(inp["path"].(string))
    })
```

`MCPTool` mirrors the MCP protocol's `Tool` object: `Name`, `Description`, and
`InputSchema` (a `map[string]any` containing a JSON Schema object).
