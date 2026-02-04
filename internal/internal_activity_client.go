package internal

import (
	"context"
	"errors"
	"iter"
	"time"

	"github.com/google/uuid"
	activitypb "go.temporal.io/api/activity/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/durationpb"
)

type (
	// ClientStartActivityOptions contains configuration parameters for starting an activity execution.
	// ID and TaskQueue are required. At least one of ScheduleToCloseTimeout or StartToCloseTimeout is required.
	// Other parameters are optional.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.StartActivityOptions]
	ClientStartActivityOptions struct {
		ID                       string
		TaskQueue                string
		ScheduleToCloseTimeout   time.Duration
		ScheduleToStartTimeout   time.Duration
		StartToCloseTimeout      time.Duration
		HeartbeatTimeout         time.Duration
		ActivityIDConflictPolicy enumspb.ActivityIdConflictPolicy
		ActivityIDReusePolicy    enumspb.ActivityIdReusePolicy
		RetryPolicy              *RetryPolicy
		TypedSearchAttributes    SearchAttributes
		Summary                  string
		Details                  string
		Priority                 Priority
	}

	// ClientGetActivityHandleOptions contains input for GetActivityHandle call.
	// ActivityID and RunID are required.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.GetActivityHandleOptions]
	ClientGetActivityHandleOptions struct {
		ActivityID string
		RunID      string
	}

	// ClientListActivitiesOptions contains input for ListActivities call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ListActivitiesOptions]
	ClientListActivitiesOptions struct {
		Query string
	}

	// ClientCountActivitiesOptions contains input for CountActivities call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CountActivitiesOptions]
	ClientCountActivitiesOptions struct {
		Query string
	}

	// ClientCountActivitiesResult contains result of CountActivities call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CountActivitiesResult]
	ClientCountActivitiesResult struct {
		Count  int64
		Groups []ClientCountActivitiesAggregationGroup
	}

	// ClientCountActivitiesAggregationGroup contains groups of activities if
	// CountActivityExecutions is grouped by a field.
	// The list might not be complete, and the counts of each group is approximate.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CountActivitiesAggregationGroup]
	ClientCountActivitiesAggregationGroup struct {
		GroupValues []any
		Count       int64
	}

	// ClientActivityHandle represents a running or completed standalone activity execution.
	// It can be used to get the result, describe, cancel, or terminate the activity.
	//
	// Methods may be added to this interface; implementing it directly is discouraged.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityHandle]
	ClientActivityHandle interface {
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
		Describe(ctx context.Context, options ClientDescribeActivityOptions) (*ClientActivityExecutionDescription, error)
		// Cancel requests cancellation of the activity.
		Cancel(ctx context.Context, options ClientCancelActivityOptions) error
		// Terminate terminates the activity.
		Terminate(ctx context.Context, options ClientTerminateActivityOptions) error
	}

	// ClientDescribeActivityOptions contains options for ClientActivityHandle.Describe call.
	// For future compatibility, currently unused.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.DescribeActivityOptions]
	ClientDescribeActivityOptions struct{}

	// ClientCancelActivityOptions contains options for ClientActivityHandle.Cancel call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CancelActivityOptions]
	ClientCancelActivityOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

	// ClientTerminateActivityOptions contains options for ClientActivityHandle.Terminate call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.TerminateActivityOptions]
	ClientTerminateActivityOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

	// ClientActivityExecutionInfo contains information about an activity execution.
	// This is returned by ListActivities and embedded in ClientActivityExecutionDescription.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityExecutionInfo]
	ClientActivityExecutionInfo struct {
		// Raw PB message this struct was built from. This field is nil in the result of ClientActivityHandle.Describe call - use
		// ClientActivityExecutionDescription.RawExecutionInfo instead.
		RawExecutionListInfo  *activitypb.ActivityExecutionListInfo
		ActivityID            string
		ActivityRunID         string
		ActivityType          string
		ScheduleTime          time.Time
		CloseTime             time.Time
		Status                enumspb.ActivityExecutionStatus
		TypedSearchAttributes SearchAttributes
		TaskQueue             string
		ExecutionDuration     time.Duration
	}

	// ClientActivityExecutionDescription contains detailed information about an activity execution.
	// This is returned by ClientActivityHandle.Describe.
	//
	//	NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityExecutionDescription]
	ClientActivityExecutionDescription struct {
		ClientActivityExecutionInfo
		// Raw PB message this struct was built from.
		RawExecutionInfo        *activitypb.ActivityExecutionInfo
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
		dataConverter           converter.DataConverter
		failureConverter        converter.FailureConverter
	}

	// clientActivityHandleImpl is the default implementation of ClientActivityHandle.
	clientActivityHandleImpl struct {
		client *WorkflowClient
		id     string
		runID  string
	}
)

