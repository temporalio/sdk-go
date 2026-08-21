package test_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
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
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		handle := startRunningSlowActivity(ctx, func(o *client.StartActivityOptions) {
			o.StartToCloseTimeout = 45 * time.Second
		})
		_, err := handle.UpdateOptions(ctx, client.ActivityOptionsChanges{
			StartToCloseTimeout: &client.DurationChange{Value: 90 * time.Second},
		})
		ts.NoError(err)

		ts.NoError(handle.Reset(ctx, client.ResetActivityOptions{RestoreOriginalOptions: true}))

		// The server defers the restore while a worker is mid-attempt, so allow a long window
		// for the worker to yield.
		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{})
			return err == nil && description.StartToCloseTimeout == 45*time.Second
		}, 60*time.Second, 500*time.Millisecond)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Describe payload fields are opt-in", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startHeartbeatReadyActivity(ctx)

		// Assert the default really is "off" rather than the SDK quietly requesting everything:
		// same activity, same moment, two describes.
		bare, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		ts.NoError(err)
		ts.False(bare.HasHeartbeatDetails())

		optedIn, err := handle.Describe(ctx, client.DescribeActivityOptions{IncludeHeartbeatDetails: true})
		ts.NoError(err)
		ts.True(optedIn.HasHeartbeatDetails())
		var details string
		ts.NoError(optedIn.GetHeartbeatDetails(&details))
		ts.Equal("hb-details", details)

		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Describe input and result are opt-in", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
		}, "opEchoActivity", "ping")
		ts.NoError(err)
		var result string
		ts.NoError(handle.Get(ctx, &result))
		ts.Equal("ping-echoed", result)

		bare, err := handle.Describe(ctx, client.DescribeActivityOptions{})
		ts.NoError(err)
		ts.False(bare.HasInput())
		ts.False(bare.HasResult())
		ts.ErrorIs(bare.GetInput(nil), temporal.ErrNoData)
		ts.ErrorIs(bare.GetResult(nil), temporal.ErrNoData)
		ts.NoError(bare.GetFailure())

		description, err := handle.Describe(ctx, client.DescribeActivityOptions{
			IncludeInput:   true,
			IncludeOutcome: true,
		})
		ts.NoError(err)
		ts.True(description.HasInput())
		var word string
		ts.NoError(description.GetInput(&word))
		ts.Equal("ping", word)
		ts.True(description.HasResult())
		var echoed string
		ts.NoError(description.GetResult(&echoed))
		ts.Equal("ping-echoed", echoed)
		// A successful outcome has no failure arm.
		ts.NoError(description.GetFailure())
	})

	ts.Run("Describe reports the outcome failure", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle, err := ts.client.ExecuteActivity(ctx, client.StartActivityOptions{
			ID:                  newID(),
			TaskQueue:           ts.taskQueueName,
			StartToCloseTimeout: 60 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}, "opAlwaysFailActivity")
		ts.NoError(err)
		ts.Error(handle.Get(ctx, nil))

		// The other arm of the oneof: a terminally failed activity has a failure and no result.
		description, err := handle.Describe(ctx, client.DescribeActivityOptions{IncludeOutcome: true})
		ts.NoError(err)
		ts.False(description.HasResult())
		ts.ErrorIs(description.GetResult(nil), temporal.ErrNoData)

		failure := description.GetFailure()
		ts.Error(failure)
		var applicationErr *temporal.ApplicationError
		ts.True(errors.As(failure, &applicationErr))
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

	ts.Run("Reset preserves heartbeat details by default", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startHeartbeatReadyActivity(ctx)
		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "hold"}))
		awaitPaused(ctx, handle)

		// Reset no longer clears heartbeat details by default (api#848, server temporal#11417);
		// the flag must be set explicitly. KeepPaused so that no new attempt starts and
		// re-heartbeats behind our back.
		ts.NoError(handle.Reset(ctx, client.ResetActivityOptions{KeepPaused: true}))

		// The details must survive; hold the assertion true for a window rather than sampling
		// once, so a late clear would still be caught.
		ts.Never(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{IncludeHeartbeatDetails: true})
			return err == nil && !description.HasHeartbeatDetails()
		}, 5*time.Second, 500*time.Millisecond)
		ts.NoError(handle.Terminate(ctx, client.TerminateActivityOptions{Reason: "cleanup"}))
	})

	ts.Run("Reset clears heartbeat details when the flag is set", func() {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()

		handle := startHeartbeatReadyActivity(ctx)
		ts.NoError(handle.Pause(ctx, client.PauseActivityOptions{Reason: "hold"}))
		awaitPaused(ctx, handle)

		// KeepPaused so that no new attempt starts and re-heartbeats.
		ts.NoError(handle.Reset(ctx, client.ResetActivityOptions{KeepPaused: true, ResetHeartbeat: true}))

		// The clear is deferred while a worker is mid-attempt, so wait for the worker to yield
		// the pause before the details disappear.
		ts.Eventually(func() bool {
			description, err := handle.Describe(ctx, client.DescribeActivityOptions{IncludeHeartbeatDetails: true})
			return err == nil && !description.HasHeartbeatDetails()
		}, 30*time.Second, 500*time.Millisecond)
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
