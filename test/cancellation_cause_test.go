package test_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func cancellationCauseWorkflow(ctx workflow.Context) (string, error) {
	if err := workflow.SetQueryHandler(ctx, "ready", func() (bool, error) {
		return ctx.Err() == nil && workflow.Cause(ctx) == nil, nil
	}); err != nil {
		return "", err
	}
	ctx.Done().Receive(ctx, nil)
	if ctx.Err() != workflow.ErrCanceled {
		return "", fmt.Errorf("context error = %v, want workflow.ErrCanceled", ctx.Err())
	}
	var canceledErr *temporal.CanceledError
	if cause := workflow.Cause(ctx); !errors.As(cause, &canceledErr) {
		return "", fmt.Errorf("cancellation cause = %T (%v), want *temporal.CanceledError", cause, cause)
	}
	return canceledErr.Error(), nil
}

func requestCancellationCauseWorkflow(ctx workflow.Context, workflowID, runID, reason string, legacy bool) error {
	if legacy {
		return workflow.RequestCancelExternalWorkflow(ctx, workflowID, runID).Get(ctx, nil)
	}
	return workflow.RequestCancelExternalWorkflowWithOptions(ctx, workflowID, runID, workflow.CancelExternalWorkflowOptions{
		Reason: reason,
	}).Get(ctx, nil)
}

func forwardCancellationCauseWorkflow(ctx workflow.Context, workflowID, runID, reason string, legacy bool) error {
	if err := workflow.SetQueryHandler(ctx, "ready", func() (bool, error) {
		return ctx.Err() == nil && workflow.Cause(ctx) == nil, nil
	}); err != nil {
		return err
	}
	ctx.Done().Receive(ctx, nil)
	var future workflow.Future
	if legacy {
		future = workflow.RequestCancelExternalWorkflow(ctx, workflowID, runID)
	} else {
		future = workflow.RequestCancelExternalWorkflowWithOptions(ctx, workflowID, runID,
			workflow.CancelExternalWorkflowOptions{Reason: reason})
	}
	disconnected, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	return future.Get(disconnected, nil)
}

func cancellationCauseParentWorkflow(ctx workflow.Context, childID string) (string, error) {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:          childID,
		WaitForCancellation: true,
	})
	child := workflow.ExecuteChildWorkflow(childCtx, cancellationCauseWorkflow)
	if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return "", err
	}
	if err := workflow.SetQueryHandler(ctx, "ready", func() (bool, error) {
		return ctx.Err() == nil && workflow.Cause(ctx) == nil, nil
	}); err != nil {
		return "", err
	}
	ctx.Done().Receive(ctx, nil)
	if workflow.Cause(ctx).Error() != "parent cancellation reason" {
		return "", fmt.Errorf("unexpected parent cancellation cause: %v", workflow.Cause(ctx))
	}
	disconnected, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	var childCause string
	if err := child.Get(disconnected, &childCause); err != nil {
		return "", err
	}
	return childCause, nil
}

func (ts *IntegrationTestSuite) TestClientCancellationCause() {
	ts.worker.RegisterWorkflow(cancellationCauseWorkflow)
	for _, legacy := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()
		run, err := ts.client.ExecuteWorkflow(ctx,
			ts.startWorkflowOptions("client-cancellation-cause-"+uuid.NewString()), cancellationCauseWorkflow)
		ts.NoError(err)
		ts.waitForQueryTrue(run, "ready")

		reason := "client supplied cancellation reason"
		if legacy {
			ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))
			reason = "canceled"
		} else {
			ts.NoError(ts.client.CancelWorkflowWithOptions(ctx, client.CancelWorkflowOptions{
				WorkflowID: run.GetID(),
				RunID:      run.GetRunID(),
				Reason:     reason,
			}))
		}
		var cause string
		ts.NoError(run.Get(ctx, &cause))
		ts.Equal(reason, cause)
		ts.replayCancellationCauseHistory(ctx, run)
	}
}

