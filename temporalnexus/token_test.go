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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeWorkflowRunOperationToken(t *testing.T) {
	wrt := workflowRunOperationToken{
		Type:          operationTokenTypeWorkflowRun,
		NamespaceName: "ns",
		WorkflowID:    "w",
	}
	token, err := generateWorkflowRunOperationToken("ns", "w")
	require.NoError(t, err)
	decoded, err := loadWorkflowRunOperationToken(token)
	require.NoError(t, err)
	require.Equal(t, wrt, decoded)
}

func TestDecodeWorkflowRunOperationTokenErrors(t *testing.T) {
	var err error

	_, err = loadWorkflowRunOperationToken("")
	require.ErrorContains(t, err, "invalid workflow run token: token is empty")

	_, err = loadWorkflowRunOperationToken("not-base64!@#$")
	require.ErrorContains(t, err, "failed to decode token: illegal base64 data at input byte 1")

	invalidJSONToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("invalid json"))
	_, err = loadWorkflowRunOperationToken(invalidJSONToken)
	require.ErrorContains(t, err, "failed to unmarshal workflow run operation token: invalid character 'i' looking for beginning of value")

	invalidTypeToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"t":2}`))
	_, err = loadWorkflowRunOperationToken(invalidTypeToken)
	require.ErrorContains(t, err, "invalid workflow token type: 2, expected: 1")

	missingWIDToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"t":1}`))
	_, err = loadWorkflowRunOperationToken(missingWIDToken)
	require.ErrorContains(t, err, "invalid workflow run token: missing workflow ID (wid)")

	versionedToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"v":1, "t":1,"wid": "workflow-id"}`))
	_, err = loadWorkflowRunOperationToken(versionedToken)
	require.ErrorContains(t, err, `invalid workflow run token: "v" field should not be present`)
}
