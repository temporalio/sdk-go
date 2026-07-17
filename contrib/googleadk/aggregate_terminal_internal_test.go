package googleadk

// Internal (white-box) tests for the authoritative-response semantics of
// aggregateResponses. Gemini's streaming iterator forcibly marks every raw
// chunk Partial and ends with a NON-partial response that already carries the
// fully aggregated text and function-call parts; folding that terminal
// response's text into the partial-accumulated aggregate duplicated the
// message and dropped the function calls (the tool was never invoked
// in-workflow). A non-partial response must therefore REPLACE
// partial-accumulated content, while metadata keeps folding — Gemini's
// terminal response omits fields (e.g. ModelVersion) that earlier chunks
// carried — and a stream carrying several non-partial responses must keep
// every part, mirroring how ADK's flow fully processes each non-partial event.
// The SSE reproduction is adopted from PR #2466.

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

// geminiSSEStubTransport replays a canned Gemini SSE stream (text chunk, then a
// function-call chunk with finish/usage metadata) so the REAL gemini model —
// including its internal stream aggregator, which appends the terminal
// non-partial response — runs offline. Fixture credit: PR #2466.
type geminiSSEStubTransport struct{}

// RoundTrip implements http.RoundTripper.
func (geminiSSEStubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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

// partialText builds a partial streamed text chunk.
func partialText(text string) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: text}}},
		Partial: true,
	}
}

// nonPartial builds a non-partial (authoritative) response from the given parts.
func nonPartial(parts ...*genai.Part) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{Role: genai.RoleModel, Parts: parts},
	}
}

// TestAggregateResponsesGeminiTerminalAuthoritative drives the real gemini
// model over a stubbed SSE transport and proves the aggregate handed back into
// the workflow carries the message exactly once, keeps the function-call part
// (so the tool actually runs in-workflow), and preserves metadata — including
// ModelVersion, which the terminal response omits — from earlier chunks.
func TestAggregateResponsesGeminiTerminalAuthoritative(t *testing.T) {
	llm, err := gemini.NewModel(t.Context(), "gemini-2.5-flash", &genai.ClientConfig{
		APIKey:     "stub-key",
		HTTPClient: &http.Client{Transport: geminiSSEStubTransport{}},
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

	// Pin the iterator shape the fix is built on: every raw chunk is yielded as
	// a Partial, then the model's own aggregate arrives LAST as a non-partial
	// response already carrying all the parts — but no ModelVersion.
	require.Len(t, responses, 3)
	assert.True(t, responses[0].Partial)
	assert.Equal(t, "Checking weather. ", concatText(responses[0].Content))
	assert.True(t, responses[1].Partial)
	require.Len(t, responses[1].Content.Parts, 1)
	require.NotNil(t, responses[1].Content.Parts[0].FunctionCall)
	assert.True(t, responses[1].TurnComplete)
	assert.False(t, responses[2].Partial)
	require.NotNil(t, responses[2].Content)
	require.Len(t, responses[2].Content.Parts, 2)
	assert.Empty(t, responses[2].ModelVersion,
		"gemini's terminal aggregate omits ModelVersion; folding metadata must keep it alive")

	// The aggregate: message once, function call kept, metadata preserved.
	require.NotNil(t, agg)
	assert.Equal(t, "Checking weather. ", concatText(agg.Content),
		"terminal response must replace partial-accumulated text, not double it")
	require.Len(t, agg.Content.Parts, 2)
	require.NotNil(t, agg.Content.Parts[1].FunctionCall)
	assert.Equal(t, "get_weather", agg.Content.Parts[1].FunctionCall.Name)
	assert.Equal(t, "Paris", agg.Content.Parts[1].FunctionCall.Args["city"])
	assert.Equal(t, genai.FinishReasonStop, agg.FinishReason)
	require.NotNil(t, agg.UsageMetadata)
	assert.EqualValues(t, 10, agg.UsageMetadata.TotalTokenCount)
	assert.Equal(t, "gemini-2.5-flash", agg.ModelVersion,
		"ModelVersion from earlier chunks must survive the terminal replace")
}

// TestAggregateResponsesTerminalReplaceCopies proves the authoritative-replace
// path deep-copies (mirrors TestAggregateResponsesCopiesFirst): the streaming
// caller mutates the aggregate after the fold, and the model's own response —
// aliased by the published chunks — must stay untouched. It also proves
// partials arriving after an authoritative response are display-only.
func TestAggregateResponsesTerminalReplaceCopies(t *testing.T) {
	final := nonPartial(
		&genai.Part{Text: "The weather. "},
		&genai.Part{FunctionCall: &genai.FunctionCall{Name: "get_weather", Args: map[string]any{"city": "Paris"}}},
	)
	agg := aggregateResponses(nil, partialText("a"))
	agg = aggregateResponses(agg, final)

	require.NotNil(t, agg.Content)
	require.Len(t, agg.Content.Parts, 2)
	assert.Equal(t, "The weather. ", concatText(agg.Content))
	assert.False(t, agg.Partial)

	// Partials after the authoritative response are display-only.
	agg = aggregateResponses(agg, partialText("z"))
	assert.Equal(t, "The weather. ", concatText(agg.Content))
	require.Len(t, agg.Content.Parts, 2)

	// Mutate the aggregate the way invokeModelStreaming and later folds do; the
	// model's response must be untouched (deep copy on the replace path).
	agg.Partial = false
	agg.TurnComplete = true
	appendText(agg.Content, " more")
	agg.Content.Parts = append(agg.Content.Parts, &genai.Part{Text: "extra"})
	assert.Equal(t, "The weather. ", concatText(final.Content))
	require.Len(t, final.Content.Parts, 2)
	assert.False(t, final.TurnComplete)
}

// TestAggregateResponsesKeepsMultipleAuthoritative proves a stream of several
// non-partial responses (a custom model.LLM that yields complete responses
// without marking partials) keeps every part in order — ADK's flow fully
// processes EACH non-partial event, so none of them may be dropped.
func TestAggregateResponsesKeepsMultipleAuthoritative(t *testing.T) {
	agg := aggregateResponses(nil, nonPartial(&genai.Part{Text: "done."}))
	agg = aggregateResponses(agg, nonPartial(
		&genai.Part{FunctionCall: &genai.FunctionCall{Name: "get_weather", Args: map[string]any{"city": "Paris"}}},
	))

	require.NotNil(t, agg.Content)
	require.Len(t, agg.Content.Parts, 2)
	assert.Equal(t, "done.", agg.Content.Parts[0].Text)
	require.NotNil(t, agg.Content.Parts[1].FunctionCall)
	assert.Equal(t, "get_weather", agg.Content.Parts[1].FunctionCall.Name)
}
