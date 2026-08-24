package test_test

import (
	"context"
	"errors"
	"time"

	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

// helper workflow that is used for testing Nexus Updates and Queries
func counterWorkflow(ctx workflow.Context) (int, error) {
	counter := 0

	if err := workflow.SetQueryHandler(
		ctx,
		queryOp,
		func(fail, delay bool) (int, error) {
			if fail { // to verify query errors fail nexus op
				return 0, errors.New("random error (for testing)")
			}
			if delay { // to verify nexus timeouts fail correctly
				// workflow.Sleep is not allowed inside query handler(QueryFailedError), so simulate this way
				// Note that this is only test code, not production
				time.Sleep(6 * time.Second)
			}
			// return next available value for better testing
			return counter + 1, nil
		},
	); err != nil {
		return 0, err
	}

	updateHandler := func(
		ctx workflow.Context,
		amount int,
		sleepDuration time.Duration,
		waitForSignal string,
	) (updateAddOutput, error) {
		counter += amount
		newCounterVal := counter
		if waitForSignal != "" {
			workflow.GetSignalChannel(ctx, waitForSignal).Receive(ctx, nil)
		} else if sleepDuration != 0 {
			_ = workflow.Sleep(ctx, sleepDuration)
		}
		return updateAddOutput{Count: newCounterVal}, nil
	}

	// used for testing invalid updates
	updateValidator := func(ctx workflow.Context, amount int, sleepDuration time.Duration, waitForSignal string) error {
		if amount%5 != 0 {
			return invalidIncrementError
		}
		return nil
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx,
		addUpdate,
		updateHandler,
		workflow.UpdateHandlerOptions{
			Validator: updateValidator,
		},
	); err != nil {
		return 0, err
	}

	workflow.GetSignalChannel(ctx, doneSignal).Receive(ctx, nil)
	workflow.GetLogger(ctx).Info("finished workflow, exiting now...", "final counter", counter)
	return counter, nil
}

// helper utils for nexus tests

func checkForLink(links []*common.Link, requiredLink *common.Link) bool {
	for _, link := range links {
		if link.Equal(requiredLink) {
			return true
		}
	}
	return false
}

func getEventLinks(ctx context.Context,
	c client.Client, workflowRun client.WorkflowRun,
	filter func(e *history.HistoryEvent) bool,
) []*common.Link {
	events := getEvents(ctx, c, workflowRun, filter)
	eventLinks := make([]*common.Link, 0, len(events))
	for _, event := range events {
		eventLinks = append(eventLinks, event.GetLinks()...)
	}
	return eventLinks
}

func getEventIDByType(ctx context.Context, c client.Client, workflowRun client.WorkflowRun, eventType enums.EventType) int64 {
	events := getEvents(ctx, c, workflowRun, func(e *history.HistoryEvent) bool {
		return e.GetEventType() == eventType
	})
	if len(events) == 0 {
		return -1
	}
	return events[0].EventId
}

// get the request ID of the singular nexus op in this workflow run
func getNexusOpRequestID(ctx context.Context, c client.Client, workflowRun client.WorkflowRun) (string, error) {
	events := getEvents(ctx, c, workflowRun, func(e *history.HistoryEvent) bool {
		return e.GetEventType() == enums.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED
	})
	if len(events) == 0 {
		return "", errors.New("no nexus op event found")
	}
	if len(events) > 1 {
		return "", errors.New("multiple nexus ops events found, cannot determine specific requestID")
	}
	return events[0].GetNexusOperationScheduledEventAttributes().RequestId, nil
}

func getEvents(ctx context.Context,
	c client.Client, workflowRun client.WorkflowRun,
	filter func(e *history.HistoryEvent) bool,
) []*history.HistoryEvent {
	iter := c.GetWorkflowHistory(ctx,
		workflowRun.GetID(), workflowRun.GetRunID(),
		false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	events := []*history.HistoryEvent{}
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			continue
		}
		if filter(event) {
			events = append(events, event)
		}
	}
	return events
}
