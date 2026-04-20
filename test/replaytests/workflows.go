package replaytests

import (
	"context"
	"fmt"
	"go.temporal.io/sdk/converter"
	iconverter "go.temporal.io/sdk/internal/converter"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Workflow1 test workflow
func Workflow1(ctx workflow.Context, name string) error {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("helloworld workflow started")
	var helloworldResult string
	v := workflow.GetVersion(ctx, "test-change", workflow.DefaultVersion, 1)
	if v == workflow.DefaultVersion {
		err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", "Error", err)
			return err
		}
	} else {
		err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", "Error", err)
			return err
		}

		err = workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", "Error", err)
			return err
		}
	}

	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
	if err != nil {
		logger.Error("Activity failed.", "Error", err)
		return err
	}

	logger.Info("Workflow completed.", "Result", helloworldResult)

	return nil
}

// Workflow2 test workflow
func Workflow2(ctx workflow.Context, name string) error {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("helloworld workflow started")
	var helloworldResult string

	workflow.GetVersion(ctx, "test-change", workflow.DefaultVersion, 1)

	_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{"CustomKeywordField": "testkey"})

	workflow.GetVersion(ctx, "test-change-2", workflow.DefaultVersion, 1)

	workflow.GetVersion(ctx, "test-change-3", workflow.DefaultVersion, 1)

	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
	if err != nil {
		logger.Error("Activity failed.", "Error", err)
		return err
	}

	logger.Info("Workflow completed.", "Result", helloworldResult)

	return nil
}

func helloworldActivity(ctx context.Context, name string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("helloworld activity started")
	return "Hello " + name + "!", nil
}

// TimerWf starts a timer and always starts another timer at workflow end, even if cancelled
func TimerWf(ctx workflow.Context) error {
	defer func() {
		// Produce another timer after being cancelled
		newCtx, _ := workflow.NewDisconnectedContext(ctx)
		_ = workflow.NewTimer(newCtx, 10*time.Minute).Get(ctx, nil)
	}()
	return workflow.NewTimer(ctx, time.Minute*10).Get(ctx, nil)
}

func LocalActivityWorkflow(ctx workflow.Context, name string) error {
	ao := workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: time.Minute,
	}

	ctx = workflow.WithLocalActivityOptions(ctx, ao)
	var helloworldResult string
	return workflow.ExecuteLocalActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
}

func ContinueAsNewWorkflow(ctx workflow.Context, continueAsNew bool) error {
	if continueAsNew {
		return workflow.NewContinueAsNewError(ctx, ContinueAsNewWorkflow, false)
	}
	return nil
}

func UpsertMemoWorkflow(ctx workflow.Context, memo string) error {
	err := workflow.UpsertMemo(ctx, map[string]interface{}{
		"Test key": memo,
	})
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	err = workflow.ExecuteActivity(ctx, helloworldActivity, memo).Get(ctx, &memo)
	if err != nil {
		return err
	}

	return workflow.UpsertMemo(ctx, map[string]interface{}{
		"Test key": memo,
	})
}

func UpsertSearchAttributesWorkflow(ctx workflow.Context, field string) error {
	err := workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"CustomStringField": field,
	})
	if err != nil {
		return err
	}

	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	err = workflow.ExecuteActivity(ctx, helloworldActivity, field).Get(ctx, &field)
	if err != nil {
		return err
	}

	return workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"CustomStringField": field,
	})
}

func SideEffectWorkflow(ctx workflow.Context, field string) error {
	encodedRandom := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return rand.Intn(100)
	})

	var random int
	err := encodedRandom.Get(&random)
	if err != nil {
		return err
	}

	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	err = workflow.ExecuteActivity(ctx, helloworldActivity, field).Get(ctx, &field)
	if err != nil {
		return err
	}

	return encodedRandom.Get(&random)
}

func EmptyWorkflow(ctx workflow.Context, _ string) error {
	return nil
}

