package google_adk_agents_test

import (
	"context"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/testsuite"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// addArgs/addResult are the JSON-tagged shapes an activity-style tool function
// exchanges with the model. functiontool derives the tool's parameter schema
// from these by reflection.
type addArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}
type addResult struct {
	Sum int `json:"sum"`
}

// TestActivityAsToolRunsInTurn proves the ActivityAsTool primitive end-to-end:
// an existing Temporal-activity-shaped function (func(ctx, args) (res, error))
// is wrapped as an adk-go tool, registered on a native agent, and actually
// invoked by the model during a real turn driven through RunTurnActivity. The
// model emits a function call, ADK executes the wrapped Go function in-process,
// and the model's follow-up text lands in the durable snapshot.
func TestActivityAsToolRunsInTurn(t *testing.T) {
	var toolCalls int32

	// An existing activity-shaped function. In production this is the body of
	// an @activity.defn-style function the user already has.
	add := func(_ context.Context, args addArgs) (addResult, error) {
		atomic.AddInt32(&toolCalls, 1)
		return addResult{Sum: args.A + args.B}, nil
	}
	addTool, err := adk.ActivityAsTool("add", "adds two integers a and b", add)
	require.NoError(t, err)

	// A model that first calls the tool, then replies with the answer.
	factory := func(_ context.Context) (*adk.AgentRunner, error) {
		fnCall := genai.NewContentFromParts([]*genai.Part{{
			FunctionCall: &genai.FunctionCall{ID: "fc-1", Name: "add", Args: map[string]any{"a": 2, "b": 3}},
		}}, genai.RoleModel)
		m := adk.NewMockModelWithResponses("calc",
			&model.LLMResponse{Content: fnCall},
			&model.LLMResponse{Content: modelMessage("the sum is 5"), TurnComplete: true},
		)
		ag, aerr := llmagent.New(llmagent.Config{
			Name:        "calc",
			Description: "calculator",
			Model:       m,
			Tools:       []tool.Tool{addTool},
		})
		if aerr != nil {
			return nil, aerr
		}
		svc := session.InMemoryService()
		r, rerr := runner.New(runner.Config{AppName: "calc", Agent: ag, SessionService: svc})
		if rerr != nil {
			return nil, rerr
		}
		return &adk.AgentRunner{Runner: r, SessionService: svc, AppName: "calc"}, nil
	}

	reg := adk.NewAgentRegistry()
	reg.Register("calc", factory)
	acts := adk.NewActivities(reg, adk.Options{})

	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(acts.RunTurnActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	val, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
		AgentName: "calc", AppName: "calc", UserID: "u", SessionID: "s",
		Message: userMessage("what is 2 + 3?"),
	})
	require.NoError(t, err)
	var res adk.TurnResult
	require.NoError(t, val.Get(&res))

	require.Equal(t, int32(1), atomic.LoadInt32(&toolCalls), "the wrapped activity function must run in-process during the turn")
	require.True(t, containsText(res.Events, "the sum is 5"), "the model's post-tool reply must be recorded")
}

// TestActivityAsToolRejectsNilFunc: a nil function is a programming error caught
// at construction, not deferred to a turn.
func TestActivityAsToolRejectsNilFunc(t *testing.T) {
	_, err := adk.ActivityAsTool[addArgs, addResult]("add", "adds", nil)
	require.Error(t, err)
}

// TestRegistryNamesAndMockCalls covers the registry's Names introspection and
// MockModel's call counter — both used by operators wiring and asserting on a
// worker.
func TestRegistryNamesAndMockCalls(t *testing.T) {
	m := adk.NewMockModel("m", modelMessage("hi"))
	reg := adk.NewAgentRegistry()
	reg.Register("english", newAgentFactory("english", m))
	reg.Register("french", newAgentFactory("french", adk.NewMockModel("fr", modelMessage("bonjour"))))

	names := reg.Names()
	sort.Strings(names)
	require.Equal(t, []string{"english", "french"}, names)

	acts := adk.NewActivities(reg, adk.Options{})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(acts.RunTurnActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})
	_, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
		AgentName: "english", AppName: "english", UserID: "u", SessionID: "s", Message: userMessage("hi"),
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, m.Calls(), 1, "the model must have been invoked during the turn")
}
