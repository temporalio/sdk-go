package internal

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"time"

	"github.com/google/uuid"
	activitypb "go.temporal.io/api/activity/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/durationpb"
)

type (
	// ClientExecuteActivityOptions contains configuration parameters for starting an activity execution.
	// ID and TaskQueue are required. At least one of ScheduleToCloseTimeout or StartToCloseTimeout is required.
	// Other parameters are optional.
	//
	// Exposed as: [go.temporal.io/sdk/client.ExecuteActivityOptions]
	ClientExecuteActivityOptions struct {
		ID                     string
		TaskQueue              string
		ScheduleToCloseTimeout time.Duration
		ScheduleToStartTimeout time.Duration
		StartToCloseTimeout    time.Duration
		HeartbeatTimeout       time.Duration
		IDConflictPolicy       enumspb.ActivityIdConflictPolicy
		IDReusePolicy          enumspb.ActivityIdReusePolicy
		RetryPolicy            *RetryPolicy
		SearchAttributes       SearchAttributes
		Summary                string
		Priority               Priority
	}

	ListActivitiesOptions struct {
		Query string
	}

	CountActivitiesOptions struct {
		Query string
	}

	CountActivitiesResult struct {
		Count  int64
		Groups []ActivityAggregationGroup
	}

	ActivityAggregationGroup struct {
		GroupValues []any
		Count       int64
	}

	ActivityHandle interface {
		// GetID returns the ID of the activity this handle points to.
		GetID() string
		// GetRunID returns the run ID that this handle was created with.
		//
		// Handle returned by [client.Client] has it set to run ID of the started execution.
		//
		// Handle returned by client.Client.GetActivityHandle has it set to the provided run ID.
		// If empty run ID was provided, then this function returns empty string and the handle points to the most
		// recent execution with matching activity ID. The run ID of this execution can be retrieved by calling Describe.
		GetRunID() string
		// Get waits until the activity finishes and gets its result. If the activity completes successfully, the result
		// is written to valuePtr and nil is returned. If the activity failed, the failure is returned as an error.
		// If an error is encountered trying to get the activity result, that error is returned.
		Get(ctx context.Context, valuePtr any) error
		// Describe returns detailed information about current state of the activity execution.
		Describe(ctx context.Context) (*ActivityExecutionDescription, error)
		// Cancel requests cancellation of the activity.
		Cancel(ctx context.Context, options CancelActivityOptions) error
		// Terminate terminates the activity.
		Terminate(ctx context.Context, options TerminateActivityOptions) error
	}

	// DescribeActivityOptions contains options for ActivityHandle.Describe call.
	// For future compatibility, currently unused.
	DescribeActivityOptions struct{}

	// CancelActivityOptions contains options for ActivityHandle.Cancel call.
	CancelActivityOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

	// TerminateActivityOptions contains options for ActivityHandle.Terminate call.
	TerminateActivityOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

	ActivityExecutionMetadata struct {
		// RawExecutionListInfo is the raw PB message this struct was built from.
		// This field is nil in the result of ActivityHandle.Describe call - use
		// ActivityExecutionDescription.RawExecutionInfo instead.
		RawExecutionListInfo *activitypb.ActivityExecutionListInfo
		ActivityID           string
		ActivityRunID        string
		ActivityType         string
		ScheduleTime         time.Time
		CloseTime            time.Time
		Status               enumspb.ActivityExecutionStatus
		SearchAttributes     SearchAttributes
		TaskQueue            string
		ExecutionDuration    time.Duration
	}

	ActivityExecutionDescription struct {
		ActivityExecutionMetadata
		// RawExecutionInfo is the raw PB message this struct was built from.
		RawExecutionInfo *activitypb.ActivityExecutionInfo
		// DataConverter is the data converter used for reading certain fields. By default, it's the data converter of
		// the client used to make the ActivityHandle.Describe call.
		DataConverter converter.DataConverter
		// FailureConverter is the failure converter used for reading last failure. By default, it's the data converter
		// of the client used to make the ActivityHandle.Describe call.
		FailureConverter        converter.FailureConverter
		RunState                enumspb.PendingActivityState
		LastHeartbeatTime       time.Time
		LastStartedTime         time.Time
		Attempt                 int32
		RetryPolicy             *RetryPolicy
		ExpirationTime          time.Time
		LastWorkerIdentity      string
		CurrentRetryInterval    time.Duration
		LastAttemptCompleteTime time.Time
		NextAttemptScheduleTime time.Time
		LastDeploymentVersion   *WorkerDeploymentVersion
		Priority                Priority
		CanceledReason          string
	}

	activityHandleImpl struct {
		client *WorkflowClient
		id     string
		runID  string
	}
)

