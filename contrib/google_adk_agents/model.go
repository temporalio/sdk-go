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

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/contrib/workflowstreams"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// StreamServer installs the workflow-side stream server that streamed model
// output lands in. Call it once, near the top of the workflow that drives
// runner.Run, whenever you set Options.StreamingTopic. The InvokeModel Activity
// publishes each model chunk back to this workflow (via a Temporal signal), and
// external consumers (UIs) read the chunks with workflowstreams.Client.Subscribe.
// Without it the published chunks have nowhere to land. It is a no-op to omit
// when streaming is disabled; the durable agent loop does not depend on it.
func StreamServer(ctx workflow.Context) error {
	_, err := workflowstreams.NewWorkflowStream(ctx, nil)
	return err
}

// invokeModelInput is the serializable payload for the InvokeModel Activity. The
// live tool references inside Request.Tools are tagged json:"-" by ADK and are
// dropped automatically; only the model name and serialized tool declarations
// cross the wire.
type invokeModelInput struct {
	// Request is the ADK LLM request. Request.Model selects the worker-side
	// ModelFactory; Request.Config carries the tool declarations the model needs.
	Request *model.LLMRequest
	// Stream selects streaming mode (model called with stream=true, chunks
	// published to StreamingTopic). The aggregated final response is still
	// returned so replay stays deterministic.
	Stream bool
	// StreamingTopic is the workflowstreams topic external consumers subscribe to.
	StreamingTopic string
	// StreamingBatchInterval coalesces published chunks. Zero uses the library
	// default.
	StreamingBatchInterval time.Duration
}

// InvokeModel runs a single LLM round-trip worker-side. It reconstructs the
// model from the registered ModelFactory keyed by Request.Model, calls it, and
// returns the (aggregated) response. The user's model.LLM is therefore never
// invoked inside the workflow. Underlying model-SDK retries should be disabled
// in the factory so Temporal's retry policy is the single source of truth;
// transient failures are surfaced as retryable ApplicationErrors.
func (a *Activities) InvokeModel(ctx context.Context, in invokeModelInput) (*model.LLMResponse, error) {
	log := activity.GetLogger(ctx)
	if in.Request == nil {
		return nil, newApplicationError(ErrorTypeModel, false, nil, "InvokeModel: nil request")
	}
	factory, ok := a.models[in.Request.Model]
	if !ok {
		return nil, newApplicationError(ErrorTypeModel, false, nil,
			"no ModelFactory registered for model %q; register it in Config.Models", in.Request.Model)
	}
	llm, err := factory(ctx, in.Request.Model)
	if err != nil {
		return nil, newApplicationError(ErrorTypeModel, false, err,
			"construct model %q: %v", in.Request.Model, err)
	}

	if in.Stream && in.StreamingTopic != "" {
		log.Debug("invoking model in streaming mode", "model", in.Request.Model, "topic", in.StreamingTopic)
		return a.invokeModelStreaming(ctx, llm, in)
	}

	log.Debug("invoking model", "model", in.Request.Model)
	var agg *model.LLMResponse
	for resp, gerr := range llm.GenerateContent(ctx, in.Request, false) {
		if gerr != nil {
			return nil, classifyModelError(gerr)
		}
		agg = aggregateResponses(agg, resp)
	}
	if agg == nil {
		return nil, newApplicationError(ErrorTypeModel, true, nil,
			"model %q returned no response", in.Request.Model)
	}
	return agg, nil
}

