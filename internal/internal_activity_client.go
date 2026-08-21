package internal

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/google/uuid"
	activitypb "go.temporal.io/api/activity/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const pollActivityTimeout = 60 * time.Second

type (
	// ClientStartActivityOptions contains configuration parameters for starting an activity execution.
	// ID and TaskQueue are required. At least one of ScheduleToCloseTimeout or StartToCloseTimeout is required.
	// Other parameters are optional.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.StartActivityOptions]
	ClientStartActivityOptions struct {
		// ID - The business identifier of the activity.
		//
		// Required
		ID string
		// TaskQueue - The task queue to schedule the activity on.
		//
		// Required
		TaskQueue string
		// ScheduleToCloseTimeout - Maximum duration the Temporal Server allows for an Activity Execution
		// from scheduling through closure, including retries. This does not control how long a client waits
		// for the result: ExecuteActivity returns after the start RPC, and the context passed to
		// ActivityHandle.Get controls the client-side wait. Use StartToCloseTimeout to limit a single attempt.
		// The zero value of this uses default value.
		// Either this option or StartToCloseTimeout is required: Defaults to unlimited.
		ScheduleToCloseTimeout time.Duration
		// ScheduleToStartTimeout - Time that the Activity Task can stay in the Task Queue before it is picked up by
		// a Worker. Do not specify this timeout unless using host specific Task Queues for Activity Tasks are being
		// used for routing. In almost all situations that don't involve routing activities to specific hosts, it is
		// better to rely on the default value.
		// ScheduleToStartTimeout is always non-retryable. Retrying after this timeout doesn't make sense, as it would
		// just put the Activity Task back into the same Task Queue.
		//
		// Optional: Defaults to unlimited.
		ScheduleToStartTimeout time.Duration
		// StartToCloseTimeout - Maximum time of a single Activity execution attempt.
		// Note that the Temporal Server doesn't detect Worker process failures directly. It relies on this timeout
		// to detect that an Activity that didn't complete on time. So this timeout should be as short as the longest
		// possible execution of the Activity body. Potentially long running Activities must specify HeartbeatTimeout
		// and call Activity.RecordHeartbeat(ctx, "my-heartbeat") periodically for timely failure detection.
		// Either this option or ScheduleToCloseTimeout is required: Defaults to the ScheduleToCloseTimeout value.
		StartToCloseTimeout time.Duration
		// HeartbeatTimeout - Heartbeat interval. Activity must call Activity.RecordHeartbeat(ctx, "my-heartbeat")
		// before this interval passes after the last heartbeat or the Activity starts.
		HeartbeatTimeout time.Duration
		// ActivityIDConflictPolicy - Defines what to do when trying to start an activity with the same ID as a
		// running activity. Note that it is never valid to have two running instances of the same activity ID.
		// See ActivityIDReusePolicy for handling activity ID duplication with a *closed* activity.
		ActivityIDConflictPolicy enumspb.ActivityIdConflictPolicy
		// ActivityIDReusePolicy - Defines whether to allow re-using an activity ID from a previously closed activity.
		// If the request is denied, the server returns an ActivityExecutionAlreadyStarted error.
		// See ActivityIDConflictPolicy for handling ID duplication with a *running* activity.
		ActivityIDReusePolicy enumspb.ActivityIdReusePolicy
		// RetryPolicy - Specifies how to retry an Activity if an error occurs.
		// More details are available at docs.temporal.io.
		// RetryPolicy is optional. If one is not specified, a default RetryPolicy is provided by the server.
		// The default RetryPolicy provided by the server specifies:
		//  - InitialInterval of 1 second
		//  - BackoffCoefficient of 2.0
		//  - MaximumInterval of 100 x InitialInterval
		//  - MaximumAttempts of 0 (unlimited)
		// To disable retries, set MaximumAttempts to 1.
		// The default RetryPolicy provided by the server can be overridden by the dynamic config.
		RetryPolicy *RetryPolicy
		// TypedSearchAttributes - Specifies Search Attributes that will be attached to the Activity Execution. Search Attributes
		// are additional indexed information attributed to the Activity Execution and used for search and visibility. The Search
		// Attributes can be used in queries to ListActivities and CountActivities. The key and its value type must be registered on
		// the Temporal Server. For supported operations on different server versions see [Visibility].
		//
		// Optional: default to none.
		//
		// [Visibility]: https://docs.temporal.io/visibility
		TypedSearchAttributes SearchAttributes
		// Summary is a single-line summary for this activity that will appear in UI/CLI. This can be
		// in single-line Temporal Markdown format.
		//
		// Optional: defaults to none/empty.
		//
		// NOTE: Experimental
		Summary string
		// Details - General fixed details for this Activity Execution that will appear in UI/CLI. This can be in
		// Temporal Markdown format and can span multiple lines. This value cannot be updated after the Activity Execution starts.
		//
		// Optional: defaults to none/empty.
		//
		// NOTE: Experimental
		Details string
		// Priority - Optional priority settings that control relative ordering of
		// task processing when tasks are backed up in a queue.
		//
		// WARNING: Task queue priority is currently experimental.
		Priority Priority
		// StartDelay - Time to wait before dispatching the activity. This delay is not applied to retry attempts.
		StartDelay time.Duration

		// requestID is the request ID used to dedup retried starts.
		// Only settable by the SDK - e.g. [temporalnexus.temporalOperation].
		requestID string
		// callbacks is the set of completion callbacks the server should invoke when the activity
		// reaches a terminal state. Only settable by the SDK - e.g. [temporalnexus.temporalOperation].
		callbacks []*commonpb.Callback
	}

	// ClientGetActivityHandleOptions contains input for GetActivityHandle call.
	// ActivityID is required. RunID is optional; if empty, the handle targets the latest Activity Execution with the given ID.
	// To target a specific run when ActivityIDReusePolicy allows reuse of an activity ID, set RunID.
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

	// ClientListActivitiesResult contains the result of the ListActivities call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ListActivitiesResult]
	ClientListActivitiesResult struct {
		Results iter.Seq2[*ClientActivityExecutionInfo, error]
	}

	// ClientCountActivitiesOptions contains input for CountActivities call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CountActivitiesOptions]
	ClientCountActivitiesOptions struct {
		Query string
	}

	// ClientCountActivitiesResult contains the result of the CountActivities call.
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
		// Pause pauses the activity. A paused activity stops being retried and, if an attempt is
		// currently running, that attempt is asked to yield. Pausing an already-paused activity
		// is a no-op.
		Pause(ctx context.Context, options ClientPauseActivityOptions) error
		// Unpause resumes a paused activity. Unpausing an activity that is not paused is a no-op.
		Unpause(ctx context.Context, options ClientUnpauseActivityOptions) error
		// Reset returns the activity to its first attempt, discarding retry state. If an attempt
		// is currently running, that attempt is asked to yield and the reset is applied once it
		// does.
		Reset(ctx context.Context, options ClientResetActivityOptions) error
		// UpdateOptions changes some of the activity's options, leaving the rest untouched, and
		// returns the options as they stand after the update. At least one change must be set.
		UpdateOptions(ctx context.Context, options ClientActivityOptionsChanges) (*ClientActivityOptions, error)
		// RestoreOriginalOptions reverts every option changed by UpdateOptions back to the value
		// the activity was scheduled with, and returns the restored options. It is a separate
		// call because the server does not allow the restore flag to be combined with any
		// individual option change.
		RestoreOriginalOptions(ctx context.Context) (*ClientActivityOptions, error)
	}

	// ClientDescribeActivityOptions contains options for ClientActivityHandle.Describe call.
	//
	// The payload-bearing fields of the description are opt-in: the server omits each of them
	// unless the corresponding flag is set. Requesting them costs an extra read on the server,
	// so ask only for what will be used.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.DescribeActivityOptions]
	ClientDescribeActivityOptions struct {
		// IncludeInput requests the arguments the activity was scheduled with.
		// See ClientActivityExecutionDescription.GetInput.
		IncludeInput bool
		// IncludeOutcome requests the activity's result or failure, if it has closed.
		// See ClientActivityExecutionDescription.GetResult and GetFailure.
		IncludeOutcome bool
		// IncludeHeartbeatDetails requests the most recent heartbeat details.
		// See ClientActivityExecutionDescription.GetHeartbeatDetails.
		IncludeHeartbeatDetails bool
		// IncludeLastFailure requests the failure of the most recent failed attempt.
		// See ClientActivityExecutionDescription.GetLastFailure.
		IncludeLastFailure bool
	}

	// ClientCancelActivityOptions contains options for ClientActivityHandle.Cancel call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CancelActivityOptions]
	ClientCancelActivityOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

	// ClientPauseActivityOptions contains options for ClientActivityHandle.Pause call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.PauseActivityOptions]
	ClientPauseActivityOptions struct {
		// Reason is optional description of the reason for pausing.
		Reason string
	}

	// ClientUnpauseActivityOptions contains options for ClientActivityHandle.Unpause call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.UnpauseActivityOptions]
	ClientUnpauseActivityOptions struct {
		// Reason is optional description of the reason for unpausing.
		Reason string
		// Jitter, if non-zero, delays the next attempt by a random duration in [0, Jitter). Use it
		// to spread the load of unpausing many activities at once.
		Jitter time.Duration
	}

	// ClientResetActivityOptions contains options for ClientActivityHandle.Reset call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ResetActivityOptions]
	ClientResetActivityOptions struct {
		// KeepPaused leaves a paused activity paused after the reset. By default a reset also
		// unpauses.
		KeepPaused bool
		// Jitter, if non-zero, delays the next attempt by a random duration in [0, Jitter).
		Jitter time.Duration
		// RestoreOriginalOptions reverts any options changed by UpdateOptions back to the values
		// the activity was scheduled with.
		RestoreOriginalOptions bool
		// ResetHeartbeat discards the persisted heartbeat details instead of carrying them into
		// the new attempt. Off by default.
		ResetHeartbeat bool
	}

	// ClientActivityOptions describes the options an activity is currently running with. It is
	// returned by ClientActivityHandle.UpdateOptions and RestoreOriginalOptions.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityOptions]
	ClientActivityOptions struct {
		TaskQueue              string
		ScheduleToCloseTimeout time.Duration
		ScheduleToStartTimeout time.Duration
		StartToCloseTimeout    time.Duration
		HeartbeatTimeout       time.Duration
		StartDelay             time.Duration
		RetryPolicy            *RetryPolicy
		Priority               Priority
	}

	// ClientActivityOptionsChanges describes changes to the options of a running activity, as
	// used by ClientActivityHandle.UpdateOptions. A nil entry means do not change that option;
	// a non-nil entry sets it to the wrapped value.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityOptionsChanges]
	ClientActivityOptionsChanges struct {
		// If non-nil, change the task queue.
		TaskQueue *TaskQueueChange
		// If non-nil, change the schedule-to-close timeout.
		ScheduleToCloseTimeout *DurationChange
		// If non-nil, change the schedule-to-start timeout.
		ScheduleToStartTimeout *DurationChange
		// If non-nil, change the start-to-close timeout.
		StartToCloseTimeout *DurationChange
		// If non-nil, change the heartbeat timeout.
		HeartbeatTimeout *DurationChange
		// If non-nil, change the start delay.
		StartDelay *DurationChange
		// If non-nil, change the retry policy.
		RetryPolicy *RetryPolicyChange
		// If non-nil, change the priority.
		Priority *PriorityChange
	}

	// TaskQueueChange sets a task queue when used with ClientActivityOptionsChanges.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.TaskQueueChange]
	TaskQueueChange struct {
		Value string
	}

	// DurationChange sets a duration when used with ClientActivityOptionsChanges. A wrapper
	// holding the zero duration clears the option, which a bare time.Duration could not express.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.DurationChange]
	DurationChange struct {
		Value time.Duration
	}

	// RetryPolicyChange sets a retry policy when used with ClientActivityOptionsChanges.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.RetryPolicyChange]
	RetryPolicyChange struct {
		Value RetryPolicy
	}

	// PriorityChange sets a priority when used with ClientActivityOptionsChanges.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.PriorityChange]
	PriorityChange struct {
		Value Priority
	}

	// ClientTerminateActivityOptions contains options for ClientActivityHandle.Terminate call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.TerminateActivityOptions]
	ClientTerminateActivityOptions struct {
		// Reason is optional description of the reason for termination.
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
		// ExecutionTime is when the activity became eligible to run, i.e. ScheduleTime plus
		// any start delay. Zero if the activity has not become eligible yet.
		ExecutionTime time.Time
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
		RawExecutionInfo *activitypb.ActivityExecutionInfo
		// RawDescription is the raw describe response. Unlike RawExecutionInfo it also carries
		// the opt-in Input and Outcome payloads. See ClientDescribeActivityOptions.
		RawDescription          *workflowservice.DescribeActivityExecutionResponse
		ScheduleToCloseTimeout  time.Duration
		ScheduleToStartTimeout  time.Duration
		StartToCloseTimeout     time.Duration
		HeartbeatTimeout        time.Duration
		StartDelay              time.Duration
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
		inboundPayloadVisitor   PayloadVisitor
		summary                 string
		details                 string
	}

	// clientActivityHandleImpl is the default implementation of ClientActivityHandle.
	clientActivityHandleImpl struct {
		client *WorkflowClient
		id     string
		runID  string
		result *ClientPollActivityResultOutput
	}
)

