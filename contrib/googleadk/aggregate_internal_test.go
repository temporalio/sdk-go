// Copyright 2026 Google LLC, Temporal Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.

package googleadk

// Internal (white-box) tests for the streaming aggregation logic. This is the
// load-bearing correctness of the streaming feature: regardless of how many
// chunks the model streams to external consumers, the InvokeModel Activity must
// fold them into ONE coherent response handed back into the workflow so replay
// stays deterministic. These run by default with no Temporal server — the real
// publish side channel is exercised by the dev-server-gated integration test.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

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
