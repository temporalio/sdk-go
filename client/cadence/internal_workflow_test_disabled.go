package cadence

import (
	"context"
	"testing"

	"encoding/json"
	"time"

	"fmt"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
	waitProcCompleteCh chan struct{}
}

func newAsyncTestCallbackProcessor() *asyncTestCallbackProcessor {
	return &asyncTestCallbackProcessor{
		callbackCh:         make(chan callbackHandlerWrapTest, 10),
		waitProcCompleteCh: make(chan struct{}),
	}
}

func (ac *asyncTestCallbackProcessor) Process() {
	go func() {
		for {
			c, more := <-ac.callbackCh
			if !more {
				break
			}
			time.Sleep(time.Millisecond)
			c.handler(c.result, c.err)
		}
		ac.waitProcCompleteCh <- struct{}{}
	}()
}

func (ac *asyncTestCallbackProcessor) Add(cb resultHandler, result []byte, err error) {
	ac.callbackCh <- callbackHandlerWrapTest{handler: cb, result: result, err: err}
}

func (ac *asyncTestCallbackProcessor) Close() {
	close(ac.callbackCh)
	<-ac.waitProcCompleteCh
}

func (w *helloWorldWorklfow) Execute(ctx Context, input []byte) (result []byte, err error) {
	return []byte(string(input) + " World!"), nil
}

func TestHelloWorldWorkflow(t *testing.T) {
	w := NewWorkflowDefinition(&helloWorldWorklfow{t: t})
	ctx := &MockWorkflowEnvironment{}
	ctx.On("Complete", []byte("Hello World!"), nil).Return().Once()
	w.Execute(ctx, []byte("Hello"))
}

type helloWorldActivityWorkflow struct {
	t *testing.T
}

func (w *helloWorldActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	ctx1 := WithActivityOptions(ctx, GetActivityOptions().(*activityOptions).WithActivityID("id1"))
	result, err = ExecuteActivity(ctx1, ActivityType{}, input)
	require.NoError(w.t, err)
	return []byte(string(result)), nil
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
	w := NewWorkflowDefinition(&helloWorldActivityWorkflow{t: t})
	ctx := &MockWorkflowEnvironment{}

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor()
	cbProcessor.Process()

	// TODO: Fix the tests to expose so mocking execute activity can inline complete the response.
	ctx.On("Complete", []byte("Hello World!"), nil).Return().Once()
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		parameters := args.Get(0).(executeActivityParameters)
		callback := args.Get(1).(resultHandler)
		result := string(parameters.Input) + " World!"
		cbProcessor.Add(callback, []byte(result), nil)
	})
	w.Execute(ctx, []byte("Hello"))
	cbProcessor.Close()
	ctx.AssertExpectations(t)
}

type splitJoinActivityWorkflow struct {
	t     *testing.T
	panic bool
}

func (w *splitJoinActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	var result1, result2 []byte
	var err1, err2 error

	c1 := NewChannel(ctx)
	c2 := NewChannel(ctx)
	Go(ctx, func(ctx Context) {
		ctx1 := WithActivityOptions(ctx, GetActivityOptions().(*activityOptions).WithActivityID("id1"))
		result1, err1 = ExecuteActivity(ctx1, ActivityType{}, nil)
		c1.Send(ctx, true)
	})
	Go(ctx, func(ctx Context) {
		ctx2 := WithActivityOptions(ctx, GetActivityOptions().(*activityOptions).WithActivityID("id2"))
		result2, err2 = ExecuteActivity(ctx2, ActivityType{}, nil)
		if w.panic {
			panic("simulated")
		}
		c2.Send(ctx, true)
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
	w := NewWorkflowDefinition(&splitJoinActivityWorkflow{t: t})
	ctx := &MockWorkflowEnvironment{}

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor()
	cbProcessor.Process()

	// TODO: Fix the tests to expose so mocking execute activity can inline complete the response.
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		parameters := args.Get(0).(executeActivityParameters)
		callback := args.Get(1).(resultHandler)
		switch *parameters.ActivityID {
		case "id1":
			cbProcessor.Add(callback, []byte("Hello"), nil)
		case "id2":
			cbProcessor.Add(callback, []byte(" Flow!"), nil)
		}
	}).Twice()

	ctx.On("Complete", []byte("Hello Flow!"), nil).Return().Once()

	w.Execute(ctx, []byte(""))

	cbProcessor.Close()
	ctx.AssertExpectations(t)
}

