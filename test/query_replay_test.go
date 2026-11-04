package test_test

import (
	"context"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func (ts *IntegrationTestSuite) TestQueryClosedWorkflowWithPendingSignal() {
	ts.worker.Stop()
	ts.workerStopped = true
	worker.SetStickyWorkflowCacheSize(0)
	defer worker.SetStickyWorkflowCacheSize(ts.config.maxWorkflowCacheSize)

	w := worker.New(ts.client, ts.taskQueueName, worker.Options{})
	w.RegisterWorkflow(queryClosedWorkflowWithPendingSignal)
	ts.Require().NoError(w.Start())
	stopped := false
	defer func() {
		if !stopped {
			w.Stop()
		}
	}()

	ctx, cancel := context.WithTimeout(ts.T().Context(), time.Minute)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       ts.taskQueueName,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: time.Minute,
	}, queryClosedWorkflowWithPendingSignal)
	ts.Require().NoError(err)
	terminated := false
	defer func() {
		if !terminated {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cleanupCancel()
			ts.NoError(ts.client.TerminateWorkflow(cleanupCtx, run.GetID(), run.GetRunID(), "test cleanup"))
		}
	}()

	value, err := ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "state")
	ts.Require().NoError(err)
	var state string
	ts.Require().NoError(value.Get(&state))
	ts.Require().Equal("waiting", state)

	// No worker may start the signal's workflow task before termination.
	w.Stop()
	stopped = true
	ts.Require().NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "signal", nil))
	ts.Require().NoError(ts.client.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test termination"))
	terminated = true

	var completedID, signalID, scheduledID, terminatedID int64
	history := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for history.HasNext() {
		event, err := history.Next()
		ts.Require().NoError(err)
		switch event.GetEventType() {
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED:
			completedID = event.GetEventId()
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
			signalID = event.GetEventId()
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED:
			if signalID != 0 {
				scheduledID = event.GetEventId()
			}
		case enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED:
			ts.Require().Zero(signalID, "the signal must not be processed by a workflow task")
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
			terminatedID = event.GetEventId()
		}
	}
	ts.Require().Positive(completedID, "the initial workflow task must have completed")
	ts.Require().Greater(signalID, completedID)
	ts.Require().Greater(scheduledID, signalID)
	ts.Require().Greater(terminatedID, scheduledID)

	queryWorker := worker.New(ts.client, ts.taskQueueName, worker.Options{})
	queryWorker.RegisterWorkflow(queryClosedWorkflowWithPendingSignal)
	ts.Require().NoError(queryWorker.Start())
	defer queryWorker.Stop()

	value, err = ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "state")
	ts.Require().NoError(err)
	ts.Require().NoError(value.Get(&state))
	ts.Equal("waiting", state, "query reconstruction must not execute a signal after its last completed workflow task")
}

func queryClosedWorkflowWithPendingSignal(ctx workflow.Context) error {
	state := "waiting"
	if err := workflow.SetQueryHandler(ctx, "state", func() (string, error) { return state, nil }); err != nil {
		return err
	}
	workflow.GetSignalChannel(ctx, "signal").Receive(ctx, nil)
	state = "received"
	return workflow.Await(ctx, func() bool { return false })
}

func (ts *IntegrationTestSuite) TestStaleQueryReplayBoundary() {
	ts.worker.Stop()
	ts.workerStopped = true
	worker.SetStickyWorkflowCacheSize(0)
	defer worker.SetStickyWorkflowCacheSize(ts.config.maxWorkflowCacheSize)

	w := worker.New(ts.client, ts.taskQueueName, worker.Options{
		WorkflowPanicPolicy: worker.FailWorkflow,
	})
	w.RegisterWorkflow(staleQueryReplayWorkflow)
	w.RegisterActivity(staleQueryReplayActivity)
	ts.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(ts.T().Context(), ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       ts.taskQueueName,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: time.Minute,
	}, staleQueryReplayWorkflow)
	ts.NoError(err)
	completed := false
	defer func() {
		if !completed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cleanupCancel()
			ts.NoError(ts.client.TerminateWorkflow(cleanupCtx, run.GetID(), run.GetRunID(), "test cleanup"))
		}
	}()

	type queryResult struct {
		count int
		err   error
	}
	results := make(chan queryResult, 1)
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		result := queryResult{}
		defer func() { results <- result }()
		for ctx.Err() == nil {
			value, err := ts.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "progress")
			if err != nil {
				if ctx.Err() == nil {
					result.err = err
					cancel()
				}
				return
			}
			var progress string
			if err := value.Get(&progress); err != nil {
				result.err = err
				cancel()
				return
			}
			if progress != "ok" {
				result.err = fmt.Errorf("progress query returned %q, want %q", progress, "ok")
				cancel()
				return
			}
			result.count++
			if result.count == 1 {
				if err := ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil); err != nil {
					result.err = err
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		select {
		case <-queryDone:
		case <-time.After(10 * time.Second):
			ts.Fail("progress query goroutine did not stop after cancellation")
		}
	}()

	err = run.Get(ctx, nil)
	completed = err == nil
	cancel()
	select {
	case result := <-results:
		ts.NoError(result.err, "progress queries must succeed during replay")
		ts.Positive(result.count, "workflow must be queried before it completes")
	case <-time.After(10 * time.Second):
		ts.FailNow("progress query goroutine did not stop after cancellation")
	}
	ts.NoError(err, "workflow must complete while queries replay its history")
}

// Keep replaying a growing history while queries race workflow-task completion (#755).
func staleQueryReplayWorkflow(ctx workflow.Context) error {
	if err := workflow.SetQueryHandler(ctx, "progress", func() (string, error) { return "ok", nil }); err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	for range 10 {
		if err := workflow.ExecuteActivity(ctx, staleQueryReplayActivity).Get(ctx, nil); err != nil {
			return err
		}
	}
	// The first successful query releases completion, so this does not depend on query/workflow timing.
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	return nil
}

func staleQueryReplayActivity(context.Context) error { return nil }