// GetHeartbeatDetailsCount returns the number of heartbeat details. Does not perform data conversion.
func (d *ActivityExecutionDescription) GetHeartbeatDetailsCount() int {
	return len(d.RawExecutionInfo.GetHeartbeatDetails().GetPayloads())
}

// GetHeartbeatDetails converts heartbeat details using DataConverter and stores them in a slice.
// If successful, valuesPtr is set to the resulting slice.
//
// valuesPtr must be a pointer to slice (*[]T). The type of the slice is retrieved from valuesPtr using reflection;
// heartbeat details are converted to that type.
func (d *ActivityExecutionDescription) GetHeartbeatDetails(valuesPtr any) error {
	valuesPtrVal := reflect.ValueOf(valuesPtr)
	if valuesPtrVal.Type().Kind() != reflect.Ptr || valuesPtrVal.Type().Elem().Kind() != reflect.Slice {
		panic("valuesPtr is not a pointer to slice")
	}

	detailsCount := d.GetHeartbeatDetailsCount()
	detailsSliceType := valuesPtrVal.Type().Elem()
	detailsSlice := reflect.MakeSlice(detailsSliceType, detailsCount, detailsCount)
	pointersSlice := make([]any, detailsCount)
	for i := 0; i < detailsCount; i++ {
		pointersSlice[i] = detailsSlice.Index(i).Addr().Interface()
	}
	if err := d.DataConverter.FromPayloads(d.RawExecutionInfo.GetHeartbeatDetails(), pointersSlice...); err != nil {
		return err
	}
	valuesPtrVal.Set(detailsSlice)
	return nil
}

// GetLastFailure returns the last failure of the activity execution, using FailureConverter for conversion.
// Returns nil if there was no failure.
func (d *ActivityExecutionDescription) GetLastFailure() error {
	return d.FailureConverter.FailureToError(d.RawExecutionInfo.GetLastFailure())
}

// GetSummary returns summary of the activity. See ActivityOptions.Summary. Returns empty string if there is no summary.
// Uses DataConverter for converting the summary payload to string. Returns error if data conversion fails.
func (d *ActivityExecutionDescription) GetSummary() (string, error) {
	payload := d.RawExecutionInfo.GetUserMetadata().GetSummary()
	if payload == nil {
		return "", nil
	}
	var summary string
	err := d.DataConverter.FromPayload(payload, &summary)
	if err != nil {
		return "", err
	}
	return summary, nil
}

func (h *activityHandleImpl) GetID() string {
	return h.id
}

func (h *activityHandleImpl) GetRunID() string {
	return h.runID
}

func (h *activityHandleImpl) Get(ctx context.Context, valuePtr any) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}

	// repeatedly poll the loop repeats until there's an outcome
	for {
		resp, err := h.pollResult(ctx)
		if err != nil {
			return err
		}
		if failure := resp.GetOutcome().GetFailure(); failure != nil {
			return h.client.failureConverter.FailureToError(failure)
		}
		if result := resp.GetOutcome().GetResult(); result != nil {
			if valuePtr == nil {
				return nil
			} else {
				return h.client.dataConverter.FromPayloads(result, valuePtr)
			}
		}
	}
}

func (h *activityHandleImpl) pollResult(ctx context.Context) (*workflowservice.PollActivityExecutionResponse, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.PollActivityExecutionRequest{
		Namespace:  h.client.namespace,
		ActivityId: h.id,
		RunId:      h.runID,
	}

	return h.client.WorkflowService().PollActivityExecution(grpcCtx, request)
}

