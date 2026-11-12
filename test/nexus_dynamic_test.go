package test_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/test/nexusclient"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// TestNexusDynamicTemporalCatchAll:
//  1. Starts a Nexus service "tools" with a dynamic handler starts a backing workflow
//     if the operation name provided is "wait".
//  2. Start a "wait" operation on the "tools" Nexus service to trigger backing workflow.
//  3. Block new "wait" operations from starting workflows and ensure new start operation fails.
//  4. Stop the Worker and start a replacement with a new service instance.
//  5. Check that the original backing Workflow is still RUNNING after the Worker restart.
//  6. Cancel the original pending operation and check that its backing Workflow is canceled.
//
// This validates that token-based cancellation still works after the application stops
// accepting an operation name and the Worker that started it has been replaced.
func TestNexusDynamicTemporalCatchAll(t *testing.T) {
	skipOnCloud(t, cloudRequiresLocalServer, "creates a Nexus endpoint")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tc := newTestContext(t, ctx)

	// Use atomic bool to control whether we start a backing workflow.
	var available atomic.Bool
	available.Store(true)
	backingWorkflow := func(ctx workflow.Context, _ string) (string, error) {
		return "", workflow.Await(ctx, func() bool { return false })
	}
	operationOptions := temporalnexus.DynamicTemporalOperationOptions{
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input *nexus.LazyValue, _ temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
			var id string
			if err := input.Consume(&id); err != nil {
				return temporalnexus.TemporalOperationResult[any]{}, err
			}
			if nexus.ExtractHandlerInfo(ctx).Operation != "wait" || !available.Load() {
				return temporalnexus.TemporalOperationResult[any]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeNotFound, "unknown tool")
			}
			return temporalnexus.StartUntypedWorkflow[any](ctx, nc, client.StartWorkflowOptions{ID: id}, backingWorkflow, id)
		},
	}
	service := nexus.NewService("tools")
	require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(operationOptions)))
	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	w.RegisterNexusService(service)
	w.RegisterWorkflow(backingWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)
	nc := tc.newNexusClient(t, service.Name)
	wait := nexus.NewOperationReference[string, string]("wait")
	id := uuid.NewString()

	// Starting the "wait" operation should start the backing Workflow.
	result, err := nexusclient.StartOperation(ctx, nc, wait, id, nexus.StartOperationOptions{CallbackURL: "http://localhost/test"})
	require.NoError(t, err)
	require.NotNil(t, result.Pending)

	// When we block new backing workflows from getting started, starting a new "wait" operation
	// should now result in error.
	available.Store(false)
	_, err = nexusclient.StartOperation(ctx, nc, wait, uuid.NewString(), nexus.StartOperationOptions{CallbackURL: "http://localhost/test"})
	require.Error(t, err)

	// Replace our worker with an entirely new worker.
	w.Stop()
	replacementService := nexus.NewService("tools")
	require.NoError(t, replacementService.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(operationOptions)))
	replacement := worker.New(tc.client, tc.taskQueue, worker.Options{})
	replacement.RegisterNexusService(replacementService)
	replacement.RegisterWorkflow(backingWorkflow)
	require.NoError(t, replacement.Start())
	t.Cleanup(replacement.Stop)

	// Ensure our backing workflow is still running.
	description, err := tc.client.DescribeWorkflow(ctx, id, "")
	require.NoError(t, err)
	require.Equal(t,
		enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		description.RawResponse.GetWorkflowExecutionInfo().GetStatus(),
	)

	// Now we try to cancel the operation via the operation token. Even though the worker has been replaced,
	// this should still work.
	require.NoError(t, result.Pending.Cancel(ctx, nexus.CancelOperationOptions{}))
	require.ErrorContains(t, tc.client.GetWorkflow(ctx, id, "").Get(ctx, nil), "canceled")
}

