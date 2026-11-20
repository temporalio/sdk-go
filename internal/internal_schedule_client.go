package internal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/sdk/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
)

type (

	// ScheduleClient is the client for starting a workflow execution.
	scheduleClient struct {
		workflowClient *WorkflowClient
	}

	// scheduleHandleImpl is the implementation of ScheduleHandle.
	scheduleHandleImpl struct {
		ID     string
		client *WorkflowClient
	}

	// scheduleListIteratorImpl is the implementation of ScheduleListIterator
	scheduleListIteratorImpl struct {
		// nextScheduleIndex - Local cached schedules events and corresponding consuming index
		nextScheduleIndex int

		// err - From getting the latest page of schedules
		err error

		// response - From getting the latest page of schedules
		response *workflowservice.ListSchedulesResponse

		// paginate - Function which use a next token to get next page of schedules events
		paginate func(nexttoken []byte) (*workflowservice.ListSchedulesResponse, error)
	}
)

func (w *workflowClientInterceptor) CreateSchedule(ctx context.Context, in *ScheduleClientCreateInput) (ScheduleHandle, error) {
	// This is always set before interceptor is invoked
	ID := in.Options.ID
	if ID == "" {
		return nil, fmt.Errorf("no schedule ID in options")
	}

	dataConverter := WithContext(ctx, w.client.dataConverter)
	if dataConverter == nil {
		dataConverter = converter.GetDefaultDataConverter()
	}

	if in.Options.Action == nil {
		return nil, fmt.Errorf("no schedule action in options")
	}
	action, _, err := convertToPBScheduleAction(ctx, w.client, in.Options.Action, nil)
	if err != nil {
		return nil, err
	}

	memo, err := GetWorkflowMemo(in.Options.Memo, dataConverter, sdkFlagsAllowed[SDKFlagMemoUserDCEncode])
	if err != nil {
		return nil, err
	}

	searchAttr, err := SerializeSearchAttributes(in.Options.SearchAttributes, in.Options.TypedSearchAttributes)
	if err != nil {
		return nil, err
	}

	var triggerImmediately *schedulepb.TriggerImmediatelyRequest
	if in.Options.TriggerImmediately {
		triggerImmediately = &schedulepb.TriggerImmediatelyRequest{
			OverlapPolicy: in.Options.Overlap,
		}
	}

	backfillRequests := convertToPBBackfillList(in.Options.ScheduleBackfill)

	// Only send an initial patch if we need to.
	var initialPatch *schedulepb.SchedulePatch
	if in.Options.TriggerImmediately || len(in.Options.ScheduleBackfill) > 0 {
		initialPatch = &schedulepb.SchedulePatch{
			TriggerImmediately: triggerImmediately,
			BackfillRequest:    backfillRequests,
		}
	}

	var catchupWindow *durationpb.Duration
	if in.Options.CatchupWindow != 0 {
		// Leave zero unset so the server applies its default catchup window.
		catchupWindow = durationpb.New(in.Options.CatchupWindow)
	}

	// run propagators to extract information about tracing and other stuff, store in headers field
	startRequest := &workflowservice.CreateScheduleRequest{
		Namespace:  w.client.namespace,
		ScheduleId: ID,
		RequestId:  uuid.NewString(),
		Schedule: &schedulepb.Schedule{
			Spec:   convertToPBScheduleSpec(&in.Options.Spec),
			Action: action,
			Policies: &schedulepb.SchedulePolicies{
				OverlapPolicy:  in.Options.Overlap,
				CatchupWindow:  catchupWindow,
				PauseOnFailure: in.Options.PauseOnFailure,
			},
			State: &schedulepb.ScheduleState{
				Notes:            in.Options.Note,
				Paused:           in.Options.Paused,
				LimitedActions:   in.Options.RemainingActions != 0,
				RemainingActions: int64(in.Options.RemainingActions),
			},
		},
		InitialPatch:     initialPatch,
		Identity:         w.client.identity,
		Memo:             memo,
		SearchAttributes: searchAttr,
	}

	storeCtx := extstore.WithStorageTarget(ctx, extstore.StorageDriverWorkflowInfo{
		Namespace:    w.client.namespace,
		WorkflowID:   action.GetStartWorkflow().GetWorkflowId(),
		WorkflowType: action.GetStartWorkflow().GetWorkflowType().GetName(),
	})
	if err := visitProtoPayloads(storeCtx, w.outboundPayloadVisitor, startRequest, 0); err != nil {
		return nil, err
	}

	grpcCtx, cancel := newGRPCContext(ctx, w.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()

	_, err = w.client.workflowService.CreateSchedule(grpcCtx, startRequest)
	if _, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); ok {
		return nil, ErrScheduleAlreadyRunning
	}
	if err != nil {
		return nil, err
	}

	return &scheduleHandleImpl{
		ID:     ID,
		client: w.client,
	}, nil
}

func (sc *scheduleClient) Create(ctx context.Context, options ScheduleOptions) (ScheduleHandle, error) {
	if err := sc.workflowClient.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	// Set header before interceptor run
	ctx = contextWithNewHeader(ctx)

	// Run via interceptor
	return sc.workflowClient.interceptor.CreateSchedule(ctx, &ScheduleClientCreateInput{
		Options: &options,
	})
}