func TestWorkflowPanic(t *testing.T) {
	w := NewWorkflowDefinition(&splitJoinActivityWorkflow{t: t, panic: true})
	ctx := &MockWorkflowEnvironment{}

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor()
	cbProcessor.Process()

	// TODO: Fix the tests to expose so mocking execute activity can inline complete the response.
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		callback := args.Get(1).(resultHandler)
		cbProcessor.Add(callback, []byte("test"), nil)
	}).Twice()
	ctx.On("Complete", []byte(nil), mock.Anything).Return().Run(func(args mock.Arguments) {
		resultErr := args.Get(1).(Error)
		require.EqualValues(t, "simulated", resultErr.Reason())
		require.Contains(t, string(resultErr.Details()), "cadence.(*splitJoinActivityWorkflow).Execute")
	}).Once()

	w.Execute(ctx, []byte("Hello"))

	cbProcessor.Close()
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
	w := NewWorkflowDefinition(&testClockWorkflow{t: t})
	ctx := &MockWorkflowEnvironment{}

	ctx.On("Now").Return(time.Now()).Once()
	ctx.On("Complete", []byte("workflow-completed"), nil).Return().Once()
	w.Execute(ctx, []byte("Hello"))
	ctx.AssertExpectations(t)
}

type testTimerWorkflow struct {
	t *testing.T
}

func (w *testTimerWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {

	// Start a timer.
	t := NewTimer(ctx, 1)

	isWokeByTimer := false

	NewSelector(ctx).AddFuture(t, func(v interface{}, err error) {
		require.NoError(w.t, err)
		isWokeByTimer = true
	}).Select(ctx)

	require.True(w.t, isWokeByTimer)

	// Start a timer and cancel it.
	ctx2, c2 := WithCancel(ctx)
	t2 := NewTimer(ctx2, 1)
	c2()
	r2, err2 := t2.Get(ctx2)

	require.Nil(w.t, r2)
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
	w := NewWorkflowDefinition(&testTimerWorkflow{t: t})
	ctx := &MockWorkflowEnvironment{}
	workflowComplete := make(chan struct{})

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor()
	cbProcessor.Process()

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

	<-workflowComplete

	cbProcessor.Close()
	ctx.AssertExpectations(t)
}

type testActivityCancelWorkflow struct {
	t *testing.T
}

func (w *testActivityCancelWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	// Sync cancellation
	ctx1, c1 := WithCancel(ctx)
	defer c1()
	ctx1 = WithActivityOptions(ctx1, GetActivityOptions().(*activityOptions).WithActivityID("id1"))
	res1, err1 := ExecuteActivity(ctx1, ActivityType{}, nil)
	require.Equal(w.t, string(res1), "test")
	require.NoError(w.t, err1)

	// Async Cancellation (Callback completes before cancel)
	ctx2, c2 := WithCancel(ctx)
	ctx2 = WithActivityOptions(ctx2, GetActivityOptions().(*activityOptions).WithActivityID("id2"))
	f := ExecuteActivityAsync(ctx2, ActivityType{}, nil)
	c2()
	res2, err2 := f.Get(ctx)
	require.NotNil(w.t, res2)
	require.NoError(w.t, err2)

	// Async Cancellation (Callback doesn't complete)
	ctx3, c3 := WithCancel(ctx)
	ctx3 = WithActivityOptions(ctx3, GetActivityOptions().(*activityOptions).WithActivityID("id3"))
	f3 := ExecuteActivityAsync(ctx3, ActivityType{}, nil)
	c3()
	res3, err3 := f3.Get(ctx)
	require.Nil(w.t, res3)
	require.Equal(w.t, "testCancelDetails", string(err3.(CanceledError).Details()))

	return []byte("workflow-completed"), nil
}

func TestActivityCancelWorkflow(t *testing.T) {
	w := NewWorkflowDefinition(&testActivityCancelWorkflow{t: t})
	ctx := &MockWorkflowEnvironment{}
	workflowComplete := make(chan struct{})

	cbProcessor := newAsyncTestCallbackProcessor()
	cbProcessor.Process()

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
				NewCanceledErrorWithDetails([]byte("testCancelDetails")),
			)
		}
	}).Times(3)

	ctx.On("Complete", mock.Anything, nil).Return().Run(func(args mock.Arguments) {
		result := args.Get(0).([]byte)
		require.Equal(t, "workflow-completed", string(result))
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte("Hello"))

	<-workflowComplete

	cbProcessor.Close()
	ctx.AssertExpectations(t)
}