// dynamicRecorder records what Nexus inbound interceptors see for each call.
type dynamicRecorder struct {
	interceptor.WorkerInterceptorBase
	mu      sync.Mutex
	starts  []string
	cancels []string
}

type dynamicRecordingInbound struct {
	interceptor.NexusOperationInboundInterceptorBase
	root *dynamicRecorder
}

func (r *dynamicRecorder) InterceptNexusOperation(_ context.Context, next interceptor.NexusOperationInboundInterceptor) interceptor.NexusOperationInboundInterceptor {
	return &dynamicRecordingInbound{
		NexusOperationInboundInterceptorBase: interceptor.NexusOperationInboundInterceptorBase{Next: next},
		root:                                 r,
	}
}

func (i *dynamicRecordingInbound) StartOperation(ctx context.Context, input interceptor.NexusStartOperationInput) (nexus.HandlerStartOperationResult[any], error) {
	i.root.mu.Lock()
	i.root.starts = append(i.root.starts, fmt.Sprintf("%s:%s", nexus.ExtractHandlerInfo(ctx).Operation, reflect.TypeOf(input.Input)))
	i.root.mu.Unlock()
	return i.Next.StartOperation(ctx, input)
}

func (i *dynamicRecordingInbound) CancelOperation(ctx context.Context, input interceptor.NexusCancelOperationInput) error {
	i.root.mu.Lock()
	i.root.cancels = append(i.root.cancels, nexus.ExtractHandlerInfo(ctx).Operation)
	i.root.mu.Unlock()
	return i.Next.CancelOperation(ctx, input)
}

// TestNexusDynamicWorkflowCaller:
//  1. Registers a named "list_tools" operation and a dynamic handler for echo operations.
//  2. Starts a Worker with the service, caller and echo Workflows, and a recording interceptor.
//  3. Runs the caller Workflow with "list_tools" and check that it receives "manifest".
//  4. Calls "echo_sync" and check the inline result, then call "echo_workflow" and check
//     the result returned after its backing Workflow completes.
//  5. Calls "missing" and check that the caller receives the fallback's NOT_FOUND error.
//  6. Checks that the interceptor saw decoded string input for the named operation and
//     raw *nexus.LazyValue input for each operation sent to the dynamic handler.
//
// This validates named-operation precedence, synchronous and Workflow-backed dynamic
// results, error propagation, and the input passed to Nexus inbound interceptors.
func TestNexusDynamicWorkflowCaller(t *testing.T) {
	skipOnCloud(t, cloudRequiresLocalServer, "creates a Nexus endpoint")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tc := newTestContext(t, ctx)
	echoWorkflow := func(_ workflow.Context, value string) (string, error) {
		return value, nil
	}
	service := nexus.NewService("tools")
	service.MustRegister(nexus.NewSyncOperation("list_tools", func(context.Context, string, nexus.StartOperationOptions) (string, error) {
		return "manifest", nil
	}))
	require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
		temporalnexus.DynamicTemporalOperationOptions{
			Start: func(ctx context.Context, nc temporalnexus.NexusClient, input *nexus.LazyValue, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
				var value string
				if err := input.Consume(&value); err != nil {
					return temporalnexus.TemporalOperationResult[any]{}, err
				}
				switch nexus.ExtractHandlerInfo(ctx).Operation {
				case "echo_sync":
					return temporalnexus.NewSyncResult[any]("sync:" + value), nil
				case "echo_workflow":
					return temporalnexus.StartUntypedWorkflow[any](ctx, nc, client.StartWorkflowOptions{ID: uuid.NewString()}, echoWorkflow, value)
				}
				return temporalnexus.TemporalOperationResult[any]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeNotFound, "unknown tool")
			},
		},
	)))
	callerWorkflow := func(ctx workflow.Context, operation, value string) (string, error) {
		var result string
		err := workflow.NewNexusClient(tc.endpoint, "tools").
			ExecuteOperation(ctx, operation, value, workflow.NexusOperationOptions{}).
			Get(ctx, &result)
		return result, err
	}
	recorder := &dynamicRecorder{}
	w := worker.New(tc.client, tc.taskQueue, worker.Options{Interceptors: []interceptor.WorkerInterceptor{recorder}})
	w.RegisterNexusService(service)
	w.RegisterWorkflow(echoWorkflow)
	w.RegisterWorkflow(callerWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	for _, testCase := range []struct {
		operation    string
		want         string
		inputType    string
		wantNotFound bool
	}{
		{operation: "list_tools", want: "manifest", inputType: "string"},
		{operation: "echo_sync", want: "sync:hello", inputType: "*nexus.LazyValue"},
		{operation: "echo_workflow", want: "hello", inputType: "*nexus.LazyValue"},
		{operation: "missing", inputType: "*nexus.LazyValue", wantNotFound: true},
	} {
		t.Run(testCase.operation, func(t *testing.T) {
			run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: tc.taskQueue}, callerWorkflow, testCase.operation, "hello")
			require.NoError(t, err)
			var got string
			err = run.Get(ctx, &got)
			if testCase.wantNotFound {
				var handlerErr *nexus.HandlerError
				require.ErrorAs(t, err, &handlerErr)
				require.Equal(t, nexus.HandlerErrorTypeNotFound, handlerErr.Type)
			} else {
				require.NoError(t, err)
				require.Equal(t, testCase.want, got)
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			require.Contains(t, recorder.starts, testCase.operation+":"+testCase.inputType)
		})
	}
}

