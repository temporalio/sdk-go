package sysinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/worker"
)

func TestGetMemoryCpuUsage(t *testing.T) {
	supplier := SysInfoProvider()
	ctx := &worker.SystemInfoContext{Logger: log.NewNopLogger()}

	usage, err := supplier.GetMemoryUsage(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, usage, 0.0)
	assert.LessOrEqual(t, usage, 1.0)

	usage, err = supplier.GetCpuUsage(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, usage, 0.0)
	assert.LessOrEqual(t, usage, 1.0)
}

func TestMaybeRefreshRateLimiting(t *testing.T) {
	supplier := SysInfoProvider().(*psUtilSystemInfoSupplier)
	ctx := &worker.SystemInfoContext{Logger: log.NewNopLogger()}

	// First call should refresh
	firstUsage, err := supplier.GetMemoryUsage(ctx)
	require.NoError(t, err)
	firstRefresh := supplier.lastRefresh.Load()

	// Immediate second call should not refresh (rate limited)
	secondUsage, err := supplier.GetMemoryUsage(ctx)
	require.NoError(t, err)
	assert.Equal(t, firstRefresh, supplier.lastRefresh.Load())

	assert.Equal(t, firstUsage, secondUsage)
}

func TestNewResourceBasedTuner(t *testing.T) {
	tuner, err := NewResourceBasedTuner(worker.ResourceBasedTunerOptions{
		TargetMem: 0.8,
		TargetCpu: 0.9,
	})
	require.NoError(t, err)
	require.NotNil(t, tuner)
}
