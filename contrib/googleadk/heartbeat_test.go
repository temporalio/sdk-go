package googleadk_test

// Tests for InvokeModel's periodic Activity heartbeating. The Activity's
// HeartbeatTimeout counts from Activity START, not from the first recorded
// heartbeat, and a model can legitimately stay silent past it before producing
// its first output (thinking models, large-context prefill). Heartbeating only
// on chunk arrival would let the server heartbeat-time-out and retry a healthy
// — and paid — model call. These tests run in the TestWorkflowEnvironment,
// which enforces HeartbeatTimeout in wall-clock time, so they genuinely
// reproduce the false timeout. They deliberately never publish stream chunks:
// the unit-test mock client cannot service the workflowstreams publish signal
// (that path is exercised by the dev-server-gated integration test).

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"

	"go.temporal.io/sdk/contrib/googleadk"
)

// slowModel is a model.LLM that stays silent for a configured delay before
// producing anything, imitating a model with a long time-to-first-output. With
// chunks == 0 it then ends the stream without yielding anything (the streaming
// pre-first-chunk window, with nothing ever published); otherwise it yields one
// complete response carrying text.
type slowModel struct {
	name   string
	delay  time.Duration
	text   string
	chunks int
}

// Name implements model.LLM.
func (m *slowModel) Name() string { return m.name }

// GenerateContent implements model.LLM by sleeping for the configured delay
// before yielding (or not yielding, when chunks == 0).
func (m *slowModel) GenerateContent(ctx context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// Stay silent past the heartbeat timeout, but honor cancellation so a
		// timed-out attempt exits promptly instead of lingering.
		select {
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		case <-time.After(m.delay):
		}
		if m.chunks == 0 {
			return
		}
		yield(&model.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: m.text}}},
			TurnComplete: true,
		}, nil)
	}
}

// slowModelInput is the serializable input to slowModelWorkflow.
type slowModelInput struct {
	ModelName        string
	UserMessage      string
	HeartbeatTimeout time.Duration
	StreamingTopic   string
}

// slowModelWorkflow drives runAgent with an explicit HeartbeatTimeout and a
// single-attempt retry policy, so a false heartbeat timeout surfaces directly
// as the workflow error instead of being masked by retries.
func slowModelWorkflow(ctx workflow.Context, in slowModelInput) (runResult, error) {
	modelOpts := []googleadk.ModelOption{
		googleadk.WithModelActivityOptions(workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			HeartbeatTimeout:    in.HeartbeatTimeout,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}),
	}
	if in.StreamingTopic != "" {
		modelOpts = append(modelOpts, googleadk.WithStreaming(in.StreamingTopic, 0))
	}
	return runAgent(ctx, agentBuild{
		modelName:      in.ModelName,
		modelOpts:      modelOpts,
		streamingTopic: in.StreamingTopic,
		userMessage:    in.UserMessage,
	})
}

// newSlowModelEnv wires a test environment around slowModelWorkflow with the
// given slow model, counting server-visible (post-throttle) heartbeats.
func newSlowModelEnv(t *testing.T, sm *slowModel) (*testsuite.TestWorkflowEnvironment, *activityCounter, *atomic.Int64) {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			sm.name: func(context.Context, string) (model.LLM, error) { return sm, nil },
		},
	})
	env.RegisterWorkflow(slowModelWorkflow)
	heartbeats := &atomic.Int64{}
	env.SetOnActivityHeartbeatListener(func(*activity.Info, converter.EncodedValues) {
		heartbeats.Add(1)
	})
	return env, counter, heartbeats
}

// TestModelHeartbeatsWhileAwaitingSlowResponse proves InvokeModel keeps a
// healthy-but-slow NON-streaming call alive against a user-set
// HeartbeatTimeout: the model stays silent for 2.5s under a 1s heartbeat
// timeout. Without the periodic heartbeater the non-streaming branch never
// heartbeats at all, so the attempt is falsely heartbeat-timed-out (and, since
// heartbeat timeouts are retryable, a real server would re-run — and re-pay
// for — the model call).
func TestModelHeartbeatsWhileAwaitingSlowResponse(t *testing.T) {
	sm := &slowModel{name: "slow-model", delay: 2500 * time.Millisecond, text: "done", chunks: 1}
	env, counter, heartbeats := newSlowModelEnv(t, sm)

	env.ExecuteWorkflow(slowModelWorkflow, slowModelInput{
		ModelName:        "slow-model",
		UserMessage:      "hi",
		HeartbeatTimeout: time.Second,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "a slow model call must not heartbeat-time-out")

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "done")
	assert.GreaterOrEqual(t, heartbeats.Load(), int64(2),
		"the periodic heartbeater must record heartbeats while the model is silent")
	assert.Equal(t, 1, counter.get(googleadk.InvokeModelActivityName),
		"a healthy slow call must not be retried")
}

// TestStreamingModelHeartbeatsBeforeFirstChunk proves the streaming branch
// stays alive through the window between Activity start and the model's first
// chunk, where the old per-chunk heartbeat never fired: the model stays silent
// for 2.5s under a 1s heartbeat timeout and then ends its stream without
// yielding, so nothing is ever published and the call legitimately fails with
// a "no streamed response" model error. What must NOT happen is a heartbeat
// timeout, which is what the pre-first-chunk window produced before the fix.
func TestStreamingModelHeartbeatsBeforeFirstChunk(t *testing.T) {
	sm := &slowModel{name: "slow-stream-model", delay: 2500 * time.Millisecond, chunks: 0}
	env, _, heartbeats := newSlowModelEnv(t, sm)

	env.ExecuteWorkflow(slowModelWorkflow, slowModelInput{
		ModelName:        "slow-stream-model",
		UserMessage:      "hi",
		HeartbeatTimeout: time.Second,
		StreamingTopic:   "hb-stream",
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err, "a zero-chunk stream is a model error")
	var timeoutErr *temporal.TimeoutError
	assert.False(t, errors.As(err, &timeoutErr),
		"a slow first chunk must not heartbeat-time-out: %v", err)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, googleadk.ErrorTypeModel, appErr.Type())
	assert.GreaterOrEqual(t, heartbeats.Load(), int64(2),
		"the periodic heartbeater must cover the pre-first-chunk window")
}
