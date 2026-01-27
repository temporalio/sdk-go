// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build darwin

package sysinfo

import (
	"context"
	"errors"
	"fmt"
	"unsafe"
)

// mach/machine.h
const (
	cpuStateUser   = 0
	cpuStateSystem = 1
	cpuStateIdle   = 2
	cpuStateNice   = 3
	cpuStateMax    = 4
)

type hostCpuLoadInfoData struct {
	cpuTicks [cpuStateMax]uint32
}

var ClocksPerSec = float64(100)

func Times(percpu bool) ([]TimesStat, error) {
	return TimesWithContext(context.Background(), percpu)
}

func TimesWithContext(_ context.Context, percpu bool) ([]TimesStat, error) {
	sys, err := newSystemLib()
	if err != nil {
		return nil, err
	}
	defer sys.close()

	if percpu {
		return perCPUTimes(sys)
	}
	return allCPUTimes(sys)
}

func perCPUTimes(sys *systemLib) ([]TimesStat, error) {
	var count, ncpu uint32
	var cpuload *hostCpuLoadInfoData

	status := sys.hostProcessorInfo(sys.machHostSelf(), processorCpuLoadInfo,
		&ncpu, uintptr(unsafe.Pointer(&cpuload)), &count)

	if status != kernSuccess {
		return nil, fmt.Errorf("host_processor_info error=%d", status)
	}

	if cpuload == nil {
		return nil, errors.New("host_processor_info returned nil cpuload")
	}

	defer sys.vmDeallocate(sys.machTaskSelf(), uintptr(unsafe.Pointer(cpuload)), uintptr(ncpu))

	ret := []TimesStat{}
	loads := unsafe.Slice(cpuload, ncpu)

	for i := 0; i < int(ncpu); i++ {
		c := TimesStat{
			CPU:    fmt.Sprintf("cpu%d", i),
			User:   float64(loads[i].cpuTicks[cpuStateUser]) / ClocksPerSec,
			System: float64(loads[i].cpuTicks[cpuStateSystem]) / ClocksPerSec,
			Nice:   float64(loads[i].cpuTicks[cpuStateNice]) / ClocksPerSec,
			Idle:   float64(loads[i].cpuTicks[cpuStateIdle]) / ClocksPerSec,
		}
		ret = append(ret, c)
	}

	return ret, nil
}

func allCPUTimes(sys *systemLib) ([]TimesStat, error) {
	var cpuload hostCpuLoadInfoData
	count := uint32(cpuStateMax)

	status := sys.hostStatistics(sys.machHostSelf(), hostCpuLoadInfo,
		uintptr(unsafe.Pointer(&cpuload)), &count)

	if status != kernSuccess {
		return nil, fmt.Errorf("host_statistics error=%d", status)
	}

	c := TimesStat{
		CPU:    "cpu-total",
		User:   float64(cpuload.cpuTicks[cpuStateUser]) / ClocksPerSec,
		System: float64(cpuload.cpuTicks[cpuStateSystem]) / ClocksPerSec,
		Nice:   float64(cpuload.cpuTicks[cpuStateNice]) / ClocksPerSec,
		Idle:   float64(cpuload.cpuTicks[cpuStateIdle]) / ClocksPerSec,
	}

	return []TimesStat{c}, nil
}