// TestNexusDynamicActivityCancel does:
//  1. Start a Worker with a dynamic handler that starts a heartbeating standalone Activity
//     and an interceptor that records Nexus start and cancel requests.
//  2. Call "wait_activity" through Nexus and check that it returns a pending operation.
//  3. Cancel that operation using its pending handle.
//  4. Wait until the backing Activity's status becomes CANCELED.
//  5. Check that the interceptor recorded the start with raw *nexus.LazyValue input
//     and the cancel request, both under the original "wait_activity" name.
//
// This validates that the default token-based cancellation reaches a dynamic operation's
// backing Activity and that Nexus inbound interceptors run for both start and cancel.
func TestNexusDynamicActivityCancel(t *testing.T) {
	skipOnCloud(t, cloudRequiresLocalServer, "creates a Nexus endpoint")
	if os.Getenv("DISABLE_ACTIVITY_BACKED_NEXUS_TESTS") != "" {
		t.SkipNow()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tc := newTestContext(t, ctx)
	backingActivity := func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
				activity.RecordHeartbeat(ctx)
			}
		}
	}
	service := nexus.NewService("tools")
	require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
		temporalnexus.DynamicTemporalOperationOptions{
			Start: func(ctx context.Context, nc temporalnexus.NexusClient, input *nexus.LazyValue, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
				var id string
				if err := input.Consume(&id); err != nil {
					return temporalnexus.TemporalOperationResult[any]{}, err
				}
				return temporalnexus.StartUntypedActivity[any](ctx, nc, client.StartActivityOptions{
					ID:                  id,
					TaskQueue:           tc.taskQueue,
					StartToCloseTimeout: 30 * time.Second,
					HeartbeatTimeout:    time.Second,
				}, backingActivity)
			},
		},
	)))
	recorder := &dynamicRecorder{}
	w := worker.New(tc.client, tc.taskQueue, worker.Options{Interceptors: []interceptor.WorkerInterceptor{recorder}})
	w.RegisterNexusService(service)
	w.RegisterActivity(backingActivity)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	nc := tc.newNexusClient(t, service.Name)
	id := uuid.NewString()
	result, err := nexusclient.StartOperation(ctx, nc, nexus.NewOperationReference[string, any]("wait_activity"), id, nexus.StartOperationOptions{CallbackURL: "http://localhost/test"})
	require.NoError(t, err)
	require.NotNil(t, result.Pending)
	require.NoError(t, result.Pending.Cancel(ctx, nexus.CancelOperationOptions{}))

	handle := tc.client.GetActivityHandle(client.GetActivityHandleOptions{ActivityID: id})
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		desc, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		require.NoError(c, err)
		require.Equal(c, enumspb.ACTIVITY_EXECUTION_STATUS_CANCELED, desc.Status)
	}, 20*time.Second, 200*time.Millisecond)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Equal(t, []string{"wait_activity:*nexus.LazyValue"}, recorder.starts)
	require.Equal(t, []string{"wait_activity"}, recorder.cancels)
}

