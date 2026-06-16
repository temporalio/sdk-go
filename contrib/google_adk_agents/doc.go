// Copyright 2026 Google LLC, Temporal Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.

// Package googleadk makes Google ADK (adk-go) agents durable and replay-safe
// under Temporal without forcing a rewrite of the agent.
//
// A user builds their agent the native ADK way — llmagent.New with a model.LLM,
// tool.Tools / tool.Toolsets and SubAgents, wrapped in runner.New and driven by
// Runner.Run. To make it durable they add exactly two things:
//
//  1. The ADK *plugin.Plugin returned by [Plugin] to their runner.Config, which
//     routes every LLM call and every tool call through a Temporal Activity via
//     ADK's BeforeModelCallback / BeforeToolCallback short-circuit seam.
//  2. The bridged context.Context returned by [NewContext], passed to Runner.Run,
//     which installs Temporal-deterministic providers for time, UUIDs and
//     concurrent fan-out on the ADK platform seams.
//
// The agent's orchestration loop then runs inside a Temporal Workflow while the
// real model and tool handlers live worker-side in the Activity registry built
// by [NewActivities]. The user's model.LLM and tool handlers are never invoked
// inside the workflow.
//
// This package depends on the deterministic platform seams
// (platform.WithTimeProvider, platform.WithUUIDProvider and
// platform.WithTaskRunner) that currently exist only on the
// add-platform-task-runner-seam branch of github.com/DABH/adk-go. See go.mod.
package googleadk

// PluginName identifies this integration for Temporal telemetry and usage
// attribution. It follows the library.PluginName convention used across the
// Temporal SDK contrib plugins.
const PluginName = "google_adk_agents"
