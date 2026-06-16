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

package googleadk

import (
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"
)

// Activity type names registered worker-side by Activities.Register and
// dispatched by name from the workflow-side plugin callbacks.
const (
	InvokeModelActivityName  = "google_adk_agents.InvokeModel"
	CallToolActivityName     = "google_adk_agents.CallTool"
	ListMcpToolsActivityName = "google_adk_agents.ListMcpTools"
	CallMcpToolActivityName  = "google_adk_agents.CallMcpTool"
)

// Stable ApplicationError.Type strings. Callers classify failures on these
// rather than string-matching err.Error(); IsNonRetryable reports whether
// Temporal will stop retrying.
const (
	// ErrorTypeModel tags failures originating from a model.LLM call.
	ErrorTypeModel = "google_adk_agents.ModelError"
	// ErrorTypeTool tags failures originating from a tool execution.
	ErrorTypeTool = "google_adk_agents.ToolError"
	// ErrorTypeMCP tags failures originating from an MCP list/call.
	ErrorTypeMCP = "google_adk_agents.McpError"
)

const (
	defaultModelTimeout    = 2 * time.Minute
	defaultToolTimeout     = 1 * time.Minute
	defaultStreamHeartbeat = 30 * time.Second
)

// inWorkflowToolNames are ADK built-in control tools that only mutate the
// invocation's EventActions (agent transfer, loop exit). They are pure and
// deterministic, carry no I/O, and MUST run inside the workflow coroutine so
// their action mutations propagate — routing them through an Activity would
// drop those mutations across the boundary. The plugin's BeforeToolCallback
// passes them through to ADK rather than short-circuiting into an Activity.
var inWorkflowToolNames = map[string]bool{
	"transfer_to_agent": true,
	"exit_loop":         true,
}

// errMissingContext is returned by the plugin callbacks when the run context
// was not produced by NewContext. It surfaces at the first workflow turn rather
// than as an obscure activity-side failure.
var errMissingContext = errors.New(
	"google_adk_agents: run context is missing the Temporal workflow bridge; " +
		"pass googleadk.NewContext(ctx) as the context to runner.Runner.Run")

// Options configures the durability behavior of the plugin returned by Plugin.
// All fields are optional; the zero value yields a working plugin with default
// per-call timeouts and a default Activity summary of the ADK agent name.
type Options struct {
	// TaskQueue overrides the task queue used for the model/tool Activities.
	// Empty means inherit the workflow's task queue.
	TaskQueue string

	// ModelActivityOptions is the base set of Activity options for the
	// InvokeModel Activity. StartToCloseTimeout, RetryPolicy and friends are
	// honored; a zero StartToCloseTimeout defaults to two minutes.
	ModelActivityOptions workflow.ActivityOptions

	// ToolActivityOptions is the base set of Activity options for the tool /
	// MCP Activities. A zero StartToCloseTimeout defaults to one minute.
	ToolActivityOptions workflow.ActivityOptions

	// PerModelTimeouts overrides StartToCloseTimeout per model name, keyed by
	// the model string carried in LLMRequest.Model. Use it to give thinking
	// models a longer budget than fast models.
	PerModelTimeouts map[string]time.Duration

	// SummaryFn computes the Temporal UI summary for a model Activity from its
	// request. When nil the summary defaults to the ADK agent name.
	SummaryFn func(*model.LLMRequest) string

	// StreamingTopic, when non-empty, makes the InvokeModel Activity call the
	// model in streaming mode and publish each chunk to this workflowstreams
	// topic for external (UI) consumers. The aggregated final response is still
	// returned into the workflow so replay stays deterministic.
	StreamingTopic string

	// StreamingBatchInterval coalesces published chunks. Defaults to 100ms when
	// StreamingTopic is set.
	StreamingBatchInterval time.Duration
}

// Plugin builds the ADK *plugin.Plugin that routes model and tool calls through
// Temporal Activities. Add it to runner.Config:
//
//	p, err := googleadk.Plugin(googleadk.Options{TaskQueue: "adk"})
//	r, err := runner.New(runner.Config{
//	    Agent:          myAgent,
//	    SessionService: session.InMemoryService(),
//	    PluginConfig:   runner.PluginConfig{Plugins: []*plugin.Plugin{p}},
//	})
func Plugin(opts Options) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name:                PluginName,
		BeforeModelCallback: opts.beforeModel,
		BeforeToolCallback:  opts.beforeTool,
	})
}

// beforeModel is ADK's BeforeModelCallback. Returning a non-nil response (or
// error) short-circuits the real model call, so the user's model.LLM is never
// invoked inside the workflow — the InvokeModel Activity calls it worker-side.
func (o Options) beforeModel(cctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	wfCtx, ok := workflowContext(cctx)
	if !ok {
		return nil, errMissingContext
	}

	ao := o.modelActivityOptions(req)
	ao.Summary = o.modelSummary(req, cctx.AgentName())
	actx := workflow.WithActivityOptions(wfCtx, ao)

	in := invokeModelInput{
		Request:                req,
		Stream:                 o.StreamingTopic != "",
		StreamingTopic:         o.StreamingTopic,
		StreamingBatchInterval: o.StreamingBatchInterval,
	}

	var resp model.LLMResponse
	if err := workflow.ExecuteActivity(actx, InvokeModelActivityName, in).Get(wfCtx, &resp); err != nil {
		return nil, err
	}
	// Non-nil response short-circuits the in-workflow model call.
	return &resp, nil
}

