package temporalnexus

import (
	"context"
	"encoding/base64"
	"sync/atomic"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

func TestNewSyncResult(t *testing.T) {
	result := NewSyncResult("hello")
	require.NotNil(t, result.sync)
	require.Equal(t, "hello", *result.sync)
	require.Empty(t, result.token)
}

func TestNewAsyncResult(t *testing.T) {
	result := NewAsyncResult[string]("tok123")
	require.Nil(t, result.sync)
	require.Equal(t, "tok123", result.token)
}

func TestToHandlerResultSync(t *testing.T) {
	result := NewSyncResult("value")
	handlerResult, err := result.toHandlerResult()
	require.NoError(t, err)
	syncResult, ok := handlerResult.(*nexus.HandlerStartOperationResultSync[string])
	require.True(t, ok, "expected sync result type")
	require.Equal(t, "value", syncResult.Value)
}

func TestToHandlerResultAsync(t *testing.T) {
	result := NewAsyncResult[string]("tok123")
	handlerResult, err := result.toHandlerResult()
	require.NoError(t, err)
	asyncResult, ok := handlerResult.(*nexus.HandlerStartOperationResultAsync)
	require.True(t, ok, "expected async result type")
	require.Equal(t, "tok123", asyncResult.OperationToken)
}

func TestToHandlerResultBothSet(t *testing.T) {
	result := TemporalOperationResult[string]{
		sync:  strPtr("value"),
		token: "tok123",
	}
	_, err := result.toHandlerResult()
	require.Error(t, err)
	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
	require.Contains(t, handlerErr.Message, "both sync and token are set")
}

func TestToHandlerResultNeitherSet(t *testing.T) {
	result := TemporalOperationResult[string]{}
	_, err := result.toHandlerResult()
	require.Error(t, err)
	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
	require.Contains(t, handlerErr.Message, "neither sync nor token is set")
}

func TestNewTemporalOperationValidation(t *testing.T) {
	_, err := NewTemporalOperation(TemporalOperationOptions[string, string]{})
	require.ErrorContains(t, err, "Name is required")

	_, err = NewTemporalOperation(TemporalOperationOptions[string, string]{
		Name: "__temporal_test",
	})
	require.ErrorContains(t, err, "__temporal_ is a reserved prefix")

	_, err = NewTemporalOperation(TemporalOperationOptions[string, string]{
		Name: "test",
	})
	require.ErrorContains(t, err, "Start is required")

	// Valid options
	_, err = NewTemporalOperation(TemporalOperationOptions[string, string]{
		Name: "test",
		Start: func(ctx context.Context, nc NexusClient, input string, opts nexus.StartOperationOptions) (TemporalOperationResult[string], error) {
			return NewSyncResult("ok"), nil
		},
	})
	require.NoError(t, err)
}

func TestMustNewTemporalOperationPanics(t *testing.T) {
	require.Panics(t, func() {
		MustNewTemporalOperation(TemporalOperationOptions[string, string]{})
	})
}

func TestMustNewTemporalOperationValid(t *testing.T) {
	require.NotPanics(t, func() {
		MustNewTemporalOperation(TemporalOperationOptions[string, string]{
			Name: "test",
			Start: func(ctx context.Context, nc NexusClient, input string, opts nexus.StartOperationOptions) (TemporalOperationResult[string], error) {
				return NewSyncResult("ok"), nil
			},
		})
	})
}

func TestLoadTokenType(t *testing.T) {
	// Valid type=1
	validToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"t":1,"ns":"ns","wid":"w"}`))
	tokenType, err := loadTokenType(validToken)
	require.NoError(t, err)
	require.Equal(t, operationTokenTypeWorkflowRun, tokenType)

	// Unknown type=99
	unknownToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"t":99}`))
	tokenType, err = loadTokenType(unknownToken)
	require.NoError(t, err)
	require.Equal(t, operationTokenType(99), tokenType)

	// Empty token
	_, err = loadTokenType("")
	require.ErrorContains(t, err, "token is empty")

	// Malformed base64
	_, err = loadTokenType("not-base64!@#$")
	require.ErrorContains(t, err, "failed to decode token")

	// Invalid JSON
	invalidJSON := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("invalid json"))
	_, err = loadTokenType(invalidJSON)
	require.ErrorContains(t, err, "failed to unmarshal operation token")

	// Missing type field (t=0)
	missingType := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"ns":"ns"}`))
	_, err = loadTokenType(missingType)
	require.ErrorContains(t, err, "missing or zero token type")
}

func TestDoubleStartGuard(t *testing.T) {
	var started atomic.Bool
	started.Store(true)
	nc := NexusClient{asyncStarted: &started}

	_, err := StartUntypedWorkflow[string](context.Background(), nc, client.StartWorkflowOptions{}, "ignored")
	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.Contains(t, handlerErr.Message, "only one async operation")

	_, err = StartWorkflow(context.Background(), nc, client.StartWorkflowOptions{}, func(_ workflow.Context, _ string) (string, error) { return "", nil }, "ignored")
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
}

func strPtr(s string) *string {
	return &s
}
