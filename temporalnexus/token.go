package temporalnexus

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

type operationTokenType int

const (
	operationTokenTypeReserved operationTokenType = iota
	operationTokenTypeWorkflowRun
	operationTokenTypeUpdateWorkflow
	// also Update With Start, Get Workflow Result, Stand Alone Activities
	operationTokenTypeMaxVal
)

func (o operationTokenType) IsValid() bool {
	return 0 < o && o < operationTokenTypeMaxVal
}

// commmon meta for all operation token types
type operationToken struct {
	// Version of the token, by default we assume we're on version 1, this field is not emitted as part of the output,
	// it's only used to reject newer token versions on load.
	Version int `json:"v,omitempty"`
	// Type of the operation.
	Type operationTokenType `json:"t"`
}

// workflowRunOperationToken is the decoded form of the workflow run operation token.
type workflowRunOperationToken struct {
	operationToken
	NamespaceName string `json:"ns"`
	WorkflowID    string `json:"wid"`
}

// updateWorkflow contains only meta - because it cannot be cancelled
type updateWorkflowOperationToken struct {
	operationToken
}

func generateWorkflowRunOperationToken(namespace, workflowID string) (string, error) {
	token := workflowRunOperationToken{
		NamespaceName: namespace,
		WorkflowID:    workflowID,
	}
	token.Type = operationTokenTypeWorkflowRun
	data, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow run operation token: %w", err)
	}
	return base64EncodedString(data), nil
}

func generateUpdateOperationToken() (string, error) {
	token := updateWorkflowOperationToken{}
	token.Type = operationTokenTypeUpdateWorkflow
	data, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to marshal operation token: %w", err)
	}
	return base64EncodedString(data), nil
}

func base64EncodedString(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
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
	if !partial.Type.IsValid() {
		return 0, fmt.Errorf("invalid operation token: %d", partial.Type)
	}
	return partial.Type, nil
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
