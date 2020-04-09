package backoff

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type someError struct{}

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

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*5)
	defer cancel()

	err := Retry(ctx, op, policy, nil)
	assert.Error(t, err)
	assert.True(t, i >= 2) // verify that we did retry
}

func TestRetryFailed(t *testing.T) {
	t.Parallel()
	i := 0
	op := func() error {
		i++

		if i == 7 {
			return nil
		}

		return &someError{}
	}

	policy := NewExponentialRetryPolicy(1 * time.Millisecond)
	policy.SetMaximumInterval(5 * time.Millisecond)
	policy.SetMaximumAttempts(5)

	err := Retry(context.Background(), op, policy, nil)
	assert.Error(t, err)
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
	retrier.Failed()
	a.Equal(int64(1), retrier.failureCount)
	retrier.Succeeded()
	a.Equal(int64(0), retrier.failureCount)
	sleepDuration := retrier.throttleInternal()
	a.Equal(done, sleepDuration)

	// Multiple count check.
	retrier.Failed()
	retrier.Failed()
	a.Equal(int64(2), retrier.failureCount)
	// Verify valid sleep times.
	ch := make(chan time.Duration, 3)
	go func() {
		for i := 0; i < 3; i++ {
			ch <- retrier.throttleInternal()
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
			ch <- retrier.throttleInternal()
		}
	}()
	for i := 0; i < 3; i++ {
		val := <-ch
		t.Logf("Duration: %d\n", val)
		a.Equal(done, val)
	}
}

func (e *someError) Error() string {
	return "Some Error"
}
