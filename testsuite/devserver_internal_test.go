package testsuite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/log"
)

func TestWaitServerReady_respectsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()

	hostPort, err := getFreeHostPort()
	require.NoError(t, err, "get free host port")

	startTime := time.Now()
	_, err = waitServerReady(ctx, client.Options{
		HostPort:  hostPort,
		Namespace: "default",
		Logger:    log.NewNopLogger(),
	})
	require.Error(t, err, "Dial should fail")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.WithinDuration(t,
		startTime.Add(time.Millisecond),
		time.Now(),
		10*time.Millisecond,
		// Even though the timeout is only a millisecond,
		// we'll allow for a slack of up to 10 milliseconds
		// to account for slow CI machines.
		// Keep this below the retry interval so an extra retry fails.
		// Increase only if CI scheduler jitter exceeds this allowance.
	)
}

func TestRetryFor_respectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	attempts := 0
	err := retryFor(ctx, 2, 100*time.Millisecond, func() error {
		attempts++
		return assert.AnError
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, attempts)
}