func (h *activityHandleImpl) Describe(ctx context.Context) (*ActivityExecutionDescription, error) {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.DescribeActivityExecutionRequest{
		Namespace:  h.client.namespace,
		ActivityId: h.id,
		RunId:      h.runID,
	}
	resp, err := h.client.WorkflowService().DescribeActivityExecution(grpcCtx, request)
	if err != nil {
		return nil, err
	}
	info := resp.GetInfo()
	if info == nil {
		return nil, errors.New("DescribeActivityExecution response doesn't contain info")
	}

	var lastDeploymentVersion *WorkerDeploymentVersion = nil
	if info.LastDeploymentVersion != nil {
		v := workerDeploymentVersionFromProto(info.LastDeploymentVersion)
		lastDeploymentVersion = &v
	}

	return &ActivityExecutionDescription{
		ActivityExecutionMetadata: ActivityExecutionMetadata{
			RawExecutionListInfo: nil,
			ActivityID:           info.ActivityId,
			ActivityRunID:        info.RunId,
			ActivityType:         info.ActivityType.GetName(),
			ScheduleTime:         info.ScheduleTime.AsTime(),
			CloseTime:            info.CloseTime.AsTime(),
			Status:               info.Status,
			SearchAttributes:     convertToTypedSearchAttributes(h.client.logger, info.SearchAttributes.IndexedFields),
			TaskQueue:            info.TaskQueue,
			ExecutionDuration:    info.ExecutionDuration.AsDuration(),
		},
		RawExecutionInfo:        info,
		DataConverter:           WithContext(ctx, h.client.dataConverter),
		FailureConverter:        h.client.failureConverter,
		RunState:                info.RunState,
		LastHeartbeatTime:       info.LastHeartbeatTime.AsTime(),
		LastStartedTime:         info.LastStartedTime.AsTime(),
		Attempt:                 info.Attempt,
		RetryPolicy:             convertFromPBRetryPolicy(info.RetryPolicy),
		ExpirationTime:          info.ExpirationTime.AsTime(),
		LastWorkerIdentity:      info.LastWorkerIdentity,
		CurrentRetryInterval:    info.CurrentRetryInterval.AsDuration(),
		LastAttemptCompleteTime: info.LastAttemptCompleteTime.AsTime(),
		NextAttemptScheduleTime: info.NextAttemptScheduleTime.AsTime(),
		LastDeploymentVersion:   lastDeploymentVersion,
		Priority:                convertFromPBPriority(info.Priority),
		CanceledReason:          info.CanceledReason,
	}, nil
}

func (h *activityHandleImpl) Cancel(ctx context.Context, options CancelActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.RequestCancelActivityExecutionRequest{
		Namespace:  h.client.namespace,
		ActivityId: h.id,
		RunId:      h.runID,
		Identity:   h.client.identity,
		RequestId:  uuid.NewString(),
		Reason:     options.Reason,
	}
	_, err := h.client.WorkflowService().RequestCancelActivityExecution(grpcCtx, request)
	return err
}

func (h *activityHandleImpl) Terminate(ctx context.Context, options TerminateActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.TerminateActivityExecutionRequest{
		Namespace:  h.client.namespace,
		ActivityId: h.id,
		RunId:      h.runID,
		Identity:   h.client.identity,
		RequestId:  uuid.NewString(),
		Reason:     options.Reason,
	}
	_, err := h.client.WorkflowService().TerminateActivityExecution(grpcCtx, request)
	return err
}

