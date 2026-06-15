// Package google_adk_agents makes a Google Agent Development Kit (adk-go)
// agent durable, recoverable, and replay-safe by running it under a Temporal
// Go worker.
//
// # What it does
//
// You keep writing native adk-go: build a model with gemini.NewModel, an
// agent with llmagent.New, register tools and sub-agents, and assemble a
// runner with runner.New. You wrap that wiring in an [AgentFactory] and
// register it under a name. The plugin then executes each conversational
// turn — one full runner.Run — inside a Temporal Activity, orchestrated by a
// durable [AgentSessionWorkflow] that owns the conversation state, retries
// transient model failures, supports human-in-the-loop tool confirmation,
// streams partial output, and continue-as-news long conversations.
//
// # Turn-as-activity model
//
// The Go Temporal SDK runs workflow code on a single cooperative coroutine
// dispatcher: native goroutines, channels, time.Sleep, and context.Context
// I/O are non-deterministic and disallowed inside a workflow. adk-go's flow
// uses all of those (parallel tool execution, channel-based streaming, sleep
// backoff, real network I/O). The two runtimes therefore cannot be
// interleaved. Instead of reimplementing ADK's loop, this plugin runs one
// complete runner.Run turn inside a single Activity ("turn-as-activity") and
// lets a durable workflow orchestrate turns and own the durable session
// snapshot.
//
// Durability and retry granularity are therefore per turn, not per LLM call:
// a mid-turn failure re-runs the whole turn. The determinism seams in
// [WithDeterministicProviders] give reproducible event IDs and timestamps
// across Activity retries and continue-as-new.
//
// # Security
//
// Model credentials and live objects (the agent tree, model, tools, session
// service) never cross the Activity boundary. A [TurnInput] carries only the
// agent NAME; the worker-side [AgentFactory] reconstructs everything,
// keeping API keys in the worker process.
package google_adk_agents

// PluginName is the telemetry identifier for this integration, in
// <library>.<plugin> form.
const PluginName = "google.AdkAgentsPlugin"
