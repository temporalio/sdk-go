package internal

import (
	"encoding/json"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
)

// System Nexus endpoint and service names
const (
	systemNexusEndpoint = "__temporal_system"
	systemNexusService  = "temporal.system.v1"

	// WaitExternalWorkflowCompletionOperation is the operation name for waiting
	// on an external workflow to complete.
	WaitExternalWorkflowCompletionOperation = "WaitExternalWorkflowCompletion"
)

// WaitExternalWorkflowOptions contains options for WaitExternalWorkflow.
type WaitExternalWorkflowOptions struct {
	// WorkflowID is the ID of the workflow to wait for. Required.
	WorkflowID string
	// RunID is the optional run ID. If empty, waits for the current run.
	RunID string
	// ScheduleToCloseTimeout is the timeout for the wait operation.
	// Optional: defaults to workflow run timeout.
	ScheduleToCloseTimeout time.Duration
}

// WaitExternalWorkflowResult contains the result of waiting for an external workflow.
type WaitExternalWorkflowResult struct {
	// Status is the final status of the workflow.
	Status enumspb.WorkflowExecutionStatus
	// rawResult is the raw result payload that can be decoded
	rawResult []byte
	// rawFailure is the raw failure if the workflow didn't complete successfully
	rawFailure []byte
}

// Get decodes the workflow result into the provided value.
// Returns an error if the workflow did not complete successfully.
func (r *WaitExternalWorkflowResult) Get(valuePtr interface{}) error {
	if r.Status != enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		return fmt.Errorf("workflow did not complete successfully, status: %v", r.Status)
	}
	if r.rawResult == nil {
		return nil
	}

	// The rawResult contains a JSON-encoded Payload object.
	// We need to extract the data field and decode it.
	var payload struct {
		Metadata map[string][]byte `json:"metadata"`
		Data     []byte            `json:"data"`
	}
	if err := json.Unmarshal(r.rawResult, &payload); err != nil {
		// If it's not a Payload structure, try direct unmarshal
		return json.Unmarshal(r.rawResult, valuePtr)
	}

	// If we have payload data, unmarshal from it
	if payload.Data != nil {
		return json.Unmarshal(payload.Data, valuePtr)
	}

	return nil
}

// GetFailure returns the failure information if the workflow failed.
// Returns nil if there is no failure information.
func (r *WaitExternalWorkflowResult) GetFailure() *WaitExternalWorkflowFailure {
	if r.rawFailure == nil {
		return nil
	}

	// The rawFailure contains a JSON-encoded Failure proto.
	// We extract the message and other useful fields.
	var failure struct {
		Message     string `json:"message"`
		FailureInfo struct {
			ApplicationFailureInfo *struct {
				Type         string `json:"type"`
				NonRetryable bool   `json:"nonRetryable"`
			} `json:"applicationFailureInfo,omitempty"`
			CanceledFailureInfo *struct {
			} `json:"canceledFailureInfo,omitempty"`
			TerminatedFailureInfo *struct {
			} `json:"terminatedFailureInfo,omitempty"`
			TimeoutFailureInfo *struct {
				TimeoutType string `json:"timeoutType"`
			} `json:"timeoutFailureInfo,omitempty"`
		} `json:"failureInfo"`
	}
	if err := json.Unmarshal(r.rawFailure, &failure); err != nil {
		return &WaitExternalWorkflowFailure{Message: "failed to decode failure"}
	}

	return &WaitExternalWorkflowFailure{
		Message: failure.Message,
	}
}

// WaitExternalWorkflowFailure contains failure information from a workflow.
type WaitExternalWorkflowFailure struct {
	// Message is the failure message.
	Message string
}

// WaitExternalWorkflowFuture represents the result of a WaitExternalWorkflow call.
type WaitExternalWorkflowFuture interface {
	// Get blocks until the external workflow completes and returns the result.
	Get(ctx Context, valuePtr *WaitExternalWorkflowResult) error
}

// waitExternalWorkflowFuture wraps a NexusOperationFuture
type waitExternalWorkflowFuture struct {
	nexusFuture NexusOperationFuture
}

func (f *waitExternalWorkflowFuture) Get(ctx Context, valuePtr *WaitExternalWorkflowResult) error {
	var rawOutput struct {
		Status  enumspb.WorkflowExecutionStatus `json:"status"`
		Result  json.RawMessage                 `json:"result,omitempty"`
		Failure json.RawMessage                 `json:"failure,omitempty"`
	}
	err := f.nexusFuture.Get(ctx, &rawOutput)
	if err != nil {
		return err
	}
	if valuePtr != nil {
		valuePtr.Status = rawOutput.Status
		valuePtr.rawResult = rawOutput.Result
		valuePtr.rawFailure = rawOutput.Failure
	}
	return nil
}

// WaitExternalWorkflowCompletionInput is the input for the WaitExternalWorkflowCompletion operation.
type waitExternalWorkflowCompletionInput struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id,omitempty"`
}

// newSystemNexusClient creates a Nexus client for system operations.
// This is an internal function that bypasses the validation for the __temporal_ prefix.
func newSystemNexusClient() NexusClient {
	return nexusClient{
		endpoint: systemNexusEndpoint,
		service:  systemNexusService,
	}
}

// WaitExternalWorkflow waits for an external workflow to complete.
// This is a convenience function that uses the system Nexus endpoint internally.
//
// Example usage:
//
//	future := workflow.WaitExternalWorkflow(ctx, workflow.WaitExternalWorkflowOptions{
//	    WorkflowID: "target-workflow-id",
//	})
//	var result WaitExternalWorkflowResult
//	if err := future.Get(ctx, &result); err != nil {
//	    return err
//	}
//	if result.Status == enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
//	    var output MyOutputType
//	    if err := result.Get(&output); err != nil {
//	        return err
//	    }
//	}
//
// NOTE: Experimental - this API is subject to change.
func WaitExternalWorkflow(ctx Context, options WaitExternalWorkflowOptions) WaitExternalWorkflowFuture {
	assertNotInReadOnlyState(ctx)

	if options.WorkflowID == "" {
		// Return a failed future
		f := &futureImpl{channel: NewChannel(ctx).(*channelImpl)}
		f.Set(nil, fmt.Errorf("WorkflowID is required"))
		return &waitExternalWorkflowFuture{nexusFuture: &nexusOperationFutureImpl{
			decodeFutureImpl: &decodeFutureImpl{futureImpl: f},
		}}
	}

	// Create input for the system operation
	input := waitExternalWorkflowCompletionInput{
		WorkflowID: options.WorkflowID,
		RunID:      options.RunID,
	}

	// Create system Nexus client and execute the operation
	client := newSystemNexusClient()
	nexusOptions := NexusOperationOptions{
		ScheduleToCloseTimeout: options.ScheduleToCloseTimeout,
	}

	nexusFuture := client.ExecuteOperation(ctx, WaitExternalWorkflowCompletionOperation, input, nexusOptions)
	return &waitExternalWorkflowFuture{nexusFuture: nexusFuture}
}
