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
	// Exposed as: [go.temporal.io/sdk/client.ExecuteActivityOptions]
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
		Priority                 Priority
	}

	// ClientGetActivityHandleOptions contains input for GetActivityHandle call.
	// ActivityID and RunID are required.
	//
	// Exposed as: [go.temporal.io/sdk/client.GetActivityHandleInput]
	ClientGetActivityHandleOptions struct {
		ActivityID string
		RunID      string
	}

	ClientListActivitiesOptions struct {
		Query string
	}

	ClientCountActivitiesOptions struct {
		Query string
	}

	ClientCountActivitiesResult struct {
		Count  int64
		Groups []ClientCountActivitiesAggregationGroup
	}

	ClientCountActivitiesAggregationGroup struct {
		GroupValues []any
		Count       int64
	}

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

		mustEmbedActivityHandleBase()
	}

	// ClientDescribeActivityOptions contains options for ClientActivityHandle.Describe call.
	// For future compatibility, currently unused.
	ClientDescribeActivityOptions struct{}

	// ClientCancelActivityOptions contains options for ClientActivityHandle.Cancel call.
	ClientCancelActivityOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

	// ClientTerminateActivityOptions contains options for ClientActivityHandle.Terminate call.
	ClientTerminateActivityOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

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

	ClientActivityHandleBase struct {
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

// GetSummary returns summary of the activity. See ActivityOptions.Summary. Returns empty string if there is no summary.
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

func (h *ClientActivityHandleBase) mustEmbedActivityHandleBase() {}

func (h *ClientActivityHandleBase) GetID() string {
	return h.id
}

func (h *ClientActivityHandleBase) GetRunID() string {
	return h.runID
}

func (h *ClientActivityHandleBase) Get(ctx context.Context, valuePtr any) error {
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

func (h *ClientActivityHandleBase) pollResult(ctx context.Context) (*workflowservice.PollActivityExecutionResponse, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.PollActivityExecutionRequest{
		Namespace:  h.client.namespace,
		ActivityId: h.id,
		RunId:      h.runID,
	}

	return h.client.WorkflowService().PollActivityExecution(grpcCtx, request)
}

func (h *ClientActivityHandleBase) Describe(ctx context.Context, options ClientDescribeActivityOptions) (*ClientActivityExecutionDescription, error) {
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

	return &ClientActivityExecutionDescription{
		ClientActivityExecutionInfo: ClientActivityExecutionInfo{
			RawExecutionListInfo:  nil,
			ActivityID:            info.ActivityId,
			ActivityRunID:         info.RunId,
			ActivityType:          info.ActivityType.GetName(),
			ScheduleTime:          info.ScheduleTime.AsTime(),
			CloseTime:             info.CloseTime.AsTime(),
			Status:                info.Status,
			TypedSearchAttributes: convertToTypedSearchAttributes(h.client.logger, info.SearchAttributes.IndexedFields),
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
		dataConverter:           WithContext(ctx, h.client.dataConverter),
		failureConverter:        h.client.failureConverter,
	}, nil
}

func (h *ClientActivityHandleBase) Cancel(ctx context.Context, options ClientCancelActivityOptions) error {
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

func (h *ClientActivityHandleBase) Terminate(ctx context.Context, options ClientTerminateActivityOptions) error {
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
	if err != nil {
		return nil, err
	}

	return &ClientActivityHandleBase{
		client: w.client,
		id:     in.Options.ID,
		runID:  resp.RunId,
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
	userMetadata, err := buildUserMetadata(options.Summary, "", dataConverter)
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
	return &ClientActivityHandleBase{
		client: w.client,
		id:     in.ActivityID,
		runID:  in.RunID,
	}
}
