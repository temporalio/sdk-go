package internal

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

const (
	deadlockDetectionTime = 400 * time.Millisecond
	payloadConverterTime  = 600 * time.Millisecond
)

func TestDeadlockDetector(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const timeout = 500 * time.Millisecond
		d := newDeadlockDetector()
		ticker := d.begin(timeout)
		defer ticker.end()
		d.pause()

		time.Sleep(timeout + 100*time.Millisecond)
		select {
		case <-ticker.reached():
			t.Fatal("unexpectedly reached deadlock while paused")
		default:
		}

		d.resume()
		time.Sleep(timeout)
		synctest.Wait()
		select {
		case <-ticker.reached():
		default:
			t.Fatal("deadlock timeout was not reached after resume")
		}
	})
}

func TestDataConverterWithoutDeadlockDetection(t *testing.T) {
	runWorkflow := func(t *testing.T, conv converter.DataConverter) error {
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
		env.SetWorkerOptions(WorkerOptions{DeadlockDetectionTimeout: deadlockDetectionTime})
		env.RegisterWorkflow(workflowFn)
		env.RegisterActivity(activityFn)
		env.ExecuteWorkflow(workflowFn)
		require.True(t, env.IsWorkflowCompleted())
		return env.GetWorkflowError()
	}

	t.Run("detects deadlock", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			conv := &slowToPayloadsConverter{
				DataConverter: converter.GetDefaultDataConverter(),
			}
			require.ErrorContains(t, runWorkflow(t, conv), "Potential deadlock detected")

			time.Sleep(payloadConverterTime - deadlockDetectionTime)
			synctest.Wait()
		})
	})

	t.Run("disables deadlock detection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			conv := converter.DataConverter(&slowToPayloadsConverter{
				DataConverter: converter.GetDefaultDataConverter(),
			})
			conv = DataConverterWithoutDeadlockDetection(conv)
			require.NoError(t, runWorkflow(t, conv))
		})
	})

	t.Run("outside workflow", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			conv := converter.DataConverter(&slowToPayloadsConverter{
				DataConverter: converter.GetDefaultDataConverter(),
			})
			conv = DataConverterWithoutDeadlockDetection(conv)
			_, err := conv.ToPayload("foo")
			require.NoError(t, err)
			_, err = conv.(ContextAware).WithWorkflowContext(Background()).ToPayload("foo")
			require.NoError(t, err)
		})
	})
}

type slowToPayloadsConverter struct {
	converter.DataConverter
}

func (s *slowToPayloadsConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	time.Sleep(payloadConverterTime)
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
