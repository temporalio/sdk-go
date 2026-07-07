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
	"fmt"
	"strings"

	"google.golang.org/adk/v2/session"
)

// SessionSnapshot is a serializable copy of an ADK session — its identity,
// session-scoped state, and full ordered event history. It is JSON-encodable by
// Temporal's default data converter, so a workflow can hand it to
// workflow.NewContinueAsNewError to carry a long conversation across a
// continue-as-new boundary and rebuild the session with ImportSession in the
// next run.
//
// Only session-scoped state is captured. App- and user-scoped state (the "app:"
// and "user:" key prefixes), which the session service manages across sessions
// rather than within one, is not carried; persist that in a durable session
// service if you need it. Every value in State and every event must be
// JSON-serializable.
type SessionSnapshot struct {
	AppName   string
	UserID    string
	SessionID string
	State     map[string]any
	Events    []*session.Event
}

// ExportSession reads the named session from svc and returns a serializable
// snapshot of its session-scoped state and events. Call it when
// workflow.GetInfo(ctx).GetContinueAsNewSuggested() reports true (or on your own
// turn boundary) to capture the conversation before continuing as new. It runs
// entirely in memory over the in-workflow session service, so it is
// deterministic and safe to call inside a workflow.
func ExportSession(ctx context.Context, svc session.Service, appName, userID, sessionID string) (*SessionSnapshot, error) {
	resp, err := svc.Get(ctx, &session.GetRequest{AppName: appName, UserID: userID, SessionID: sessionID})
	if err != nil {
		return nil, fmt.Errorf("googleadk: export session %q: %w", sessionID, err)
	}
	if resp == nil || resp.Session == nil {
		return nil, fmt.Errorf("googleadk: export session %q: not found", sessionID)
	}
	s := resp.Session
	snap := &SessionSnapshot{
		AppName:   s.AppName(),
		UserID:    s.UserID(),
		SessionID: s.ID(),
		State:     map[string]any{},
	}
	for k, v := range s.State().All() {
		// Skip app/user (cross-session) and temp (transient) scopes; only
		// session-scoped keys belong to this session's continuation.
		if strings.HasPrefix(k, session.KeyPrefixApp) ||
			strings.HasPrefix(k, session.KeyPrefixUser) ||
			strings.HasPrefix(k, session.KeyPrefixTemp) {
			continue
		}
		snap.State[k] = v
	}
	for ev := range s.Events().All() {
		snap.Events = append(snap.Events, ev)
	}
	return snap, nil
}

// ImportSession recreates a session in svc from a snapshot: it creates the
// session with the snapshot's identity and session-scoped state, then re-appends
// every event in order (re-applying their state deltas). Call it at the top of a
// continued-as-new run, before runner.Run, to resume the conversation where it
// left off.
func ImportSession(ctx context.Context, svc session.Service, snap *SessionSnapshot) (session.Session, error) {
	if snap == nil {
		return nil, fmt.Errorf("googleadk: import session: nil snapshot")
	}
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   snap.AppName,
		UserID:    snap.UserID,
		SessionID: snap.SessionID,
		State:     snap.State,
	})
	if err != nil {
		return nil, fmt.Errorf("googleadk: import session %q: %w", snap.SessionID, err)
	}
	s := resp.Session
	for _, ev := range snap.Events {
		if ev == nil {
			continue
		}
		if err := svc.AppendEvent(ctx, s, ev); err != nil {
			return nil, fmt.Errorf("googleadk: import session %q: append event: %w", snap.SessionID, err)
		}
	}
	return s, nil
}