// beforeTool is ADK's BeforeToolCallback. Returning a non-nil result (or error)
// skips the in-workflow tool.Run; the registered tool runs worker-side in the
// CallTool / CallMcpTool Activity instead. MCP proxy tools route to CallMcpTool;
// every other tool routes to the generic CallTool.
func (o Options) beforeTool(tctx agent.ToolContext, t tool.Tool, args map[string]any) (map[string]any, error) {
	// ADK's built-in control tools (agent transfer, loop exit) only mutate the
	// invocation's actions — they are pure, deterministic and carry no I/O, so
	// they run in-workflow. Returning (nil, nil) tells ADK to run the real tool
	// rather than short-circuiting into an Activity.
	if inWorkflowToolNames[t.Name()] {
		return nil, nil
	}

	wfCtx, ok := workflowContext(tctx)
	if !ok {
		return nil, errMissingContext
	}

	snap := snapshotContext(tctx)
	var res map[string]any
	var err error
	switch tt := t.(type) {
	case *activityTool:
		// ActivityAsTool: dispatch the user's own Temporal activity directly by
		// name, rather than routing through the CallTool registry.
		ao := o.activityToolOptions(tt)
		ao.Summary = o.toolSummary(t, tctx.AgentName())
		actx := workflow.WithActivityOptions(wfCtx, ao)
		var raw any
		if err = workflow.ExecuteActivity(actx, tt.activityName, args).Get(wfCtx, &raw); err == nil {
			res = toResultMap(raw)
		}
	case *mcpProxyTool:
		ao := o.toolActivityOptions()
		ao.Summary = o.toolSummary(t, tctx.AgentName())
		actx := workflow.WithActivityOptions(wfCtx, ao)
		in := mcpCallInput{Toolset: tt.toolset, Tool: t.Name(), Args: args, Ctx: snap}
		err = workflow.ExecuteActivity(actx, CallMcpToolActivityName, in).Get(wfCtx, &res)
	default:
		ao := o.toolActivityOptions()
		ao.Summary = o.toolSummary(t, tctx.AgentName())
		actx := workflow.WithActivityOptions(wfCtx, ao)
		in := toolInvocation{ToolName: t.Name(), Args: args, Ctx: snap}
		err = workflow.ExecuteActivity(actx, CallToolActivityName, in).Get(wfCtx, &res)
	}
	if err != nil {
		return nil, err
	}
	if res == nil {
		// Guarantee a non-nil result so ADK treats the call as short-circuited
		// and does not fall through to running the real tool in-workflow.
		res = map[string]any{}
	}
	return res, nil
}

func (o Options) modelActivityOptions(req *model.LLMRequest) workflow.ActivityOptions {
	ao := o.ModelActivityOptions
	if ao.StartToCloseTimeout == 0 {
		ao.StartToCloseTimeout = defaultModelTimeout
	}
	if d, ok := o.PerModelTimeouts[req.Model]; ok {
		ao.StartToCloseTimeout = d
	}
	if ao.TaskQueue == "" {
		ao.TaskQueue = o.TaskQueue
	}
	if o.StreamingTopic != "" && ao.HeartbeatTimeout == 0 {
		ao.HeartbeatTimeout = defaultStreamHeartbeat
	}
	return ao
}

func (o Options) toolActivityOptions() workflow.ActivityOptions {
	ao := o.ToolActivityOptions
	if ao.StartToCloseTimeout == 0 {
		ao.StartToCloseTimeout = defaultToolTimeout
	}
	if ao.TaskQueue == "" {
		ao.TaskQueue = o.TaskQueue
	}
	return ao
}

// activityToolOptions resolves the Activity options for an ActivityAsTool
// dispatch: the tool's own ActivityOptions if set, else the plugin tool
// defaults, with the plugin TaskQueue filled in when unspecified.
func (o Options) activityToolOptions(at *activityTool) workflow.ActivityOptions {
	ao := at.activityOptions
	if ao.StartToCloseTimeout == 0 {
		ao.StartToCloseTimeout = defaultToolTimeout
	}
	if ao.TaskQueue == "" {
		ao.TaskQueue = o.TaskQueue
	}
	return ao
}

func (o Options) modelSummary(req *model.LLMRequest, agentName string) string {
	if o.SummaryFn != nil {
		return o.SummaryFn(req)
	}
	if agentName != "" {
		return agentName
	}
	return "google_adk_agents.InvokeModel"
}

func (o Options) toolSummary(t tool.Tool, agentName string) string {
	if agentName != "" {
		return fmt.Sprintf("%s: %s", agentName, t.Name())
	}
	return t.Name()
}

// IsNonRetryable reports whether err is a Temporal ApplicationError marked
// non-retryable. Callers use it to classify model/tool/MCP failures without
// string-matching the error message.
func IsNonRetryable(err error) bool {
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		return appErr.NonRetryable()
	}
	return false
}

// newApplicationError builds a typed Temporal ApplicationError. retryable=false
// marks the error non-retryable so Temporal stops retrying.
func newApplicationError(errType string, retryable bool, cause error, format string, args ...any) error {
	return temporal.NewApplicationErrorWithOptions(
		fmt.Sprintf(format, args...),
		errType,
		temporal.ApplicationErrorOptions{NonRetryable: !retryable, Cause: cause},
	)
}
