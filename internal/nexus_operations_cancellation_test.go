package internal

import (
	"testing"

	commonpb "go.temporal.io/api/common/v1"
)

type captureNexusEnv struct {
	WorkflowEnvironment
	started   func(token string, err error)
	completed func(*commonpb.Payload, error)
}

func (e *captureNexusEnv) ExecuteNexusOperation(
	_ ExecuteNexusOperationParams,
	callback func(*commonpb.Payload, error),
	startedHandler func(token string, e error),
) int64 {
	e.started = startedHandler
	e.completed = callback
	return 1
}

func (e *captureNexusEnv) AbandonNexusOperation(int64) {
	e.started = nil
	e.completed = nil
}

// deliverStarted and deliverCompleted mimic the event handlers, which invoke a callback only
// while it is still set (see handleNexusOperationStarted / handleNexusOperationCompleted).
func (e *captureNexusEnv) deliverStarted(token string, err error) {
	if e.started != nil {
		e.started(token, err)
	}
}

func (e *captureNexusEnv) deliverCompleted(result *commonpb.Payload, err error) {
	if e.completed != nil {
		e.completed(result, err)
	}
}

// This cannot be reproduced through the workflow test environment as an ordinary workflow: its
// Nexus operation handle independently guards duplicate starts/completions (see
// testNexusOperationHandle), which masks the unguarded callbacks in the real event handler. So we
// drive the interceptor directly and feed it the late events a real environment would deliver.
func TestNexusAbandonCancellationClearsCallbacks(t *testing.T) {
	interceptor, ctx := createRootTestContext()
	capture := &captureNexusEnv{WorkflowEnvironment: interceptor.env}
	interceptor.env = capture

	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		cancelCtx, cancel := WithCancel(ctx)
		interceptor.ExecuteNexusOperation(cancelCtx, ExecuteNexusOperationInput{
			Client:    NewNexusClient("endpoint", "service"),
			Operation: "operation",
			Options:   NexusOperationOptions{CancellationType: NexusOperationCancellationTypeAbandon},
		})
		cancel()
		capture.deliverStarted("token", nil)
		capture.deliverCompleted(nil, nil)
	}, func() bool { return false })
	d.interceptor = interceptor
	defer d.Close()

	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
}
