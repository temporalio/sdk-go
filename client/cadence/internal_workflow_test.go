package cadence

import (
	"testing"

	"time"

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
	err     Error
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

func (ac *asyncTestCallbackProcessor) Add(cb resultHandler, result []byte, err Error) {
	ac.callbackCh <- callbackHandlerWrapTest{handler: cb, result: result, err: err}
}

func (ac *asyncTestCallbackProcessor) Close() {
	close(ac.callbackCh)
	<-ac.waitProcCompleteCh
}

func (w *helloWorldWorklfow) Execute(ctx Context, input []byte) (result []byte, err Error) {
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

func (w *helloWorldActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err Error) {
	id := "id1"
	parameters := ExecuteActivityParameters{
		ActivityID: &id,
		Input:      input,
	}
	result, err = ExecuteActivity(ctx, parameters)
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
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		parameters := args.Get(0).(ExecuteActivityParameters)
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

func (w *splitJoinActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err Error) {
	var result1, result2 []byte
	var err1, err2 error

	c1 := NewChannel(ctx)
	c2 := NewChannel(ctx)
	Go(ctx, func(ctx Context) {
		id1 := "id1"
		parameters := ExecuteActivityParameters{
			ActivityID: &id1,
			Input:      input,
		}

		result1, err1 = ExecuteActivity(ctx, parameters)
		c1.Send(ctx, true)
	})
	Go(ctx, func(ctx Context) {
		id2 := "id2"
		parameters := ExecuteActivityParameters{
			ActivityID: &id2,
			Input:      input,
		}
		result2, err2 = ExecuteActivity(ctx, parameters)
		if w.panic {
			panic("simulated")
		}
		c2.Send(ctx, true)
	})

	c1.Receive(ctx)
	// Use selector to test it
	selected := false
	NewSelector(ctx).AddReceive(c2, func(v interface{}, more bool) {
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
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		parameters := args.Get(0).(ExecuteActivityParameters)
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
	ctx.On("ExecuteActivity", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
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

func (w *testClockWorkflow) Execute(ctx Context, input []byte) (result []byte, err Error) {
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

func (w *testTimerWorkflow) Execute(ctx Context, input []byte) (result []byte, err Error) {
	t, err := NewTimer(ctx, 1)
	require.NoError(w.t, err)
	require.NotNil(w.t, t)

	isWokeByTimer := false

	NewSelector(ctx).AddReceive(t.C, func(v interface{}, more bool) {
		isWokeByTimer = true
	}).Select(ctx)

	require.True(w.t, isWokeByTimer)
	return []byte("workflow-completed"), nil
}

func TestTimerWorkflow(t *testing.T) {
	w := NewWorkflowDefinition(&testTimerWorkflow{t: t})
	ctx := &MockWorkflowEnvironment{}

	// Process timer callbacks.
	cbProcessor := newAsyncTestCallbackProcessor()
	cbProcessor.Process()

	ctx.On("NewTimer", mock.Anything, mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		callback := args.Get(1).(resultHandler)
		cbProcessor.Add(callback, nil, nil)
	})
	ctx.On("Complete", []byte("workflow-completed"), nil).Return().Once()

	w.Execute(ctx, []byte("Hello"))
	cbProcessor.Close()
	ctx.AssertExpectations(t)
}
