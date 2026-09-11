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
		// StaticDetails - General fixed details for this Activity Execution that will appear in UI/CLI. This can be in
		// Temporal Markdown format and can span multiple lines. This value cannot be updated after the Activity Execution starts.
		//
		// Optional: defaults to none/empty.
		//
		// NOTE: Experimental
		StaticDetails string
		// Priority - Optional priority settings that control relative ordering of
		// task processing when tasks are backed up in a queue.
		//
		// WARNING: Task queue priority is currently experimental.
		Priority Priority
		// StartDelay - Time to wait before dispatching the activity. This delay is not applied to retry attempts.
		StartDelay time.Duration

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
		// UpdateOptions changes some of the activity's options, leaving the rest untouched, and
		// returns the options as they stand after the update. At least one change must be set.
		UpdateOptions(ctx context.Context, update ClientActivityOptionsUpdate) (*ClientActivityExecutionOptions, error)
		// RestoreOriginalOptions reverts every option changed by UpdateOptions back to the value
		// the activity was scheduled with, and returns the restored options. It is a separate
		// call because the server does not allow the restore flag to be combined with any
		// individual option change.
		RestoreOriginalOptions(ctx context.Context) (*ClientActivityExecutionOptions, error)
	}

	// ClientDescribeActivityOptions contains options for ClientActivityHandle.Describe call.
	//
	// The payload-bearing fields of the description are opt-in, since payloads are large.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.DescribeActivityOptions]
	ClientDescribeActivityOptions struct {
		// IncludeInput requests the arguments the activity was scheduled with.
		// See ClientActivityExecutionDescription.GetInput.
		IncludeInput bool
		// IncludeOutcome requests the activity's result or failure, if it has closed.
		// See ClientActivityExecutionDescription.GetResult and GetOutcomeFailure.
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

	// ClientActivityExecutionOptions describes the options an activity is currently running
	// with. It is returned by ClientActivityHandle.UpdateOptions and RestoreOriginalOptions.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityExecutionOptions]
	ClientActivityExecutionOptions struct {
		TaskQueue              string
		ScheduleToCloseTimeout time.Duration
		ScheduleToStartTimeout time.Duration
		StartToCloseTimeout    time.Duration
		HeartbeatTimeout       time.Duration
		StartDelay             time.Duration
		RetryPolicy            *RetryPolicy
		Priority               Priority
	}

	// ClientActivityOptionsUpdate describes changes to an activity's options in
	// ClientActivityHandle.UpdateOptions. An entry with a nil pointer means do not change that
	// option.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityOptionsUpdate]
	ClientActivityOptionsUpdate struct {
		// If non-nil, change the task queue.
		TaskQueue *ClientActivityOptionChange[string]
		// If non-nil, change the schedule-to-close timeout.
		ScheduleToCloseTimeout *ClientActivityOptionChange[time.Duration]
		// If non-nil, change the schedule-to-start timeout.
		ScheduleToStartTimeout *ClientActivityOptionChange[time.Duration]
		// If non-nil, change the start-to-close timeout.
		StartToCloseTimeout *ClientActivityOptionChange[time.Duration]
		// If non-nil, change the heartbeat timeout.
		HeartbeatTimeout *ClientActivityOptionChange[time.Duration]
		// If non-nil, change the start delay.
		StartDelay *ClientActivityOptionChange[time.Duration]
		// If non-nil, change the retry policy.
		RetryPolicy *ClientActivityOptionChange[RetryPolicy]
		// If non-nil, change the priority.
		Priority *ClientActivityOptionChange[Priority]
	}

	// ClientActivityOptionChange sets or clears one activity option when used with
	// [ClientActivityOptionsUpdate].
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityOptionChange]
	ClientActivityOptionChange[T any] struct {
		// Set the option to Value if non-nil. If nil, clear the option so the server applies
		// its default.
		Value *T
	}
)

func (u ClientActivityOptionsUpdate) isEmpty() bool {
	return u.TaskQueue == nil &&
		u.ScheduleToCloseTimeout == nil &&
		u.ScheduleToStartTimeout == nil &&
		u.StartToCloseTimeout == nil &&
		u.HeartbeatTimeout == nil &&
		u.StartDelay == nil &&
		u.RetryPolicy == nil &&
		u.Priority == nil
}

