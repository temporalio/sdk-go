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
	ctx := &worker.SysInfoContext{Logger: log.NewNopLogger()}

	usage, err := supplier.MemoryUsage(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, usage, 0.0)
	assert.LessOrEqual(t, usage, 1.0)

	usage, err = supplier.CpuUsage(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, usage, 0.0)
	assert.LessOrEqual(t, usage, 1.0)
}

func TestMaybeRefreshRateLimiting(t *testing.T) {
	supplier := SysInfoProvider().(*psUtilSystemInfoSupplier)
	ctx := &worker.SysInfoContext{Logger: log.NewNopLogger()}

	// First call should refresh
	firstUsage, err := supplier.MemoryUsage(ctx)
	require.NoError(t, err)
	firstRefresh := supplier.lastRefresh.Load()

	// Immediate second call should not refresh (rate limited)
	secondUsage, err := supplier.MemoryUsage(ctx)
	require.NoError(t, err)
	assert.Equal(t, firstRefresh, supplier.lastRefresh.Load())

	assert.Equal(t, firstUsage, secondUsage)
}
