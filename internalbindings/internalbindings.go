// Package internalbindings contains low level APIs to be used by non Go SDKs
// built on top of the Go SDK.
//
// ATTENTION!
// The APIs found in this package should never be referenced from any application code.
// There is absolutely no guarantee of compatibility between releases.
// Always talk to Temporal team before building anything on top of them.
package internalbindings

import (
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/internal"
)

type (
	// WorkflowType information
	WorkflowType = internal.WorkflowType
	// WorkflowExecution identifiers
	WorkflowExecution = internal.WorkflowExecution
	// WorkflowDefinitionFactory used to create instances of WorkflowDefinition
	WorkflowDefinitionFactory = internal.WorkflowDefinitionFactory
	// WorkflowDefinition is an asynchronous workflow definition
	WorkflowDefinition = internal.WorkflowDefinition
	// WorkflowEnvironment exposes APIs to the WorkflowDefinition
	WorkflowEnvironment = internal.WorkflowEnvironment
	// ExecuteWorkflowParams parameters of the workflow invocation
	ExecuteWorkflowParams = internal.ExecuteWorkflowParams
	// WorkflowOptions options passed to the workflow function
	WorkflowOptions = internal.WorkflowOptions
	// ExecuteActivityParams activity invocation parameters
	ExecuteActivityParams = internal.ExecuteActivityParams
	// ActivityID uniquely identifies activity
	ActivityID = internal.ActivityID
	// ExecuteActivityOptions option for executing an activity
	ExecuteActivityOptions = internal.ExecuteActivityOptions
	// ExecuteLocalActivityParams local activity invocation parameters
	ExecuteLocalActivityParams = internal.ExecuteLocalActivityParams
	// LocalActivityID uniquely identifies a local activity
	LocalActivityID = internal.LocalActivityID
	// ExecuteLocalActivityOptions options for executing a local activity
	ExecuteLocalActivityOptions = internal.ExecuteLocalActivityOptions
	// LocalActivityResultHandler that returns local activity result
	LocalActivityResultHandler = internal.LocalActivityResultHandler
	// LocalActivityResultWrapper contains the result of a local activity
	LocalActivityResultWrapper = internal.LocalActivityResultWrapper
	// ActivityType type of activity
	ActivityType = internal.ActivityType
	// ResultHandler result handler function
	ResultHandler = internal.ResultHandler
	// TimerID uniquely identifies timer
	TimerID = internal.TimerID
	// ContinueAsNewError used by a workflow to request continue as new
	ContinueAsNewError = internal.ContinueAsNewError
	// UpdateCallbacks used to report the result of an update
	UpdateCallbacks = internal.UpdateCallbacks
	// ExecuteNexusOperationParams parameters for invoking a Nexus operation from
	// a workflow via WorkflowEnvironment.ExecuteNexusOperation. Exposed so that
	// non-Go SDKs (e.g. roadrunner-temporal proxying for PHP) can build the
	// params struct directly when they receive an `ExecuteNexusOperation`
	// command from the worker.
	ExecuteNexusOperationParams = internal.ExecuteNexusOperationParams
	// NexusOperationOptions are workflow-level options for a Nexus operation.
	NexusOperationOptions = internal.NexusOperationOptions
	// NexusClient is the workflow-level client used to issue Nexus operations.
	// Build via internal.NewNexusClient(endpoint, service).
	NexusClient = internal.NexusClient
)

// GetLastCompletionResult returns last completion result from workflow.
func GetLastCompletionResult(env WorkflowEnvironment) *commonpb.Payloads {
	return internal.GetLastCompletionResultFromWorkflowInfo(env.WorkflowInfo())
}

// NewNexusClient builds a NexusClient targeted at the given endpoint and
// service. Use the result with NewExecuteNexusOperationParams when feeding
// WorkflowEnvironment.ExecuteNexusOperation directly.
func NewNexusClient(endpoint, service string) NexusClient {
	return internal.NewNexusClient(endpoint, service)
}

// NewExecuteNexusOperationParams builds an ExecuteNexusOperationParams struct
// from outside the `internal` package — see internal.NewExecuteNexusOperationParams
// for the rationale.
func NewExecuteNexusOperationParams(
	client NexusClient,
	operation string,
	input *commonpb.Payload,
	options NexusOperationOptions,
	nexusHeader map[string]string,
) ExecuteNexusOperationParams {
	return internal.NewExecuteNexusOperationParams(client, operation, input, options, nexusHeader)
}