// HasHeartbeatDetails returns whether heartbeat details are present. Use GetHeartbeatDetails to retrieve them.
// The details are only returned when ClientDescribeActivityOptions.IncludeHeartbeatDetails was set.
func (d *ClientActivityExecutionDescription) HasHeartbeatDetails() bool {
	return len(d.RawExecutionInfo.GetHeartbeatDetails().GetPayloads()) > 0
}

// GetHeartbeatDetails retrieves heartbeat details. Returns ErrNoData if heartbeat details are not
// present, which includes the case where they were not requested via
// ClientDescribeActivityOptions.IncludeHeartbeatDetails.
// The details are deserialized into provided pointers using the data converter of the client used to make the Describe call.
// Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetHeartbeatDetails(valuePtrs ...any) error {
	details := d.RawExecutionInfo.GetHeartbeatDetails()
	if details == nil {
		return ErrNoData
	}
	if err := visitProtoPayloads(context.Background(), d.inboundPayloadVisitor, details, 0); err != nil {
		return err
	}
	return d.dataConverter.FromPayloads(details, valuePtrs...)
}

// HasInput returns whether the activity's input is present. Use GetInput to retrieve it.
// The input is only returned when ClientDescribeActivityOptions.IncludeInput was set.
func (d *ClientActivityExecutionDescription) HasInput() bool {
	return len(d.RawDescription.GetInput().GetPayloads()) > 0
}

