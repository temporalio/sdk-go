package hostmetrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPSUtilSystemInfoSupplier_GetCpuUsage(t *testing.T) {
	p := NewPSUtilSystemInfoSupplier(nil)

	cpu, err := p.GetCpuUsage()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cpu, 0.0)
	assert.LessOrEqual(t, cpu, 1.0)
}

func TestPSUtilSystemInfoSupplier_GetMemoryUsage(t *testing.T) {
	p := NewPSUtilSystemInfoSupplier(nil)

	mem, err := p.GetMemoryUsage()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, mem, 0.0)
	assert.LessOrEqual(t, mem, 1.0)
}

func TestPSUtilSystemInfoSupplier_RateLimiting(t *testing.T) {
	p := NewPSUtilSystemInfoSupplier(nil)

	// First call should refresh
	_, err := p.GetCpuUsage()
	require.NoError(t, err)
	firstRefresh := p.lastRefresh

	// Immediate second call should use cached value
	_, err = p.GetCpuUsage()
	require.NoError(t, err)
	assert.Equal(t, firstRefresh, p.lastRefresh)

	// Wait past the refresh interval
	time.Sleep(150 * time.Millisecond)

	// Third call should refresh
	_, err = p.GetCpuUsage()
	require.NoError(t, err)
	assert.NotEqual(t, firstRefresh, p.lastRefresh)
}
