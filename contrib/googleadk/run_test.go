package googleadk_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// TestMultiAgentSubAgents drives a real two-agent tree: the root model emits
// transfer_to_agent (a control tool that runs in-workflow, never as an
// Activity), and the specialist sub-agent then produces the answer — its model
// call still a durable InvokeModel Activity.
func TestMultiAgentSubAgents(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"root-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "transfer_to_agent", map[string]any{"agent_name": "specialist"}),
				googleadk.TextResponse("(root fallback)"),
			),
			"specialist-model": scriptedModelFactory(googleadk.TextResponse("specialist answer")),
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "root-model",
		UserMessage: "please handle this specialist task",
		SubAgents: []subAgentSpec{{
			Name:        "specialist",
			Description: "handles specialist tasks",
			ModelName:   "specialist-model",
		}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "specialist answer")
	assert.Contains(t, res.Authors, "specialist", "the specialist sub-agent should author an event")

	// Two durable model calls: the root turn that transfers, and the specialist.
	assert.GreaterOrEqual(t, counter.get(googleadk.InvokeModelActivityName), 2)
}

// errorModel is a model.LLM that always fails with a genai.APIError of the given
// HTTP status, so InvokeModel classifies the failure per Temporal's retry
// contract.
type errorModel struct {
	name string
	code int
}

func (m *errorModel) Name() string { return m.name }

func (m *errorModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(nil, &genai.APIError{Code: m.code, Message: "boom"})
	}
}

// TestModelErrorClassification proves a non-retryable upstream status (HTTP 400)
// is surfaced as a non-retryable ApplicationError tagged ErrorTypeModel, so the
// run fails fast and the caller can classify it without string-matching.
func TestModelErrorClassification(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"broken-model": func(context.Context, string) (model.LLM, error) {
				return &errorModel{name: "broken-model", code: 400}, nil
			},
		},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{ModelName: "broken-model", UserMessage: "hi"})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err, "a non-retryable model error must fail the run")
	assert.True(t, googleadk.IsNonRetryable(err), "HTTP 400 must be classified non-retryable")

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "error must be a Temporal ApplicationError")
	assert.Equal(t, googleadk.ErrorTypeModel, appErr.Type())
}

// boomToolWorkflow wires an in-workflow function tool whose handler always
// returns an error, to prove ADK's tool-error contract: the error is handed back
// to the model rather than failing the workflow, and the model recovers on its
// next turn. (In-workflow tools are not dispatched through an Activity, so there
// is no Temporal retry; the error propagates straight into the agent loop.)
func boomToolWorkflow(ctx workflow.Context) (runResult, error) {
	boom, err := funcTool("boom", func(agent.Context, map[string]any) (map[string]any, error) {
		return nil, errors.New("transient boom")
	})
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		modelName:   "fake-model",
		userMessage: "call boom",
		tools:       []tool.Tool{boom},
	})
}

// TestToolErrorFedBackToModel proves an in-workflow tool failure is — per ADK's
// tool-error contract — handed back to the model rather than failing the
// workflow, so the model can recover on the following turn.
func TestToolErrorFedBackToModel(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(boomToolWorkflow)
	wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "boom", map[string]any{}),
				googleadk.TextResponse("recovered after tool failure"),
			),
		},
	})

	env.ExecuteWorkflow(boomToolWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "recovered after tool failure")
	assert.Contains(t, res.FunctionCalls, "boom")
}
