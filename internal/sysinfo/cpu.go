// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
package sysinfo

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"
)

// TimesStat contains the amounts of time the CPU has spent performing different
// kinds of work. Time units are in seconds. It is based on linux /proc/stat file.
type TimesStat struct {
	CPU       string  `json:"cpu"`
	User      float64 `json:"user"`
	System    float64 `json:"system"`
	Idle      float64 `json:"idle"`
	Nice      float64 `json:"nice"`
	Iowait    float64 `json:"iowait"`
	Irq       float64 `json:"irq"`
	Softirq   float64 `json:"softirq"`
	Steal     float64 `json:"steal"`
	Guest     float64 `json:"guest"`
	GuestNice float64 `json:"guestNice"`
}

type lastPercent struct {
	sync.Mutex
	lastCPUTimes    []TimesStat
	lastPerCPUTimes []TimesStat
}

var lastCPUPercent lastPercent

func init() {
	lastCPUPercent.Lock()
	lastCPUPercent.lastCPUTimes, _ = Times(false)
	lastCPUPercent.lastPerCPUTimes, _ = Times(true)
	lastCPUPercent.Unlock()
}

func (c TimesStat) Total() float64 {
	total := c.User + c.System + c.Idle + c.Nice + c.Iowait + c.Irq +
		c.Softirq + c.Steal + c.Guest + c.GuestNice
	return total
}

func getAllBusy(t TimesStat) (float64, float64) {
	tot := t.Total()
	if runtime.GOOS == "linux" {
		tot -= t.Guest     // Linux 2.6.24+
		tot -= t.GuestNice // Linux 3.2.0+
	}
	busy := tot - t.Idle - t.Iowait
	return tot, busy
}

func calculateBusy(t1, t2 TimesStat) float64 {
	t1All, t1Busy := getAllBusy(t1)
	t2All, t2Busy := getAllBusy(t2)

	if t2Busy <= t1Busy {
		return 0
	}
	if t2All <= t1All {
		return 100
	}
	return math.Min(100, math.Max(0, (t2Busy-t1Busy)/(t2All-t1All)*100))
}

func calculateAllBusy(t1, t2 []TimesStat) ([]float64, error) {
	if len(t1) != len(t2) {
		return nil, fmt.Errorf(
			"received two CPU counts: %d != %d",
			len(t1), len(t2),
		)
	}

	ret := make([]float64, len(t1))
	for i, t := range t2 {
		ret[i] = calculateBusy(t1[i], t)
	}
	return ret, nil
}

// Percent calculates the percentage of cpu used either per CPU or combined.
// If an interval of 0 is given it will compare the current cpu times against the last call.
// Returns one value per cpu, or a single value if percpu is set to false.
func Percent(interval time.Duration, percpu bool) ([]float64, error) {
	return PercentWithContext(context.Background(), interval, percpu)
}

func PercentWithContext(ctx context.Context, interval time.Duration, percpu bool) ([]float64, error) {
	if interval <= 0 {
		return percentUsedFromLastCallWithContext(ctx, percpu)
	}

	// Get CPU usage at the start of the interval.
	cpuTimes1, err := TimesWithContext(ctx, percpu)
	if err != nil {
		return nil, err
	}

	if err := Sleep(ctx, interval); err != nil {
		return nil, err
	}

	// And at the end of the interval.
	cpuTimes2, err := TimesWithContext(ctx, percpu)
	if err != nil {
		return nil, err
	}

	return calculateAllBusy(cpuTimes1, cpuTimes2)
}

func percentUsedFromLastCallWithContext(ctx context.Context, percpu bool) ([]float64, error) {
	cpuTimes, err := TimesWithContext(ctx, percpu)
	if err != nil {
		return nil, err
	}
	lastCPUPercent.Lock()
	defer lastCPUPercent.Unlock()
	var lastTimes []TimesStat
	if percpu {
		lastTimes = lastCPUPercent.lastPerCPUTimes
		lastCPUPercent.lastPerCPUTimes = cpuTimes
	} else {
		lastTimes = lastCPUPercent.lastCPUTimes
		lastCPUPercent.lastCPUTimes = cpuTimes
	}

	if lastTimes == nil {
		return nil, fmt.Errorf("error getting times for cpu percent. lastTimes was nil")
	}
	return calculateAllBusy(lastTimes, cpuTimes)
}