func DeadlockedWorkflow(ctx workflow.Context, _ string) error {
	// Sleep for just over 1 second to trigger deadlock detection
	time.Sleep(1100 * time.Millisecond)
	return nil
}

func MutableSideEffectWorkflow(ctx workflow.Context) ([]int, error) {
	f := func(retVal int) (newVal int) {
		err := workflow.MutableSideEffect(
			ctx,
			"side-effect-1",
			func(ctx workflow.Context) interface{} { return retVal },
			func(a, b interface{}) bool { return a.(int) == b.(int) },
		).Get(&newVal)
		if err != nil {
			panic(err)
		}
		return
	}
	results := []int{f(0)}
	results = append(results, f(0))
	results = append(results, f(0))
	results = append(results, f(1))
	results = append(results, f(1))
	results = append(results, f(2))
	err := workflow.Sleep(ctx, time.Second)
	if err != nil {
		return nil, err
	}
	results = append(results, f(3))
	results = append(results, f(3))
	results = append(results, f(4))
	err = workflow.Sleep(ctx, time.Second)
	if err != nil {
		return nil, err
	}
	results = append(results, f(4))
	results = append(results, f(5))

	return results, nil
}

func VersionLoopWorkflow(ctx workflow.Context, changeID string, iterations int) error {
	for i := 0; i < iterations; i++ {
		workflow.GetVersion(ctx, fmt.Sprintf("%s:%d", changeID, i), workflow.DefaultVersion, 1)
	}
	return workflow.Sleep(ctx, time.Second)
}

func VersionLoopWorkflowMultipleTasks(ctx workflow.Context, changeID string, iterations int) error {
	for i := 0; i < iterations; i++ {
		workflow.GetVersion(ctx, fmt.Sprintf("%s:%d", changeID, i), workflow.DefaultVersion, 1)
		err := workflow.Sleep(ctx, time.Millisecond)
		if err != nil {
			return err
		}
	}
	return nil
}

func ChildWorkflowWaitOnSignal(ctx workflow.Context) error {
	workflow.GetSignalChannel(ctx, "unblock").Receive(ctx, nil)
	return nil
}

func DuplicateChildWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: "ABC-SIMPLE-CHILD-WORKFLOW-ID",
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)

	child1 := workflow.ExecuteChildWorkflow(childCtx, ChildWorkflowWaitOnSignal)
	var childWE workflow.Execution
	err := child1.GetChildWorkflowExecution().Get(ctx, &childWE)
	if err != nil {
		return err
	}

	duplicateChildWFFuture := workflow.ExecuteChildWorkflow(childCtx, ChildWorkflowWaitOnSignal)
	selector := workflow.NewSelector(ctx)
	selector.AddFuture(duplicateChildWFFuture, func(f workflow.Future) {
		logger.Info("child workflow is ready")
		err = f.Get(ctx, nil)
		if _, ok := err.(*temporal.ChildWorkflowExecutionAlreadyStartedError); !ok {
			panic("Second child must fail to start as duplicate")
		}
		err = workflow.Sleep(ctx, time.Second)
	}).AddFuture(duplicateChildWFFuture.GetChildWorkflowExecution(), func(f workflow.Future) {
		logger.Info("child workflow execution is ready")
		err = f.Get(ctx, nil)
		if _, ok := err.(*temporal.ChildWorkflowExecutionAlreadyStartedError); !ok {
			panic("Second child must fail to start as duplicate")
		}
		ao := workflow.ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		field := "hello"
		err = workflow.ExecuteActivity(ctx, helloworldActivity, field).Get(ctx, &field)
	}).AddDefault(func() {
		err = workflow.Sleep(ctx, time.Second)
	})
	for i := 0; i < 2; i++ {
		selector.Select(ctx)
		if err != nil {
			return err
		}
	}

	workflow.SignalExternalWorkflow(ctx, childWE.ID, childWE.RunID, "unblock", nil)
	if err != nil {
		return err
	}
	err = child1.Get(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func UpdateWorkflow(ctx workflow.Context) error {
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context, d time.Duration) error {
			return workflow.Sleep(ctx, d)
		}); err != nil {
		return err
	}
	workflow.GetSignalChannel(ctx, "shutdown").Receive(ctx, nil)
	return nil
}