type (
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
		// ClientActivityExecutionDescription.RawResponse instead.
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
		ExecutionTime         time.Time
	}

	// ClientActivityExecutionDescription contains detailed information about an activity execution.
	// This is returned by ClientActivityHandle.Describe.
	//
	//	NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.ActivityExecutionDescription]
	ClientActivityExecutionDescription struct {
		ClientActivityExecutionInfo
		// Raw server response this struct was built from.
		RawResponse             *workflowservice.DescribeActivityExecutionResponse
		ScheduleToCloseTimeout  time.Duration
		ScheduleToStartTimeout  time.Duration
		StartToCloseTimeout     time.Duration
		HeartbeatTimeout        time.Duration
		StartDelay              time.Duration
		RunState                enumspb.PendingActivityState
		LastHeartbeatTime       time.Time
		LastStartedTime         time.Time
		Attempt                 int32
		TotalHeartbeatCount     int64
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
		staticDetails           string
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
	return len(d.RawResponse.GetInfo().GetHeartbeatDetails().GetPayloads()) > 0
}

// GetHeartbeatDetails retrieves heartbeat details. Returns ErrNoData if heartbeat details are not
// present (nonexistent or unrequested via IncludeHeartbeatDetails).
// The details are deserialized into provided pointers using the data converter of the client used to make the Describe call.
// Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetHeartbeatDetails(valuePtrs ...any) error {
	details := d.RawResponse.GetInfo().GetHeartbeatDetails()
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
	return len(d.RawResponse.GetInput().GetPayloads()) > 0
}

// GetInput retrieves the arguments the activity was scheduled with. Returns ErrNoData if the
// input is not present (nonexistent or unrequested via IncludeInput).
// The arguments are deserialized into the provided pointers, one per argument, using the data
// converter of the client used to make the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetInput(valuePtrs ...any) error {
	input := d.RawResponse.GetInput()
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
	_, ok := d.RawResponse.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Result)
	return ok
}

// GetResult retrieves the result of a successfully completed activity. Returns ErrNoData if the
// result is not present, which includes an activity that is still running, one that failed, and
// one whose outcome was not requested via ClientDescribeActivityOptions.IncludeOutcome.
// The result is deserialized into valuePtr using the data converter of the client used to make
// the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetResult(valuePtr any) error {
	outcome, ok := d.RawResponse.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Result)
	if !ok {
		return ErrNoData
	}
	if err := visitProtoPayloads(context.Background(), d.inboundPayloadVisitor, outcome.Result, 0); err != nil {
		return err
	}
	return d.dataConverter.FromPayloads(outcome.Result, valuePtr)
}

// HasOutcomeFailure returns whether the activity closed with a failure and that failure is
// present. Use GetOutcomeFailure to retrieve it. The outcome is only returned when
// ClientDescribeActivityOptions.IncludeOutcome was set.
func (d *ClientActivityExecutionDescription) HasOutcomeFailure() bool {
	_, ok := d.RawResponse.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Failure)
	return ok
}

// GetOutcomeFailure returns the failure the activity closed with, using the failure converter of
// the client used to make the Describe call. Returns nil if the activity did not fail, or if the
// outcome was not requested via ClientDescribeActivityOptions.IncludeOutcome.
//
// This is the terminal failure of the execution. It differs from GetLastFailure, which reports
// the failure of the most recent attempt of an activity that may still be retrying.
func (d *ClientActivityExecutionDescription) GetOutcomeFailure() error {
	outcome, ok := d.RawResponse.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Failure)
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
	return d.RawResponse.GetInfo().GetLastFailure() != nil
}

// GetLastFailure returns the failure of the most recent failed attempt, using the failure converter
// of the client used to make the Describe call. Returns nil if there was no failure, or if it was
// not requested via ClientDescribeActivityOptions.IncludeLastFailure.
//
// For the terminal failure of a closed execution, see GetOutcomeFailure.
func (d *ClientActivityExecutionDescription) GetLastFailure() error {
	failure := d.RawResponse.GetInfo().GetLastFailure()
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
	payload := d.RawResponse.GetInfo().GetUserMetadata().GetSummary()
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

// GetStaticDetails returns details of the activity. See ClientStartActivityOptions.StaticDetails. Returns empty string if there are no details.
// Uses the data converter of the client used to make the Describe call. Returns error if data conversion fails.
func (d *ClientActivityExecutionDescription) GetStaticDetails() (string, error) {
	if d.staticDetails != "" {
		return d.staticDetails, nil
	}
	payload := d.RawResponse.GetInfo().GetUserMetadata().GetDetails()
	if payload == nil {
		return "", nil
	}
	var err error
	if payload, err = visitPayload(context.Background(), d.inboundPayloadVisitor, payload); err != nil {
		return "", err
	}
	var staticDetails string
	err = d.dataConverter.FromPayload(payload, &staticDetails)
	if err != nil {
		return "", err
	}
	d.staticDetails = staticDetails
	return staticDetails, nil
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
		ActivityID: h.id,
		RunID:      h.runID,
		Options:    &options,
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
		Options:    &options,
	})
}

