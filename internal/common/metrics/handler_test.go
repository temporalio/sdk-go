package metrics_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/common/metrics"
)

func TestReplayAwareHandler(t *testing.T) {
	var replaying bool
	capture := metrics.NewCapturingHandler()
	handler := metrics.NewReplayAwareHandler(&replaying, capture)

	// As replaying
	replaying = true
	handler.Counter("counter1").Inc(1)
	handler.Gauge("gauge1").Update(2.0)
	handler.Timer("timer1").Record(3 * time.Second)
	require.Len(t, capture.Counters(), 1)
	require.Equal(t, int64(0), capture.Counters()[0].Value())
	require.Len(t, capture.Gauges(), 1)
	require.Equal(t, 0.0, capture.Gauges()[0].Value())
	require.Len(t, capture.Timers(), 1)
	require.Equal(t, 0*time.Second, capture.Timers()[0].Value())

	// As not replaying
	replaying = false
	handler.Counter("counter1").Inc(4)
	handler.Gauge("gauge1").Update(5.0)
	handler.Timer("timer1").Record(6 * time.Second)
	require.Len(t, capture.Counters(), 1)
	require.Equal(t, int64(4), capture.Counters()[0].Value())
	require.Len(t, capture.Gauges(), 1)
	require.Equal(t, 5.0, capture.Gauges()[0].Value())
	require.Len(t, capture.Timers(), 1)
	require.Equal(t, 6*time.Second, capture.Timers()[0].Value())
}
