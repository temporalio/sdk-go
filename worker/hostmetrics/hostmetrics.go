// Package hostmetrics provides host-level CPU and memory metrics collection
// for worker heartbeats. It supports Linux, macOS, and Windows, with
// cgroup metrics for containerized environments.
package hostmetrics

import (
	"context"
	"runtime"
	"sync"
	"time"

	"go.temporal.io/sdk/internal/sysinfo"
	"go.temporal.io/sdk/log"
)

// PSUtilSystemInfoSupplier implements worker.TunerHostMetricsProvider for system metrics.
type PSUtilSystemInfoSupplier struct {
	mu                        sync.Mutex
	lastRefresh               time.Time
	lastMemStat               *sysinfo.VirtualMemoryStat
	lastCpuUsage              float64
	cGroupInfo                cGroupInfo
	stopTryingToGetCGroupInfo bool
	logger                    log.Logger
}

// NewPSUtilSystemInfoSupplier creates a new PSUtilSystemInfoSupplier.
func NewPSUtilSystemInfoSupplier(logger log.Logger) *PSUtilSystemInfoSupplier {
	return &PSUtilSystemInfoSupplier{
		logger:     logger,
		cGroupInfo: newCGroupInfo(),
	}
}

// GetCpuUsage returns the current host CPU usage as a fraction (0.0-1.0).
// In containerized environments, it prefers cgroup metrics if available.
func (p *PSUtilSystemInfoSupplier) GetCpuUsage() (float64, error) {
	return p.GetCpuUsageWithLogger(p.logger)
}

// GetCpuUsageWithLogger is like GetCpuUsage but uses the provided logger for warnings.
func (p *PSUtilSystemInfoSupplier) GetCpuUsageWithLogger(logger log.Logger) (float64, error) {
	if err := p.maybeRefresh(logger); err != nil {
		return 0, err
	}
	// Prefer cgroup metrics in containerized environments
	if p.cGroupInfo != nil {
		if cgroupCPU := p.cGroupInfo.GetLastCPUUsage(); cgroupCPU != 0 {
			return cgroupCPU, nil
		}
	}
	return p.lastCpuUsage / 100, nil
}

// GetMemoryUsage returns the current host memory usage as a fraction (0.0-1.0).
// In containerized environments, it prefers cgroup metrics if available.
func (p *PSUtilSystemInfoSupplier) GetMemoryUsage() (float64, error) {
	return p.GetMemoryUsageWithLogger(p.logger)
}

// GetMemoryUsageWithLogger is like GetMemoryUsage but uses the provided logger for warnings.
func (p *PSUtilSystemInfoSupplier) GetMemoryUsageWithLogger(logger log.Logger) (float64, error) {
	if err := p.maybeRefresh(logger); err != nil {
		return 0, err
	}
	if p.cGroupInfo != nil {
		if cgroupMem := p.cGroupInfo.GetLastMemUsage(); cgroupMem != 0 {
			return cgroupMem, nil
		}
	}
	return p.lastMemStat.UsedPercent / 100, nil
}

func (p *PSUtilSystemInfoSupplier) maybeRefresh(logger log.Logger) error {
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Double check refresh is still needed
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}

	ctx, cancelFn := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelFn()
	memStat, err := sysinfo.VirtualMemoryWithContext(ctx)
	if err != nil {
		return err
	}
	cpuUsage, err := sysinfo.PercentWithContext(ctx, 0, false)
	if err != nil {
		return err
	}

	p.lastMemStat = memStat
	p.lastCpuUsage = cpuUsage[0]

	// Try cgroup metrics on Linux for containerized environments
	if runtime.GOOS == "linux" && !p.stopTryingToGetCGroupInfo && p.cGroupInfo != nil {
		continueUpdates, err := p.cGroupInfo.Update()
		if err != nil && logger != nil {
			logger.Warn("Failed to get cgroup stats", "error", err)
		}
		p.stopTryingToGetCGroupInfo = !continueUpdates
	}

	p.lastRefresh = time.Now()
	return nil
}

type cGroupInfo interface {
	// Update requests an update of the cgroup stats. Returns true if cgroup stats
	// should continue to be updated, false if not in a cgroup or error is unrecoverable.
	Update() (bool, error)
	// GetLastMemUsage returns last known memory usage as a fraction of cgroup limit.
	// Returns 0 if not in a cgroup or limit is not set.
	GetLastMemUsage() float64
	// GetLastCPUUsage returns last known CPU usage as a fraction of cgroup limit.
	// Returns 0 if not in a cgroup or limit is not set.
	GetLastCPUUsage() float64
}