func (sc *scheduleClient) GetHandle(ctx context.Context, scheduleID string) ScheduleHandle {
	return &scheduleHandleImpl{
		ID:     scheduleID,
		client: sc.workflowClient,
	}
}

func (sc *scheduleClient) List(ctx context.Context, options ScheduleListOptions) (ScheduleListIterator, error) {
	paginate := func(nextToken []byte) (*workflowservice.ListSchedulesResponse, error) {
		grpcCtx, cancel := newGRPCContext(ctx, sc.workflowClient.rpcTimeout, defaultGrpcRetryParameters(ctx))
		defer cancel()
		request := &workflowservice.ListSchedulesRequest{
			Namespace:       sc.workflowClient.namespace,
			MaximumPageSize: int32(options.PageSize),
			NextPageToken:   nextToken,
			Query:           options.Query,
		}

		return sc.workflowClient.workflowService.ListSchedules(grpcCtx, request)
	}

	return &scheduleListIteratorImpl{
		paginate: paginate,
	}, nil
}

func (iter *scheduleListIteratorImpl) HasNext() bool {
	if iter.err == nil {
		if iter.response == nil ||
			(iter.nextScheduleIndex >= len(iter.response.Schedules) && len(iter.response.NextPageToken) > 0) {
			iter.response, iter.err = iter.paginate(iter.response.GetNextPageToken())
			iter.nextScheduleIndex = 0
		}
	}
	return iter.nextScheduleIndex < len(iter.response.GetSchedules()) || iter.err != nil
}

func (iter *scheduleListIteratorImpl) Next() (*ScheduleListEntry, error) {
	if !iter.HasNext() {
		panic("ScheduleListIterator Next() called without checking HasNext()")
	} else if iter.err != nil {
		return nil, iter.err
	}
	schedule := iter.response.Schedules[iter.nextScheduleIndex]
	iter.nextScheduleIndex++
	return convertFromPBScheduleListEntry(schedule), nil
}

func (scheduleHandle *scheduleHandleImpl) GetID() string {
	return scheduleHandle.ID
}

