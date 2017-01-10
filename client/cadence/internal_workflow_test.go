package cadence

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

type testContext struct {
}

type helloWorldWorklfow struct {
	t *testing.T
}

func (w *helloWorldWorklfow) Execute(ctx Context, input []byte) (result []byte, err Error) {
	return []byte(string(input) + " World!"), nil
}

func TestHelloWorldWorkflow(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w := NewWorkflowDefinition(&helloWorldWorklfow{t: t})
	ctx := NewMockWorkflowContext(mockCtrl)
	ctx.EXPECT().Complete([]byte("Hello World!"), nil)
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
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w := NewWorkflowDefinition(&helloWorldActivityWorkflow{t: t})
	ctx := NewMockWorkflowContext(mockCtrl)
	ctx.EXPECT().Complete([]byte("Hello Flow!"), nil)
	m := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m)
	w.Execute(ctx, []byte("Hello"))
	m.resultHandler([]byte("Hello Flow!"), nil)
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

	c1.Recv(ctx)
	// Use selector to test it
	selected := false
	NewSelector(ctx).AddRecv(c2, func(v interface{}, more bool) {
		require.True(w.t, more)
		selected = true
	}).Select(ctx)
	require.True(w.t, selected)
	require.NoError(w.t, err1)
	require.NoError(w.t, err2)

	return []byte(string(result1) + string(result2)), nil
}

func TestSplitJoinActivityWorkflow(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w := NewWorkflowDefinition(&splitJoinActivityWorkflow{t: t})
	ctx := NewMockWorkflowContext(mockCtrl)
	m1 := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m1)
	m2 := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m2)
	ctx.EXPECT().Complete([]byte("Hello Flow!"), nil)

	w.Execute(ctx, []byte("Hello"))

	m2.resultHandler([]byte(" Flow!"), nil)
	m1.resultHandler([]byte("Hello"), nil)
}

func TestWorkflowPanic(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w := NewWorkflowDefinition(&splitJoinActivityWorkflow{t: t, panic: true})
	ctx := NewMockWorkflowContext(mockCtrl)
	m1 := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m1)
	m2 := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m2)

	ctx.EXPECT().Complete(nil, gomock.Any()).Do(func(result []byte, err Error) {
		require.Nil(t, result)
		require.NotNil(t, err)
		require.EqualValues(t, "simulated", err.Reason())
		require.Contains(t, string(err.Details()), "cadence.(*splitJoinActivityWorkflow).Execute")
	})
	w.Execute(ctx, []byte("Hello"))
	m2.resultHandler([]byte(" Flow!"), nil) // causes panic
}
