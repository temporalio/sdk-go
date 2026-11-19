package internal

import (
	"context"
	"fmt"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	updatepb "go.temporal.io/api/update/v1"

	"go.temporal.io/sdk/converter"
)

func (t *TaskHandlersTestSuite) TestQueryReplayTargetTaskMarkers() {
	const workflowName = "query-target-task-markers"
	sideEffectCalls, mutableSideEffectCalls, localActivityCalls := 0, 0, 0
	localActivity := func(context.Context) (int, error) {
		localActivityCalls++
		return -1, nil
	}
	t.registry.RegisterActivityWithOptions(localActivity, RegisterActivityOptions{Name: "query-marker-local-activity"})
	queryWorkflow := func(ctx Context) error {
		var version Version
		var sideEffect, mutableSideEffect, localResult int
		if err := SetQueryHandler(ctx, "state", func() (string, error) {
			return fmt.Sprintf("version=%d side=%d mutable=%d local=%d", version, sideEffect, mutableSideEffect, localResult), nil
		}); err != nil {
			return err
		}
		version = GetVersion(ctx, "query-marker-version", DefaultVersion, 7)
		if err := SideEffect(ctx, func(Context) interface{} {
			sideEffectCalls++
			return -1
		}).Get(&sideEffect); err != nil {
			return err
		}
		if err := MutableSideEffect(ctx, "query-marker-mutable", func(Context) interface{} {
			mutableSideEffectCalls++
			return -1
		}, func(a, b interface{}) bool { return a == b }).Get(&mutableSideEffect); err != nil {
			return err
		}
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{StartToCloseTimeout: time.Minute})
		if err := ExecuteLocalActivity(ctx, "query-marker-local-activity").Get(ctx, &localResult); err != nil {
			return err
		}
		return NewTimer(ctx, time.Minute).Get(ctx, nil)
	}
	t.registry.RegisterWorkflowWithOptions(queryWorkflow, RegisterWorkflowOptions{Name: workflowName})

	payloads := func(values ...interface{}) *commonpb.Payloads {
		result, err := converter.GetDefaultDataConverter().ToPayloads(values...)
		t.Require().NoError(err)
		return result
	}
	events := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 2,
			StartedEventId:   3,
		}),
		createTestEventVersionMarker(5, 4, "query-marker-version", 7),
		createTestUpsertWorkflowSearchAttributesForChangeVersion(6, 4, "query-marker-version", 7),
		createTestEventSideEffectMarker(7, 4, 1, 42),
		createTestEventMarkerRecorded(8, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   mutableSideEffectMarkerName,
			WorkflowTaskCompletedEventId: 4,
			Details: map[string]*commonpb.Payloads{
				sideEffectMarkerIDName:           payloads("query-marker-mutable_8"),
				sideEffectMarkerDataName:         payloads("query-marker-mutable", payloads(43)),
				mutableSideEffectCallCounterName: payloads(1),
			},
		}),
		{
			EventId:         9,
			EventType:       enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_OPTIONS_UPDATED,
			WorkerMayIgnore: true,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionOptionsUpdatedEventAttributes{
				WorkflowExecutionOptionsUpdatedEventAttributes: &historypb.WorkflowExecutionOptionsUpdatedEventAttributes{},
			},
		},
		createTestEventMarkerRecorded(10, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			WorkflowTaskCompletedEventId: 4,
			Details: map[string]*commonpb.Payloads{
				localActivityMarkerDataName: payloads(localActivityMarkerData{
					ActivityID:   "1",
					ActivityType: "query-marker-local-activity",
					ReplayTime:   time.Unix(123, 0),
					Attempt:      1,
				}),
				localActivityResultName: payloads(44),
			},
		}),
		createTestEventTimerStarted(11, 11),
	}
	for _, paginated := range []bool{false, true} {
		t.Run(fmt.Sprintf("paginated=%t", paginated), func() {
			page := 0
			wftask := workflowTask{
				task: createQueryTask(nil, 3, workflowName, "state"),
				historyIterator: &historyIteratorImpl{
					iteratorFunc: func(token []byte) (*historypb.History, []byte, error) {
						if !paginated {
							t.Empty(token)
							return &historypb.History{Events: events}, nil, nil
						}
						page++
						switch page {
						case 1:
							t.Empty(token)
							return &historypb.History{Events: events[:4]}, []byte("version-and-side-effect"), nil
						case 2:
							t.Equal("version-and-side-effect", string(token))
							return &historypb.History{Events: events[4:7]}, []byte("mutable-and-local-activity"), nil
						default:
							t.Equal(3, page)
							t.Equal("mutable-and-local-activity", string(token))
							return &historypb.History{Events: events[7:]}, nil, nil
						}
					},
				},
			}
			taskHandler := newWorkflowTaskHandler(t.getTestWorkerExecutionParams(), nil, t.registry)
			wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
			completion, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
			wfctx.Unlock(err)
			t.Require().NoError(err)
			t.verifyQueryResult(completion, "version=7 side=42 mutable=43 local=44")
			t.Zero(sideEffectCalls, "replay must use the recorded side effect")
			t.Zero(mutableSideEffectCalls, "replay must use the recorded mutable side effect")
			t.Zero(localActivityCalls, "replay must use the recorded local activity result")
			if paginated {
				t.Equal(3, page)
			}
		})
	}
}

func (t *TaskHandlersTestSuite) TestQueryReplayTargetTaskAcceptedUpdate() {
	const workflowName = "query-target-task-accepted-update"
	queryWorkflow := func(ctx Context) error {
		state := "initial"
		if err := SetQueryHandler(ctx, "state", func() (string, error) {
			return state, nil
		}); err != nil {
			return err
		}
		if err := SetUpdateHandler(ctx, "set-state", func(ctx Context, value string) error {
			state = value
			return Await(ctx, func() bool { return false })
		}, UpdateHandlerOptions{}); err != nil {
			return err
		}
		return Await(ctx, func() bool { return false })
	}
	t.registry.RegisterWorkflowWithOptions(queryWorkflow, RegisterWorkflowOptions{Name: workflowName})
	request := func(id, value string) *updatepb.Request {
		args, err := converter.GetDefaultDataConverter().ToPayloads(value)
		t.Require().NoError(err)
		return &updatepb.Request{
			Meta:  &updatepb.Meta{UpdateId: id},
			Input: &updatepb.Input{Name: "set-state", Args: args},
		}
	}
	events := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 2,
			StartedEventId:   3,
		}),
		createTestEventWorkflowExecutionUpdateAccepted(5, &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
			AcceptedRequestSequencingEventId: 2,
			ProtocolInstanceId:               "target-update",
			AcceptedRequest:                  request("target-update", "target update ran"),
		}),
		createTestEventWorkflowExecutionUpdateAdmitted(6, &historypb.WorkflowExecutionUpdateAdmittedEventAttributes{
			Request: request("future-update", "future update ran"),
		}),
		createTestEventWorkflowTaskScheduled(7, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(8),
	}
	wftask := workflowTask{
		task: createQueryTask(nil, 3, workflowName, "state"),
		historyIterator: &historyIteratorImpl{
			iteratorFunc: func(token []byte) (*historypb.History, []byte, error) {
				t.Empty(token)
				return &historypb.History{Events: events}, nil, nil
			},
		},
	}
	taskHandler := newWorkflowTaskHandler(t.getTestWorkerExecutionParams(), nil, t.registry)
	wfctx := t.mustWorkflowContextImpl(&wftask, taskHandler)
	completion, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	t.Require().NoError(err)
	t.verifyQueryResult(completion, "target update ran")
}
