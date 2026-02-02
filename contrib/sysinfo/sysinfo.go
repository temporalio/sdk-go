package sysinfo

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"go.temporal.io/sdk/worker"
)

var (
	sysInfoOnce     sync.Once
	sysInfoInstance *psUtilSystemInfoSupplier
)

// SysInfoProvider returns a shared SystemInfoSupplier using gopsutil.
// Supports cgroup metrics in containerized Linux environments.
func SysInfoProvider() worker.SystemInfoSupplier {
	sysInfoOnce.Do(func() {
		sysInfoInstance = &psUtilSystemInfoSupplier{
			cGroupInfo: newCGroupInfo(),
		}
	})
	return sysInfoInstance
}

// NewResourceBasedTuner creates a resource-based tuner with gopsutil-based system info.
func NewResourceBasedTuner(opts worker.ResourceBasedTunerOptions) (worker.WorkerTuner, error) {
	opts.InfoSupplier = SysInfoProvider()
	return worker.NewResourceBasedTuner(opts)
}

type psUtilSystemInfoSupplier struct {
	mu          sync.Mutex
	lastRefresh time.Time

	lastMemStat  *mem.VirtualMemoryStat
	lastCpuUsage float64

	stopTryingToGetCGroupInfo bool
	cGroupInfo                cGroupInfo
}

type cGroupInfo interface {
	// Update requests an update of the cgroup stats. This is a no-op if not in a cgroup. Returns
	// true if cgroup stats should continue to be updated, false if not in a cgroup or the returned
	// error is considered unrecoverable.
	Update() (bool, error)
	// GetLastMemUsage returns last known memory usage as a fraction of the cgroup limit. 0 if not
	// in a cgroup or limit is not set.
	GetLastMemUsage() float64
	// GetLastCPUUsage returns last known CPU usage as a fraction of the cgroup limit. 0 if not in a
	// cgroup or limit is not set.
	GetLastCPUUsage() float64
}

func (p *psUtilSystemInfoSupplier) GetMemoryUsage(infoContext *worker.SystemInfoContext) (float64, error) {
	if err := p.maybeRefresh(infoContext); err != nil {
		return 0, err
	}
	lastCGroupMem := p.cGroupInfo.GetLastMemUsage()
	if lastCGroupMem != 0 {
		return lastCGroupMem, nil
	}
	return p.lastMemStat.UsedPercent / 100, nil
}

func (p *psUtilSystemInfoSupplier) GetCpuUsage(infoContext *worker.SystemInfoContext) (float64, error) {
	if err := p.maybeRefresh(infoContext); err != nil {
		return 0, err
	}

	lastCGroupCPU := p.cGroupInfo.GetLastCPUUsage()
	if lastCGroupCPU != 0 {
		return lastCGroupCPU, nil
	}
	return p.lastCpuUsage / 100, nil
}

func (p *psUtilSystemInfoSupplier) maybeRefresh(infoContext *worker.SystemInfoContext) error {
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
	memStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return err
	}
	cpuUsage, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return err
	}

	p.lastMemStat = memStat
	p.lastCpuUsage = cpuUsage[0]

	if runtime.GOOS == "linux" && !p.stopTryingToGetCGroupInfo {
		continueUpdates, err := p.cGroupInfo.Update()
		if err != nil {
			infoContext.Logger.Warn("Failed to get cgroup stats", "error", err)
		}
		p.stopTryingToGetCGroupInfo = !continueUpdates
	}

	p.lastRefresh = time.Now()
	return nil
}