// HasHeartbeatDetailsCount returns whether heartbeat details are present. Use GetHeartbeatDetails to retrieve them.
func (d *ClientActivityExecutionDescription) HasHeartbeatDetailsCount() bool {
	return len(d.RawExecutionInfo.GetHeartbeatDetails().GetPayloads()) > 0
}

// GetHeartbeatDetails retrieves heartbeat details. Returns ErrNoData if heartbeat details are not present.
// The details are deserialized into provided pointers using the data converter of the client used to make the Describe call.
// Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetHeartbeatDetails(valuePtrs ...any) error {
	details := d.RawExecutionInfo.GetHeartbeatDetails()
	if details == nil {
		return ErrNoData
	}
	return d.dataConverter.FromPayloads(details, valuePtrs...)
}

// GetLastFailure returns the last failure of the activity execution, using the failure converter of the client used to
// make the Describe call. Returns nil if there was no failure.
func (d *ClientActivityExecutionDescription) GetLastFailure() error {
	failure := d.RawExecutionInfo.GetLastFailure()
	if failure == nil {
		return nil
	}
	return d.failureConverter.FailureToError(failure)
}

// GetSummary returns summary of the activity. See ClientStartActivityOptions.Summary. Returns empty string if there is no summary.
// Uses the data converter of the client used to make the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetSummary() (string, error) {
	payload := d.RawExecutionInfo.GetUserMetadata().GetSummary()
	if payload == nil {
		return "", nil
	}
	var summary string
	err := d.dataConverter.FromPayload(payload, &summary)
	if err != nil {
		return "", err
	}
	return summary, nil
}

// GetDetails returns details of the activity. See ClientStartActivityOptions.Details. Returns empty string if there are no details.
// Uses the data converter of the client used to make the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetDetails() (string, error) {
	payload := d.RawExecutionInfo.GetUserMetadata().GetDetails()
	if payload == nil {
		return "", nil
	}
	var details string
	err := d.dataConverter.FromPayload(payload, &details)
	if err != nil {
		return "", err
	}
	return details, nil
}

func (h *clientActivityHandleImpl) GetID() string {
	return h.id
}

func (h *clientActivityHandleImpl) GetRunID() string {
	return h.runID
}

func (h *clientActivityHandleImpl) Get(ctx context.Context, valuePtr any) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}

	// repeatedly poll, the loop repeats until there's an outcome
	for {
		resp, err := h.client.interceptor.PollActivityResult(ctx, &ClientPollActivityResultInput{
			ActivityID: h.id,
			RunID:      h.runID,
		})
		if err != nil {
			return err
		}
		if resp.Failure != nil {
			return h.client.failureConverter.FailureToError(resp.Failure)
		}
		if resp.Result != nil {
			if valuePtr == nil {
				return nil
			}
			return h.client.dataConverter.FromPayloads(resp.Result, valuePtr)
		}
	}
}

func (h *clientActivityHandleImpl) Describe(ctx context.Context, options ClientDescribeActivityOptions) (*ClientActivityExecutionDescription, error) {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	out, err := h.client.interceptor.DescribeActivity(ctx, &ClientDescribeActivityInput{
		ActivityID: h.id,
		RunID:      h.runID,
	})
	if err != nil {
		return nil, err
	}
	return out.Description, nil
}

func (h *clientActivityHandleImpl) Cancel(ctx context.Context, options ClientCancelActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}
	return h.client.interceptor.CancelActivity(ctx, &ClientCancelActivityInput{
		ActivityID: h.id,
		RunID:      h.runID,
		Reason:     options.Reason,
	})
}

func (h *clientActivityHandleImpl) Terminate(ctx context.Context, options ClientTerminateActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}
	return h.client.interceptor.TerminateActivity(ctx, &ClientTerminateActivityInput{
		ActivityID: h.id,
		RunID:      h.runID,
		Reason:     options.Reason,
	})
}

func (wc *WorkflowClient) ExecuteActivity(ctx context.Context, options ClientStartActivityOptions, activity any, args ...any) (ClientActivityHandle, error) {
	if err := wc.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	activityType, err := getValidatedActivityFunction(activity, args, wc.registry)
	if err != nil {
		return nil, err
	}

	return wc.interceptor.ExecuteActivity(ctx, &ClientExecuteActivityInput{
		Options:      &options,
		ActivityType: activityType,
		Args:         args,
	})
}