func UpdateAndExit(ctx workflow.Context) error {
	ch := workflow.NewChannel(ctx)
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context, d time.Duration) error {
			// passing a non-zero duration here controls whether the update is
			// accepted+completed in the same WFT or accepted in one WFT and
			// completed in a subsquent task.
			if d != time.Duration(0) {
				_ = workflow.Sleep(ctx, d)
			}
			ch.Close()
			return nil
		}); err != nil {
		return err
	}

	// by waiting on a channel that is closed by a call to update we ensure that
	// the update completion and workflow completion commands occur on the same
	// WFT completion.
	ch.Receive(ctx, nil)
	return ctx.Err()
}

func NonDeterministicUpdate(ctx workflow.Context) error {
	ch := workflow.NewChannel(ctx)
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context) error {
			// The workflow.Sleep below was not commented out when the json
			// history was generated. By commenting it out we make the update
			// code non-deterministic.
			//
			//_ = workflow.Sleep(ctx, 1*time.Second)

			ch.Close()
			return nil
		}); err != nil {
		return err
	}
	ch.Receive(ctx, nil)
	return ctx.Err()
}

func VersionAndMutableSideEffectWorkflow(ctx workflow.Context, name string) (string, error) {
	uid := ""
	logger := workflow.GetLogger(ctx)

	v := workflow.GetVersion(ctx, "mutable-side-effect-bug", workflow.DefaultVersion, 1)
	if v == 1 {
		var err error
		uid, err = generateUUID(ctx, "generate-random-uuid")
		if err != nil {
			logger.Error("failed to generated uuid", "Error", err)
			return "", err
		}

		logger.Info("generated uuid", "uuid-val", uid)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result string
	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &result)
	if err != nil {
		logger.Error("Activity failed.", "Error", err)
		return "", err
	}
	return uid, nil
}

func generateUUID(ctx workflow.Context, sideEffectID string) (string, error) {
	var generatedUUID string

	err := workflow.MutableSideEffect(ctx, sideEffectID, func(ctx workflow.Context) interface{} {
		return uuid.NewString()
	}, func(a, b interface{}) bool {
		return a.(string) == b.(string)
	}).Get(&generatedUUID)
	if err != nil {
		return "", err
	}

	return generatedUUID, nil
}

func CancelOrderSelectWorkflow(ctx workflow.Context) error {
	timerf := workflow.NewTimer(ctx, 5*time.Minute)

	var err error
	disCtx, _ := workflow.NewDisconnectedContext(ctx)
	selector := workflow.NewSelector(ctx)

	selector.AddFuture(timerf, func(f workflow.Future) {
		err = timerf.Get(ctx, nil)
		// do something different on cancel error
		if !temporal.IsCanceledError(err) {
			_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{"CustomKeywordField": "testkey"})
		} else {
			var result string
			ao := workflow.ActivityOptions{
				ScheduleToStartTimeout: time.Minute,
				StartToCloseTimeout:    time.Minute,
				HeartbeatTimeout:       time.Second * 20,
			}
			disCtx = workflow.WithActivityOptions(disCtx, ao)
			err = workflow.ExecuteActivity(disCtx, helloworldActivity, "world").Get(ctx, &result)
		}

	})
	selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, nil)
		err = workflow.Sleep(disCtx, 1*time.Second)

	})
	selector.Select(ctx)
	return err
}

