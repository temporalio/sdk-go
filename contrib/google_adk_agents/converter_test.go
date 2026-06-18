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

package googleadk_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/converter"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

// TestLLMRequestResponseRoundTrip verifies that the ADK/genai types the plugin
// ships across the Activity boundary survive Temporal's default DataConverter
// (the plugin installs no custom converter). It covers the realistic shapes:
// text, function calls, function responses, tool declarations, and response
// usage/finish/citation/custom metadata.
//
// Caveat: genai types are tagged `json:",omitempty"`, so a field explicitly set
// to its zero value (empty string, false, 0) is indistinguishable from unset and
// is dropped on the wire. That is acceptable here — the plugin ships
// model-produced values, which are non-zero when meaningful — which is why this
// test asserts populated, non-zero fields.
func TestLLMRequestResponseRoundTrip(t *testing.T) {
	dc := converter.GetDefaultDataConverter()

	req := &model.LLMRequest{
		Model: "gemini-2.5-flash",
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "weather in Paris?"}}},
			{Role: genai.RoleModel, Parts: []*genai.Part{{
				FunctionCall: &genai.FunctionCall{Name: "get_weather", Args: map[string]any{"city": "Paris"}},
			}}},
			{Role: genai.RoleUser, Parts: []*genai.Part{{
				FunctionResponse: &genai.FunctionResponse{Name: "get_weather", Response: map[string]any{"forecast": "sunny"}},
			}}},
		},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "get_weather", Description: "look up the weather"}},
			}},
		},
	}

	payload, err := dc.ToPayload(req)
	require.NoError(t, err)
	var gotReq model.LLMRequest
	require.NoError(t, dc.FromPayload(payload, &gotReq))

	assert.Equal(t, "gemini-2.5-flash", gotReq.Model)
	require.Len(t, gotReq.Contents, 3)
	assert.Equal(t, "weather in Paris?", gotReq.Contents[0].Parts[0].Text)
	require.NotNil(t, gotReq.Contents[1].Parts[0].FunctionCall)
	assert.Equal(t, "get_weather", gotReq.Contents[1].Parts[0].FunctionCall.Name)
	assert.Equal(t, "Paris", gotReq.Contents[1].Parts[0].FunctionCall.Args["city"])
	require.NotNil(t, gotReq.Contents[2].Parts[0].FunctionResponse)
	assert.Equal(t, "sunny", gotReq.Contents[2].Parts[0].FunctionResponse.Response["forecast"])
	require.NotNil(t, gotReq.Config)
	require.Len(t, gotReq.Config.Tools, 1)
	require.Len(t, gotReq.Config.Tools[0].FunctionDeclarations, 1)
	assert.Equal(t, "get_weather", gotReq.Config.Tools[0].FunctionDeclarations[0].Name)

	resp := &model.LLMResponse{
		Content:          &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "It is sunny in Paris."}}},
		FinishReason:     genai.FinishReasonStop,
		ModelVersion:     "gemini-2.5-flash",
		UsageMetadata:    &genai.GenerateContentResponseUsageMetadata{TotalTokenCount: 42},
		CitationMetadata: &genai.CitationMetadata{},
		CustomMetadata:   map[string]any{"trace": "abc"},
	}

	rp, err := dc.ToPayload(resp)
	require.NoError(t, err)
	var gotResp model.LLMResponse
	require.NoError(t, dc.FromPayload(rp, &gotResp))

	require.NotNil(t, gotResp.Content)
	assert.Equal(t, "It is sunny in Paris.", gotResp.Content.Parts[0].Text)
	assert.Equal(t, genai.FinishReasonStop, gotResp.FinishReason)
	assert.Equal(t, "gemini-2.5-flash", gotResp.ModelVersion)
	require.NotNil(t, gotResp.UsageMetadata)
	assert.EqualValues(t, 42, gotResp.UsageMetadata.TotalTokenCount)
	require.NotNil(t, gotResp.CitationMetadata)
	require.NotNil(t, gotResp.CustomMetadata)
	assert.Equal(t, "abc", gotResp.CustomMetadata["trace"])
}