//
// A sample example of external workflow.
//

// Workflow Deciders and Activities.
type greetingsWorkflow struct{}
type getNameActivity struct{}
type getGreetingActivity struct{}
type sayGreetingActivity struct{}

type sayGreetingActivityRequest struct {
	Name     string
	Greeting string
}

// Greetings Workflow Decider.
func (w greetingsWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	// Get Greeting.

	ctx1 := WithActivityOptions(ctx, GetActivityOptions().
		WithTaskList("exampleTaskList").
		WithScheduleToCloseTimeout(10).
		WithScheduleToStartTimeout(2))

	greetResult, err := ExecuteActivity(ctx1, ActivityType{Name: "getGreetingActivity"}, input)
	if err != nil {
		return nil, err
	}

	// Get Name.
	nameResult, err := ExecuteActivity(ctx1, ActivityType{Name: "getNameActivity"}, input)
	if err != nil {
		return nil, err
	}

	// Say Greeting.
	request := &sayGreetingActivityRequest{Name: string(nameResult), Greeting: string(greetResult)}
	sayGreetInput, err := json.Marshal(request)
	if err != nil {
		panic(fmt.Sprintf("Marshalling failed with error: %+v", err))
	}
	_, err = ExecuteActivity(ctx1, ActivityType{Name: "sayGreetingActivity"}, sayGreetInput)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// Get Name Activity.
func (g getNameActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("World"), nil
}

func (g getNameActivity) ActivityType() ActivityType {
	return ActivityType{Name: "getNameActivity"}
}

// Get Greeting Activity.
func (ga getGreetingActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("Hello"), nil
}

func (ga getGreetingActivity) ActivityType() ActivityType {
	return ActivityType{Name: "getGreetingActivity"}
}

// Say Greeting Activity.
func (ga sayGreetingActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	greeetingParams := &sayGreetingActivityRequest{}
	err := json.Unmarshal(input, greeetingParams)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Saying Final Greeting: ")
	fmt.Printf("%s %s!\n", greeetingParams.Greeting, greeetingParams.Name)
	return nil, nil
}

func (ga sayGreetingActivity) ActivityType() ActivityType {
	return ActivityType{Name: "sayGreetingActivity"}
}

func TestExternalExampleWorkflow(t *testing.T) {
	w := NewWorkflowDefinition(&greetingsWorkflow{})
	ctx := &MockWorkflowEnvironment{}
	workflowComplete := make(chan struct{})

	cbProcessor := newAsyncTestCallbackProcessor()
	cbProcessor.Process()

	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		callback := args.Get(1).(resultHandler)
		cbProcessor.Add(callback, []byte("test"), nil)
	}).Times(3)

	ctx.On("Complete", mock.Anything, nil).Return().Run(func(args mock.Arguments) {
		workflowComplete <- struct{}{}
	}).Once()

	w.Execute(ctx, []byte(""))
	<-workflowComplete
}