func ChildWorkflowCancelWithUpdate(ctx workflow.Context) error {
	if err := workflow.SetUpdateHandler(ctx, "update",
		func(ctx workflow.Context) error {
			activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: 5 * time.Second,
			})

			return workflow.ExecuteActivity(activityCtx, helloworldActivity, "world", time.Second).Get(ctx, nil)
		}); err != nil {
		return err
	}
	childCtx, cancel := workflow.WithCancel(ctx)

	childCtx = workflow.WithChildOptions(childCtx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Second * 30,
	})
	childFut := workflow.ExecuteChildWorkflow(childCtx, ChildWorkflowWaitOnSignal)
	cancel()
	_ = childFut.GetChildWorkflowExecution().Get(ctx, nil)

	workflow.GetSignalChannel(ctx, "shutdown").Receive(ctx, nil)
	return nil
}

func MultipleUpdateWorkflow(ctx workflow.Context) (int, error) {
	inflightUpdates := 0
	updatesRan := 0
	sleepHandle := func(ctx workflow.Context) error {
		inflightUpdates++
		updatesRan++
		defer func() {
			inflightUpdates--
		}()
		return workflow.Sleep(ctx, time.Second)
	}
	echoHandle := func(ctx workflow.Context) error {
		inflightUpdates++
		updatesRan++
		defer func() {
			inflightUpdates--
		}()

		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			ScheduleToStartTimeout: 5 * time.Second,
			ScheduleToCloseTimeout: 5 * time.Second,
			StartToCloseTimeout:    9 * time.Second,
		})
		return workflow.ExecuteActivity(ctx, "Echo", 1, 1).Get(ctx, nil)
	}
	emptyHandle := func(ctx workflow.Context) error {
		inflightUpdates++
		updatesRan++
		defer func() {
			inflightUpdates--
		}()
		return ctx.Err()
	}
	// Register multiple update handles in the first workflow task to make sure we process an
	// update only when its handle is registered, not when any handle is registered
	workflow.SetUpdateHandler(ctx, "echo", echoHandle)
	workflow.SetUpdateHandler(ctx, "sleep", sleepHandle)
	workflow.SetUpdateHandler(ctx, "empty", emptyHandle)
	err := workflow.Await(ctx, func() bool { return inflightUpdates == 0 })
	if err != nil {
		return 0, err
	}
	return updatesRan, nil
}

func CounterWorkflow(ctx workflow.Context) (int, error) {
	counter := 0

	if err := workflow.SetUpdateHandlerWithOptions(
		ctx,
		"fetch_and_add",
		func(ctx workflow.Context, i int) (int, error) {
			tmp := counter
			counter += i
			_ = workflow.Sleep(ctx, 1*time.Second)
			return tmp, nil
		},
		workflow.UpdateHandlerOptions{Validator: nonNegative},
	); err != nil {
		return 0, err
	}

	_ = workflow.GetSignalChannel(ctx, "done").Receive(ctx, nil)
	return counter, ctx.Err()
}

func nonNegative(ctx workflow.Context, i int) error {
	if i < 0 {
		return fmt.Errorf("addend must be non-negative (%v)", i)
	}
	return nil
}

func ListAndDescribeWorkflow(ctx workflow.Context) (int, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result workflowservice.ListWorkflowExecutionsResponse
	err := workflow.ExecuteActivity(ctx, "ListWorkflow").Get(ctx, &result)
	if err != nil {
		return 0, err
	}
	for _, execution := range result.Executions {
		if execution.Status == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			err := workflow.Sleep(ctx, 1*time.Second)
			if err != nil {
				return 0, err
			}
		} else {
			var wf workflowservice.DescribeWorkflowExecutionResponse
			err := workflow.ExecuteActivity(ctx, "DescribeWorkflowExecution", execution.GetExecution().WorkflowId).Get(ctx, &wf)
			if err != nil {
				return 0, err
			}
			if wf.ExecutionConfig.WorkflowExecutionTimeout != nil {
				err = workflow.Sleep(ctx, time.Second)
				if err != nil {
					return 0, err
				}
			}
		}
	}
	return len(result.Executions), nil
}

func SelectorBlockingDefaultWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	ch1 := workflow.NewChannel(ctx)
	ch2 := workflow.NewChannel(ctx)

	workflow.Go(ctx, func(ctx workflow.Context) {
		ch1.Send(ctx, "one")

	})

	workflow.Go(ctx, func(ctx workflow.Context) {
		ch2.Send(ctx, "two")
	})

	selector := workflow.NewSelector(ctx)
	var s string
	selector.AddReceive(ch1, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &s)
	})
	selector.AddDefault(func() {
		ch2.Receive(ctx, &s)
	})
	selector.Select(ctx)
	if selector.HasPending() {
		var result string
		activity := workflow.ExecuteActivity(ctx, SelectorBlockingDefaultActivity, "Signal not lost")
		activity.Get(ctx, &result)
		logger.Info("Result", result)
	} else {
		logger.Info("Signal in ch1 lost")
		return nil
	}
	return nil
}

func SelectorBlockingDefaultActivity(ctx context.Context, value string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Activity", "value", value)
	return value + " was logged!", nil
}

func TripWorkflow(ctx workflow.Context, tripCounter int) error {
	logger := workflow.GetLogger(ctx)
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	logger.Info("Trip Workflow Started for User.",
		"User", workflowID,
		"TripCounter", tripCounter)

	// TripCh to wait on trip completed event signals
	tripCh := workflow.GetSignalChannel(ctx, "trip_event")
	for i := 0; i < 10; i++ {
		var trip int
		tripCh.Receive(ctx, &trip)
		logger.Info("Trip complete event received.", "Total", trip)
		tripCounter++
	}

	logger.Info("Starting a new run.", "TripCounter", tripCounter)
	return workflow.NewContinueAsNewError(ctx, "TripWorkflow", tripCounter)
}

// TestWorkflowWithChild is a test workflow that executes a child workflow and returns the result from it.
func ResetWorkflowWithChild(ctx workflow.Context) (string, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("Starting workflow with child...")
	cwo := workflow.ChildWorkflowOptions{
		ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_TERMINATE,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}
	ctx = workflow.WithChildOptions(ctx, cwo)
	child := workflow.ExecuteChildWorkflow(ctx, "TestChildWorkflow", "CHILD INPUT")

	var result string
	if err := child.Get(ctx, &result); err != nil {
		logger.Error("Child execution failed: " + err.Error())
		return "", err
	}

	logger.Info("Child execution completed with result: " + result)
	return result, nil
}

func NexusCancelHandlerWorkflow(ctx workflow.Context, action string) (nexus.NoValue, error) {
	if action == "succeed" {
		return nil, nil
	}
	return nil, workflow.Await(ctx, func() bool { return false })
}

var CancelOp = temporalnexus.NewWorkflowRunOperation(
	"wait-on-signal-op",
	NexusCancelHandlerWorkflow,
	func(ctx context.Context, action string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
		if action == "delay-start" {
			time.Sleep(1 * time.Second)
		}
		return client.StartWorkflowOptions{
			ID: "nexus-handler-wait-for-cancel",
		}, nil
	},
)

func CancelNexusOperationBeforeSentWorkflow(ctx workflow.Context) (string, error) {
	nc := workflow.NewNexusClient("replay-endpoint", "replay-service")
	opCtx, cancel := workflow.NewDisconnectedContext(ctx)
	cancel()
	op := nc.ExecuteOperation(opCtx, CancelOp, "fail-to-send", workflow.NexusOperationOptions{})
	_ = op.Get(ctx, nil)
	return generateUUID(ctx, "nxs-cancel-before-sent-id")
}

func CancelNexusOperationBeforeStartWorkflow(ctx workflow.Context) (string, error) {
	nc := workflow.NewNexusClient("replay-endpoint", "replay-service")
	opCtx, cancel := workflow.NewDisconnectedContext(ctx)
	op := nc.ExecuteOperation(opCtx, CancelOp, "delay-start", workflow.NexusOperationOptions{})
	if err := workflow.Sleep(ctx, 200*time.Millisecond); err != nil {
		// Wait for scheduled event to be recorded
		return "", err
	}
	cancel()
	_ = op.Get(ctx, nil)
	return generateUUID(ctx, "nxs-cancel-before-start-id")
}

