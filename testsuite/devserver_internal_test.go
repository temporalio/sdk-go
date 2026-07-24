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
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
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
	assert.Less(t, time.Since(startTime), time.Second)
}