func (scheduleHandle *scheduleHandleImpl) Delete(ctx context.Context) error {
	request := &workflowservice.DeleteScheduleRequest{
		Namespace:  scheduleHandle.client.namespace,
		ScheduleId: scheduleHandle.ID,
		Identity:   scheduleHandle.client.identity,
	}
	grpcCtx, cancel := newGRPCContext(ctx, scheduleHandle.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()
	_, err := scheduleHandle.client.workflowService.DeleteSchedule(grpcCtx, request)
	return err
}

func (scheduleHandle *scheduleHandleImpl) Backfill(ctx context.Context, options ScheduleBackfillOptions) error {
	request := &workflowservice.PatchScheduleRequest{
		Namespace:  scheduleHandle.client.namespace,
		ScheduleId: scheduleHandle.ID,
		Patch: &schedulepb.SchedulePatch{
			BackfillRequest: convertToPBBackfillList(options.Backfill),
		},
		Identity:  scheduleHandle.client.identity,
		RequestId: uuid.NewString(),
	}
	grpcCtx, cancel := newGRPCContext(ctx, scheduleHandle.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()
	_, err := scheduleHandle.client.workflowService.PatchSchedule(grpcCtx, request)
	return err
}

func (scheduleHandle *scheduleHandleImpl) Update(ctx context.Context, options ScheduleUpdateOptions) error {
	grpcCtx, cancel := newGRPCContext(ctx, scheduleHandle.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()
	ctx = contextWithNewHeader(ctx)

	describeRequest := &workflowservice.DescribeScheduleRequest{
		Namespace:  scheduleHandle.client.namespace,
		ScheduleId: scheduleHandle.ID,
	}
	describeResponse, err := scheduleHandle.client.workflowService.DescribeSchedule(grpcCtx, describeRequest)
	if err != nil {
		return err
	}
	scheduleDescription, err := scheduleDescriptionFromPB(ctx, scheduleHandle.client, describeResponse)
	if err != nil {
		return err
	}
	// Captured before DoUpdate, which may change the described action in place.
	described := newDescribedScheduleAction(describeResponse.GetSchedule().GetAction(), scheduleDescription.Schedule.Action)
	newSchedule, err := options.DoUpdate(ScheduleUpdateInput{
		Description: *scheduleDescription,
	})
	if err != nil {
		if errors.Is(err, ErrSkipScheduleUpdate) {
			return nil
		}
		return err
	}
	newSchedulePB, retained, err := convertToPBSchedule(ctx, scheduleHandle.client, newSchedule.Schedule, described)
	if err != nil {
		return err
	}

	var newSA *commonpb.SearchAttributes
	attributes := newSchedule.TypedSearchAttributes
	if attributes != nil {
		newSA, err = serializeTypedSearchAttributes(attributes.GetUntypedValues())
		if err != nil {
			return err
		}
	}

	updateRequest := &workflowservice.UpdateScheduleRequest{
		Namespace:        scheduleHandle.client.namespace,
		ScheduleId:       scheduleHandle.ID,
		Schedule:         newSchedulePB,
		ConflictToken:    nil,
		Identity:         scheduleHandle.client.identity,
		RequestId:        uuid.NewString(),
		SearchAttributes: newSA,
	}

	storeCtx := extstore.WithStorageTarget(ctx, extstore.StorageDriverWorkflowInfo{
		Namespace:    scheduleHandle.client.namespace,
		WorkflowID:   newSchedulePB.GetAction().GetStartWorkflow().GetWorkflowId(),
		WorkflowType: newSchedulePB.GetAction().GetStartWorkflow().GetWorkflowType().GetName(),
	})

	// Retained payloads went through storage when they were first sent, so they are restored only
	// after the update's own payloads have been stored.
	if err := visitProtoPayloads(storeCtx, scheduleHandle.client.newStoragePayloadVisitor(), updateRequest, 0); err != nil {
		return err
	}
	if newSchedulePB != nil {
		retained.restore(newSchedulePB.Action)
	}
	if err := visitProtoPayloads(ctx, scheduleHandle.client.newPayloadLimitsCheckVisitor(), updateRequest, 0); err != nil {
		return err
	}

	_, err = scheduleHandle.client.workflowService.UpdateSchedule(grpcCtx, updateRequest)
	return err
}

func (scheduleHandle *scheduleHandleImpl) Describe(ctx context.Context) (*ScheduleDescription, error) {
	request := &workflowservice.DescribeScheduleRequest{
		Namespace:  scheduleHandle.client.namespace,
		ScheduleId: scheduleHandle.ID,
	}
	grpcCtx, cancel := newGRPCContext(ctx, scheduleHandle.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()
	describeResponse, err := scheduleHandle.client.workflowService.DescribeSchedule(grpcCtx, request)
	if err != nil {
		return nil, err
	}
	return scheduleDescriptionFromPB(ctx, scheduleHandle.client, describeResponse)
}

func (scheduleHandle *scheduleHandleImpl) Trigger(ctx context.Context, options ScheduleTriggerOptions) error {
	request := &workflowservice.PatchScheduleRequest{
		Namespace:  scheduleHandle.client.namespace,
		ScheduleId: scheduleHandle.ID,
		Patch: &schedulepb.SchedulePatch{
			TriggerImmediately: &schedulepb.TriggerImmediatelyRequest{
				OverlapPolicy: options.Overlap,
			},
		},
		Identity:  scheduleHandle.client.identity,
		RequestId: uuid.NewString(),
	}
	grpcCtx, cancel := newGRPCContext(ctx, scheduleHandle.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()
	_, err := scheduleHandle.client.workflowService.PatchSchedule(grpcCtx, request)
	return err
}

func (scheduleHandle *scheduleHandleImpl) Pause(ctx context.Context, options SchedulePauseOptions) error {
	pauseNote := "Paused via Go SDK"
	if options.Note != "" {
		pauseNote = options.Note
	}
	request := &workflowservice.PatchScheduleRequest{
		Namespace:  scheduleHandle.client.namespace,
		ScheduleId: scheduleHandle.ID,
		Patch: &schedulepb.SchedulePatch{
			Pause: pauseNote,
		},
		Identity:  scheduleHandle.client.identity,
		RequestId: uuid.NewString(),
	}
	grpcCtx, cancel := newGRPCContext(ctx, scheduleHandle.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()
	_, err := scheduleHandle.client.workflowService.PatchSchedule(grpcCtx, request)
	return err
}

func (scheduleHandle *scheduleHandleImpl) Unpause(ctx context.Context, options ScheduleUnpauseOptions) error {
	unpauseNote := "Unpaused via Go SDK"
	if options.Note != "" {
		unpauseNote = options.Note
	}
	request := &workflowservice.PatchScheduleRequest{
		Namespace:  scheduleHandle.client.namespace,
		ScheduleId: scheduleHandle.ID,
		Patch: &schedulepb.SchedulePatch{
			Unpause: unpauseNote,
		},
		Identity:  scheduleHandle.client.identity,
		RequestId: uuid.NewString(),
	}
	grpcCtx, cancel := newGRPCContext(ctx, scheduleHandle.client.rpcTimeout, defaultGrpcRetryParameters(ctx))
	defer cancel()
	_, err := scheduleHandle.client.workflowService.PatchSchedule(grpcCtx, request)
	return err
}

func convertToPBScheduleSpec(scheduleSpec *ScheduleSpec) *schedulepb.ScheduleSpec {
	if scheduleSpec == nil {
		return nil
	}

	calendar := convertToPBScheduleCalendarSpecList(scheduleSpec.Calendars)

	intervals := make([]*schedulepb.IntervalSpec, len(scheduleSpec.Intervals))
	for i, interval := range scheduleSpec.Intervals {
		intervalSpec := interval
		intervals[i] = &schedulepb.IntervalSpec{
			Interval: durationpb.New(intervalSpec.Every),
			Phase:    durationpb.New(intervalSpec.Offset),
		}
	}

	skip := convertToPBScheduleCalendarSpecList(scheduleSpec.Skip)

	var startTime *timestamppb.Timestamp
	if !scheduleSpec.StartAt.IsZero() {
		startTime = timestamppb.New(scheduleSpec.StartAt)
	}

	var endTime *timestamppb.Timestamp
	if !scheduleSpec.EndAt.IsZero() {
		endTime = timestamppb.New(scheduleSpec.EndAt)
	}

	return &schedulepb.ScheduleSpec{
		StructuredCalendar:        calendar,
		Interval:                  intervals,
		CronString:                scheduleSpec.CronExpressions,
		ExcludeStructuredCalendar: skip,
		StartTime:                 startTime,
		EndTime:                   endTime,
		Jitter:                    durationpb.New(scheduleSpec.Jitter),
		// TODO support custom time zone data
		TimezoneName: scheduleSpec.TimeZoneName,
	}
}

func convertFromPBScheduleSpec(scheduleSpec *schedulepb.ScheduleSpec) *ScheduleSpec {
	if scheduleSpec == nil {
		return nil
	}

	calendars := convertFromPBScheduleCalendarSpecList(scheduleSpec.GetStructuredCalendar())

	intervals := make([]ScheduleIntervalSpec, len(scheduleSpec.GetInterval()))
	for i, s := range scheduleSpec.GetInterval() {
		intervals[i] = ScheduleIntervalSpec{
			Every:  s.Interval.AsDuration(),
			Offset: s.Phase.AsDuration(),
		}
	}

	skip := convertFromPBScheduleCalendarSpecList(scheduleSpec.GetExcludeStructuredCalendar())

	startAt := time.Time{}
	if scheduleSpec.GetStartTime() != nil {
		startAt = scheduleSpec.GetStartTime().AsTime()
	}

	endAt := time.Time{}
	if scheduleSpec.GetEndTime() != nil {
		endAt = scheduleSpec.GetEndTime().AsTime()
	}

	return &ScheduleSpec{
		Calendars:    calendars,
		Intervals:    intervals,
		Skip:         skip,
		StartAt:      startAt,
		EndAt:        endAt,
		Jitter:       scheduleSpec.GetJitter().AsDuration(),
		TimeZoneName: scheduleSpec.GetTimezoneName(),
	}
}

func scheduleDescriptionFromPB(
	ctx context.Context,
	client *WorkflowClient,
	describeResponse *workflowservice.DescribeScheduleResponse,
) (*ScheduleDescription, error) {
	if describeResponse == nil {
		return nil, nil
	}

	runningWorkflows := make([]ScheduleWorkflowExecution, len(describeResponse.Info.GetRunningWorkflows()))
	for i, s := range describeResponse.Info.GetRunningWorkflows() {
		runningWorkflows[i] = ScheduleWorkflowExecution{
			WorkflowID:          s.GetWorkflowId(),
			FirstExecutionRunID: s.GetRunId(),
		}
	}

	recentActions := convertFromPBScheduleActionResultList(describeResponse.Info.GetRecentActions())

	nextActionTimes := make([]time.Time, len(describeResponse.Info.GetFutureActionTimes()))
	for i, t := range describeResponse.Info.GetFutureActionTimes() {
		nextActionTimes[i] = t.AsTime()
	}

	actionDescription, err := convertFromPBScheduleAction(ctx, client, describeResponse.Schedule.Action)
	if err != nil {
		return nil, err
	}

	var typedSearchAttributes SearchAttributes
	searchAttributes := describeResponse.SearchAttributes
	if searchAttributes != nil {
		typedSearchAttributes = convertToTypedSearchAttributes(client.logger, searchAttributes.IndexedFields)
	}

	return &ScheduleDescription{
		Schedule: Schedule{
			Action: actionDescription,
			Spec:   convertFromPBScheduleSpec(describeResponse.Schedule.Spec),
			Policy: &SchedulePolicies{
				Overlap:        describeResponse.Schedule.Policies.GetOverlapPolicy(),
				CatchupWindow:  describeResponse.Schedule.Policies.GetCatchupWindow().AsDuration(),
				PauseOnFailure: describeResponse.Schedule.Policies.GetPauseOnFailure(),
			},
			State: &ScheduleState{
				Note:             describeResponse.Schedule.State.GetNotes(),
				Paused:           describeResponse.Schedule.State.GetPaused(),
				LimitedActions:   describeResponse.Schedule.State.GetLimitedActions(),
				RemainingActions: int(describeResponse.Schedule.State.GetRemainingActions()),
			},
		},
		Info: ScheduleInfo{
			NumActions:                    int(describeResponse.Info.ActionCount),
			NumActionsMissedCatchupWindow: int(describeResponse.Info.MissedCatchupWindow),
			NumActionsSkippedOverlap:      int(describeResponse.Info.OverlapSkipped),
			RunningWorkflows:              runningWorkflows,
			RecentActions:                 recentActions,
			NextActionTimes:               nextActionTimes,
			CreatedAt:                     describeResponse.Info.GetCreateTime().AsTime(),
			LastUpdateAt:                  describeResponse.Info.GetUpdateTime().AsTime(),
		},
		Memo:                  describeResponse.Memo,
		SearchAttributes:      searchAttributes,
		TypedSearchAttributes: typedSearchAttributes,
	}, nil
}

func convertToPBSchedule(
	ctx context.Context,
	client *WorkflowClient,
	schedule *Schedule,
	described *describedScheduleAction,
) (*schedulepb.Schedule, *retainedPayloads, error) {
	if schedule == nil {
		return nil, nil, nil
	}
	action, retained, err := convertToPBScheduleAction(ctx, client, schedule.Action, described)
	if err != nil {
		return nil, nil, err
	}

	var catchupWindow *durationpb.Duration
	if schedule.Policy.CatchupWindow != 0 {
		// Only convert non-zero CatchWindow so server treats 0 as nil and uses the default catchup window.
		catchupWindow = durationpb.New(schedule.Policy.CatchupWindow)
	}

	return &schedulepb.Schedule{
		Spec:   convertToPBScheduleSpec(schedule.Spec),
		Action: action,
		Policies: &schedulepb.SchedulePolicies{
			OverlapPolicy:  schedule.Policy.Overlap,
			CatchupWindow:  catchupWindow,
			PauseOnFailure: schedule.Policy.PauseOnFailure,
		},
		State: &schedulepb.ScheduleState{
			Notes:            schedule.State.Note,
			Paused:           schedule.State.Paused,
			LimitedActions:   schedule.State.LimitedActions,
			RemainingActions: int64(schedule.State.RemainingActions),
		},
	}, retained, nil
}

func convertFromPBScheduleListEntry(schedule *schedulepb.ScheduleListEntry) *ScheduleListEntry {
	scheduleInfo := schedule.GetInfo()

	recentActions := convertFromPBScheduleActionResultList(scheduleInfo.GetRecentActions())

	nextActionTimes := make([]time.Time, len(schedule.Info.GetFutureActionTimes()))
	for i, t := range schedule.Info.GetFutureActionTimes() {
		nextActionTimes[i] = t.AsTime()
	}

	return &ScheduleListEntry{
		ID:     schedule.ScheduleId,
		Spec:   convertFromPBScheduleSpec(scheduleInfo.GetSpec()),
		Note:   scheduleInfo.GetNotes(),
		Paused: scheduleInfo.GetPaused(),
		WorkflowType: WorkflowType{
			Name: scheduleInfo.GetWorkflowType().GetName(),
		},
		RecentActions:    recentActions,
		NextActionTimes:  nextActionTimes,
		Memo:             schedule.Memo,
		SearchAttributes: schedule.SearchAttributes,
	}
}

func convertToPBScheduleAction(
	ctx context.Context,
	client *WorkflowClient,
	scheduleAction ScheduleAction,
	described *describedScheduleAction,
) (*schedulepb.ScheduleAction, *retainedPayloads, error) {
	switch action := scheduleAction.(type) {
	case *ScheduleWorkflowAction:
		// Apply defaults to a copy so callers can reuse the action.
		actionCopy := *action
		action = &actionCopy

		// Set header before interceptor run
		dataConverter := WithContext(ctx, client.dataConverter)

		// Default workflow ID
		if action.ID == "" {
			action.ID = uuid.NewString()
		}

		dataConverter = converter.WithDataConverterSerializationContext(dataConverter, converter.WorkflowSerializationContext{
			Namespace:  client.namespace,
			WorkflowID: action.ID,
		})

		// Validate function and get name
		if err := validateFunctionArgs(action.Workflow, action.Args, true); err != nil {
			return nil, nil, err
		}
		workflowType, err := GetWorkflowFunctionName(client.registry, action.Workflow)
		if err != nil {
			return nil, nil, err
		}
		retained := &retainedPayloads{}
		// Encode workflow inputs that may already be encoded
		input, err := encodeScheduleWorklowArgs(dataConverter, action.Args, described, retained)
		if err != nil {
			return nil, nil, err
		}
		// Encode workflow memos that may already be encoded
		memo, err := encodeScheduleWorkflowMemo(dataConverter, action.Memo, described, retained)
		if err != nil {
			return nil, nil, err
		}

		searchAttrs, err := SerializeSearchAttributes(nil, action.TypedSearchAttributes)
		if err != nil {
			return nil, nil, err
		}
		// Add any untyped search attributes that aren't already there
		for k, v := range action.UntypedSearchAttributes {
			if searchAttrs.GetIndexedFields()[k] == nil {
				if searchAttrs == nil || searchAttrs.IndexedFields == nil {
					searchAttrs = &commonpb.SearchAttributes{IndexedFields: map[string]*commonpb.Payload{}}
				}
				searchAttrs.IndexedFields[k] = v
			}
		}

		// get workflow headers from the context
		header, err := headerPropagated(ctx, client.contextPropagators, client.workflowHeaderCodec(action.ID))
		if err != nil {
			return nil, nil, err
		}

		userMetadata, err := buildScheduleUserMetadata(action.ID, action.StaticSummary, action.StaticDetails, described, dataConverter, retained)
		if err != nil {
			return nil, nil, err
		}

		return &schedulepb.ScheduleAction{
			Action: &schedulepb.ScheduleAction_StartWorkflow{
				StartWorkflow: &workflowpb.NewWorkflowExecutionInfo{
					WorkflowId:               action.ID,
					WorkflowType:             &commonpb.WorkflowType{Name: workflowType},
					TaskQueue:                &taskqueuepb.TaskQueue{Name: action.TaskQueue, Kind: enumspb.TASK_QUEUE_KIND_NORMAL},
					Input:                    input,
					WorkflowExecutionTimeout: durationpb.New(action.WorkflowExecutionTimeout),
					WorkflowRunTimeout:       durationpb.New(action.WorkflowRunTimeout),
					WorkflowTaskTimeout:      durationpb.New(action.WorkflowTaskTimeout),
					RetryPolicy:              ConvertToPBRetryPolicy(action.RetryPolicy),
					Memo:                     memo,
					SearchAttributes:         searchAttrs,
					Header:                   header,
					UserMetadata:             userMetadata,
					VersioningOverride:       VersioningOverrideToProto(action.VersioningOverride),
					Priority:                 ConvertToPBPriority(action.Priority),
				},
			},
		}, retained, nil
	default:
		// TODO maybe just panic instead?
		return nil, nil, fmt.Errorf("could not parse ScheduleAction")
	}
}

func convertFromPBScheduleAction(
	ctx context.Context,
	client *WorkflowClient,
	action *schedulepb.ScheduleAction,
) (ScheduleAction, error) {
	switch action := action.Action.(type) {
	case *schedulepb.ScheduleAction_StartWorkflow:
		workflow := action.StartWorkflow
		dc := client.dataConverter
		if workflow.GetWorkflowId() != "" {
			dc = converter.WithDataConverterSerializationContext(dc, converter.WorkflowSerializationContext{
				Namespace:  client.namespace,
				WorkflowID: workflow.GetWorkflowId(),
			})
		}

		args := make([]any, len(workflow.GetInput().GetPayloads()))
		for i, p := range workflow.GetInput().GetPayloads() {
			args[i] = p
		}

		memos := make(map[string]any)
		for key, element := range workflow.GetMemo().GetFields() {
			memos[key] = element
		}

		searchAttrs := convertToTypedSearchAttributes(client.logger, workflow.GetSearchAttributes().GetIndexedFields())
		// Create untyped list for any attribute not in the existing list
		untypedSearchAttrs := map[string]*commonpb.Payload{}
		for k, v := range workflow.GetSearchAttributes().GetIndexedFields() {
			var inTyped bool
			for typedKey := range searchAttrs.untypedValue {
				if inTyped = typedKey.GetName() == k; inTyped {
					break
				}
			}
			if !inTyped {
				untypedSearchAttrs[k] = v
			}
		}

		inbound := client.newInboundPayloadVisitor()
		summary, err := decodeScheduleUserMetadataValue(ctx, inbound, dc, workflow.GetUserMetadata().GetSummary())
		if err != nil {
			return nil, fmt.Errorf("could not decode user metadata summary: %w", err)
		}
		details, err := decodeScheduleUserMetadataValue(ctx, inbound, dc, workflow.GetUserMetadata().GetDetails())
		if err != nil {
			return nil, fmt.Errorf("could not decode user metadata details: %w", err)
		}

		return &ScheduleWorkflowAction{
			ID:                       workflow.GetWorkflowId(),
			Workflow:                 workflow.WorkflowType.GetName(),
			Args:                     args,
			TaskQueue:                workflow.TaskQueue.GetName(),
			WorkflowExecutionTimeout: workflow.GetWorkflowExecutionTimeout().AsDuration(),
			WorkflowRunTimeout:       workflow.GetWorkflowRunTimeout().AsDuration(),
			WorkflowTaskTimeout:      workflow.GetWorkflowTaskTimeout().AsDuration(),
			RetryPolicy:              convertFromPBRetryPolicy(workflow.RetryPolicy),
			Memo:                     memos,
			TypedSearchAttributes:    searchAttrs,
			UntypedSearchAttributes:  untypedSearchAttrs,
			VersioningOverride:       versioningOverrideFromProto(workflow.VersioningOverride),
			StaticSummary:            summary,
			StaticDetails:            details,
			Priority:                 convertFromPBPriority(workflow.GetPriority()),
		}, nil
	default:
		// TODO maybe just panic instead?
		return nil, fmt.Errorf("could not parse ScheduleAction")
	}
}

func convertToPBBackfillList(backfillRequests []ScheduleBackfill) []*schedulepb.BackfillRequest {
	backfillRequestsPB := make([]*schedulepb.BackfillRequest, len(backfillRequests))
	for i, b := range backfillRequests {
		backfill := b
		backfillRequestsPB[i] = &schedulepb.BackfillRequest{
			StartTime:     timestamppb.New(backfill.Start),
			EndTime:       timestamppb.New(backfill.End),
			OverlapPolicy: backfill.Overlap,
		}
	}
	return backfillRequestsPB
}

func convertToPBRangeList(scheduleRange []ScheduleRange) []*schedulepb.Range {
	rangesPB := make([]*schedulepb.Range, len(scheduleRange))
	for i, r := range scheduleRange {
		rangesPB[i] = &schedulepb.Range{
			Start: int32(r.Start),
			End:   int32(r.End),
			Step:  int32(r.Step),
		}
	}
	return rangesPB
}

func convertFromPBRangeList(scheduleRangePB []*schedulepb.Range) []ScheduleRange {
	scheduleRange := make([]ScheduleRange, len(scheduleRangePB))
	for i, r := range scheduleRangePB {
		if r == nil {
			continue
		}
		scheduleRange[i] = ScheduleRange{
			Start: int(r.Start),
			End:   int(r.End),
			Step:  int(r.Step),
		}
	}
	return scheduleRange
}

func convertFromPBScheduleCalendarSpecList(calendarSpecPB []*schedulepb.StructuredCalendarSpec) []ScheduleCalendarSpec {
	calendarSpec := make([]ScheduleCalendarSpec, len(calendarSpecPB))
	for i, e := range calendarSpecPB {
		calendarSpec[i] = ScheduleCalendarSpec{
			Second:     convertFromPBRangeList(e.Second),
			Minute:     convertFromPBRangeList(e.Minute),
			Hour:       convertFromPBRangeList(e.Hour),
			DayOfMonth: convertFromPBRangeList(e.DayOfMonth),
			Month:      convertFromPBRangeList(e.Month),
			Year:       convertFromPBRangeList(e.Year),
			DayOfWeek:  convertFromPBRangeList(e.DayOfWeek),
			Comment:    e.Comment,
		}
	}
	return calendarSpec
}

func applyScheduleCalendarSpecDefault(calendarSpec *ScheduleCalendarSpec) {
	if calendarSpec.Second == nil {
		calendarSpec.Second = []ScheduleRange{{Start: 0}}
	}

	if calendarSpec.Minute == nil {
		calendarSpec.Minute = []ScheduleRange{{Start: 0}}
	}

	if calendarSpec.Hour == nil {
		calendarSpec.Hour = []ScheduleRange{{Start: 0}}
	}

	if calendarSpec.DayOfMonth == nil {
		calendarSpec.DayOfMonth = []ScheduleRange{{Start: 1, End: 31}}
	}

	if calendarSpec.Month == nil {
		calendarSpec.Month = []ScheduleRange{{Start: 1, End: 12}}
	}

	if calendarSpec.DayOfWeek == nil {
		calendarSpec.DayOfWeek = []ScheduleRange{{Start: 0, End: 6}}
	}
}

func convertToPBScheduleCalendarSpecList(calendarSpec []ScheduleCalendarSpec) []*schedulepb.StructuredCalendarSpec {
	calendarSpecPB := make([]*schedulepb.StructuredCalendarSpec, len(calendarSpec))
	for i, e := range calendarSpec {
		applyScheduleCalendarSpecDefault(&e)

		calendarSpecPB[i] = &schedulepb.StructuredCalendarSpec{
			Second:     convertToPBRangeList(e.Second),
			Minute:     convertToPBRangeList(e.Minute),
			Hour:       convertToPBRangeList(e.Hour),
			DayOfMonth: convertToPBRangeList(e.DayOfMonth),
			Month:      convertToPBRangeList(e.Month),
			Year:       convertToPBRangeList(e.Year),
			DayOfWeek:  convertToPBRangeList(e.DayOfWeek),
			Comment:    e.Comment,
		}
	}
	return calendarSpecPB
}

func convertFromPBScheduleActionResultList(aa []*schedulepb.ScheduleActionResult) []ScheduleActionResult {
	recentActions := make([]ScheduleActionResult, len(aa))
	for i, a := range aa {
		var workflowExecution *ScheduleWorkflowExecution
		if a.GetStartWorkflowResult() != nil {
			workflowExecution = &ScheduleWorkflowExecution{
				WorkflowID:          a.GetStartWorkflowResult().GetWorkflowId(),
				FirstExecutionRunID: a.GetStartWorkflowResult().GetRunId(),
			}
		}
		recentActions[i] = ScheduleActionResult{
			ScheduleTime:        a.GetScheduleTime().AsTime(),
			ActualTime:          a.GetActualTime().AsTime(),
			StartWorkflowResult: workflowExecution,
		}
	}
	return recentActions
}

// retainedPayloads are the described payloads an update sends back unchanged. They are left out of
// the built action until restore, so that the update's store pass doesn't store them again.
type retainedPayloads struct {
	args             map[int]*commonpb.Payload
	memo             map[string]*commonpb.Payload
	summary, details *commonpb.Payload
}

func (r *retainedPayloads) restore(action *schedulepb.ScheduleAction) {
	workflow := action.GetStartWorkflow()
	if r == nil || workflow == nil {
		return
	}
	if len(r.args) > 0 {
		encoded := workflow.GetInput().GetPayloads()
		payloads := make([]*commonpb.Payload, len(encoded)+len(r.args))
		for i := range payloads {
			if retained, ok := r.args[i]; ok {
				payloads[i] = retained
			} else {
				payloads[i], encoded = encoded[0], encoded[1:]
			}
		}
		workflow.Input = &commonpb.Payloads{Payloads: payloads}
	}
	for k, p := range r.memo {
		workflow.Memo.Fields[k] = p
	}
	if r.summary != nil || r.details != nil {
		if workflow.UserMetadata == nil {
			workflow.UserMetadata = &sdk.UserMetadata{}
		}
		if r.summary != nil {
			workflow.UserMetadata.Summary = r.summary
		}
		if r.details != nil {
			workflow.UserMetadata.Details = r.details
		}
	}
}

// describedScheduleAction is the start-workflow action a schedule update started from.
type describedScheduleAction struct {
	args             []*commonpb.Payload
	memo             []*commonpb.Payload
	workflowID       string
	userMetadata     *sdk.UserMetadata
	summary, details string
}

func newDescribedScheduleAction(pb *schedulepb.ScheduleAction, action ScheduleAction) *describedScheduleAction {
	workflowAction, ok := action.(*ScheduleWorkflowAction)
	if !ok || pb.GetStartWorkflow() == nil {
		return nil
	}
	// Cloned because DoUpdate receives these same payloads and may change them in place.
	var args, memo []*commonpb.Payload
	for _, p := range pb.GetStartWorkflow().GetInput().GetPayloads() {
		args = append(args, proto.Clone(p).(*commonpb.Payload))
	}
	for _, p := range pb.GetStartWorkflow().GetMemo().GetFields() {
		memo = append(memo, proto.Clone(p).(*commonpb.Payload))
	}
	return &describedScheduleAction{
		args:         args,
		memo:         memo,
		workflowID:   pb.GetStartWorkflow().GetWorkflowId(),
		userMetadata: pb.GetStartWorkflow().GetUserMetadata(),
		summary:      workflowAction.StaticSummary,
		details:      workflowAction.StaticDetails,
	}
}

// An unchanged summary or details goes back as the payload it was described from. The payload was
// encoded for the described workflow ID, so it is encoded again when the ID changes.
func buildScheduleUserMetadata(
	workflowID string,
	summary string,
	details string,
	described *describedScheduleAction,
	dc converter.DataConverter,
	retained *retainedPayloads,
) (*sdk.UserMetadata, error) {
	if described != nil && described.workflowID == workflowID {
		if summary != "" && summary == described.summary {
			retained.summary, summary = described.userMetadata.GetSummary(), ""
		}
		if details != "" && details == described.details {
			retained.details, details = described.userMetadata.GetDetails(), ""
		}
	}
	return BuildUserMetadata(summary, details, dc)
}

func decodeScheduleUserMetadataValue(
	ctx context.Context,
	inbound PayloadVisitor,
	dc converter.DataConverter,
	payload *commonpb.Payload,
) (string, error) {
	if payload == nil {
		return "", nil
	}
	payload, err := visitPayload(ctx, inbound, payload)
	if err != nil {
		return "", err
	}
	var value string
	if err := dc.FromPayload(payload, &value); err != nil {
		return "", err
	}
	return value, nil
}

// An already encoded payload is only known to have been through storage if it was described.
func containsEqualPayload(payloads []*commonpb.Payload, p *commonpb.Payload) bool {
	for _, described := range payloads {
		if proto.Equal(described, p) {
			return true
		}
	}
	return false
}

func encodeScheduleWorklowArgs(
	dc converter.DataConverter,
	args []any,
	described *describedScheduleAction,
	retained *retainedPayloads,
) (*commonpb.Payloads, error) {
	payloads := make([]*commonpb.Payload, 0, len(args))
	for i, arg := range args {
		enc, ok := arg.(*commonpb.Payload)
		if ok && described != nil && containsEqualPayload(described.args, enc) {
			if retained.args == nil {
				retained.args = map[int]*commonpb.Payload{}
			}
			retained.args[i] = enc
		} else if ok {
			payloads = append(payloads, enc)
		} else {
			payload, err := dc.ToPayload(arg)
			if err != nil {
				return nil, err
			}
			payloads = append(payloads, payload)
		}
	}
	return &commonpb.Payloads{
		Payloads: payloads,
	}, nil
}

func encodeScheduleWorkflowMemo(
	dc converter.DataConverter,
	input map[string]any,
	described *describedScheduleAction,
	retained *retainedPayloads,
) (*commonpb.Memo, error) {
	if input == nil {
		return nil, nil
	}

	memo := make(map[string]*commonpb.Payload)
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}

	for k, v := range input {
		enc, ok := v.(*commonpb.Payload)
		if ok && described != nil && containsEqualPayload(described.memo, enc) {
			if retained.memo == nil {
				retained.memo = map[string]*commonpb.Payload{}
			}
			retained.memo[k] = enc
		} else if ok {
			memo[k] = enc
		} else {

			memoBytes, err := encodeMemoValue(v, dc, sdkFlagsAllowed[SDKFlagMemoUserDCEncode])
			if err != nil {
				return nil, fmt.Errorf("encode workflow memo error: %v", err.Error())
			}
			memo[k] = memoBytes
		}
	}
	return &commonpb.Memo{Fields: memo}, nil
}