func CancelNexusOperationAfterStartWorkflow(ctx workflow.Context) (string, error) {
	nc := workflow.NewNexusClient("replay-endpoint", "replay-service")
	opCtx, cancel := workflow.WithCancel(ctx)
	op := nc.ExecuteOperation(opCtx, CancelOp, "wait-for-cancel", workflow.NexusOperationOptions{})
	if err := op.GetNexusOperationExecution().Get(opCtx, nil); err != nil {
		return "", err
	}
	cancel()
	_ = op.Get(ctx, nil)
	return generateUUID(ctx, "nxs-cancel-after-start-id")
}

func CancelNexusOperationAfterCompleteWorkflow(ctx workflow.Context) (string, error) {
	nc := workflow.NewNexusClient("replay-endpoint", "replay-service")
	opCtx, cancel := workflow.WithCancel(ctx)
	op := nc.ExecuteOperation(opCtx, CancelOp, "succeed", workflow.NexusOperationOptions{})
	_ = op.Get(opCtx, nil)
	cancel()
	return generateUUID(ctx, "nxs-cancel-after-complete-id")
}

// AwaitWithTimeoutNoTimerCancelWorkflow is used to test replay of old workflow histories
// that were created before SDKFlagCancelAwaitTimerOnCondition was introduced.
// In the old behavior, the timer is NOT cancelled when the condition becomes true.
func AwaitWithTimeoutNoTimerCancelWorkflow(ctx workflow.Context) (bool, error) {
	conditionMet := false

	workflow.Go(ctx, func(ctx workflow.Context) {
		_ = workflow.Sleep(ctx, 100*time.Millisecond)
		conditionMet = true
	})

	return workflow.AwaitWithTimeout(ctx, 10*time.Second, func() bool {
		return conditionMet
	})
}

func MemoChildWorkflowGob(ctx workflow.Context, input string) (string, error) {
	info := workflow.GetInfo(ctx)

	memoPayload, ok := info.Memo.Fields["test-memo-key"]
	if !ok {
		return "", fmt.Errorf("memo key 'test-memo-key' not found")
	}

	// Use strict gob converter - will fail if memo is JSON-encoded
	var memoValue string
	dc := iconverter.NewTestDataConverter()
	err := dc.FromPayload(memoPayload, &memoValue)
	if err != nil {
		return "", fmt.Errorf("failed to decode memo with gob converter: %w", err)
	}

	return fmt.Sprintf("child-read-memo: %s", memoValue), nil
}

func MemoEncodingWorkflowGob(ctx workflow.Context, memoValue string) (string, error) {
	// Execute a child workflow with memo
	cwo := workflow.ChildWorkflowOptions{
		Memo: map[string]interface{}{
			"test-memo-key": memoValue,
		},
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)

	var childResult string
	err := workflow.ExecuteChildWorkflow(childCtx, MemoChildWorkflowGob, memoValue).Get(ctx, &childResult)
	if err != nil {
		return "", err
	}

	// Also upsert memo in the parent workflow
	err = workflow.UpsertMemo(ctx, map[string]interface{}{
		"parent-memo-key": "parent-" + memoValue,
	})
	if err != nil {
		return "", err
	}

	info := workflow.GetInfo(ctx)
	memoPayload, ok := info.Memo.Fields["parent-memo-key"]
	if !ok {
		return "", fmt.Errorf("memo key 'parent-memo-key' not found")
	}

	// Use strict gob converter - will fail if memo is JSON-encoded
	var parentMemoValue string
	dc := iconverter.NewTestDataConverter()
	err = dc.FromPayload(memoPayload, &parentMemoValue)
	if err != nil {
		return "", fmt.Errorf("failed to decode memo with gob converter: %w", err)
	}

	return childResult, nil
}

