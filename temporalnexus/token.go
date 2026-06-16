package temporalnexus

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

type operationTokenType int

const (
	operationTokenTypeWorkflowRun       = operationTokenType(1)
	operationTokenTypeActivityExecution = operationTokenType(2)
)

// workflowRunOperationToken is the decoded form of the workflow run operation token.
type workflowRunOperationToken struct {
	// Version of the token, by default we assume we're on version 1, this field is not emitted as part of the output,
	// it's only used to reject newer token versions on load.
	Version int `json:"v,omitempty"`
	// Type of the operation. Must be operationTypeWorkflowRun.
	Type          operationTokenType `json:"t"`
	NamespaceName string             `json:"ns"`
	WorkflowID    string             `json:"wid"`
}

// activityExecutionOperationToken is the decoded form of the activity-execution operation token.
type activityExecutionOperationToken struct {
	// Version of the token. Not emitted; only used to reject newer token versions on load.
	Version int `json:"v,omitempty"`
	// Type of the operation. Must be operationTokenTypeActivityExecution.
	Type          operationTokenType `json:"t"`
	NamespaceName string             `json:"ns"`
	ActivityID    string             `json:"aid"`
	// RunID of the activity execution. Omitted on the token used for the callback's
	// HeaderOperationToken, since the run ID is only known after the activity has been started.
	RunID string `json:"rid,omitempty"`
}

func generateWorkflowRunOperationToken(namespace, workflowID string) (string, error) {
	token := workflowRunOperationToken{
		Type:          operationTokenTypeWorkflowRun,
		NamespaceName: namespace,
		WorkflowID:    workflowID,
	}
	data, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow run operation token: %w", err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data), nil
}

// loadTokenType decodes just the type field from an operation token without full validation.
// This allows cancel dispatch to route by token type before deserializing the full token.
func loadTokenType(data string) (operationTokenType, error) {
	if len(data) == 0 {
		return 0, errors.New("invalid operation token: token is empty")
	}
	b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(data)
	if err != nil {
		return 0, fmt.Errorf("failed to decode token: %w", err)
	}
	var partial struct {
		Type operationTokenType `json:"t"`
	}
	if err := json.Unmarshal(b, &partial); err != nil {
		return 0, fmt.Errorf("failed to unmarshal operation token: %w", err)
	}
	if partial.Type == 0 {
		return 0, errors.New("invalid operation token: missing or zero token type")
	}
	return partial.Type, nil
}

func generateActivityExecutionOperationToken(namespace, activityID, runID string) (string, error) {
	token := activityExecutionOperationToken{
		Type:          operationTokenTypeActivityExecution,
		NamespaceName: namespace,
		ActivityID:    activityID,
		RunID:         runID,
	}
	data, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to marshal activity execution operation token: %w", err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data), nil
}

func loadActivityExecutionOperationToken(data string) (activityExecutionOperationToken, error) {
	var token activityExecutionOperationToken
	if len(data) == 0 {
		return token, errors.New("invalid activity execution token: token is empty")
	}
	b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(data)
	if err != nil {
		return token, fmt.Errorf("failed to decode token: %w", err)
	}
	if err := json.Unmarshal(b, &token); err != nil {
		return token, fmt.Errorf("failed to unmarshal activity execution operation token: %w", err)
	}
	if token.Type != operationTokenTypeActivityExecution {
		return token, fmt.Errorf("invalid activity execution token type: %v, expected: %v", token.Type, operationTokenTypeActivityExecution)
	}
	if token.Version != 0 {
		return token, fmt.Errorf(`invalid activity execution token: "v" field should not be present`)
	}
	if token.ActivityID == "" {
		return token, errors.New("invalid activity execution token: missing activity ID (aid)")
	}
	return token, nil
}

func loadWorkflowRunOperationToken(data string) (workflowRunOperationToken, error) {
	var token workflowRunOperationToken
	if len(data) == 0 {
		return token, errors.New("invalid workflow run token: token is empty")
	}
	b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(data)
	if err != nil {
		return token, fmt.Errorf("failed to decode token: %w", err)
	}
	if err := json.Unmarshal(b, &token); err != nil {
		return token, fmt.Errorf("failed to unmarshal workflow run operation token: %w", err)
	}
	if token.Type != operationTokenTypeWorkflowRun {
		return token, fmt.Errorf("invalid workflow token type: %v, expected: %v", token.Type, operationTokenTypeWorkflowRun)
	}
	if token.Version != 0 {
		return token, fmt.Errorf(`invalid workflow run token: "v" field should not be present`)
	}
	if token.WorkflowID == "" {
		return token, errors.New("invalid workflow run token: missing workflow ID (wid)")
	}

	return token, nil
}
