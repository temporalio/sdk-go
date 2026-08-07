// Package googleadk makes Google ADK (adk-go) agents durable and replay-safe
// under Temporal without forcing a rewrite of the agent.
//
// A user builds their agent the native ADK way — llmagent.New with a model.LLM,
// tool.Tools / tool.Toolsets and SubAgents, wrapped in runner.New and driven by
// Runner.Run. To make it durable they change two things:
//
//  1. Use [NewModel] as the agent's Model. It is a model.LLM whose calls are
//     dispatched to the InvokeModel Temporal Activity, so the real model runs
//     worker-side (reconstructed by a ModelFactory) and never in the workflow.
//  2. Pass the bridged context.Context returned by [NewContext] to Runner.Run,
//     which installs Temporal-deterministic providers for time, UUIDs and
//     concurrent fan-out on the ADK platform seams.
//
// Tools run in-workflow by default, on the deterministic Temporal dispatcher —
// the idiomatic Temporal model. A tool that does I/O opts into a durable
// Activity: wrap an existing Temporal activity with [ActivityAsTool], or use
// [NewMCPToolset] for MCP. The real model and any activity/MCP tool handlers live
// worker-side in the Activity registry built by [NewActivities].
//
// Human-in-the-loop (HITL) tool confirmation and continue-as-new state carry are
// supported; see the confirmation helpers ([PendingConfirmations],
// [ConfirmationResponse]) and the session-snapshot helpers ([ExportSession],
// [ImportSession]).
//
// This package targets Google ADK for Go v2 (google.golang.org/adk/v2). It
// depends on the deterministic platform seams (platform.WithTimeProvider,
// platform.WithUUIDProvider and platform.WithTaskRunner) and the public
// tool-declaration packer (tool/toolutils.PackTool). See go.mod.
package googleadk

// PluginName identifies this integration for Temporal telemetry and usage
// attribution.
const PluginName = "googleadk"
