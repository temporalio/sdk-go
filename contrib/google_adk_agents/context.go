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
	"sync"
	"time"

	"github.com/google/uuid"

	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/platform"
)

// wfCtxKey is the private context key under which NewContext stashes the
// workflow-context holder so the plugin's BeforeModelCallback /
// BeforeToolCallback can recover the *currently active* workflow.Context from
// the ADK context they are handed.
type wfCtxKey struct{}

// wfCtxHolder carries the workflow.Context that plugin callbacks should use for
// their blocking Activity dispatch. It exists because concurrent tool fan-out
// runs each ADK task in its own workflow.Go coroutine, and Temporal requires a
// blocking call (Future.Get / Channel.Receive) to use the *coroutine-local*
// context — blocking on the parent coroutine's context from a child triggers
// "trying to block on coroutine which is already blocked ... wrong Context".
//
// The opaque ADK task closures (func()) give us no way to thread a per-task
// context in, so the task runner swaps holder.current to the coroutine's gctx
// just before invoking each task. Workflow coroutines are cooperatively
// scheduled (never truly parallel), and a callback reads holder.current exactly
// once — synchronously, before its first blocking call — so the value it reads
// is always the coroutine it is running on. After fan-out completes the runner
// restores the root context for subsequent main-coroutine work.
type wfCtxHolder struct {
	current workflow.Context
}

// ContextOption customizes the bridged context produced by NewContext.
type ContextOption func(*contextConfig)

type contextConfig struct {
	sequentialToolFanout bool
}

// WithSequentialToolFanout disables the default concurrent tool fan-out and
// runs ADK's batched tool tasks one at a time on the workflow coroutine.
//
// By default NewContext installs a TaskRunner that schedules ADK's independent
// tool tasks as concurrent durable Activities using workflow.Go, joined
// deterministically through a workflow.Channel. That is the right choice when a
// single LLM turn fans out to several tool calls. Sequential mode is the fully
// determinism-safe fallback: every task runs to completion before the next
// starts, which removes any dependence on coroutine scheduling order at the
// cost of losing parallelism.
func WithSequentialToolFanout() ContextOption {
	return func(c *contextConfig) { c.sequentialToolFanout = true }
}

// NewContext bridges a workflow.Context into the context.Context that ADK reads
// its determinism and execution seams from, and returns it for passing to
// runner.Runner.Run.
//
// It installs three providers on the returned context:
//
//   - time:  platform.Now resolves to workflow.Now(ctx), so every ADK event
//     timestamp is deterministic and replay-stable.
//   - uuid:  platform.NewUUID resolves to a deterministic generator seeded from
//     a single workflow.SideEffect value plus a per-context counter, so IDs are
//     replay-stable without emitting one history event per ID.
//   - tasks: platform.RunTasks resolves to a workflow.Go-based fan-out (or
//     sequential execution when WithSequentialToolFanout is set), so ADK's tool
//     fan-out never spawns real goroutines inside the workflow.
//
// The workflow.Context itself is also stashed on the returned context so the
// plugin callbacks can dispatch Activities. Pass the result straight to Run:
//
//	for ev, err := range r.Run(googleadk.NewContext(ctx), userID, sessionID, msg, cfg) {
//	    // ...
//	}
func NewContext(ctx workflow.Context, opts ...ContextOption) context.Context {
	cfg := contextConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	holder := &wfCtxHolder{current: ctx}
	base := context.WithValue(context.Background(), wfCtxKey{}, holder)
	base = platform.WithTimeProvider(base, func() time.Time { return workflow.Now(ctx) })
	base = platform.WithUUIDProvider(base, newDeterministicUUIDProvider(ctx))
	base = platform.WithTaskRunner(base, newWorkflowTaskRunner(ctx, holder, cfg.sequentialToolFanout))
	return base
}

// workflowContext recovers the currently active workflow.Context from an ADK
// context (CallbackContext / ToolContext both embed context.Context). During
// concurrent tool fan-out this is the per-coroutine context set by the task
// runner; otherwise it is the root context stashed by NewContext.
func workflowContext(ctx context.Context) (workflow.Context, bool) {
	if ctx == nil {
		return nil, false
	}
	holder, ok := ctx.Value(wfCtxKey{}).(*wfCtxHolder)
	if !ok || holder == nil || holder.current == nil {
		return nil, false
	}
	return holder.current, true
}

// newDeterministicUUIDProvider returns a platform.UUIDProvider whose output is
// stable across workflow replays. The seed is captured once through
// workflow.SideEffect (recorded in history), and each call derives a fresh v5
// UUID from (seed, counter). Because the values are derived rather than drawn
// from a random source, no per-ID history event is written.
func newDeterministicUUIDProvider(ctx workflow.Context) platform.UUIDProvider {
	var (
		mu        sync.Mutex
		counter   uint64
		namespace uuid.UUID
		seeded    bool
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		if !seeded {
			var seed string
			// SideEffect records the seed in history, so replay reuses it.
			_ = workflow.SideEffect(ctx, func(workflow.Context) interface{} {
				return uuid.NewString()
			}).Get(&seed)
			namespace = uuid.NewSHA1(uuid.Nil, []byte(seed))
			seeded = true
		}
		counter++
		return uuid.NewSHA1(namespace, []byte(fmt.Sprintf("%d", counter))).String()
	}
}

// newWorkflowTaskRunner returns a platform.TaskRunner that executes ADK's
// batched tool tasks on the Temporal workflow dispatcher rather than on real
// OS goroutines. Concurrent mode runs each task in its own workflow.Go
// coroutine and joins them through a workflow.Channel; sequential mode runs
// them in order on the calling coroutine.
func newWorkflowTaskRunner(ctx workflow.Context, holder *wfCtxHolder, sequential bool) platform.TaskRunner {
	return func(_ context.Context, tasks []func()) {
		switch {
		case len(tasks) == 0:
			return
		case sequential || len(tasks) == 1:
			for _, t := range tasks {
				t()
			}
		default:
			root := holder.current
			done := workflow.NewChannel(ctx)
			for _, t := range tasks {
				t := t
				workflow.Go(ctx, func(gctx workflow.Context) {
					// Make this coroutine's context the one the plugin callbacks
					// dispatch on, so their Future.Get blocks on gctx (this
					// coroutine) rather than the parent that is blocked on the
					// join below. Cooperative scheduling guarantees the callback
					// reads holder.current before any other coroutine runs.
					holder.current = gctx
					t()
					done.Send(gctx, nil)
				})
			}
			for range tasks {
				done.Receive(ctx, nil)
			}
			// Restore the root context for subsequent main-coroutine dispatch.
			holder.current = root
		}
	}
}