// invokeModelStreaming calls the model with stream=true, heartbeats and
// publishes each chunk to the workflowstreams topic for external (UI) consumers,
// and returns the aggregated final response into the workflow.
func (a *Activities) invokeModelStreaming(ctx context.Context, llm model.LLM, in invokeModelInput) (*model.LLMResponse, error) {
	log := activity.GetLogger(ctx)
	opts := workflowstreams.Options{}
	if in.StreamingBatchInterval > 0 {
		opts.BatchInterval = in.StreamingBatchInterval
	}
	// The streaming client publishes chunks to external (UI) consumers and needs
	// a live Temporal server. If it cannot be constructed (e.g. in a unit-test
	// environment without a server), we degrade gracefully: chunks are not
	// published, but the aggregated final response is still returned into the
	// workflow, so the agent loop and replay stay correct.
	var topic *workflowstreams.TopicHandle
	if wsc, cerr := workflowstreams.NewClientFromActivity(ctx, opts); cerr != nil {
		log.Warn("streaming client unavailable; not publishing chunks", "error", cerr)
	} else {
		defer func() { _ = wsc.Close(ctx) }()
		topic = wsc.Topic(in.StreamingTopic)
	}

	var agg *model.LLMResponse
	for resp, gerr := range llm.GenerateContent(ctx, in.Request, true) {
		if gerr != nil {
			return nil, classifyModelError(gerr)
		}
		// Heartbeat keeps a slow streaming call alive against the activity's
		// HeartbeatTimeout instead of looking stuck to the scheduler.
		activity.RecordHeartbeat(ctx, resp.Partial)
		if topic != nil {
			topic.Publish(resp, false)
		}
		agg = aggregateResponses(agg, resp)
	}
	if agg == nil {
		return nil, newApplicationError(ErrorTypeModel, true, nil,
			"model %q returned no streamed response", in.Request.Model)
	}
	// Mark the aggregate complete: it is the single response handed back into the
	// workflow regardless of how many chunks were streamed to the topic.
	agg.Partial = false
	agg.TurnComplete = true
	return agg, nil
}

// aggregateResponses folds streamed/partial responses into one. The latest
// non-text metadata wins; text parts are concatenated so the returned response
// carries the full message even when the model streamed it in chunks.
func aggregateResponses(agg, next *model.LLMResponse) *model.LLMResponse {
	if next == nil {
		return agg
	}
	if agg == nil {
		// Deep-copy on the first fold so later appendText/metadata folds never
		// mutate the model's own response buffer. A shallow struct copy would
		// leave cp.Content (and its Parts) aliased to next.Content, so the next
		// chunk's appendText would corrupt the source ("a" -> "ab").
		cp := *next
		cp.Content = cloneContent(next.Content)
		return &cp
	}
	if next.Content != nil {
		if agg.Content == nil {
			// Clone rather than alias: a later chunk's appendText must not reach
			// back into the model's buffer.
			agg.Content = cloneContent(next.Content)
		} else if text := concatText(next.Content); text != "" {
			appendText(agg.Content, text)
		}
	}
	if next.UsageMetadata != nil {
		agg.UsageMetadata = next.UsageMetadata
	}
	if next.GroundingMetadata != nil {
		agg.GroundingMetadata = next.GroundingMetadata
	}
	if next.FinishReason != "" {
		agg.FinishReason = next.FinishReason
	}
	if next.ModelVersion != "" {
		agg.ModelVersion = next.ModelVersion
	}
	return agg
}

// cloneContent returns a deep-enough copy of c for streaming aggregation: a
// fresh *genai.Content with a fresh Parts slice whose first text part can be
// appended to without touching the source. The Part structs are copied by value
// so mutating cp's text part leaves the model's buffer untouched.
func cloneContent(c *genai.Content) *genai.Content {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Parts != nil {
		cp.Parts = make([]*genai.Part, len(c.Parts))
		for i, p := range c.Parts {
			if p == nil {
				continue
			}
			pc := *p
			cp.Parts[i] = &pc
		}
	}
	return &cp
}

func concatText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range c.Parts {
		if p != nil {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func appendText(c *genai.Content, text string) {
	for _, p := range c.Parts {
		// Append onto the first text part to keep one coherent message.
		if p != nil && p.FunctionCall == nil && p.FunctionResponse == nil {
			p.Text += text
			return
		}
	}
	c.Parts = append(c.Parts, &genai.Part{Text: text})
}

// classifyModelError maps a model-call failure to Temporal's retry contract. A
// genai.APIError is classified by HTTP status (408/409/429 and 5xx retryable,
// other 4xx non-retryable); anything without a status is treated as a
// retryable transient fault.
func classifyModelError(err error) error {
	code := apiErrorCode(err)
	retryable := true
	if code != 0 {
		retryable = code == 408 || code == 409 || code == 429 || (code >= 500 && code < 600)
	}
	return newApplicationError(ErrorTypeModel, retryable, err, "model call failed: %v", err)
}

// apiErrorCode extracts the HTTP status from a genai.APIError, which the SDK
// returns sometimes by value and sometimes by pointer.
func apiErrorCode(err error) int {
	var ptr *genai.APIError
	if errors.As(err, &ptr) && ptr != nil {
		return ptr.Code
	}
	var val genai.APIError
	if errors.As(err, &val) {
		return val.Code
	}
	return 0
}
