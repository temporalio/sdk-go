//go:build linux

package sysinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/cgroups/v3/cgroup2/stats"
)

func newCGroupInfo() cGroupInfo {
	return &cGroupInfoImpl{}
}

type cGroupInfoImpl struct {
	lastCGroupMemStat *stats.MemoryStat
	cgroupCpuCalc     cgroupCpuCalc
}

func (p *cGroupInfoImpl) Update() (bool, error) {
	err := p.updateCGroupStats()
	// Stop updates if not in a container. No need to return the error and log it.
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return true, err
	}
	return true, nil
}

func (p *cGroupInfoImpl) GetLastMemUsage() float64 {
	if p.lastCGroupMemStat != nil {
		return float64(p.lastCGroupMemStat.Usage) / float64(p.lastCGroupMemStat.UsageLimit)
	}
	return 0
}

func (p *cGroupInfoImpl) GetLastCPUUsage() float64 {
	return p.cgroupCpuCalc.lastCalculatedPercent
}

func (p *cGroupInfoImpl) updateCGroupStats() error {
	control, err := cgroup2.Load("/")
	if err != nil {
		return fmt.Errorf("failed to get cgroup mem stats %v", err)
	}
	metrics, err := control.Stat()
	if err != nil {
		return fmt.Errorf("failed to get cgroup mem stats %v", err)
	}
	// Only update if a limit has been set
	if metrics.Memory.UsageLimit != 0 {
		p.lastCGroupMemStat = metrics.Memory
	}

	err = p.cgroupCpuCalc.updateCpuUsage(metrics)
	if err != nil {
		return fmt.Errorf("failed to get cgroup cpu usage %v", err)
	}
	return nil
}

type cgroupCpuCalc struct {
	lastRefresh           time.Time
	lastCpuUsage          uint64
	lastCalculatedPercent float64
}

func (p *cgroupCpuCalc) updateCpuUsage(metrics *stats.Metrics) error {
	// Read CPU quota and period from cpu.max
	cpuQuota, cpuPeriod, err := readCpuMax("/sys/fs/cgroup/cpu.max")
	// We might simply be in a container with an unset cpu.max in which case we don't want to error
	if err == nil {
		// CPU usage calculation based on delta
		currentCpuUsage := metrics.CPU.UsageUsec
		now := time.Now()

		if p.lastCpuUsage == 0 || p.lastRefresh.IsZero() {
			p.lastCpuUsage = currentCpuUsage
			p.lastRefresh = now
			return nil
		}

		// Time passed between this and last check
		timeDelta := now.Sub(p.lastRefresh).Microseconds() // Convert to microseconds

		// Calculate CPU usage percentage based on the delta
		cpuUsageDelta := float64(currentCpuUsage - p.lastCpuUsage)

		if cpuQuota > 0 {
			p.lastCalculatedPercent = cpuUsageDelta * float64(cpuPeriod) / float64(cpuQuota*timeDelta)
		}

		// Update for next call
		p.lastCpuUsage = currentCpuUsage
		p.lastRefresh = now
	}

	return nil
}

// readCpuMax reads the cpu.max file to get the CPU quota and period
func readCpuMax(path string) (quota int64, period int64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid format in cpu.max")
	}

	// Parse the quota (first value)
	if parts[0] == "max" {
		quota = 0 // Unlimited quota
	} else {
		quota, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, err
		}
	}

	// Parse the period (second value)
	period, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}

	return quota, period, nil
}
