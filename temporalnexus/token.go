package temporalnexus

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

type operationTokenType int

const (
	operationTokenTypeWorkflowRun = operationTokenType(1)
)

// workflowRunOperationToken is the decoded form of the workflow run operation token.
type workflowRunOperationToken struct {
	// Version of the token, by default we assume we're on version 1, this field is not emitted as part of the output,
	// it's only used to reject newer token versions on load.
	Version int `json:"v"`
	// Type of the operation. Must be operationTypeWorkflowRun.
	Type          operationTokenType `json:"t"`
	NamespaceName string             `json:"ns"`
	WorkflowID    string             `json:"wid"`
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
