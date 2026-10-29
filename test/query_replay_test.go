package test_test

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

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

	ctx, cancel := context.WithTimeout(ts.T().Context(), 3*time.Minute)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       ts.taskQueueName,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: 3 * time.Minute,
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
		ts.Positive(result.count, "workflow must be queried while activities execute")
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
	// Cache-disabled replay is quadratic in history size, so keep this smaller than the issue's stress reproducer.
	for range 250 {
		if err := workflow.ExecuteActivity(ctx, staleQueryReplayActivity).Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func staleQueryReplayActivity(context.Context) error { return nil }