func (wc *WorkflowClient) ExecuteActivity(ctx context.Context, options ClientExecuteActivityOptions, activity any, args ...any) (ActivityHandle, error) {
	if err := wc.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	activityType, err := getValidatedActivityFunction(activity, args, wc.registry)
	if err != nil {
		return nil, err
	}

	ctx = contextWithNewHeader(ctx)
	dataConverter := WithContext(ctx, wc.dataConverter)
	if dataConverter == nil {
		dataConverter = converter.GetDefaultDataConverter()
	}

	request := &workflowservice.StartActivityExecutionRequest{
		Namespace:    wc.namespace,
		Identity:     wc.identity,
		RequestId:    uuid.NewString(),
		ActivityType: &commonpb.ActivityType{Name: activityType.Name},
	}
	if err = options.validateAndSetInRequest(request, dataConverter); err != nil {
		return nil, err
	}
	if request.Input, err = encodeArgs(dataConverter, args); err != nil {
		return nil, err
	}
	if request.Header, err = headerPropagated(ctx, wc.contextPropagators); err != nil {
		return nil, err
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	resp, err := wc.WorkflowService().StartActivityExecution(grpcCtx, request)
	if err != nil {
		return nil, err
	}

	return &activityHandleImpl{
		client: wc,
		id:     options.ID,
		runID:  resp.RunId,
	}, nil
}

func (options *ClientExecuteActivityOptions) validateAndSetInRequest(request *workflowservice.StartActivityExecutionRequest, dataConverter converter.DataConverter) error {
	if options.ID == "" {
		return errors.New("activity ID is required")
	}
	if options.TaskQueue == "" {
		return errors.New("task queue is required")
	}
	if options.ScheduleToCloseTimeout < 0 {
		return errors.New("negative ScheduleToCloseTimeout")
	}
	if options.StartToCloseTimeout < 0 {
		return errors.New("negative StartToCloseTimeout")
	}
	startToCloseTimeout := options.StartToCloseTimeout
	if startToCloseTimeout == 0 {
		if options.ScheduleToCloseTimeout == 0 {
			return errors.New("at least one of ScheduleToCloseTimeout and StartToCloseTimeout is required")
		} else {
			startToCloseTimeout = options.ScheduleToCloseTimeout
		}
	}
	searchAttrs, err := serializeTypedSearchAttributes(options.SearchAttributes.GetUntypedValues())
	if err != nil {
		return err
	}
	userMetadata, err := buildUserMetadata(options.Summary, "", dataConverter)
	if err != nil {
		return err
	}

	request.ActivityId = options.ID
	request.TaskQueue = &taskqueuepb.TaskQueue{Name: options.TaskQueue}
	request.ScheduleToCloseTimeout = durationpb.New(options.ScheduleToCloseTimeout)
	request.ScheduleToStartTimeout = durationpb.New(options.ScheduleToStartTimeout)
	request.StartToCloseTimeout = durationpb.New(startToCloseTimeout)
	request.HeartbeatTimeout = durationpb.New(options.HeartbeatTimeout)
	request.RetryPolicy = convertToPBRetryPolicy(options.RetryPolicy)
	request.IdReusePolicy = options.IDReusePolicy
	request.IdConflictPolicy = options.IDConflictPolicy
	request.SearchAttributes = searchAttrs
	request.UserMetadata = userMetadata
	request.Priority = convertToPBPriority(options.Priority)
	return nil
}

func (wc *WorkflowClient) GetActivityHandle(activityID string, runID string) ActivityHandle {
	return &activityHandleImpl{
		client: wc,
		id:     activityID,
		runID:  runID,
	}
}

func (wc *WorkflowClient) ListActivities(ctx context.Context, options ListActivitiesOptions) iter.Seq2[*ActivityExecutionMetadata, error] {
	return func(yield func(*ActivityExecutionMetadata, error) bool) {
		if err := wc.ensureInitialized(ctx); err != nil {
			yield(nil, err)
			return
		}

		request := &workflowservice.ListActivityExecutionsRequest{
			Namespace: wc.namespace,
			Query:     options.Query,
		}

		for {
			resp, err := wc.getListActivitiesPage(ctx, request)
			if err != nil {
				yield(nil, err)
				return
			}

			for _, ex := range resp.Executions {
				if !yield(&ActivityExecutionMetadata{
					RawExecutionListInfo: ex,
					ActivityID:           ex.ActivityId,
					ActivityRunID:        ex.RunId,
					ActivityType:         ex.ActivityType.GetName(),
					ScheduleTime:         ex.ScheduleTime.AsTime(),
					CloseTime:            ex.CloseTime.AsTime(),
					Status:               ex.Status,
					SearchAttributes:     convertToTypedSearchAttributes(wc.logger, ex.SearchAttributes.IndexedFields),
					TaskQueue:            ex.TaskQueue,
					ExecutionDuration:    ex.ExecutionDuration.AsDuration(),
				}, nil) {
					return
				}
			}

			if resp.NextPageToken != nil {
				request.NextPageToken = resp.NextPageToken
			} else {
				return
			}
		}
	}
}

func (wc *WorkflowClient) getListActivitiesPage(ctx context.Context, request *workflowservice.ListActivityExecutionsRequest) (*workflowservice.ListActivityExecutionsResponse, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	return wc.WorkflowService().ListActivityExecutions(grpcCtx, request)
}

func (wc *WorkflowClient) CountActivities(ctx context.Context, options CountActivitiesOptions) (*CountActivitiesResult, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.CountActivityExecutionsRequest{
		Namespace: wc.namespace,
		Query:     options.Query,
	}
	resp, err := wc.WorkflowService().CountActivityExecutions(grpcCtx, request)
	if err != nil {
		return nil, err
	}

	if resp.Groups == nil {
		return &CountActivitiesResult{
			Count:  resp.Count,
			Groups: nil,
		}, nil
	}

	groups := make([]ActivityAggregationGroup, len(resp.Groups))
	for i, group := range resp.Groups {
		groupValues := make([]any, len(group.GroupValues))
		for j, groupValue := range group.GroupValues {
			// should never fail, and if it does, leaving nil behind
			_ = converter.GetDefaultDataConverter().FromPayload(groupValue, &groupValues[j])
		}
		groups[i] = ActivityAggregationGroup{
			GroupValues: groupValues,
			Count:       group.Count,
		}
	}

	return &CountActivitiesResult{
		Count:  resp.Count,
		Groups: groups,
	}, nil
}
