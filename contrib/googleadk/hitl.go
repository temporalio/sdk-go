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
	"google.golang.org/genai"

	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/toolconfirmation"
)

// ConfirmationSignalName is a convenient default Temporal signal name for
// delivering human tool-confirmation decisions to a workflow. Using a Temporal
// Update instead is equally valid; this name is only a suggested default.
const ConfirmationSignalName = "googleadk-tool-confirmation"

// ConfirmationDecision is a human's response to a pending tool confirmation.
// Deliver it to the workflow (for example via a signal named
// ConfirmationSignalName, or an Update) and turn one or more into a resume
// message with ConfirmationResponse.
type ConfirmationDecision struct {
	// FunctionCallID is the ID of the adk_request_confirmation FunctionCall the
	// agent emitted (see PendingConfirmation.FunctionCallID).
	FunctionCallID string
	// Confirmed is the user's decision: true approves the original tool call,
	// false denies it.
	Confirmed bool
}

// PendingConfirmation describes a tool call the agent has paused on, awaiting a
// human's approval. It is produced by PendingConfirmations.
type PendingConfirmation struct {
	// FunctionCallID identifies the adk_request_confirmation FunctionCall; echo it
	// back in the matching ConfirmationDecision.
	FunctionCallID string
	// OriginalCall is the tool call the model intends to make once approved. It is
	// nil only if the confirmation event was malformed.
	OriginalCall *genai.FunctionCall
	// Hint is the human-readable explanation the tool supplied via
	// RequestConfirmation, when present.
	Hint string
}

// PendingConfirmations scans the events emitted by a single runner.Run pass and
// returns the human-in-the-loop tool confirmations awaiting a decision. ADK
// signals a confirmation request by emitting a FunctionCall named
// toolconfirmation.FunctionCallName ("adk_request_confirmation") and ending the
// invocation.
//
// When this returns a non-empty slice the agent has paused. Gather the human's
// decisions (for example by awaiting a Temporal signal or Update), build a
// resume message with ConfirmationResponse, and call runner.Run again with it —
// ADK re-queues the original tool call (or blocks it) based on each decision.
//
// This works because tools run in-workflow: a tool's RequestConfirmation call
// records the request in the workflow's own EventActions, which the runner then
// surfaces through these events.
func PendingConfirmations(events []*session.Event) []PendingConfirmation {
	var pending []PendingConfirmation
	for _, ev := range events {
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil || p.FunctionCall == nil || p.FunctionCall.Name != toolconfirmation.FunctionCallName {
				continue
			}
			fc := p.FunctionCall
			pc := PendingConfirmation{FunctionCallID: fc.ID, Hint: confirmationHint(fc)}
			if orig, err := toolconfirmation.OriginalCallFrom(fc); err == nil {
				pc.OriginalCall = orig
			}
			pending = append(pending, pc)
		}
	}
	return pending
}

// ConfirmationResponse builds the genai.Content that resumes a paused agent with
// one or more human decisions. Pass it as the new message to runner.Run: ADK
// matches each FunctionResponse to its adk_request_confirmation call by ID and
// then executes or blocks the original tool call.
func ConfirmationResponse(decisions ...ConfirmationDecision) *genai.Content {
	parts := make([]*genai.Part, 0, len(decisions))
	for _, d := range decisions {
		parts = append(parts, &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				ID:       d.FunctionCallID,
				Name:     toolconfirmation.FunctionCallName,
				Response: map[string]any{"confirmed": d.Confirmed},
			},
		})
	}
	return &genai.Content{Role: genai.RoleUser, Parts: parts}
}

// confirmationHint extracts the user-facing hint from an adk_request_confirmation
// FunctionCall's "toolConfirmation" argument, tolerating both the typed and the
// JSON-decoded (map) representations. It returns "" when no hint is present.
func confirmationHint(fc *genai.FunctionCall) string {
	v, ok := fc.Args["toolConfirmation"]
	if !ok {
		return ""
	}
	switch tc := v.(type) {
	case *toolconfirmation.ToolConfirmation:
		if tc != nil {
			return tc.Hint
		}
	case toolconfirmation.ToolConfirmation:
		return tc.Hint
	case map[string]any:
		if h, ok := tc["hint"].(string); ok {
			return h
		}
	}
	return ""
}
