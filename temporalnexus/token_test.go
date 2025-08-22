package temporalnexus

import (
	"encoding/base64"
	"encoding/json"
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

func TestEncodeWorkflowRunOperationTokenDoesNotIncludeVersion(t *testing.T) {
	data, err := generateWorkflowRunOperationToken("ns", "w")
	require.NoError(t, err)

	b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(data)
	require.NoError(t, err)

	var token map[string]interface{}
	err = json.Unmarshal(b, &token)
	require.NoError(t, err)
	require.NotContains(t, token, "v", "version field should not be present in the token")
	require.Equal(t, 1.0, token["t"], "token type should be workflow run")
	require.Equal(t, "ns", token["ns"], "namespace name should match")
	require.Equal(t, "w", token["wid"], "workflow ID should match")
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
