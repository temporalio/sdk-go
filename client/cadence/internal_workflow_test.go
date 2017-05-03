package cadence

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type testContext struct {
}

type helloWorldWorklfow struct {
	t *testing.T
}

type callbackHandlerWrapTest struct {
	handler resultHandler
	result  []byte
	err     error
}

type asyncTestCallbackProcessor struct {
	callbackCh         chan callbackHandlerWrapTest
	eventLoopCallback  func()
	waitProcCompleteCh chan struct{}
}

func newAsyncTestCallbackProcessor(eventLoopCallback func()) *asyncTestCallbackProcessor {
	return &asyncTestCallbackProcessor{
		callbackCh:         make(chan callbackHandlerWrapTest, 10),
		waitProcCompleteCh: make(chan struct{}),
		eventLoopCallback:  eventLoopCallback,
	}
}

func (ac *asyncTestCallbackProcessor) ProcessOrWait(waitForComplete <-chan struct{}) bool {
	for {
		select {
		case c := <-ac.callbackCh:
			time.Sleep(time.Millisecond)
			c.handler(c.result, c.err)
			// Run all available callbacks
		runCallbacks:
			for {
				select {
				case c1 := <-ac.callbackCh:
					time.Sleep(time.Millisecond)
					c1.handler(c1.result, c1.err)
				default:
					break runCallbacks
				}
			}
			ac.eventLoopCallback()
		case <-waitForComplete:
			return true

		case <-time.After(2 * time.Second):
			fmt.Println("timeout 2 second")
			return false
		}
	}
}

func (ac *asyncTestCallbackProcessor) Add(cb resultHandler, result []byte, err error) {
	ac.callbackCh <- callbackHandlerWrapTest{handler: cb, result: result, err: err}
}

func (w *helloWorldWorklfow) Execute(ctx Context, input []byte) (result []byte, err error) {
	return []byte(string(input) + " World!"), nil
}

func TestHelloWorldWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&helloWorldWorklfow{t: t})
	ctx := &mockWorkflowEnvironment{}
	ctx.On("Complete", []byte("Hello World!"), nil).Return().Once()
	w.Execute(ctx, []byte("Hello"))
}

type helloWorldActivityWorkflow struct {
	t *testing.T
}

func (w *helloWorldActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	ctx = WithActivityOptions(ctx, NewActivityOptions().
		WithScheduleToStartTimeout(10*time.Second).
		WithStartToCloseTimeout(5*time.Second).
		WithScheduleToCloseTimeout(10*time.Second))
	ctx1 := WithActivityOptions(ctx, NewActivityOptions().(*activityOptions).WithActivityID("id1"))
	f := ExecuteActivity(ctx1, "testAct", input)
	var r1 []byte
	err = f.Get(ctx, &r1)
	if err != nil {
		fmt.Printf("Error: %v \n", err.Error())
	}
	require.NoError(w.t, err)
	return r1, nil
}

type resultHandlerMatcher struct {
	resultHandler resultHandler
}

func (m *resultHandlerMatcher) Matches(x interface{}) bool {
	m.resultHandler = x.(resultHandler)
	return true
}

func (m *resultHandlerMatcher) String() string {
	return "ResultHandlerMatcher"
}

func TestSingleActivityWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&helloWorldActivityWorkflow{t: t})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor(w.OnDecisionTaskStarted)

	ctx.On("Complete", mock.Anything, nil).Return().Run(func(args mock.Arguments) {
		//result := args.Get(0).([]byte)
		//require.Contains(t, string(result), "Hello World!")
		workflowComplete <- struct{}{}
	}).Once()
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		parameters := args.Get(0).(executeActivityParameters)
		callback := args.Get(1).(resultHandler)
		result := string(parameters.Input) + " World!"
		cbProcessor.Add(callback, []byte(result), nil)
	})
	w.Execute(ctx, []byte("Hello"))
	w.OnDecisionTaskStarted()
	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)
}

type splitJoinActivityWorkflow struct {
	t     *testing.T
	panic bool
}

