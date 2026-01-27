//go:build linux

package hostmetrics

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const cgroupBasePath = "/sys/fs/cgroup"

func newCGroupInfo() cGroupInfo {
	return &cGroupInfoImpl{}
}

type cGroupInfoImpl struct {
	lastMemUsage  uint64
	lastMemLimit  uint64
	cgroupCpuCalc cgroupCpuCalc
}

func (p *cGroupInfoImpl) Update() (bool, error) {
	memUsage, memLimit, err := readMemoryStat()
	if errors.Is(err, fs.ErrNotExist) {
		// Stop updates if not in a container. No need to return the error and log it.
		return false, nil
	} else if err != nil {
		return true, err
	}

	// Only update if limit is set
	if memLimit != 0 {
		p.lastMemUsage = memUsage
		p.lastMemLimit = memLimit
	}

	cpuUsageUsec, err := readCPUUsage()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true, err
	}

	p.cgroupCpuCalc.updateCpuUsage(cpuUsageUsec)
	return true, nil
}

func (p *cGroupInfoImpl) GetLastMemUsage() float64 {
	if p.lastMemLimit != 0 {
		return float64(p.lastMemUsage) / float64(p.lastMemLimit)
	}
	return 0
}

func (p *cGroupInfoImpl) GetLastCPUUsage() float64 {
	return p.cgroupCpuCalc.lastCalculatedPercent
}

type cgroupCpuCalc struct {
	lastRefresh           time.Time
	lastCpuUsage          uint64
	lastCalculatedPercent float64
}

func (p *cgroupCpuCalc) updateCpuUsage(currentCpuUsageUsec uint64) {
	cpuQuota, cpuPeriod, err := readCpuMax()
	if err != nil {
		return // No CPU limit set or file doesn't exist
	}

	now := time.Now()
	if p.lastCpuUsage == 0 || p.lastRefresh.IsZero() {
		p.lastCpuUsage = currentCpuUsageUsec
		p.lastRefresh = now
		return
	}

	timeDelta := now.Sub(p.lastRefresh).Microseconds()
	cpuUsageDelta := float64(currentCpuUsageUsec - p.lastCpuUsage)

	if cpuQuota > 0 && timeDelta > 0 {
		p.lastCalculatedPercent = cpuUsageDelta * float64(cpuPeriod) / float64(cpuQuota*timeDelta)
	}

	// Update for next call
	p.lastCpuUsage = currentCpuUsageUsec
	p.lastRefresh = now
}

// readMemoryStat reads memory.current and memory.max from cgroup v2.
// Returns (usage, limit, error). Limit is 0 if set to "max" (unlimited).
func readMemoryStat() (uint64, uint64, error) {
	usage, err := readIntFromFile(filepath.Join(cgroupBasePath, "memory.current"), false)
	if err != nil {
		return 0, 0, err
	}

	limit, err := readIntFromFile(filepath.Join(cgroupBasePath, "memory.max"), true)
	if err != nil {
		return 0, 0, err
	}

	return usage, limit, nil
}

// readCPUUsage reads usage_usec from cpu.stat.
func readCPUUsage() (uint64, error) {
	data, err := os.ReadFile(filepath.Join(cgroupBasePath, "cpu.stat"))
	if err != nil {
		return 0, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "usage_usec ") {
			return strconv.ParseUint(strings.TrimPrefix(line, "usage_usec "), 10, 64)
		}
	}
	return 0, errors.New("usage_usec not found in cpu.stat")
}

// readCpuMax reads the cpu.max file to get the CPU quota and period.
func readCpuMax() (quota int64, period int64, err error) {
	data, err := os.ReadFile(filepath.Join(cgroupBasePath, "cpu.max"))
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid format in cpu.max")
	}

	if parts[0] == "max" {
		quota = 0
	} else {
		quota, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, err
		}
	}

	period, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}

	return quota, period, nil
}

// readIntFromFile reads a file containing a single uint64 value and
// can optionally detect if the file has "max" as its value, where
// it returns 0 as the value read.
func readIntFromFile(path string, canBeMax bool) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	if canBeMax && s == "max" {
		return 0, nil
	}
	return strconv.ParseUint(s, 10, 64)
}
