package googleadk_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"go.temporal.io/sdk/contrib/googleadk"
)

// stubGeminiAPI is an http.RoundTripper standing in for the Gemini
// generateContent endpoint. It returns canned responses in the real wire format,
// so a genuine ADK gemini model (real genai client, real request/response
// serialization) can be driven entirely offline — the same technique adk-go's own
// gemini tests use to inject an httprr transport. It records each request body so
// the test can assert what the genai client actually sent.
//
// Turn 1 (no functionResponse in the request yet) -> a functionCall to get_weather.
// Turn 2 (after the tool result is sent back)      -> a final text answer.
type stubGeminiAPI struct {
	mu       sync.Mutex
	requests [][]byte
}

func (s *stubGeminiAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	s.mu.Lock()
	s.requests = append(s.requests, body)
	s.mu.Unlock()

	payload := geminiFunctionCallResponse
	if bytes.Contains(body, []byte("functionResponse")) {
		payload = geminiFinalTextResponse
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(payload)),
		Request:    req,
	}, nil
}

const geminiFunctionCallResponse = `{
  "candidates": [{
    "content": {"role": "model", "parts": [{"functionCall": {"name": "get_weather", "args": {"city": "Paris"}}}]},
    "finishReason": "STOP",
    "index": 0
  }],
  "usageMetadata": {"promptTokenCount": 20, "candidatesTokenCount": 8, "totalTokenCount": 28},
  "modelVersion": "gemini-2.5-flash"
}`

const geminiFinalTextResponse = `{
  "candidates": [{
    "content": {"role": "model", "parts": [{"text": "It is sunny and 24C in Paris."}]},
    "finishReason": "STOP",
    "index": 0
  }],
  "usageMetadata": {"promptTokenCount": 30, "candidatesTokenCount": 9, "totalTokenCount": 39},
  "modelVersion": "gemini-2.5-flash"
}`

// geminiWeatherTool is set by TestGeminiModelEndToEnd before executing
// geminiWeatherWorkflow (the test does not run in parallel), so the in-workflow
// tool can carry a test-owned handler across the workflow boundary.
var geminiWeatherTool tool.Tool

// geminiWeatherWorkflow drives the agent with the test's in-workflow weather
// tool against a real gemini model.
func geminiWeatherWorkflow(ctx workflow.Context) (runResult, error) {
	return runAgent(ctx, agentBuild{
		modelName:   "gemini-2.5-flash",
		userMessage: "What is the weather in Paris?",
		tools:       []tool.Tool{geminiWeatherTool},
	})
}

// TestGeminiModelEndToEnd drives a REAL ADK gemini model
// (google.golang.org/adk/model/gemini) through the plugin: the genai client builds
// and serializes a real generateContent request, the model call happens worker-side
// inside the InvokeModel Activity, the canned response is parsed by the real genai
// client, and a real function-calling loop runs the tool in-workflow before the
// model returns its final answer. Only the HTTP backend is stubbed (canned
// generateContent JSON), exactly as adk-go's own gemini tests inject an httprr
// transport — so this exercises the genuine ADK<->Temporal seam end to end, not a
// FakeModel.
func TestGeminiModelEndToEnd(t *testing.T) {
	stub := &stubGeminiAPI{}
	geminiFactory := func(ctx context.Context, name string) (model.LLM, error) {
		return gemini.NewModel(ctx, name, &genai.ClientConfig{
			APIKey:     "stub-key",
			HTTPClient: &http.Client{Transport: stub},
		})
	}

	geminiWeatherTool = recordingTool(t, "get_weather", map[string]any{"forecast": "sunny and 24C"}, nil)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(geminiWeatherWorkflow)
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{"gemini-2.5-flash": geminiFactory},
	})

	env.ExecuteWorkflow(geminiWeatherWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))

	// The real gemini model emitted a function call, the tool ran in-workflow, and
	// the model produced a final answer on the following turn.
	assert.Contains(t, res.FunctionCalls, "get_weather")
	assert.Contains(t, strings.Join(res.Texts, " "), "sunny")
	assert.Equal(t, 2, counter.get(googleadk.InvokeModelActivityName), "two real model turns")

	// Two real generateContent round-trips: the genai client serialized the tool
	// declaration into the first request and the tool result into the second.
	stub.mu.Lock()
	defer stub.mu.Unlock()
	require.Len(t, stub.requests, 2)
	assert.Contains(t, string(stub.requests[0]), "get_weather", "tool declaration should reach the model request")
	assert.Contains(t, string(stub.requests[1]), "functionResponse", "tool result should be sent back to the model")
	assert.Contains(t, string(stub.requests[1]), "sunny and 24C")
}

// errStubGeminiAPI returns a non-2xx Gemini error response so the test can verify
// the real genai client surfaces it as an APIError that the plugin classifies and
// maps onto Temporal's retry contract.
type errStubGeminiAPI struct{ status int }

func (e errStubGeminiAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{
		StatusCode: e.status,
		Status:     http.StatusText(e.status),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":400,"message":"invalid argument","status":"INVALID_ARGUMENT"}}`)),
		Request:    req,
	}, nil
}

// TestGeminiModelErrorIsNonRetryable drives a real ADK gemini model whose backend
// returns HTTP 400. It verifies the genai client surfaces a real APIError, the
// plugin classifies a 4xx as non-retryable, and the workflow fails fast (the model
// Activity is attempted exactly once, not retried).
func TestGeminiModelErrorIsNonRetryable(t *testing.T) {
	geminiFactory := func(ctx context.Context, name string) (model.LLM, error) {
		return gemini.NewModel(ctx, name, &genai.ClientConfig{
			APIKey:     "stub-key",
			HTTPClient: &http.Client{Transport: errStubGeminiAPI{status: http.StatusBadRequest}},
		})
	}

	var s testsuite.WorkflowTestSuite
	env, counter := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{"gemini-2.5-flash": geminiFactory},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "gemini-2.5-flash",
		UserMessage: "hi",
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.True(t, googleadk.IsNonRetryable(err), "a 4xx from the model must be non-retryable; got %v", err)
	// A non-retryable error is not retried: exactly one model Activity attempt.
	assert.Equal(t, 1, counter.get(googleadk.InvokeModelActivityName))
}
