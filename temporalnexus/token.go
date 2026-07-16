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
	operationTokenTypeActivity
	operationTokenTypeUpdateWorkflow
	// also Update With Start, Get Workflow Result, Stand Alone Activities
	operationTokenTypeMaxVal
)

func (o operationTokenType) IsValid() bool {
	if o == operationTokenTypeActivity { // temporary until activity is added
		return false
	}
	return 0 < o && o < operationTokenTypeMaxVal
}

// commmon meta for all operation token types
type operationToken struct {
	// Version of the token, by default we assume we're on version 1, this field is not emitted as part of the output,
	// it's only used to reject newer token versions on load.
	Version int `json:"v,omitempty"`
	// Type of the operation.
	Type          operationTokenType `json:"t"`
	NamespaceName string             `json:"ns"`
}

type operationTokenI interface {
	getOperationToken() operationToken
}

// workflowRunOperationToken is the decoded form of the workflow run operation token.
type workflowRunOperationToken struct {
	operationToken
	WorkflowID string `json:"wid"`
}

func (w workflowRunOperationToken) getOperationToken() operationToken {
	return w.operationToken
}

// updateWorkflow contains only meta - because it cannot be cancelled
type updateWorkflowOperationToken struct {
	operationToken
	WorkflowID string `json:"wid"`
	RunID      string `json:"rid"`
	UpdateID   string `json:"uid"`
}

func (w updateWorkflowOperationToken) getOperationToken() operationToken {
	return w.operationToken
}

func generateOperationToken(opType operationTokenType, namespace string) operationToken {
	return operationToken{
		Type:          opType,
		NamespaceName: namespace,
	}
}

func generateWorkflowRunOperationToken(namespace, workflowID string) (string, error) {
	token := workflowRunOperationToken{
		WorkflowID: workflowID,
	}
	token.operationToken = generateOperationToken(operationTokenTypeWorkflowRun, namespace)
	data, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow run operation token: %w", err)
	}
	return base64EncodedString(data), nil
}

func generateUpdateOperationToken(namespace, workflowID, runID, updateID string) (string, error) {
	if namespace == "" || workflowID == "" || updateID == "" {
		return "", fmt.Errorf("missing required param[s]: ns %s, wid: %s, uid: %s", namespace, workflowID, updateID)
	}
	token := updateWorkflowOperationToken{
		WorkflowID: workflowID,
		RunID:      runID,
		UpdateID:   updateID,
	}
	token.Type = operationTokenTypeUpdateWorkflow
	token.operationToken = generateOperationToken(operationTokenTypeUpdateWorkflow, namespace)
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
	if err := loadOperationToken(data, &token); err != nil {
		return token, err
	}
	if token.WorkflowID == "" {
		return token, errors.New("invalid workflow run token: missing workflow ID (wid)")
	}
	return token, nil
}

func loadUpdateWorkflowOperationToken(data string) (updateWorkflowOperationToken, error) {
	var token updateWorkflowOperationToken
	if err := loadOperationToken(data, &token); err != nil {
		return token, err
	}
	if token.WorkflowID == "" {
		return token, errors.New("invalid token: missing workflow ID (wid)")
	}
	if token.UpdateID == "" {
		return token, errors.New("invalid token: missing update ID (uid)")
	}
	return token, nil
}

func loadOperationToken(data string, token any) error {
	if len(data) == 0 {
		return errors.New("invalid token: token is empty")
	}
	var opType operationTokenType
	switch token.(type) {
	case *workflowRunOperationToken:
		opType = operationTokenTypeWorkflowRun
	case *updateWorkflowOperationToken:
		opType = operationTokenTypeUpdateWorkflow
	default:
		return errors.New("unknown op token type")
	}
	b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(data)
	if err != nil {
		return fmt.Errorf("failed to decode token: %w", err)
	}
	if err := json.Unmarshal(b, token); err != nil {
		return fmt.Errorf("failed to unmarshal operation token: %w", err)
	}
	tokenOpMeta, ok := token.(operationTokenI)
	if !ok {
		return errors.New("failed to load operation token metadata")
	}
	if tokenOpMeta.getOperationToken().Version != 0 {
		return fmt.Errorf(`invalid token: "v" field should not be present`)
	}
	if tokenOpMeta.getOperationToken().NamespaceName == "" {
		return errors.New("invalid token: missing namespace")
	}
	if tokenOpMeta.getOperationToken().Type != opType {
		return fmt.Errorf("invalid token type: %v, expected: %v", tokenOpMeta.getOperationToken().Type, opType)
	}
	return nil
}