func (w *splitJoinActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	var result1, result2 []byte
	var err1, err2 error

	ctx = WithActivityOptions(ctx, NewActivityOptions().
		WithScheduleToStartTimeout(10*time.Second).
		WithStartToCloseTimeout(5*time.Second).
		WithScheduleToCloseTimeout(10*time.Second))

	c1 := NewChannel(ctx)
	c2 := NewChannel(ctx)
	Go(ctx, func(ctx Context) {
		ctx1 := WithActivityOptions(ctx, NewActivityOptions().(*activityOptions).WithActivityID("id1"))
		f := ExecuteActivity(ctx1, "testAct")
		err1 = f.Get(ctx, &result1)
		require.NoError(w.t, err1, err1)
		c1.Send(ctx, true)
	})
	Go(ctx, func(ctx Context) {
		ctx2 := WithActivityOptions(ctx, NewActivityOptions().(*activityOptions).WithActivityID("id2"))
		f := ExecuteActivity(ctx2, "testAct")
		err1 := f.Get(ctx, &result2)
		require.NoError(w.t, err1, err1)
		if w.panic {
			panic("simulated")
		}
		fmt.Println("Before c2.Send")

		c2.Send(ctx, true)
		fmt.Println("After c2.Send")
	})

	c1.Receive(ctx)
	// Use selector to test it
	selected := false
	NewSelector(ctx).AddReceiveWithMoreFlag(c2, func(v interface{}, more bool) {
		require.True(w.t, more)
		selected = true
	}).Select(ctx)
	require.True(w.t, selected)
	require.NoError(w.t, err1)
	require.NoError(w.t, err2)

	return []byte(string(result1) + string(result2)), nil
}

func TestSplitJoinActivityWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&splitJoinActivityWorkflow{t: t})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor(w.OnDecisionTaskStarted)

	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		parameters := args.Get(0).(executeActivityParameters)
		callback := args.Get(1).(resultHandler)
		switch *parameters.ActivityID {
		case "id1":
			cbProcessor.Add(callback, testEncodeFunctionResult([]byte("Hello")), nil)
		case "id2":
			cbProcessor.Add(callback, testEncodeFunctionResult([]byte(" Flow!")), nil)
		default:
			panic(fmt.Sprintf("Unexpected activityID: %v", *parameters.ActivityID))
		}
	}).Twice()

	ctx.On("Complete", []byte("Hello Flow!"), nil).Return().Run(func(args mock.Arguments) {
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte(""))
	w.OnDecisionTaskStarted()
	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)
}

func TestWorkflowPanic(t *testing.T) {
	w := newWorkflowDefinition(&splitJoinActivityWorkflow{t: t, panic: true})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor(w.OnDecisionTaskStarted)

	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		callback := args.Get(1).(resultHandler)
		cbProcessor.Add(callback, []byte("test"), nil)
	}).Twice()
	ctx.On("Complete", []byte(nil), mock.Anything).Return().Run(func(args mock.Arguments) {
		resultErr := args.Get(1).(ErrorWithDetails)
		require.EqualValues(t, "simulated", resultErr.Reason())
		var details []byte
		resultErr.Details(&details)
		require.Contains(t, string(details), "cadence.(*splitJoinActivityWorkflow).Execute")
		workflowComplete <- struct{}{}
	}).Once()
	ctx.On("GetLogger").Return(zap.NewNop())

	w.Execute(ctx, []byte("Hello"))
	w.OnDecisionTaskStarted()

	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)
}

type testClockWorkflow struct {
	t *testing.T
}

func (w *testClockWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	c := Now(ctx)
	require.False(w.t, c.IsZero(), c)
	return []byte("workflow-completed"), nil
}

func TestClockWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&testClockWorkflow{t: t})
	ctx := &mockWorkflowEnvironment{}

	ctx.On("Now").Return(time.Now()).Once()
	ctx.On("Complete", []byte("workflow-completed"), nil).Return().Once()
	w.Execute(ctx, []byte("Hello"))
	w.OnDecisionTaskStarted()

	ctx.AssertExpectations(t)
}

type testTimerWorkflow struct {
	t *testing.T
}