func (wc *WorkflowClient) GetActivityHandle(options ClientGetActivityHandleOptions) ClientActivityHandle {
	return wc.interceptor.GetActivityHandle((*ClientGetActivityHandleInput)(&options))
}

func (wc *WorkflowClient) ListActivities(ctx context.Context, options ClientListActivitiesOptions) iter.Seq2[*ClientActivityExecutionInfo, error] {
	return func(yield func(*ClientActivityExecutionInfo, error) bool) {
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
				if !yield(&ClientActivityExecutionInfo{
					RawExecutionListInfo:  ex,
					ActivityID:            ex.ActivityId,
					ActivityRunID:         ex.RunId,
					ActivityType:          ex.ActivityType.GetName(),
					ScheduleTime:          ex.ScheduleTime.AsTime(),
					CloseTime:             ex.CloseTime.AsTime(),
					Status:                ex.Status,
					TypedSearchAttributes: convertToTypedSearchAttributes(wc.logger, ex.SearchAttributes.IndexedFields),
					TaskQueue:             ex.TaskQueue,
					ExecutionDuration:     ex.ExecutionDuration.AsDuration(),
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

func (wc *WorkflowClient) CountActivities(ctx context.Context, options ClientCountActivitiesOptions) (*ClientCountActivitiesResult, error) {
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

	groups := make([]ClientCountActivitiesAggregationGroup, len(resp.Groups))
	for i, group := range resp.Groups {
		groupValues := make([]any, len(group.GroupValues))
		for j, groupValue := range group.GroupValues {
			// should never fail, and if it does, leaving nil behind
			_ = converter.GetDefaultDataConverter().FromPayload(groupValue, &groupValues[j])
		}
		groups[i] = ClientCountActivitiesAggregationGroup{
			GroupValues: groupValues,
			Count:       group.Count,
		}
	}

	return &ClientCountActivitiesResult{
		Count:  resp.Count,
		Groups: groups,
	}, nil
}

func (w *workflowClientInterceptor) ExecuteActivity(
	ctx context.Context,
	in *ClientExecuteActivityInput,
) (ClientActivityHandle, error) {
	ctx = contextWithNewHeader(ctx)
	dataConverter := WithContext(ctx, w.client.dataConverter)
	if dataConverter == nil {
		dataConverter = converter.GetDefaultDataConverter()
	}

	request := &workflowservice.StartActivityExecutionRequest{
		Namespace:    w.client.namespace,
		Identity:     w.client.identity,
		RequestId:    uuid.NewString(),
		ActivityType: &commonpb.ActivityType{Name: in.ActivityType.Name},
	}
	var err error
	if err = in.Options.validateAndSetInRequest(request, dataConverter); err != nil {
		return nil, err
	}
	if request.Input, err = encodeArgs(dataConverter, in.Args); err != nil {
		return nil, err
	}
	if request.Header, err = headerPropagated(ctx, w.client.contextPropagators); err != nil {
		return nil, err
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	resp, err := w.client.WorkflowService().StartActivityExecution(grpcCtx, request)

	var runID string
	if e, ok := err.(*serviceerror.ActivityExecutionAlreadyStarted); ok &&
		in.Options.ActivityIDConflictPolicy == enumspb.ACTIVITY_ID_CONFLICT_POLICY_USE_EXISTING {
		runID = e.RunId
	} else if err != nil {
		return nil, err
	} else {
		runID = resp.RunId
	}

	return &clientActivityHandleImpl{
		client: w.client,
		id:     in.Options.ID,
		runID:  runID,
	}, nil
}

func (options *ClientStartActivityOptions) validateAndSetInRequest(request *workflowservice.StartActivityExecutionRequest, dataConverter converter.DataConverter) error {
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
	if options.StartToCloseTimeout == 0 && options.ScheduleToCloseTimeout == 0 {
		return errors.New("at least one of ScheduleToCloseTimeout and StartToCloseTimeout is required")
	}
	searchAttrs, err := serializeTypedSearchAttributes(options.TypedSearchAttributes.GetUntypedValues())
	if err != nil {
		return err
	}
	userMetadata, err := buildUserMetadata(options.Summary, options.Details, dataConverter)
	if err != nil {
		return err
	}

	request.ActivityId = options.ID
	request.TaskQueue = &taskqueuepb.TaskQueue{Name: options.TaskQueue}
	request.ScheduleToCloseTimeout = durationpb.New(options.ScheduleToCloseTimeout)
	request.ScheduleToStartTimeout = durationpb.New(options.ScheduleToStartTimeout)
	request.StartToCloseTimeout = durationpb.New(options.StartToCloseTimeout)
	request.HeartbeatTimeout = durationpb.New(options.HeartbeatTimeout)
	request.RetryPolicy = convertToPBRetryPolicy(options.RetryPolicy)
	request.IdReusePolicy = options.ActivityIDReusePolicy
	request.IdConflictPolicy = options.ActivityIDConflictPolicy
	request.SearchAttributes = searchAttrs
	request.UserMetadata = userMetadata
	request.Priority = convertToPBPriority(options.Priority)
	return nil
}

func (w *workflowClientInterceptor) GetActivityHandle(
	in *ClientGetActivityHandleInput,
) ClientActivityHandle {
	return &clientActivityHandleImpl{
		client: w.client,
		id:     in.ActivityID,
		runID:  in.RunID,
	}
}

func (w *workflowClientInterceptor) PollActivityResult(
	ctx context.Context,
	in *ClientPollActivityResultInput,
) (*ClientPollActivityResultOutput, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx), grpcLongPoll(true))
	defer cancel()

	request := &workflowservice.PollActivityExecutionRequest{
		Namespace:  w.client.namespace,
		ActivityId: in.ActivityID,
		RunId:      in.RunID,
	}

	resp, err := w.client.WorkflowService().PollActivityExecution(grpcCtx, request)
	if err != nil {
		return nil, err
	}

	return &ClientPollActivityResultOutput{
		Result:  resp.GetOutcome().GetResult(),
		Failure: resp.GetOutcome().GetFailure(),
	}, nil
}

func (w *workflowClientInterceptor) DescribeActivity(
	ctx context.Context,
	in *ClientDescribeActivityInput,
) (*ClientDescribeActivityOutput, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.DescribeActivityExecutionRequest{
		Namespace:  w.client.namespace,
		ActivityId: in.ActivityID,
		RunId:      in.RunID,
	}
	resp, err := w.client.WorkflowService().DescribeActivityExecution(grpcCtx, request)
	if err != nil {
		return nil, err
	}
	info := resp.GetInfo()
	if info == nil {
		return nil, errors.New("DescribeActivityExecution response doesn't contain info")
	}

	var lastDeploymentVersion *WorkerDeploymentVersion
	if info.LastDeploymentVersion != nil {
		v := workerDeploymentVersionFromProto(info.LastDeploymentVersion)
		lastDeploymentVersion = &v
	}

	return &ClientDescribeActivityOutput{
		Description: &ClientActivityExecutionDescription{
			ClientActivityExecutionInfo: ClientActivityExecutionInfo{
				RawExecutionListInfo:  nil,
				ActivityID:            info.ActivityId,
				ActivityRunID:         info.RunId,
				ActivityType:          info.ActivityType.GetName(),
				ScheduleTime:          info.ScheduleTime.AsTime(),
				CloseTime:             info.CloseTime.AsTime(),
				Status:                info.Status,
				TypedSearchAttributes: convertToTypedSearchAttributes(w.client.logger, info.SearchAttributes.IndexedFields),
				TaskQueue:             info.TaskQueue,
				ExecutionDuration:     info.ExecutionDuration.AsDuration(),
			},
			RawExecutionInfo:        info,
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
			dataConverter:           WithContext(ctx, w.client.dataConverter),
			failureConverter:        w.client.failureConverter,
		},
	}, nil
}

func (w *workflowClientInterceptor) CancelActivity(
	ctx context.Context,
	in *ClientCancelActivityInput,
) error {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.RequestCancelActivityExecutionRequest{
		Namespace:  w.client.namespace,
		ActivityId: in.ActivityID,
		RunId:      in.RunID,
		Identity:   w.client.identity,
		RequestId:  uuid.NewString(),
		Reason:     in.Reason,
	}
	_, err := w.client.WorkflowService().RequestCancelActivityExecution(grpcCtx, request)
	return err
}

func (w *workflowClientInterceptor) TerminateActivity(
	ctx context.Context,
	in *ClientTerminateActivityInput,
) error {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.TerminateActivityExecutionRequest{
		Namespace:  w.client.namespace,
		ActivityId: in.ActivityID,
		RunId:      in.RunID,
		Identity:   w.client.identity,
		RequestId:  uuid.NewString(),
		Reason:     in.Reason,
	}
	_, err := w.client.WorkflowService().TerminateActivityExecution(grpcCtx, request)
	return err
}