func (h *clientActivityHandleImpl) Unpause(ctx context.Context, options ClientUnpauseActivityOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}
	return h.client.interceptor.UnpauseActivity(ctx, &ClientUnpauseActivityInput{
		ActivityID: h.id,
		RunID:      h.runID,
		Options:    &options,
	})
}

func (h *clientActivityHandleImpl) UpdateOptions(
	ctx context.Context,
	update ClientActivityOptionsUpdate,
) (*ClientActivityExecutionOptions, error) {
	// An update naming nothing would send an empty mask and silently change nothing. Fail here
	// rather than making a round trip that looks like it worked.
	if update.isEmpty() {
		return nil, errors.New("UpdateOptions requires at least one option change")
	}
	if err := h.client.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	out, err := h.client.interceptor.UpdateActivityOptions(ctx, &ClientUpdateActivityOptionsInput{
		ActivityID: h.id,
		RunID:      h.runID,
		Update:     &update,
	})
	if err != nil {
		return nil, err
	}
	return out.Options, nil
}

func (h *clientActivityHandleImpl) RestoreOriginalOptions(ctx context.Context) (*ClientActivityExecutionOptions, error) {
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
	dataConverter = converter.WithDataConverterSerializationContext(dataConverter,
		converter.ActivitySerializationContext{
			Namespace:    w.client.namespace,
			ActivityType: in.ActivityType,
			TaskQueue:    in.Options.TaskQueue,
		})

	request := &workflowservice.StartActivityExecutionRequest{
		Namespace:    w.client.namespace,
		Identity:     w.client.identity,
		RequestId:    uuid.NewString(),
		ActivityType: &commonpb.ActivityType{Name: in.ActivityType},
	}
	// Activity starts from a Nexus handler inherit its normalized request ID. Other
	// Activity starts keep the fresh request ID initialized above.
	if nctx, ok := NexusOperationContextFromGoContext(ctx); ok && nctx.RequestID != "" {
		request.RequestId = nctx.RequestID
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
	userMetadata, err := BuildUserMetadata(options.Summary, options.StaticDetails, dataConverter)
	if err != nil {
		return err
	}

	request.ActivityId = options.ID
	request.TaskQueue = &taskqueuepb.TaskQueue{Name: options.TaskQueue}
	request.ScheduleToCloseTimeout = durationpb.New(options.ScheduleToCloseTimeout)
	request.ScheduleToStartTimeout = durationpb.New(options.ScheduleToStartTimeout)
	request.StartToCloseTimeout = durationpb.New(options.StartToCloseTimeout)
	request.HeartbeatTimeout = durationpb.New(options.HeartbeatTimeout)
	request.RetryPolicy = ConvertToPBRetryPolicy(options.RetryPolicy)
	request.IdReusePolicy = options.ActivityIDReusePolicy
	request.IdConflictPolicy = options.ActivityIDConflictPolicy
	request.SearchAttributes = searchAttrs
	request.UserMetadata = userMetadata
	request.Priority = ConvertToPBPriority(options.Priority)
	request.StartDelay = durationpb.New(options.StartDelay)
	request.CompletionCallbacks = options.callbacks
	return nil
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

	actCtx := converter.ActivitySerializationContext{Namespace: w.client.namespace}
	dataConverter := converter.WithDataConverterSerializationContext(
		WithContext(ctx, w.client.dataConverter), actCtx)
	failureConverter := converter.WithFailureConverterSerializationContext(
		w.client.failureConverter, actCtx)

	switch v := resp.GetOutcome().GetValue().(type) {
	case *activitypb.ActivityExecutionOutcome_Result:
		return &ClientPollActivityResultOutput{Result: newEncodedValue(v.Result, dataConverter)}, nil
	case *activitypb.ActivityExecutionOutcome_Failure:
		return &ClientPollActivityResultOutput{Error: failureConverter.FailureToError(v.Failure)}, nil
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
		IncludeInput:            in.Options.IncludeInput,
		IncludeOutcome:          in.Options.IncludeOutcome,
		IncludeHeartbeatDetails: in.Options.IncludeHeartbeatDetails,
		IncludeLastFailure:      in.Options.IncludeLastFailure,
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

	actCtx := converter.ActivitySerializationContext{
		Namespace:    w.client.namespace,
		ActivityType: info.ActivityType.GetName(),
		TaskQueue:    info.TaskQueue,
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
			RawResponse:             resp,
			ScheduleToCloseTimeout:  info.ScheduleToCloseTimeout.AsDuration(),
			ScheduleToStartTimeout:  info.ScheduleToStartTimeout.AsDuration(),
			StartToCloseTimeout:     info.StartToCloseTimeout.AsDuration(),
			HeartbeatTimeout:        info.HeartbeatTimeout.AsDuration(),
			StartDelay:              info.StartDelay.AsDuration(),
			RunState:                info.RunState,
			LastHeartbeatTime:       info.LastHeartbeatTime.AsTime(),
			LastStartedTime:         info.LastStartedTime.AsTime(),
			Attempt:                 info.Attempt,
			TotalHeartbeatCount:     info.TotalHeartbeatCount,
			RetryPolicy:             convertFromPBRetryPolicy(info.RetryPolicy),
			ExpirationTime:          info.ExpirationTime.AsTime(),
			LastWorkerIdentity:      info.LastWorkerIdentity,
			CurrentRetryInterval:    info.CurrentRetryInterval.AsDuration(),
			LastAttemptCompleteTime: info.LastAttemptCompleteTime.AsTime(),
			NextAttemptScheduleTime: info.NextAttemptScheduleTime.AsTime(),
			LastDeploymentVersion:   lastDeploymentVersion,
			Priority:                convertFromPBPriority(info.Priority),
			CanceledReason:          info.CanceledReason,
			dataConverter: converter.WithDataConverterSerializationContext(
				WithContext(ctx, w.client.dataConverter), actCtx),
			failureConverter: converter.WithFailureConverterSerializationContext(
				w.client.failureConverter, actCtx),
			inboundPayloadVisitor: w.inboundPayloadVisitor,
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
		Reason:     in.Options.Reason,
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
		Reason:     in.Options.Reason,
	}
	if in.Options.Jitter != 0 {
		request.Jitter = durationpb.New(in.Options.Jitter)
	}
	_, err := w.client.WorkflowService().UnpauseActivityExecution(grpcCtx, request)
	return err
}

func activityOptionsUpdateToProto(update ClientActivityOptionsUpdate) (*activitypb.ActivityOptions, []string) {
	options := &activitypb.ActivityOptions{}
	var paths []string

	// Each non-nil change names its path in the mask. A change whose Value is nil leaves the
	// field absent, which is how the server is told to clear the option.
	if c := update.TaskQueue; c != nil {
		paths = append(paths, "task_queue.name")
		if c.Value != nil {
			options.TaskQueue = &taskqueuepb.TaskQueue{Name: *c.Value}
		}
	}
	if c := update.ScheduleToCloseTimeout; c != nil {
		paths = append(paths, "schedule_to_close_timeout")
		if c.Value != nil {
			options.ScheduleToCloseTimeout = durationpb.New(*c.Value)
		}
	}
	if c := update.ScheduleToStartTimeout; c != nil {
		paths = append(paths, "schedule_to_start_timeout")
		if c.Value != nil {
			options.ScheduleToStartTimeout = durationpb.New(*c.Value)
		}
	}
	if c := update.StartToCloseTimeout; c != nil {
		paths = append(paths, "start_to_close_timeout")
		if c.Value != nil {
			options.StartToCloseTimeout = durationpb.New(*c.Value)
		}
	}
	if c := update.HeartbeatTimeout; c != nil {
		paths = append(paths, "heartbeat_timeout")
		if c.Value != nil {
			options.HeartbeatTimeout = durationpb.New(*c.Value)
		}
	}
	if c := update.StartDelay; c != nil {
		paths = append(paths, "start_delay")
		if c.Value != nil {
			options.StartDelay = durationpb.New(*c.Value)
		}
	}
	if c := update.RetryPolicy; c != nil {
		paths = append(paths, "retry_policy")
		if c.Value != nil {
			options.RetryPolicy = ConvertToPBRetryPolicy(c.Value)
		}
	}
	if c := update.Priority; c != nil {
		paths = append(paths, "priority")
		if c.Value != nil {
			options.Priority = ConvertToPBPriority(*c.Value)
		}
	}
	return options, paths
}

func activityOptionsFromProto(options *activitypb.ActivityOptions) *ClientActivityExecutionOptions {
	return &ClientActivityExecutionOptions{
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
	options := &activitypb.ActivityOptions{}
	var paths []string
	if in.Update != nil {
		options, paths = activityOptionsUpdateToProto(*in.Update)
	}
	// The handle doesn't do this, but an interceptor could.
	if in.RestoreOriginal && len(paths) > 0 {
		return nil, errors.New("RestoreOriginalOptions cannot be combined with individual option changes")
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