// GetInput retrieves the arguments the activity was scheduled with. Returns ErrNoData if the
// input is not present, which includes the case where it was not requested via
// ClientDescribeActivityOptions.IncludeInput.
// The arguments are deserialized into the provided pointers, one per argument, using the data
// converter of the client used to make the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetInput(valuePtrs ...any) error {
	input := d.RawDescription.GetInput()
	if input == nil {
		return ErrNoData
	}
	if err := visitProtoPayloads(context.Background(), d.inboundPayloadVisitor, input, 0); err != nil {
		return err
	}
	return d.dataConverter.FromPayloads(input, valuePtrs...)
}

// HasResult returns whether the activity completed successfully and its result is present. Use
// GetResult to retrieve it. The outcome is only returned when
// ClientDescribeActivityOptions.IncludeOutcome was set.
func (d *ClientActivityExecutionDescription) HasResult() bool {
	_, ok := d.RawDescription.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Result)
	return ok
}

// GetResult retrieves the result of a successfully completed activity. Returns ErrNoData if the
// result is not present, which includes an activity that is still running, one that failed, and
// one whose outcome was not requested via ClientDescribeActivityOptions.IncludeOutcome.
// The result is deserialized into valuePtr using the data converter of the client used to make
// the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetResult(valuePtr any) error {
	outcome, ok := d.RawDescription.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Result)
	if !ok {
		return ErrNoData
	}
	if err := visitProtoPayloads(context.Background(), d.inboundPayloadVisitor, outcome.Result, 0); err != nil {
		return err
	}
	return d.dataConverter.FromPayloads(outcome.Result, valuePtr)
}

