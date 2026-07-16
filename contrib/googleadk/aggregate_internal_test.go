package googleadk

// Internal (white-box) tests for the streaming aggregation logic. This is the
// load-bearing correctness of the streaming feature: regardless of how many
// chunks the model streams to external consumers, the InvokeModel Activity must
// fold them into ONE coherent response handed back into the workflow so replay
// stays deterministic. These run by default with no Temporal server — the real
// publish side channel is exercised by the dev-server-gated integration test.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
)

type geminiSSETransport struct{}

func (geminiSSETransport) RoundTrip(req *http.Request) (*http.Response, error) {
	const stream = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Checking weather. "}]}}],"modelVersion":"gemini-2.5-flash"}

data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":10},"modelVersion":"gemini-2.5-flash"}

`
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
		Request:    req,
	}, nil
}

func partial(text string) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: text}}},
		Partial: true,
	}
}

// TestAggregateResponsesConcatenatesText proves streamed text chunks fold into a
// single message in order.
func TestAggregateResponsesConcatenatesText(t *testing.T) {
	var agg *model.LLMResponse
	for _, c := range []string{"Hello", ", ", "world"} {
		agg = aggregateResponses(agg, partial(c))
	}
	require.NotNil(t, agg)
	require.NotNil(t, agg.Content)
	assert.Equal(t, "Hello, world", concatText(agg.Content))
}

func TestAggregateResponsesUsesGeminiTerminalResponse(t *testing.T) {
	llm, err := gemini.NewModel(t.Context(), "gemini-2.5-flash", &genai.ClientConfig{
		APIKey:     "stub-key",
		HTTPClient: &http.Client{Transport: geminiSSETransport{}},
	})
	require.NoError(t, err)

	req := &model.LLMRequest{
		Model:    "gemini-2.5-flash",
		Contents: []*genai.Content{genai.NewContentFromText("weather?", genai.RoleUser)},
	}
	var responses []*model.LLMResponse
	var agg *model.LLMResponse
	for resp, streamErr := range llm.GenerateContent(t.Context(), req, true) {
		require.NoError(t, streamErr)
		responses = append(responses, resp)
		agg = aggregateResponses(agg, resp)
	}

	require.Len(t, responses, 3)
	assert.True(t, responses[0].Partial)
	assert.Equal(t, "Checking weather. ", concatText(responses[0].Content))
	assert.True(t, responses[1].Partial)
	require.Len(t, responses[1].Content.Parts, 1)
	require.NotNil(t, responses[1].Content.Parts[0].FunctionCall)
	assert.Equal(t, "get_weather", responses[1].Content.Parts[0].FunctionCall.Name)
	assert.False(t, responses[2].Partial)
	assert.Equal(t, "Checking weather. ", concatText(responses[2].Content))
	require.Len(t, responses[2].Content.Parts, 2)
	require.NotNil(t, responses[2].Content.Parts[1].FunctionCall)
	assert.Equal(t, "Paris", responses[2].Content.Parts[1].FunctionCall.Args["city"])

	require.NotNil(t, agg)
	assert.Equal(t, "Checking weather. ", concatText(agg.Content))
	require.Len(t, agg.Content.Parts, 2)
	require.NotNil(t, agg.Content.Parts[1].FunctionCall)
	assert.Equal(t, "get_weather", agg.Content.Parts[1].FunctionCall.Name)
	assert.Equal(t, "Paris", agg.Content.Parts[1].FunctionCall.Args["city"])
}

// TestAggregateResponsesCopiesFirst proves the first fold copies the response so
// the model's own buffer is never mutated across iterations.
func TestAggregateResponsesCopiesFirst(t *testing.T) {
	first := partial("a")
	agg := aggregateResponses(nil, first)
	_ = aggregateResponses(agg, partial("b"))
	// The original first response must be untouched (still just "a").
	assert.Equal(t, "a", concatText(first.Content))
	assert.Equal(t, "ab", concatText(agg.Content))
}

// TestAggregateResponsesFoldsMetadata proves later-chunk metadata wins: finish
// reason, usage, model version are carried onto the aggregate.
func TestAggregateResponsesFoldsMetadata(t *testing.T) {
	agg := aggregateResponses(nil, partial("hi"))
	next := partial(" there")
	next.FinishReason = genai.FinishReasonStop
	next.ModelVersion = "gemini-test"
	next.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{TotalTokenCount: 7}

	agg = aggregateResponses(agg, next)
	assert.Equal(t, "hi there", concatText(agg.Content))
	assert.Equal(t, genai.FinishReasonStop, agg.FinishReason)
	assert.Equal(t, "gemini-test", agg.ModelVersion)
	require.NotNil(t, agg.UsageMetadata)
	assert.EqualValues(t, 7, agg.UsageMetadata.TotalTokenCount)
}

// TestAggregateResponsesNilNext proves a nil chunk is a no-op.
func TestAggregateResponsesNilNext(t *testing.T) {
	agg := aggregateResponses(nil, partial("x"))
	assert.Same(t, agg, aggregateResponses(agg, nil))
}

// TestClassifyModelErrorRetryability proves the HTTP-status → retry-contract
// mapping used by InvokeModel: 429/5xx retryable, other 4xx non-retryable,
// status-less errors retryable.
func TestClassifyModelErrorRetryability(t *testing.T) {
	cases := []struct {
		code      int
		retryable bool
	}{
		{400, false},
		{404, false},
		{408, true},
		{429, true},
		{500, true},
		{503, true},
		{0, true}, // no status → transient
	}
	for _, tc := range cases {
		var err error = &genai.APIError{Code: tc.code, Message: "boom"}
		if tc.code == 0 {
			err = assert.AnError
		}
		classified := classifyModelError(err)
		assert.Equal(t, !tc.retryable, IsNonRetryable(classified), "code %d", tc.code)
	}
}

// TestAggregateResponsesFoldsAllLateFields proves metadata a model emits only on
// a later streaming chunk — citations, logprobs, transcriptions, custom metadata,
// error fields — is carried onto the aggregate rather than dropped.
func TestAggregateResponsesFoldsAllLateFields(t *testing.T) {
	agg := aggregateResponses(nil, partial("hi"))

	last := partial(" there")
	last.CitationMetadata = &genai.CitationMetadata{}
	last.LogprobsResult = &genai.LogprobsResult{}
	last.CustomMetadata = map[string]any{"trace": "xyz"}
	last.OutputTranscription = &genai.Transcription{}
	last.AvgLogprobs = -0.25
	last.ErrorCode = "SAFETY"
	last.ErrorMessage = "blocked by safety"

	agg = aggregateResponses(agg, last)

	assert.Equal(t, "hi there", concatText(agg.Content))
	assert.NotNil(t, agg.CitationMetadata, "citation metadata from a later chunk must survive")
	assert.NotNil(t, agg.LogprobsResult)
	assert.NotNil(t, agg.OutputTranscription)
	require.NotNil(t, agg.CustomMetadata)
	assert.Equal(t, "xyz", agg.CustomMetadata["trace"])
	assert.Equal(t, -0.25, agg.AvgLogprobs)
	assert.Equal(t, "SAFETY", agg.ErrorCode)
	assert.Equal(t, "blocked by safety", agg.ErrorMessage)
}
