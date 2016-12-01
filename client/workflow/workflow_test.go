package workflow

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	"code.uber.internal/devexp/minions-client-go.git/client/flow"
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
	ctx := flow.NewMockWorkflowContext(mockCtrl)
	ctx.EXPECT().Complete([]byte("Hello World!"), nil)
	w.Execute(ctx, []byte("Hello"))
}

type helloWorldActivityWorkflow struct {
	t *testing.T
}

func (w *helloWorldActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err Error) {
	id := "id1"
	parameters := flow.ExecuteActivityParameters{
		ActivityID: &id,
		Input:      input,
	}
	result, err = ctx.ExecuteActivity(parameters)
	require.NoError(w.t, err)
	return []byte(string(result)), nil
}

type resultHandlerMatcher struct {
	resultHandler flow.ResultHandler
}

func (m *resultHandlerMatcher) Matches(x interface{}) bool {
	m.resultHandler = x.(flow.ResultHandler)
	return true
}

func (m *resultHandlerMatcher) String() string {
	return "ResultHandlerMatcher"
}

func TestSingleActivityWorkflow(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w := NewWorkflowDefinition(&helloWorldActivityWorkflow{t: t})
	ctx := flow.NewMockWorkflowContext(mockCtrl)
	ctx.EXPECT().Complete([]byte("Hello Flow!"), nil)
	m := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m)
	w.Execute(ctx, []byte("Hello"))
	m.resultHandler([]byte("Hello Flow!"), nil)
}

type splitJoinActivityWorkflow struct {
	t *testing.T
}

func (w *splitJoinActivityWorkflow) Execute(ctx Context, input []byte) (result []byte, err Error) {
	var result1, result2 []byte
	var err1, err2 error

	c1 := ctx.NewChannel()
	c2 := ctx.NewChannel()
	ctx.Go(func(ctx Context) {
		id1 := "id1"
		parameters := flow.ExecuteActivityParameters{
			ActivityID: &id1,
			Input:      input,
		}

		result1, err1 = ctx.ExecuteActivity(parameters)
		c1.Send(ctx, true)
	})
	ctx.Go(func(ctx Context) {
		id2 := "id2"
		parameters := flow.ExecuteActivityParameters{
			ActivityID: &id2,
			Input:      input,
		}
		result2, err2 = ctx.ExecuteActivity(parameters)
		c2.Send(ctx, true)
	})

	c1.Recv(ctx)
	// Use selector to test it
	selected := false
	ctx.NewSelector().AddRecv(c2, func(v interface{}, more bool) {
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
	ctx := flow.NewMockWorkflowContext(mockCtrl)
	m1 := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m1)
	m2 := &resultHandlerMatcher{}
	ctx.EXPECT().ExecuteActivity(gomock.Any(), m2)
	ctx.EXPECT().Complete([]byte("Hello Flow!"), nil)

	w.Execute(ctx, []byte("Hello"))

	m2.resultHandler([]byte(" Flow!"), nil)
	m1.resultHandler([]byte("Hello"), nil)
}
