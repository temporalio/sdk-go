package googleadk

import (
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Activity type names registered worker-side by Activities.Register and
// dispatched by name from the workflow side (the TemporalModel, ActivityAsTool
// tools, and the MCP proxy).
const (
	InvokeModelActivityName  = "googleadk.InvokeModel"
	ListMcpToolsActivityName = "googleadk.ListMcpTools"
	CallMcpToolActivityName  = "googleadk.CallMcpTool"
)

// Stable ApplicationError.Type strings. Callers classify failures on these
// rather than string-matching err.Error(); IsNonRetryable reports whether
// Temporal will stop retrying.
const (
	// ErrorTypeModel tags failures originating from a model.LLM call.
	ErrorTypeModel = "googleadk.ModelError"
	// ErrorTypeTool tags failures originating from a tool execution.
	ErrorTypeTool = "googleadk.ToolError"
	// ErrorTypeMCP tags failures originating from an MCP list/call.
	ErrorTypeMCP = "googleadk.McpError"
)

const (
	defaultModelTimeout    = 2 * time.Minute
	defaultToolTimeout     = 1 * time.Minute
	defaultStreamHeartbeat = 30 * time.Second
)

// errMissingContext is returned on the workflow side when the run context was
// not produced by NewContext. It surfaces at the first workflow turn rather than
// as an obscure activity-side failure.
var errMissingContext = errors.New(
	"googleadk: run context is missing the Temporal workflow bridge; " +
		"pass googleadk.NewContext(ctx) as the context to runner.Runner.Run")

// resolveToolActivityOptions fills in the tool-Activity defaults: a one-minute
// StartToCloseTimeout and the fallback task queue when unset.
func resolveToolActivityOptions(ao workflow.ActivityOptions, taskQueue string) workflow.ActivityOptions {
	if ao.StartToCloseTimeout == 0 {
		ao.StartToCloseTimeout = defaultToolTimeout
	}
	if ao.TaskQueue == "" {
		ao.TaskQueue = taskQueue
	}
	return ao
}

// toolSummary builds the Temporal UI summary for a tool Activity from the live
// agent context (which knows the agent name) and the tool name.
func toolSummary(ctx interface{ AgentName() string }, toolName string) string {
	if ctx != nil {
		if name := ctx.AgentName(); name != "" {
			return fmt.Sprintf("%s: %s", name, toolName)
		}
	}
	return toolName
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