// TestNexusDynamicUpdateCustomCancel does:
//  1. Register a dynamic handler that starts a Workflow Update and a custom cancel
//     callback that records the identifiers it receives.
//  2. Start a target Workflow whose "block" Update waits indefinitely.
//  3. Call "block" through Nexus with the Workflow ID and keep the pending operation
//     returned after the Update is accepted.
//  4. Cancel the pending Nexus operation.
//  5. Check that the custom cancel callback receives the target Workflow ID and a
//     nonempty Update ID decoded from the operation token.
//
// This validates that a dynamic operation's Update token supplies the identifiers needed
// by the application's custom Workflow Update cancellation handler.
func TestNexusDynamicUpdateCustomCancel(t *testing.T) {
	skipOnCloud(t, cloudRequiresLocalServer, "creates a Nexus endpoint")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tc := newTestContext(t, ctx)
	backingWorkflow := func(ctx workflow.Context) error {
		if err := workflow.SetUpdateHandler(ctx, "block", func(ctx workflow.Context) error {
			return workflow.Await(ctx, func() bool { return false })
		}); err != nil {
			return err
		}
		return workflow.Await(ctx, func() bool { return false })
	}
	canceled := make(chan temporalnexus.CancelTemporalUpdateWorkflowOptions, 1)
	service := nexus.NewService("tools")
	require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
		temporalnexus.DynamicTemporalOperationOptions{
			Start: func(ctx context.Context, nc temporalnexus.NexusClient, input *nexus.LazyValue, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
				var workflowID string
				if err := input.Consume(&workflowID); err != nil {
					return temporalnexus.TemporalOperationResult[any]{}, err
				}
				return temporalnexus.StartUpdateWorkflow[any](ctx, nc, client.UpdateWorkflowOptions{
					WorkflowID:   workflowID,
					UpdateName:   "block",
					WaitForStage: client.WorkflowUpdateStageAccepted,
				})
			},
			CancelWorkflowUpdate: func(_ context.Context, _ client.Client, opts temporalnexus.CancelTemporalUpdateWorkflowOptions, _ nexus.CancelOperationOptions) error {
				canceled <- opts
				return nil
			},
		},
	)))
	w := worker.New(tc.client, tc.taskQueue, worker.Options{})
	w.RegisterNexusService(service)
	w.RegisterWorkflow(backingWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	workflowID := "dynamic-update-" + uuid.NewString()
	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: workflowID, TaskQueue: tc.taskQueue}, backingWorkflow)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tc.client.TerminateWorkflow(context.Background(), workflowID, run.GetRunID(), "test done") })

	nc := tc.newNexusClient(t, service.Name)
	result, err := nexusclient.StartOperation(ctx, nc, nexus.NewOperationReference[string, any]("block"), workflowID, nexus.StartOperationOptions{CallbackURL: "http://localhost/test"})
	require.NoError(t, err)
	require.NotNil(t, result.Pending)
	require.NoError(t, result.Pending.Cancel(ctx, nexus.CancelOperationOptions{}))
	select {
	case opts := <-canceled:
		require.Equal(t, workflowID, opts.WorkflowID)
		require.NotEmpty(t, opts.UpdateID)
	case <-ctx.Done():
		t.Fatal("custom Workflow Update cancel handler was not called")
	}
}