func MemoChildWorkflowJSON(ctx workflow.Context) (string, error) {
	info := workflow.GetInfo(ctx)

	memoPayload, ok := info.Memo.Fields["test-memo-key"]
	if !ok {
		return "", fmt.Errorf("memo key 'test-memo-key' not found")
	}

	// will fail if memo is gob-encoded
	var memoValue string
	dc := converter.NewJSONPayloadConverter()
	err := dc.FromPayload(memoPayload, &memoValue)
	if err != nil {
		return "", fmt.Errorf("failed to decode memo with JSON converter: %w", err)
	}

	return fmt.Sprintf("child-read-memo: %s", memoValue), nil
}

func MemoEncodingWorkflowJSON(ctx workflow.Context, memoValue string) (string, error) {
	cwo := workflow.ChildWorkflowOptions{
		Memo: map[string]interface{}{
			"test-memo-key": memoValue,
		},
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)

	var childResult string
	err := workflow.ExecuteChildWorkflow(childCtx, MemoChildWorkflowJSON).Get(ctx, &childResult)
	if err != nil {
		return "", err
	}

	// Also upsert memo in the parent workflow
	err = workflow.UpsertMemo(ctx, map[string]interface{}{
		"parent-memo-key": "parent-" + memoValue,
	})
	if err != nil {
		return "", err
	}

	info := workflow.GetInfo(ctx)

	memoPayload, ok := info.Memo.Fields["parent-memo-key"]
	if !ok {
		return "", fmt.Errorf("memo key 'parent-memo-key' not found")
	}

	// will fail if memo is gob-encoded
	var parentMemoValue string
	dc := converter.NewJSONPayloadConverter()
	err = dc.FromPayload(memoPayload, &parentMemoValue)
	if err != nil {
		return "", fmt.Errorf("failed to decode memo with JSON converter: %w", err)
	}

	return childResult, nil
}

// ScheduleMemoWorkflowJSON is a workflow that validates memo passed from a schedule
// can be decoded with JSON. This is used to test backward compatibility for
// workflows started by schedules before the SDKFlagMemoUserDCEncode flag.
func ScheduleMemoWorkflowJSON(ctx workflow.Context) (string, error) {
	info := workflow.GetInfo(ctx)

	memoPayload, ok := info.Memo.Fields["schedule-memo-key"]
	if !ok {
		return "", fmt.Errorf("memo key 'schedule-memo-key' not found")
	}

	// will fail if memo is gob-encoded
	var memoValue string
	dc := converter.NewJSONPayloadConverter()
	err := dc.FromPayload(memoPayload, &memoValue)
	if err != nil {
		return "", fmt.Errorf("failed to decode memo with JSON converter: %w", err)
	}

	return fmt.Sprintf("schedule-read-memo: %s", memoValue), nil
}

func ChannelWorkerWorkflow(ctx workflow.Context) error {
	ch := workflow.NewChannel(ctx)
	var received []string

	workflow.Go(ctx, func(ctx workflow.Context) {
		ch.Send(ctx, "item-1")
		ch.Send(ctx, "item-2")
		ch.Close()
	})

	wg := workflow.NewWaitGroup(ctx)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		workflow.Go(ctx, func(ctx workflow.Context) {
			defer wg.Done()
			for {
				selector := workflow.NewSelector(ctx)
				done := false
				selector.AddReceive(ch, func(c workflow.ReceiveChannel, more bool) {
					if more {
						var v string
						c.Receive(ctx, &v)
						received = append(received, v)
					} else {
						done = true
					}
				})
				selector.Select(ctx)
				if done {
					break
				}
			}
		})
	}

	wg.Wait(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	for _, item := range received {
		if item == "" {
			continue
		}
		if err := workflow.ExecuteActivity(ctx, helloworldActivity, item).Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}