func (ts *IntegrationTestSuite) TestExternalWorkflowCancellationCause() {
	ts.worker.RegisterWorkflow(cancellationCauseWorkflow)
	ts.worker.RegisterWorkflow(requestCancellationCauseWorkflow)
	for _, legacy := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()
		target, err := ts.client.ExecuteWorkflow(ctx,
			ts.startWorkflowOptions("external-cancellation-target-"+uuid.NewString()), cancellationCauseWorkflow)
		ts.NoError(err)
		ts.waitForQueryTrue(target, "ready")

		reason := "external workflow supplied cancellation reason"
		requester, err := ts.client.ExecuteWorkflow(ctx,
			ts.startWorkflowOptions("external-cancellation-requester-"+uuid.NewString()),
			requestCancellationCauseWorkflow, target.GetID(), target.GetRunID(), reason, legacy)
		ts.NoError(err)
		ts.NoError(requester.Get(ctx, nil))
		var cause string
		ts.NoError(target.Get(ctx, &cause))
		if legacy {
			reason = "canceled"
		}
		ts.Equal(reason, cause)
		ts.replayCancellationCauseHistory(ctx, target)
		ts.replayCancellationCauseHistory(ctx, requester)
	}
}

func (ts *IntegrationTestSuite) TestExternalWorkflowCancellationCauseInheritance() {
	ts.worker.RegisterWorkflow(cancellationCauseWorkflow)
	ts.worker.RegisterWorkflow(forwardCancellationCauseWorkflow)
	for _, testCase := range []struct {
		reason string
		legacy bool
	}{
		{legacy: true},
		{},
		{reason: "explicit override"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()
		target, err := ts.client.ExecuteWorkflow(ctx,
			ts.startWorkflowOptions("inherited-cancellation-target-"+uuid.NewString()), cancellationCauseWorkflow)
		ts.NoError(err)
		ts.waitForQueryTrue(target, "ready")
		requester, err := ts.client.ExecuteWorkflow(ctx,
			ts.startWorkflowOptions("inherited-cancellation-requester-"+uuid.NewString()),
			forwardCancellationCauseWorkflow, target.GetID(), target.GetRunID(), testCase.reason, testCase.legacy)
		ts.NoError(err)
		ts.waitForQueryTrue(requester, "ready")
		ts.NoError(ts.client.CancelWorkflowWithOptions(ctx, client.CancelWorkflowOptions{
			WorkflowID: requester.GetID(),
			RunID:      requester.GetRunID(),
			Reason:     "inherited cancellation reason",
		}))
		ts.NoError(requester.Get(ctx, nil))
		var cause string
		ts.NoError(target.Get(ctx, &cause))
		expected := testCase.reason
		if expected == "" {
			expected = "inherited cancellation reason"
		}
		ts.Equal(expected, cause)
		ts.replayCancellationCauseHistory(ctx, target)
		ts.replayCancellationCauseHistory(ctx, requester)
	}
}

func (ts *IntegrationTestSuite) TestCancellationCauseForwardedToChild() {
	ts.worker.RegisterWorkflow(cancellationCauseWorkflow)
	ts.worker.RegisterWorkflow(cancellationCauseParentWorkflow)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	childID := "cancellation-cause-child-" + uuid.NewString()
	run, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions("cancellation-cause-parent-"+uuid.NewString()), cancellationCauseParentWorkflow, childID)
	ts.NoError(err)
	ts.waitForQueryTrue(run, "ready")
	ts.NoError(ts.client.CancelWorkflowWithOptions(ctx, client.CancelWorkflowOptions{
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
		Reason:     "parent cancellation reason",
	}))
	var childCause string
	ts.NoError(run.Get(ctx, &childCause))
	ts.Equal("parent cancellation reason", childCause)
	ts.replayCancellationCauseHistory(ctx, run)
	ts.replayCancellationCauseHistory(ctx, ts.client.GetWorkflow(ctx, childID, ""))
}

func (ts *IntegrationTestSuite) replayCancellationCauseHistory(ctx context.Context, run client.WorkflowRun) {
	ts.T().Helper()
	history := &historypb.History{}
	iter := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		history.Events = append(history.Events, event)
	}
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(cancellationCauseWorkflow)
	replayer.RegisterWorkflow(requestCancellationCauseWorkflow)
	replayer.RegisterWorkflow(forwardCancellationCauseWorkflow)
	replayer.RegisterWorkflow(cancellationCauseParentWorkflow)
	ts.NoError(replayer.ReplayWorkflowHistory(nil, history))
}