// GetFailure returns the failure the activity closed with, using the failure converter of the
// client used to make the Describe call. Returns nil if the activity did not fail, or if the
// outcome was not requested via ClientDescribeActivityOptions.IncludeOutcome.
//
// This is the terminal failure of the execution. It differs from GetLastFailure, which reports
// the failure of the most recent attempt of an activity that may still be retrying.
func (d *ClientActivityExecutionDescription) GetFailure() error {
	outcome, ok := d.RawDescription.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Failure)
	if !ok {
		return nil
	}
	if err := visitProtoPayloads(context.Background(), d.inboundPayloadVisitor, outcome.Failure, 0); err != nil {
		return err
	}
	return d.failureConverter.FailureToError(outcome.Failure)
}

// HasLastFailure returns whether the failure of the most recent failed attempt is present. Use
// GetLastFailure to retrieve it. The last failure is only returned when
// ClientDescribeActivityOptions.IncludeLastFailure was set.
func (d *ClientActivityExecutionDescription) HasLastFailure() bool {
	return d.RawExecutionInfo.GetLastFailure() != nil
}

// GetLastFailure returns the failure of the most recent failed attempt, using the failure converter
// of the client used to make the Describe call. Returns nil if there was no failure, or if it was
// not requested via ClientDescribeActivityOptions.IncludeLastFailure.
//
// For the terminal failure of a closed execution, see GetFailure.
func (d *ClientActivityExecutionDescription) GetLastFailure() error {
	failure := d.RawExecutionInfo.GetLastFailure()
	if failure == nil {
		return nil
	}
	if err := visitProtoPayloads(context.Background(), d.inboundPayloadVisitor, failure, 0); err != nil {
		return err
	}
	return d.failureConverter.FailureToError(failure)
}

