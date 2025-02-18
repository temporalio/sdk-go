// The MIT License
//
// Copyright (c) 2025 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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

var errFallbackToWorkflowID = errors.New("fall back to workflow ID as token")

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
		return token, fmt.Errorf("%w: failed to decode token: %w", errFallbackToWorkflowID, err)
	}
	if err := json.Unmarshal(b, &token); err != nil {
		return token, fmt.Errorf("%w: failed to unmarshal workflow run operation token: %w", errFallbackToWorkflowID, err)
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
