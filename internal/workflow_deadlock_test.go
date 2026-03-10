package internal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func TestDeadlockDetector(t *testing.T) {
	// Create a 500ms ticker and confirm it pauses/resumes properly. We have
	// chosen to use real time instead of an abstract clock here for simplicity.
	d := newDeadlockDetector()
	ticker := d.begin(500 * time.Millisecond)
	defer ticker.end()
	d.pause()
	// Confirm never reached while paused
	select {
	case <-time.After(600 * time.Millisecond):
	case <-ticker.reached():
		t.Fatal("unexpectedly reached deadlock")
	}
	// Resume and confirm reached
	d.resume()
	select {
	case <-time.After(600 * time.Millisecond):
		t.Fatal("unexpectedly didn't deadlock")
	case <-ticker.reached():
	}
}

func TestDataConverterWithoutDeadlockDetection(t *testing.T) {
	runWorkflow := func(conv converter.DataConverter) error {
		var suite WorkflowTestSuite
		activityFn := func(ctx context.Context, arg string) error {
			return nil
		}
		workflowFn := func(ctx Context) error {
			ctx = WithDataConverter(ctx, conv)
			ctx = WithActivityOptions(ctx, ActivityOptions{ScheduleToCloseTimeout: 10 * time.Second})
			return ExecuteActivity(ctx, activityFn, "some arg").Get(ctx, nil)
		}
		env := suite.NewTestWorkflowEnvironment()
		env.SetWorkerOptions(WorkerOptions{DeadlockDetectionTimeout: 400 * time.Millisecond})
		env.RegisterWorkflow(workflowFn)
		env.RegisterActivity(activityFn)
		env.ExecuteWorkflow(workflowFn)
		require.True(t, env.IsWorkflowCompleted())
		return env.GetWorkflowError()
	}

	// Run with a slow converter and confirm a deadlock is detected
	conv := converter.GetDefaultDataConverter()
	conv = &slowToPayloadsConverter{conv}
	require.ErrorContains(t, runWorkflow(conv), "Potential deadlock detected")

	// Run with that same payload converter without deadlock detection
	conv = DataConverterWithoutDeadlockDetection(conv)
	require.NoError(t, runWorkflow(conv))

	// Also confirm outside of workflow, pause/resume is noop
	_, err := conv.ToPayload("foo")
	require.NoError(t, err)
	_, err = conv.(ContextAware).WithWorkflowContext(Background()).ToPayload("foo")
	require.NoError(t, err)
}

type slowToPayloadsConverter struct{ converter.DataConverter }

func (s *slowToPayloadsConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	time.Sleep(600 * time.Millisecond)
	return s.DataConverter.ToPayloads(value...)
}

func TestDataConverterWithoutDeadlockDetectionContext(t *testing.T) {
	contextAwareDataConverter := NewContextAwareDataConverter(converter.GetDefaultDataConverter())
	conv := DataConverterWithoutDeadlockDetection(contextAwareDataConverter)

	t.Parallel()
	t.Run("default", func(t *testing.T) {
		t.Parallel()
		payload, _ := conv.ToPayload("test")
		result := conv.ToString(payload)

		require.Equal(t, `"test"`, result)
	})
	t.Run("implements ContextAware", func(t *testing.T) {
		t.Parallel()
		_, ok := conv.(ContextAware)
		require.True(t, ok)
	})
	t.Run("with activity context", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		ctx = context.WithValue(ctx, ContextAwareDataConverterContextKey, "e")

		dc := WithContext(ctx, conv)

		payload, _ := dc.ToPayload("test")
		result := dc.ToString(payload)

		require.Equal(t, `"t?st"`, result)
	})
	t.Run("with workflow context", func(t *testing.T) {
		t.Parallel()
		ctx := Background()
		ctx = WithValue(ctx, ContextAwareDataConverterContextKey, "e")

		dc := WithWorkflowContext(ctx, conv)

		payload, _ := dc.ToPayload("test")
		result := dc.ToString(payload)

		require.Equal(t, `"t?st"`, result)
	})

}
