package test_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	activitypb "go.temporal.io/api/activity/v1"
	enumspb "go.temporal.io/api/enums/v1"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// A running activity does not transition straight to PAUSED on pause: the server records
// PAUSE_REQUESTED and only moves to PAUSED once the worker drops the attempt. A long-running
// heartbeating activity that has not yet noticed the pause stays in PAUSE_REQUESTED, so both
// states count as "paused" for an observability assertion.
func isPaused(state enumspb.PendingActivityState) bool {
	return state == enumspb.PENDING_ACTIVITY_STATE_PAUSED ||
		state == enumspb.PENDING_ACTIVITY_STATE_PAUSE_REQUESTED
}

// TestActivityOperatorCommandsSuite covers pause, unpause, reset and update-options on standalone
// activities. Each case asserts an observable server-side state change rather than a successful
// RPC.
func (ts *IntegrationTestSuite) TestActivityOperatorCommandsSuite() {
	if os.Getenv("DISABLE_STANDALONE_ACTIVITY_TESTS") != "" {
		ts.T().SkipNow()
	}

	// Long-running activity that heartbeats and runs until cancellation.
	slowActivity := func(ctx context.Context) error {
		for {
			activity.RecordHeartbeat(ctx)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	// Returns immediately. Used with a start delay so it can be paused while scheduled.
	quickActivity := func(ctx context.Context) (string, error) {
		return "resumed", nil
	}
	// Fails the first two attempts so retries are forced, then succeeds.
	failThenSucceedActivity := func(ctx context.Context) (string, error) {
		if activity.GetInfo(ctx).Attempt < 3 {
			return "", errors.New("retryable failure")
		}
		return "done", nil
	}
	// Takes an argument and returns a value derived from it, so a completed execution has both
	// an input and a successful outcome to read back off describe.
	echoActivity := func(ctx context.Context, word string) (string, error) {
		return word + "-echoed", nil
	}
	// Always fails. Paired with a single-attempt retry policy so the activity reaches a terminal
	// failure outcome rather than retrying.
	alwaysFailActivity := func(ctx context.Context) error {
		return temporal.NewApplicationError("deliberate failure", "")
	}
	// Heartbeats, fails the first attempt, then succeeds. One execution of this carries
	// input, a result, heartbeat details and a last failure all at once, which is what lets a
	// single describe exercise every payload field.
	heartbeatFailIncrement := func(ctx context.Context, value int) (int, error) {
		activity.RecordHeartbeat(ctx, "heartbeat details")
		if activity.GetInfo(ctx).Attempt == 1 {
			return 0, temporal.NewApplicationError("deliberate first-attempt failure", "")
		}
		return value + 1, nil
	}

	// Records the same heartbeat details until cancelled. The details are re-sent on every beat
	// rather than once, because the SDK delivers cancellation through the heartbeat response: an
	// activity that stops heartbeating never learns it has been asked to yield, and the server
	// defers a paused activity's reset until the running attempt does yield.
	heartbeatingActivity := func(ctx context.Context) error {
		for {
			activity.RecordHeartbeat(ctx, "hb-details")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	ts.worker.RegisterActivityWithOptions(slowActivity, activity.RegisterOptions{Name: "opSlowActivity"})
	ts.worker.RegisterActivityWithOptions(quickActivity, activity.RegisterOptions{Name: "opQuickActivity"})
	ts.worker.RegisterActivityWithOptions(failThenSucceedActivity, activity.RegisterOptions{Name: "opFailThenSucceedActivity"})
	ts.worker.RegisterActivityWithOptions(echoActivity, activity.RegisterOptions{Name: "opEchoActivity"})
	ts.worker.RegisterActivityWithOptions(alwaysFailActivity, activity.RegisterOptions{Name: "opAlwaysFailActivity"})
	ts.worker.RegisterActivityWithOptions(heartbeatingActivity, activity.RegisterOptions{Name: "opHeartbeatingActivity"})
	ts.worker.RegisterActivityWithOptions(heartbeatFailIncrement, activity.RegisterOptions{Name: "opHeartbeatFailIncrement"})

	newID := func() string { return fmt.Sprintf("act-%v", uuid.NewString()) }

	// startRunningSlowActivity starts a slow activity and waits until it is actually running on
	// the worker, so that the operator command under test acts on a started attempt.
	startRunningSlowActivity := func(ctx context.Context, mutate func(*client.StartActivityOptions)) client.ActivityHandle {
		options := client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			HeartbeatTimeout:    30 * time.Second,
		}
		if mutate != nil {
			mutate(&options)
		}
		handle, err := ts.client.ExecuteActivity(ctx, options, "opSlowActivity")
		ts.NoError(err)
		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && description.RunState == enumspb.PENDING_ACTIVITY_STATE_STARTED
		}, 20*time.Second, 200*time.Millisecond)
		return handle
	}

	// startHeartbeatReadyActivity starts the heartbeating activity and waits until the details
	// have actually been persisted, so a later assertion about them is meaningful.
	startHeartbeatReadyActivity := func(ctx context.Context) client.ActivityHandle {
		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			HeartbeatTimeout:    30 * time.Second,
		}, "opHeartbeatingActivity")
		ts.NoError(err)
		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{
				IncludeHeartbeatDetails: true,
			})
			return err == nil && description.HasHeartbeatDetails()
		}, 20*time.Second, 200*time.Millisecond)
		return handle
	}

	awaitPaused := func(ctx context.Context, handle client.ActivityHandle) {
		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && isPaused(description.RunState)
		}, 20*time.Second, 200*time.Millisecond)
	}

	ts.Run("Unpause resumes", func() {
		// The start delay below still has to elapse after the unpause, so this case needs more
		// headroom than the suite's default context timeout.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// Start delayed so the activity sits scheduled and can be paused before it runs.
		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			StartDelay:          30 * time.Second,
		}, "opQuickActivity")
		ts.NoError(err)

		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "pause-before-unpause"}))

		// A not-yet-started (scheduled) activity transitions fully to PAUSED.
		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && description.RunState == enumspb.PENDING_ACTIVITY_STATE_PAUSED
		}, 20*time.Second, 200*time.Millisecond)

		ts.NoError(handle.Unpause(ctx, client.UnpauseActivityOptions{}))

		// After unpause the activity proceeds and completes successfully.
		var result string
		ts.NoError(handle.Get(ctx, &result))
		ts.Equal("resumed", result)
	})

	ts.Run("Reset returns to the first attempt", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    200 * time.Millisecond,
				BackoffCoefficient: 1.0,
				MaximumInterval:    200 * time.Millisecond,
				MaximumAttempts:    50,
			},
		}, "opFailThenSucceedActivity")
		ts.NoError(err)

		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && description.Attempt > 1
		}, 20*time.Second, 200*time.Millisecond)

		ts.NoError(handle.Reset(ctx, client.ResetActivityOptions{}))

		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && description.Attempt == 1
		}, 20*time.Second, 200*time.Millisecond)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Describe reports a paused activity as paused", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		// Start delayed so the activity sits scheduled; pausing from there reaches a true PAUSED
		// state rather than the PAUSE_REQUESTED of a running activity.
		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			StartDelay:          30 * time.Second,
		}, "opQuickActivity")
		ts.NoError(err)

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		ts.NoError(err)
		ts.Equal(enumspb.ACTIVITY_EXECUTION_STATUS_RUNNING, description.Status)

		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "hold"}))

		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil &&
				description.Status == enumspb.ACTIVITY_EXECUTION_STATUS_PAUSED &&
				description.RunState == enumspb.PENDING_ACTIVITY_STATE_PAUSED
		}, 20*time.Second, 200*time.Millisecond)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("UpdateOptions respects the mask", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startRunningSlowActivity(ctx, func(o *client.StartActivityOptions) {
			o.ScheduleToCloseTimeout = 120 * time.Second
		})

		updated, err := handle.UpdateOptions(ctx, client.ActivityOptionsChanges{
			StartToCloseTimeout: &client.DurationChange{Value: 90 * time.Second},
		})
		ts.NoError(err)

		// Only start-to-close changed; schedule-to-close kept its original value.
		ts.Equal(90*time.Second, updated.StartToCloseTimeout)
		ts.Equal(120*time.Second, updated.ScheduleToCloseTimeout)

		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil &&
				description.StartToCloseTimeout == 90*time.Second &&
				description.ScheduleToCloseTimeout == 120*time.Second
		}, 20*time.Second, 200*time.Millisecond)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("UpdateOptions changes every field", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		// Start delayed so the activity stays scheduled while every option is updated.
		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                     newID(),
			TaskQueue:              ts.taskQueueName,
			ScheduleToCloseTimeout: 100 * time.Second,
			StartToCloseTimeout:    30 * time.Second,
			StartDelay:             300 * time.Second,
		}, "opQuickActivity")
		ts.NoError(err)

		updated, err := handle.UpdateOptions(ctx, client.ActivityOptionsChanges{
			TaskQueue:              &client.TaskQueueChange{Value: "updated-tq"},
			ScheduleToCloseTimeout: &client.DurationChange{Value: 200 * time.Second},
			ScheduleToStartTimeout: &client.DurationChange{Value: 15 * time.Second},
			StartToCloseTimeout:    &client.DurationChange{Value: 90 * time.Second},
			HeartbeatTimeout:       &client.DurationChange{Value: 25 * time.Second},
			StartDelay:             &client.DurationChange{Value: 500 * time.Second},
			RetryPolicy: &client.RetryPolicyChange{Value: temporal.RetryPolicy{
				InitialInterval:    time.Second,
				BackoffCoefficient: 2.0,
				MaximumAttempts:    7,
			}},
			Priority: &client.PriorityChange{Value: temporal.Priority{PriorityKey: 3}},
		})
		ts.NoError(err)

		ts.Equal("updated-tq", updated.TaskQueue)
		ts.Equal(200*time.Second, updated.ScheduleToCloseTimeout)
		ts.Equal(15*time.Second, updated.ScheduleToStartTimeout)
		ts.Equal(90*time.Second, updated.StartToCloseTimeout)
		ts.Equal(25*time.Second, updated.HeartbeatTimeout)
		ts.NotNil(updated.RetryPolicy)
		ts.EqualValues(7, updated.RetryPolicy.MaximumAttempts)
		ts.Equal(3, updated.Priority.PriorityKey)
		ts.Equal(500*time.Second, updated.StartDelay)

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		ts.NoError(err)
		ts.Equal("updated-tq", description.TaskQueue)
		ts.Equal(500*time.Second, description.StartDelay)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("RestoreOriginalOptions reverts an update", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startRunningSlowActivity(ctx, func(o *client.StartActivityOptions) {
			o.StartToCloseTimeout = 45 * time.Second
		})

		changed, err := handle.UpdateOptions(ctx, client.ActivityOptionsChanges{
			StartToCloseTimeout: &client.DurationChange{Value: 90 * time.Second},
		})
		ts.NoError(err)
		ts.Equal(90*time.Second, changed.StartToCloseTimeout)

		// Restore alone reverts to the value the activity was created with.
		restored, err := handle.RestoreOriginalOptions(ctx)
		ts.NoError(err)
		ts.Equal(45*time.Second, restored.StartToCloseTimeout)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("UpdateOptions applies to a paused activity", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startRunningSlowActivity(ctx, nil)
		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "hold"}))
		awaitPaused(ctx, handle)

		// Updating options while paused applies, and leaves the activity paused.
		updated, err := handle.UpdateOptions(ctx, client.ActivityOptionsChanges{
			StartToCloseTimeout: &client.DurationChange{Value: 99 * time.Second},
		})
		ts.NoError(err)
		ts.Equal(99*time.Second, updated.StartToCloseTimeout)

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		ts.NoError(err)
		ts.True(isPaused(description.RunState))
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Reset keeps the activity paused", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startRunningSlowActivity(ctx, nil)
		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "hold"}))
		awaitPaused(ctx, handle)

		ts.NoError(handle.Reset(ctx, client.ResetActivityOptions{KeepPaused: true}))

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		ts.NoError(err)
		ts.True(isPaused(description.RunState))
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Reset restores the original options", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		// Start delayed so the activity sits scheduled. With no worker holding an attempt the
		// server applies the restore immediately, rather than deferring it until the running
		// attempt yields on its next heartbeat.
		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 45 * time.Second,
			StartDelay:          300 * time.Second,
		}, "opQuickActivity")
		ts.NoError(err)

		_, err = handle.UpdateOptions(ctx, client.ActivityOptionsChanges{
			StartToCloseTimeout: &client.DurationChange{Value: 90 * time.Second},
		})
		ts.NoError(err)

		ts.NoError(handle.Reset(ctx, client.ResetActivityOptions{RestoreOriginalOptions: true}))

		// RestoreOriginalOptions reverts the changed option to the value the activity was
		// created with.
		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && description.StartToCloseTimeout == 45*time.Second
		}, 20*time.Second, 200*time.Millisecond)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Describe reports the total heartbeat count", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		// The count tracks heartbeats the server recorded, not calls the activity made: the
		// SDK throttles them to roughly 0.8x the heartbeat timeout, so a short timeout is what
		// makes a second heartbeat arrive promptly.
		handle := startRunningSlowActivity(ctx, func(o *client.StartActivityOptions) {
			o.HeartbeatTimeout = 3 * time.Second
		})

		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && description.TotalHeartbeatCount >= 2
		}, 20*time.Second, 200*time.Millisecond)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Description exposes the whole response", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
		}, "opEchoActivity", "ping")
		ts.NoError(err)
		ts.NoError(handle.Get(ctx, nil))

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{
			IncludeInput:   true,
			IncludeOutcome: true,
		})
		ts.NoError(err)

		// RawDescription is the entire describe response. The decoded accessors are derived
		// from it, so the raw payloads and the decoded values must agree, and input must not
		// be confused with the result.
		ts.Equal(handle.GetID(), description.RawDescription.GetInfo().GetActivityId())
		ts.Same(description.RawExecutionInfo, description.RawDescription.GetInfo())

		var word string
		ts.NoError(description.GetInput(&word))
		ts.Equal("ping", word)
		ts.Len(description.RawDescription.GetInput().GetPayloads(), 1)

		var echoed string
		ts.NoError(description.GetResult(&echoed))
		ts.Equal("ping-echoed", echoed)
		_, rawHasResult := description.RawDescription.GetOutcome().GetValue().(*activitypb.ActivityExecutionOutcome_Result)
		ts.True(rawHasResult)
		ts.Equal(description.HasResult(), rawHasResult)
	})

	ts.Run("Describe payloads", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			HeartbeatTimeout:    5 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    100 * time.Millisecond,
				BackoffCoefficient: 1.0,
				MaximumAttempts:    2,
			},
		}, "opHeartbeatFailIncrement", 1)
		ts.NoError(err)
		var result int
		ts.NoError(handle.Get(ctx, &result))
		ts.Equal(2, result)

		// Nothing requested: every payload field is absent, and the accessors agree with the
		// Has* flags rather than merely being empty.
		bare, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		ts.NoError(err)
		ts.False(bare.HasInput())
		ts.False(bare.HasResult())
		ts.False(bare.HasHeartbeatDetails())
		ts.False(bare.HasLastFailure())
		ts.ErrorIs(bare.GetInput(nil), temporal.ErrNoData)
		ts.ErrorIs(bare.GetResult(nil), temporal.ErrNoData)
		ts.NoError(bare.GetFailure())
		ts.NoError(bare.GetLastFailure())

		// All four requested. The activity succeeded on its second attempt, so it has a result
		// and a last failure at the same time, and no terminal failure.
		full, err := handle.Describe(ctx, client.DescribeActivityOptions{
			IncludeInput:            true,
			IncludeOutcome:          true,
			IncludeHeartbeatDetails: true,
			IncludeLastFailure:      true,
		})
		ts.NoError(err)
		var input int
		ts.NoError(full.GetInput(&input))
		ts.Equal(1, input)
		ts.True(full.HasResult())
		var got int
		ts.NoError(full.GetResult(&got))
		ts.Equal(2, got)
		ts.NoError(full.GetFailure())
		ts.True(full.HasHeartbeatDetails())
		var details string
		ts.NoError(full.GetHeartbeatDetails(&details))
		ts.Equal("heartbeat details", details)
		ts.True(full.HasLastFailure())
		ts.Error(full.GetLastFailure())

		// The other arm of the oneof, on an activity that never succeeds.
		failed, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}, "opAlwaysFailActivity")
		ts.NoError(err)
		ts.Error(failed.Get(ctx, nil))

		desc, err := failed.Describe(ctx, client.DescribeActivityOptions{
			IncludeOutcome: true, IncludeLastFailure: true,
		})
		ts.NoError(err)
		ts.False(desc.HasResult())
		ts.ErrorIs(desc.GetResult(nil), temporal.ErrNoData)
		var applicationErr *temporal.ApplicationError
		ts.True(errors.As(desc.GetFailure(), &applicationErr))
		ts.Contains(applicationErr.Error(), "deliberate failure")
	})

	ts.Run("Pause preserves heartbeat details", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startHeartbeatReadyActivity(ctx)
		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "hold"}))
		awaitPaused(ctx, handle)

		// Pause never touches heartbeat details.
		description, err := handle.Describe(ctx, client.DescribeActivityOptions{IncludeHeartbeatDetails: true})
		ts.NoError(err)
		ts.True(description.HasHeartbeatDetails())
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Unpause preserves heartbeat details", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startHeartbeatReadyActivity(ctx)
		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "hold"}))
		awaitPaused(ctx, handle)
		ts.NoError(handle.Unpause(ctx, client.UnpauseActivityOptions{}))

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{IncludeHeartbeatDetails: true})
		ts.NoError(err)
		ts.True(description.HasHeartbeatDetails())
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("UpdateOptions preserves heartbeat details", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startHeartbeatReadyActivity(ctx)
		_, err := handle.UpdateOptions(ctx, client.ActivityOptionsChanges{
			StartToCloseTimeout: &client.DurationChange{Value: 90 * time.Second},
		})
		ts.NoError(err)

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{IncludeHeartbeatDetails: true})
		ts.NoError(err)
		ts.True(description.HasHeartbeatDetails())
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})
}