// GetSummary returns summary of the activity. See ClientStartActivityOptions.Summary. Returns empty string if there is no summary.
// Uses the data converter of the client used to make the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetSummary() (string, error) {
	if d.summary != "" {
		return d.summary, nil
	}
	payload := d.RawExecutionInfo.GetUserMetadata().GetSummary()
	if payload == nil {
		return "", nil
	}
	var err error
	if payload, err = visitPayload(context.Background(), d.inboundPayloadVisitor, payload); err != nil {
		return "", err
	}
	var summary string
	err = d.dataConverter.FromPayload(payload, &summary)
	if err != nil {
		return "", err
	}
	d.summary = summary
	return summary, nil
}

// GetDetails returns details of the activity. See ClientStartActivityOptions.Details. Returns empty string if there are no details.
// Uses the data converter of the client used to make the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetDetails() (string, error) {
	if d.details != "" {
		return d.details, nil
	}
	payload := d.RawExecutionInfo.GetUserMetadata().GetDetails()
	if payload == nil {
		return "", nil
	}
	var err error
	if payload, err = visitPayload(context.Background(), d.inboundPayloadVisitor, payload); err != nil {
		return "", err
	}
	var details string
	err = d.dataConverter.FromPayload(payload, &details)
	if err != nil {
		return "", err
	}
	d.details = details
	return details, nil
}

func (h *clientActivityHandleImpl) GetID() string {
	return h.id
}

func (h *clientActivityHandleImpl) GetRunID() string {
	return h.runID
}

func (h *clientActivityHandleImpl) Get(ctx context.Context, valuePtr any) error {
	if h.result != nil {
		if h.result.Error != nil {
			return h.result.Error
		}
		if h.result.Result != nil {
			if valuePtr == nil {
				return nil
			}
			return h.result.Result.Get(valuePtr)
		}
	}
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
		if resp.Error != nil {
			h.result = &ClientPollActivityResultOutput{Error: resp.Error}
			return resp.Error
		}
		if resp.Result != nil {
			if valuePtr == nil {
				return nil
			}
			h.result = &ClientPollActivityResultOutput{Result: resp.Result}
			return resp.Result.Get(valuePtr)
		}
	}
}

func (h *clientActivityHandleImpl) Describe(ctx context.Context, options ClientDescribeActivityOptions) (*ClientActivityExecutionDescription, error) {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	out, err := h.client.interceptor.DescribeActivity(ctx, &ClientDescribeActivityInput{
		ActivityID:              h.id,
		RunID:                   h.runID,
		IncludeInput:            options.IncludeInput,
		IncludeOutcome:          options.IncludeOutcome,
		IncludeHeartbeatDetails: options.IncludeHeartbeatDetails,
		IncludeLastFailure:      options.IncludeLastFailure,
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

func (h *clientActivityHandleImpl) Pause(ctx context.Context, options ClientPauseActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}
	return h.client.interceptor.PauseActivity(ctx, &ClientPauseActivityInput{
		ActivityID: h.id,
		RunID:      h.runID,
		Reason:     options.Reason,
	})
}

func (h *clientActivityHandleImpl) Unpause(ctx context.Context, options ClientUnpauseActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}
	return h.client.interceptor.UnpauseActivity(ctx, &ClientUnpauseActivityInput{
		ActivityID: h.id,
		RunID:      h.runID,
		Reason:     options.Reason,
		Jitter:     options.Jitter,
	})
}

func (h *clientActivityHandleImpl) Reset(ctx context.Context, options ClientResetActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}
	return h.client.interceptor.ResetActivity(ctx, &ClientResetActivityInput{
		ActivityID:             h.id,
		RunID:                  h.runID,
		KeepPaused:             options.KeepPaused,
		Jitter:                 options.Jitter,
		RestoreOriginalOptions: options.RestoreOriginalOptions,
		ResetHeartbeat:         options.ResetHeartbeat,
	})
}

func (h *clientActivityHandleImpl) UpdateOptions(
	ctx context.Context,
	options ClientActivityOptionsChanges,
) (*ClientActivityOptions, error) {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	out, err := h.client.interceptor.UpdateActivityOptions(ctx, &ClientUpdateActivityOptionsInput{
		ActivityID: h.id,
		RunID:      h.runID,
		Changes:    options,
	})
	if err != nil {
		return nil, err
	}
	return out.Options, nil
}

func (h *clientActivityHandleImpl) RestoreOriginalOptions(ctx context.Context) (*ClientActivityOptions, error) {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	out, err := h.client.interceptor.UpdateActivityOptions(ctx, &ClientUpdateActivityOptionsInput{
		ActivityID:      h.id,
		RunID:           h.runID,
		RestoreOriginal: true,
	})
	if err != nil {
		return nil, err
	}
	return out.Options, nil
}