func (w *testTimerWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {

	// Start a timer.
	t := NewTimer(ctx, 1)

	isWokeByTimer := false

	NewSelector(ctx).AddFuture(t, func(f Future) {
		err := f.Get(ctx, nil)
		require.NoError(w.t, err)
		isWokeByTimer = true
	}).Select(ctx)

	require.True(w.t, isWokeByTimer)

	// Start a timer and cancel it.
	ctx2, c2 := WithCancel(ctx)
	t2 := NewTimer(ctx2, 1)
	c2()
	err2 := t2.Get(ctx2, nil)

	require.Error(w.t, err2)
	_, isCancelErr := err2.(CanceledError)
	require.True(w.t, isCancelErr)

	// Sleep 1 sec
	ctx3, _ := WithCancel(ctx)
	err3 := Sleep(ctx3, 1)
	require.NoError(w.t, err3)

	// Sleep and cancel.
	ctx4, c4 := WithCancel(ctx)
	c4()
	err4 := Sleep(ctx4, 1)

	require.Error(w.t, err4)
	_, isCancelErr = err4.(CanceledError)
	require.True(w.t, isCancelErr)

	return []byte("workflow-completed"), nil
}

func TestTimerWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&testTimerWorkflow{t: t})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor(w.OnDecisionTaskStarted)

	newTimerCount := 0
	var callbackHandler2 resultHandler
	ctx.On("NewTimer", mock.Anything, mock.Anything, mock.Anything).Return(&timerInfo{timerID: "testTimer"}).Run(func(args mock.Arguments) {
		newTimerCount++
		callback := args.Get(1).(resultHandler)
		if newTimerCount == 1 || newTimerCount == 3 {
			cbProcessor.Add(callback, nil, nil)
		} else {
			callbackHandler2 = callback
		}
	}).Times(4)

	cancelTimerCount := 0
	ctx.On("RequestCancelTimer", mock.Anything).Return().Run(func(args mock.Arguments) {
		cancelTimerCount++
		cbProcessor.Add(callbackHandler2, nil, NewCanceledError())
	}).Twice()

	ctx.On("Complete", mock.Anything, nil).Return().Run(func(args mock.Arguments) {
		result := args.Get(0).([]byte)
		require.Equal(t, "workflow-completed", string(result))
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte("Hello"))
	w.OnDecisionTaskStarted()

	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)
}

type testActivityCancelWorkflow struct {
	t *testing.T
}

func (w *testActivityCancelWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	ctx = WithActivityOptions(ctx, NewActivityOptions().
		WithScheduleToStartTimeout(10*time.Second).
		WithStartToCloseTimeout(5*time.Second).
		WithScheduleToCloseTimeout(10*time.Second))

	// Sync cancellation
	ctx1, c1 := WithCancel(ctx)
	defer c1()
	ctx1 = WithActivityOptions(ctx1, NewActivityOptions().(*activityOptions).WithActivityID("id1"))
	f := ExecuteActivity(ctx1, "testAct")
	var res1 []byte
	err1 := f.Get(ctx, &res1)
	require.NoError(w.t, err1, err1)
	require.Equal(w.t, string(res1), "test")

	// Async Cancellation (Callback completes before cancel)
	ctx2, c2 := WithCancel(ctx)
	ctx2 = WithActivityOptions(ctx2, NewActivityOptions().(*activityOptions).WithActivityID("id2"))
	f = ExecuteActivity(ctx2, "testAct")
	c2()
	var res2 []byte
	err2 := f.Get(ctx, &res2)
	require.NotNil(w.t, res2)
	require.NoError(w.t, err2)

	// Async Cancellation (Callback doesn't complete)
	ctx3, c3 := WithCancel(ctx)
	ctx3 = WithActivityOptions(ctx3, NewActivityOptions().(*activityOptions).WithActivityID("id3"))
	f3 := ExecuteActivity(ctx3, "testAct")
	c3()
	var res3 []byte
	err3 := f3.Get(ctx, &res3)
	require.Nil(w.t, res3)
	var details []byte
	err3.(CanceledError).Details(&details)
	require.Equal(w.t, "testCancelDetails", string(details))

	return []byte("workflow-completed"), nil
}

func TestActivityCancelWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&testActivityCancelWorkflow{t: t})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	cbProcessor := newAsyncTestCallbackProcessor(w.OnDecisionTaskStarted)

	executeCount := 0
	var callbackHandler3 resultHandler
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(&activityInfo{activityID: "testAct"}).Run(func(args mock.Arguments) {
		executeCount++
		callback := args.Get(1).(resultHandler)
		if executeCount < 3 {
			cbProcessor.Add(callback, []byte("test"), nil)
		} else {
			callbackHandler3 = callback
		}
	}).Times(3)

	cancelCount := 0
	ctx.On("RequestCancelActivity", "testAct").Return().Run(func(args mock.Arguments) {
		cancelCount++
		if cancelCount == 2 { // Because of defer.
			cbProcessor.Add(
				callbackHandler3,
				nil,
				NewCanceledError([]byte("testCancelDetails")),
			)
		}
	}).Times(3)

	ctx.On("Complete", mock.Anything, nil).Return().Run(func(args mock.Arguments) {
		result := args.Get(0).([]byte)
		require.Equal(t, "workflow-completed", string(result))
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte("Hello"))
	w.OnDecisionTaskStarted()

	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)
}

//
// A sample example of external workflow.
//

// Workflow Deciders and Activities.
type greetingsWorkflow struct{}

type sayGreetingActivityRequest struct {
	Name     string
	Greeting string
}

// Greetings Workflow Decider.
func (w greetingsWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	// Get Greeting.

	ctx1 := WithActivityOptions(ctx, NewActivityOptions().
		WithTaskList("exampleTaskList").
		WithScheduleToStartTimeout(10*time.Second).
		WithStartToCloseTimeout(5*time.Second).
		WithScheduleToCloseTimeout(10*time.Second))

	f := ExecuteActivity(ctx1, "getGreetingActivity", input)
	var greetResult []byte
	err = f.Get(ctx, &greetResult)
	if err != nil {
		return nil, err
	}

	// Get Name.
	f = ExecuteActivity(ctx1, "getNameActivity", input)
	var nameResult []byte
	err = f.Get(ctx, &nameResult)

	if err != nil {
		return nil, err
	}

	// Say Greeting.
	request := &sayGreetingActivityRequest{Name: string(nameResult), Greeting: string(greetResult)}
	sayGreetInput, err := json.Marshal(request)
	if err != nil {
		panic(fmt.Sprintf("Marshalling failed with error: %+v", err))
	}

	err = ExecuteActivity(ctx1, "sayGreetingActivity", sayGreetInput).Get(ctx, nil)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func TestExternalExampleWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&greetingsWorkflow{})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	cbProcessor := newAsyncTestCallbackProcessor(w.OnDecisionTaskStarted)

	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		callback := args.Get(1).(resultHandler)
		cbProcessor.Add(callback, []byte("test"), nil)
	}).Times(3)

	ctx.On("Complete", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		if args.Get(1) != nil {
			err := args.Get(1).(ErrorWithDetails)
			var details []byte
			err.Details(&details)
			fmt.Printf("ErrorWithDetails: %v, Stack: %v \n", err.Reason(), string(details))
		}
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte(""))
	w.OnDecisionTaskStarted()

	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)
}

type continueAsNewWorkflowTest struct{}

func (w continueAsNewWorkflowTest) Execute(ctx Context, input []byte) (result []byte, err error) {
	ctx = WithWorkflowTaskList(ctx, "newTaskList")
	ctx = WithExecutionStartToCloseTimeout(ctx, time.Second)
	ctx = WithWorkflowTaskStartToCloseTimeout(ctx, time.Second)
	return nil, NewContinueAsNewError(ctx, "continueAsNewWorkflowTest", []byte("start"))
}

func TestContinueAsNewWorkflow(t *testing.T) {
	w := newWorkflowDefinition(&continueAsNewWorkflowTest{})
	ctx := &mockWorkflowEnvironment{}
	workflowComplete := make(chan struct{}, 1)

	cbProcessor := newAsyncTestCallbackProcessor(w.OnDecisionTaskStarted)

	ctx.On("Complete", []byte(nil), mock.Anything).Return().Run(func(args mock.Arguments) {
		resultErr := args.Get(1).(*continueAsNewError)
		require.EqualValues(t, "continueAsNewWorkflowTest", resultErr.options.workflowType.Name)
		require.EqualValues(t, 1, *resultErr.options.executionStartToCloseTimeoutSeconds)
		require.EqualValues(t, 1, *resultErr.options.taskStartToCloseTimeoutSeconds)
		require.EqualValues(t, "newTaskList", *resultErr.options.taskListName)
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte(""))
	w.OnDecisionTaskStarted()

	c := cbProcessor.ProcessOrWait(workflowComplete)
	require.True(t, c, "Workflow failed to complete")
	ctx.AssertExpectations(t)

}
