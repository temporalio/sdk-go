package backoff

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/internal/common/retry"
)

type someError struct{}

func (e *someError) Error() string {
	return "Some Error"
}

func TestRetrySuccess(t *testing.T) {
	t.Parallel()
	i := 0
	op := func() error {
		i++

		if i == 5 {
			return nil
		}

		return &someError{}
	}

	policy := NewExponentialRetryPolicy(1 * time.Millisecond)
	policy.SetMaximumInterval(5 * time.Millisecond)
	policy.SetMaximumAttempts(10)

	err := Retry(context.Background(), op, policy, nil)
	assert.NoError(t, err)
	assert.Equal(t, 5, i)
}

func TestNoRetryAfterContextDone(t *testing.T) {
	t.Parallel()
	retryCounter := 0
	op := func() error {
		retryCounter++

		if retryCounter == 5 {
			return nil
		}

		return &someError{}
	}

	policy := NewExponentialRetryPolicy(10 * time.Millisecond)
	policy.SetMaximumInterval(50 * time.Millisecond)
	policy.SetMaximumAttempts(10)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := Retry(ctx, op, policy, nil)
	assert.Error(t, err)
	assert.True(t, retryCounter >= 2, "retryCounter should be at least 2 but was %d", retryCounter) // verify that we did retry
}

func TestRetryFailed(t *testing.T) {
	t.Parallel()
	i := 0
	op := func() error {
		i++

		if i == 6 {
			assert.Fail(t, "Should never be called because retry is set to 5 attempts")
		}

		return &someError{}
	}

	policy := NewExponentialRetryPolicy(1 * time.Millisecond)
	policy.SetMaximumInterval(5 * time.Millisecond)
	policy.SetMaximumAttempts(5)

	err := Retry(context.Background(), op, policy, nil)
	assert.Error(t, err)
	assert.Equal(t, 5, i)
}

func TestIsRetryableSuccess(t *testing.T) {
	t.Parallel()
	i := 0
	op := func() error {
		i++

		if i == 5 {
			return nil
		}

		return &someError{}
	}

	isRetryable := func(err error) bool {
		if _, ok := err.(*someError); ok {
			return true
		}

		return false
	}

	policy := NewExponentialRetryPolicy(1 * time.Millisecond)
	policy.SetMaximumInterval(5 * time.Millisecond)
	policy.SetMaximumAttempts(10)

	err := Retry(context.Background(), op, policy, isRetryable)
	assert.NoError(t, err, "Retry count: %v", i)
	assert.Equal(t, 5, i)
}

func TestIsRetryableFailure(t *testing.T) {
	t.Parallel()
	i := 0
	op := func() error {
		i++

		if i == 5 {
			return nil
		}

		return &someError{}
	}

	policy := NewExponentialRetryPolicy(1 * time.Millisecond)
	policy.SetMaximumInterval(5 * time.Millisecond)
	policy.SetMaximumAttempts(10)

	err := Retry(context.Background(), op, policy, IgnoreErrors([]error{&someError{}}))
	assert.Error(t, err)
	assert.Equal(t, 1, i)
}

func TestConcurrentRetrier(t *testing.T) {
	t.Parallel()
	a := assert.New(t)
	policy := NewExponentialRetryPolicy(1 * time.Millisecond)
	policy.SetMaximumInterval(10 * time.Millisecond)
	policy.SetMaximumAttempts(4)

	// Basic checks
	retrier := NewConcurrentRetrier(policy)
	retrier.Failed(false)
	a.Equal(int64(1), retrier.failureCount)
	retrier.Succeeded()
	a.Equal(int64(0), retrier.failureCount)
	sleepDuration := retrier.throttleInternal(nil)
	a.Equal(done, sleepDuration)

	// Multiple count check.
	retrier.Failed(false)
	retrier.Failed(false)
	a.Equal(int64(2), retrier.failureCount)
	// Verify valid sleep times.
	ch := make(chan time.Duration, 3)
	go func() {
		for i := 0; i < 3; i++ {
			ch <- retrier.throttleInternal(nil)
		}
	}()
	for i := 0; i < 3; i++ {
		val := <-ch
		t.Logf("Duration: %d\n", val)
		a.True(val > 0)
	}
	retrier.Succeeded()
	a.Equal(int64(0), retrier.failureCount)
	// Verify we don't have any sleep times.
	go func() {
		for i := 0; i < 3; i++ {
			ch <- retrier.throttleInternal(nil)
		}
	}()
	for i := 0; i < 3; i++ {
		val := <-ch
		t.Logf("Duration: %d\n", val)
		a.Equal(done, val)
	}

	// Secondary retrier check
	secPolicy := NewExponentialRetryPolicy(20 * time.Millisecond)
	retrier.SetSecondaryRetryPolicy(secPolicy)
	retrier.Failed(false)
	a.InDelta(1*time.Millisecond, retrier.throttleInternal(nil), float64(1*time.Millisecond)*retry.DefaultJitter)
	retrier.Failed(true)
	a.InDelta(20*time.Millisecond, retrier.throttleInternal(nil), float64(20*time.Millisecond)*retry.DefaultJitter)
	retrier.Succeeded()
}

func TestRetryDeadlineExceeded(t *testing.T) {
	t.Parallel()
	attempt := 0
	actualError := &someError{}
	op := func() error {
		attempt++
		if attempt == 3 {
			// Last attempt returns DeadlineExceeded but Retry should return actualError.
			return context.DeadlineExceeded
		}
		return actualError
	}

	policy := NewExponentialRetryPolicy(1 * time.Millisecond)
	policy.SetBackoffCoefficient(1)
	policy.SetMaximumAttempts(3)

	err := Retry(context.Background(), op, policy, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, actualError)

	attempt = 0
	op = func() error {
		attempt++
		if attempt == 3 {
			// Last attempt returns DeadlineExceeded but Retry should return actualError.
			return serviceerror.NewDeadlineExceeded("deadline exceeded")
		}
		return actualError
	}
	err = Retry(context.Background(), op, policy, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, actualError)
}
