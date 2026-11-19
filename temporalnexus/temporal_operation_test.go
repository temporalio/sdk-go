package temporalnexus

import (
	"context"
	"encoding/base64"
	"sync/atomic"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
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
		sync:  new("value"),
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
		Start: func(ctx context.Context, nc NexusClient, input string, opts StartTemporalOperationOptions) (TemporalOperationResult[string], error) {
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
			Start: func(ctx context.Context, nc NexusClient, input string, opts StartTemporalOperationOptions) (TemporalOperationResult[string], error) {
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

	// Valid type=2 (activity execution)
	activityToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"t":2,"ns":"ns","aid":"a"}`))
	tokenType, err = loadTokenType(activityToken)
	require.NoError(t, err)
	require.Equal(t, operationTokenTypeActivityExecution, tokenType)

	// Unknown type=99
	unknownToken := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"t":99}`))
	_, err = loadTokenType(unknownToken)
	require.EqualError(t, err, "invalid operation token: 99")

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
	require.ErrorContains(t, err, "invalid operation token: 0")
}

func TestNewTemporalOperationDefaultsCancelActivityExecution(t *testing.T) {
	opAny, err := NewTemporalOperation(TemporalOperationOptions[string, string]{
		Name: "test",
		Start: func(ctx context.Context, nc NexusClient, input string, opts StartTemporalOperationOptions) (TemporalOperationResult[string], error) {
			return NewSyncResult("ok"), nil
		},
	})
	require.NoError(t, err)
	op := opAny.(*temporalOperation[string, string])
	require.NotNil(t, op.options.CancelActivityExecution)
}

func TestDoubleStartGuard(t *testing.T) {
	var started atomic.Bool
	started.Store(true)
	nc := NexusClient{asyncStarted: &started}

	_, err := StartUntypedWorkflow[string](t.Context(), nc, client.StartWorkflowOptions{}, "ignored")
	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.Contains(t, handlerErr.Message, "only one async operation")

	_, err = StartWorkflow(t.Context(), nc, client.StartWorkflowOptions{}, func(_ workflow.Context, _ string) (string, error) { return "", nil }, "ignored")
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
}

func TestStartUpdateWorkflowGuards(t *testing.T) {
	nc := NexusClient{
		asyncStarted:          &atomic.Bool{},
		startOperationOptions: nexus.StartOperationOptions{CallbackURL: "temporal://dummy"},
	}
	// attempt running async with an exhuasted nexusClient handler
	nc.asyncStarted.Store(true)
	ctx := internal.ContextWithNexusOperationContext(t.Context(), &internal.NexusOperationContext{})
	_, err := StartUpdateWorkflow[string](ctx, nc, client.UpdateWorkflowOptions{
		WorkflowID:   "emptyWorkflowID!",
		UpdateName:   "upd",
		WaitForStage: client.WorkflowUpdateStageAccepted,
	})
	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, "only one async operation")
	// attempt to run async without callback
	_, err = StartUpdateWorkflow[string](
		ctx,
		NexusClient{
			asyncStarted: &atomic.Bool{},
		},
		client.UpdateWorkflowOptions{
			WorkflowID:   "anotherEmptyWorkflowID!",
			UpdateName:   "upd",
			WaitForStage: client.WorkflowUpdateStageAccepted,
		},
	)
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, "callback URL required")
	// attempt running an invalid update
	_, err = StartUpdateWorkflow[string](
		ctx,
		NexusClient{
			asyncStarted: &atomic.Bool{},
		},
		client.UpdateWorkflowOptions{
			WorkflowID: "dontTriggerValidation",
			UpdateName: "upd",
		},
	)
	var opError *nexus.HandlerError
	require.ErrorAs(t, err, &opError)
	require.Equal(t, opError.Cause.Error(), "nexus op workflow updates only support WorkflowUpdateStageAccepted for async updates")
}

func TestStartUpdateWithStartWorkflowGuards(t *testing.T) {
	c, err := client.NewLazyClient(client.Options{})
	require.NoError(t, err)
	defer c.Close()
	ctx := internal.ContextWithNexusOperationContext(t.Context(), &internal.NexusOperationContext{TaskQueue: "handler-tq"})
	update := client.UpdateWorkflowOptions{WorkflowID: "wf", UpdateName: "upd", WaitForStage: client.WorkflowUpdateStageAccepted}
	var handlerErr *nexus.HandlerError

	// start operation not created by NewWithStartWorkflowOperation
	_, err = StartUpdateWithStartWorkflow[string](ctx, NexusClient{asyncStarted: &atomic.Bool{}}, client.UpdateWithStartWorkflowOptions{
		UpdateOptions: update,
	})
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
	require.ErrorContains(t, err, "must be created by NewWithStartWorkflowOperation")

	// invalid start operation surfaces its own error
	_, err = StartUpdateWithStartWorkflow[string](ctx, NexusClient{asyncStarted: &atomic.Bool{}}, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{ID: "wf", TaskQueue: "tq"}, "wf"),
		UpdateOptions:          update,
	})
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
	require.ErrorContains(t, err, "WorkflowIDConflictPolicy must be set")

	// a generated start workflow ID differs on each retried Nexus task, so it is rejected
	_, err = StartUpdateWithStartWorkflow[string](ctx, NexusClient{asyncStarted: &atomic.Bool{}}, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		}, "wf"),
		UpdateOptions: update,
	})
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
	require.ErrorContains(t, err, "StartWorkflowOptions.ID must be set")

	// matching workflow IDs pass validation, so the callback check fails
	validStartOp := func() client.WithStartWorkflowOperation {
		return c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
			ID:                       "wf",
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		}, "wf")
	}
	startOp := validStartOp()
	_, err = StartUpdateWithStartWorkflow[string](ctx, NexusClient{asyncStarted: &atomic.Bool{}}, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: startOp,
		UpdateOptions:          update,
	})
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, "callback URL required")
	startOpts, err := internal.StartWorkflowOptionsFromWithStartOperation(startOp)
	require.NoError(t, err)
	require.Equal(t, "handler-tq", startOpts.TaskQueue)

	// UpdateOptions.WorkflowID must target the start's workflow
	otherWorkflowID := update
	otherWorkflowID.WorkflowID = "other"
	_, err = StartUpdateWithStartWorkflow[string](ctx, NexusClient{asyncStarted: &atomic.Bool{}}, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: validStartOp(),
		UpdateOptions:          otherWorkflowID,
	})
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeInternal, handlerErr.Type)
	require.ErrorContains(t, err, `UpdateOptions.WorkflowID "other" must match StartWorkflowOptions.ID "wf"`)

	// an empty UpdateOptions.WorkflowID defaults to the start's, so validation passes and the callback check fails
	noWorkflowID := update
	noWorkflowID.WorkflowID = ""
	_, err = StartUpdateWithStartWorkflow[string](ctx, NexusClient{asyncStarted: &atomic.Bool{}}, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: validStartOp(),
		UpdateOptions:          noWorkflowID,
	})
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, "callback URL required")

	// only one async operation per invocation
	started := &atomic.Bool{}
	started.Store(true)
	_, err = StartUpdateWithStartWorkflow[string](ctx, NexusClient{
		asyncStarted:          started,
		startOperationOptions: nexus.StartOperationOptions{CallbackURL: "temporal://dummy"},
	}, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: validStartOp(),
		UpdateOptions:          update,
	})
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, errMultipleAsyncOperationsMsg)
}