func (wc *WorkflowClient) ExecuteActivity(ctx context.Context, options ClientStartActivityOptions, activity any, args ...any) (ClientActivityHandle, error) {
	if err := wc.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	activityType, err := getValidatedActivityFunction(activity, args, wc.registry)
	if err != nil {
		return nil, err
	}

	// Set header before interceptor run so interceptors can access it
	ctx = contextWithNewHeader(ctx)

	return wc.interceptor.ExecuteActivity(ctx, &ClientExecuteActivityInput{
		Options:      &options,
		ActivityType: activityType.Name,
		Args:         args,
	})
}

func (wc *WorkflowClient) GetActivityHandle(options ClientGetActivityHandleOptions) ClientActivityHandle {
	return wc.interceptor.GetActivityHandle((*ClientGetActivityHandleInput)(&options))
}

func (wc *WorkflowClient) ListActivities(ctx context.Context, options ClientListActivitiesOptions) (ClientListActivitiesResult, error) {
	return ClientListActivitiesResult{
		Results: func(yield func(*ClientActivityExecutionInfo, error) bool) {
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
						ExecutionTime:         ex.ExecutionTime.AsTime(),
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
		},
	}, nil
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
	dataConverter := WithContext(ctx, w.client.dataConverter)
	if dataConverter == nil {
		dataConverter = converter.GetDefaultDataConverter()
	}

	request := &workflowservice.StartActivityExecutionRequest{
		Namespace:    w.client.namespace,
		Identity:     w.client.identity,
		RequestId:    uuid.NewString(),
		ActivityType: &commonpb.ActivityType{Name: in.ActivityType},
	}
	var err error
	if err = in.Options.validateAndSetInRequest(request, dataConverter); err != nil {
		return nil, err
	}
	// When invoked from inside a Nexus operation handler, attach the operation's inbound caller
	// links to the start request so the backing activity links back to the caller. Async
	// Nexus-backed activities carry these on the completion callback instead, so skip when a
	// callback is already present to avoid duplicating them.
	if len(request.CompletionCallbacks) == 0 {
		if links, ok := ctx.Value(NexusOperationRequestLinksKey).([]*commonpb.Link); ok {
			request.Links = links
		}
	}
	if _, ok := NexusOperationContextFromGoContext(ctx); ok &&
		(len(request.GetCompletionCallbacks()) > 0 || len(request.GetLinks()) > 0) {
		request.OnConflictOptions = &commonpb.OnConflictOptions{
			AttachRequestId:           request.GetRequestId() != "",
			AttachCompletionCallbacks: len(request.GetCompletionCallbacks()) > 0,
			AttachLinks:               len(request.GetLinks()) > 0,
		}
	}
	if request.Input, err = encodeArgs(dataConverter, in.Args); err != nil {
		return nil, err
	}
	if request.Header, err = headerPropagated(ctx, w.client.contextPropagators); err != nil {
		return nil, err
	}

	storeCtx := extstore.WithStorageTarget(ctx, extstore.StorageDriverActivityInfo{
		Namespace:    w.client.namespace,
		ActivityID:   request.ActivityId,
		ActivityType: in.ActivityType,
	})
	if err := visitProtoPayloads(storeCtx, w.outboundPayloadVisitor, request, 0); err != nil {
		return nil, err
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	resp, err := w.client.WorkflowService().StartActivityExecution(grpcCtx, request)

	var runID string
	if err != nil {
		return nil, err
	} else {
		runID = resp.RunId
	}
	if nctx, ok := NexusOperationContextFromGoContext(ctx); ok {
		nctx.AddResponseLink(resp.GetLink())
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
	request.StartDelay = durationpb.New(options.StartDelay)
	if options.requestID != "" {
		request.RequestId = options.requestID
	}
	request.CompletionCallbacks = options.callbacks
	return nil
}

// SetRequestIDOnStartActivityOptions is an internal-only method for setting the request ID on
// ClientStartActivityOptions. Used by [temporalnexus.temporalOperation] for retry idempotency.
func SetRequestIDOnStartActivityOptions(opts *ClientStartActivityOptions, requestID string) {
	opts.requestID = requestID
}

// SetCallbacksOnStartActivityOptions is an internal-only method for setting completion callbacks on
// ClientStartActivityOptions. Callbacks are purposefully not exposed to users for the time being.
func SetCallbacksOnStartActivityOptions(opts *ClientStartActivityOptions, callbacks []*commonpb.Callback) {
	opts.callbacks = callbacks
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
	request := &workflowservice.PollActivityExecutionRequest{
		Namespace:  w.client.namespace,
		ActivityId: in.ActivityID,
		RunId:      in.RunID,
	}

	var resp *workflowservice.PollActivityExecutionResponse
	for resp.GetOutcome() == nil {
		grpcCtx, cancel := newGRPCContext(ctx, grpcLongPoll(true), grpcTimeout(pollActivityTimeout), defaultGrpcRetryParameters(ctx))
		var err error
		resp, err = w.client.WorkflowService().PollActivityExecution(grpcCtx, request)
		cancel()
		if err != nil {
			return nil, err
		}
	}

	if err := visitProtoPayloads(ctx, w.inboundPayloadVisitor, resp, 0); err != nil {
		return nil, err
	}

	switch v := resp.GetOutcome().GetValue().(type) {
	case *activitypb.ActivityExecutionOutcome_Result:
		return &ClientPollActivityResultOutput{Result: newEncodedValue(v.Result, w.client.dataConverter)}, nil
	case *activitypb.ActivityExecutionOutcome_Failure:
		return &ClientPollActivityResultOutput{Error: w.client.failureConverter.FailureToError(v.Failure)}, nil
	default:
		return nil, fmt.Errorf("unexpected activity outcome type: %T", v)
	}
}

func (w *workflowClientInterceptor) DescribeActivity(
	ctx context.Context,
	in *ClientDescribeActivityInput,
) (*ClientDescribeActivityOutput, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.DescribeActivityExecutionRequest{
		Namespace:               w.client.namespace,
		ActivityId:              in.ActivityID,
		RunId:                   in.RunID,
		IncludeInput:            in.IncludeInput,
		IncludeOutcome:          in.IncludeOutcome,
		IncludeHeartbeatDetails: in.IncludeHeartbeatDetails,
		IncludeLastFailure:      in.IncludeLastFailure,
	}
	resp, err := w.client.WorkflowService().DescribeActivityExecution(grpcCtx, request)
	if err != nil {
		return nil, err
	}
	info := resp.GetInfo()
	if info == nil {
		return nil, errors.New("DescribeActivityExecution response doesn't contain info")
	}

	// The server is expected to omit payload fields that were not requested, but an older or
	// buggy server may return them anyway, which would let the Has* accessors disagree with
	// what the caller asked for.
	if !in.IncludeInput {
		resp.Input = nil
	}
	if !in.IncludeOutcome {
		resp.Outcome = nil
	}
	if !in.IncludeHeartbeatDetails {
		info.HeartbeatDetails = nil
	}
	if !in.IncludeLastFailure {
		info.LastFailure = nil
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
				ExecutionTime:         info.ExecutionTime.AsTime(),
			},
			RawExecutionInfo:        info,
			RawDescription:          resp,
			ScheduleToCloseTimeout:  info.ScheduleToCloseTimeout.AsDuration(),
			ScheduleToStartTimeout:  info.ScheduleToStartTimeout.AsDuration(),
			StartToCloseTimeout:     info.StartToCloseTimeout.AsDuration(),
			HeartbeatTimeout:        info.HeartbeatTimeout.AsDuration(),
			StartDelay:              info.StartDelay.AsDuration(),
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
			inboundPayloadVisitor:   w.inboundPayloadVisitor,
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

func (w *workflowClientInterceptor) PauseActivity(
	ctx context.Context,
	in *ClientPauseActivityInput,
) error {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.PauseActivityExecutionRequest{
		Namespace:  w.client.namespace,
		ActivityId: in.ActivityID,
		RunId:      in.RunID,
		Identity:   w.client.identity,
		RequestId:  uuid.NewString(),
		Reason:     in.Reason,
	}
	_, err := w.client.WorkflowService().PauseActivityExecution(grpcCtx, request)
	return err
}

func (w *workflowClientInterceptor) UnpauseActivity(
	ctx context.Context,
	in *ClientUnpauseActivityInput,
) error {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.UnpauseActivityExecutionRequest{
		Namespace:  w.client.namespace,
		ActivityId: in.ActivityID,
		RunId:      in.RunID,
		Identity:   w.client.identity,
		RequestId:  uuid.NewString(),
		Reason:     in.Reason,
	}
	if in.Jitter != 0 {
		request.Jitter = durationpb.New(in.Jitter)
	}
	_, err := w.client.WorkflowService().UnpauseActivityExecution(grpcCtx, request)
	return err
}

func (w *workflowClientInterceptor) ResetActivity(
	ctx context.Context,
	in *ClientResetActivityInput,
) error {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.ResetActivityExecutionRequest{
		Namespace:              w.client.namespace,
		ActivityId:             in.ActivityID,
		RunId:                  in.RunID,
		Identity:               w.client.identity,
		RequestId:              uuid.NewString(),
		KeepPaused:             in.KeepPaused,
		RestoreOriginalOptions: in.RestoreOriginalOptions,
		ResetHeartbeat:         in.ResetHeartbeat,
	}
	if in.Jitter != 0 {
		request.Jitter = durationpb.New(in.Jitter)
	}
	_, err := w.client.WorkflowService().ResetActivityExecution(grpcCtx, request)
	return err
}

// activityOptionsChangesToProto builds the ActivityOptions message and the field mask naming
// exactly the options the caller asked to change.
func activityOptionsChangesToProto(changes ClientActivityOptionsChanges) (*activitypb.ActivityOptions, []string) {
	options := &activitypb.ActivityOptions{}
	var paths []string
	if changes.TaskQueue != nil {
		options.TaskQueue = &taskqueuepb.TaskQueue{Name: changes.TaskQueue.Value}
		paths = append(paths, "task_queue.name")
	}
	if changes.ScheduleToCloseTimeout != nil {
		options.ScheduleToCloseTimeout = durationpb.New(changes.ScheduleToCloseTimeout.Value)
		paths = append(paths, "schedule_to_close_timeout")
	}
	if changes.ScheduleToStartTimeout != nil {
		options.ScheduleToStartTimeout = durationpb.New(changes.ScheduleToStartTimeout.Value)
		paths = append(paths, "schedule_to_start_timeout")
	}
	if changes.StartToCloseTimeout != nil {
		options.StartToCloseTimeout = durationpb.New(changes.StartToCloseTimeout.Value)
		paths = append(paths, "start_to_close_timeout")
	}
	if changes.HeartbeatTimeout != nil {
		options.HeartbeatTimeout = durationpb.New(changes.HeartbeatTimeout.Value)
		paths = append(paths, "heartbeat_timeout")
	}
	if changes.StartDelay != nil {
		options.StartDelay = durationpb.New(changes.StartDelay.Value)
		paths = append(paths, "start_delay")
	}
	if changes.RetryPolicy != nil {
		policy := changes.RetryPolicy.Value
		options.RetryPolicy = convertToPBRetryPolicy(&policy)
		paths = append(paths, "retry_policy")
	}
	if changes.Priority != nil {
		options.Priority = convertToPBPriority(changes.Priority.Value)
		paths = append(paths, "priority")
	}
	return options, paths
}

func activityOptionsFromProto(options *activitypb.ActivityOptions) *ClientActivityOptions {
	return &ClientActivityOptions{
		TaskQueue:              options.GetTaskQueue().GetName(),
		ScheduleToCloseTimeout: options.GetScheduleToCloseTimeout().AsDuration(),
		ScheduleToStartTimeout: options.GetScheduleToStartTimeout().AsDuration(),
		StartToCloseTimeout:    options.GetStartToCloseTimeout().AsDuration(),
		HeartbeatTimeout:       options.GetHeartbeatTimeout().AsDuration(),
		StartDelay:             options.GetStartDelay().AsDuration(),
		RetryPolicy:            convertFromPBRetryPolicy(options.GetRetryPolicy()),
		Priority:               convertFromPBPriority(options.GetPriority()),
	}
}

func (w *workflowClientInterceptor) UpdateActivityOptions(
	ctx context.Context,
	in *ClientUpdateActivityOptionsInput,
) (*ClientUpdateActivityOptionsOutput, error) {
	options, paths := activityOptionsChangesToProto(in.Changes)
	// The server rejects the restore flag alongside individual changes, and an update naming
	// nothing would silently do nothing. Fail before the round trip in both cases.
	if in.RestoreOriginal && len(paths) > 0 {
		return nil, errors.New("RestoreOriginalOptions cannot be combined with individual option changes")
	}
	if !in.RestoreOriginal && len(paths) == 0 {
		return nil, errors.New("UpdateOptions requires at least one option change")
	}
	mask, err := fieldmaskpb.New(&activitypb.ActivityOptions{}, paths...)
	if err != nil {
		return nil, fmt.Errorf("invalid field mask for ActivityOptions: %w", err)
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.UpdateActivityExecutionOptionsRequest{
		Namespace:       w.client.namespace,
		ActivityId:      in.ActivityID,
		RunId:           in.RunID,
		Identity:        w.client.identity,
		RequestId:       uuid.NewString(),
		ActivityOptions: options,
		UpdateMask:      mask,
		RestoreOriginal: in.RestoreOriginal,
	}
	resp, err := w.client.WorkflowService().UpdateActivityExecutionOptions(grpcCtx, request)
	if err != nil {
		return nil, err
	}
	return &ClientUpdateActivityOptionsOutput{
		Options: activityOptionsFromProto(resp.GetActivityOptions()),
	}, nil
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
