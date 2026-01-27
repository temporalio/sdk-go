// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build darwin

package sysinfo

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

type vmStatisticsData struct {
	freeCount     uint32
	activeCount   uint32
	inactiveCount uint32
	wireCount     uint32
	_             [44]byte
}

func getHwMemsize() (uint64, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, err
	}
	return total, nil
}

func VirtualMemory() (*VirtualMemoryStat, error) {
	return VirtualMemoryWithContext(context.Background())
}

func VirtualMemoryWithContext(_ context.Context) (*VirtualMemoryStat, error) {
	sys, err := newSystemLib()
	if err != nil {
		return nil, err
	}
	defer sys.close()

	count := uint32(hostVMInfoCount)
	var vmstat vmStatisticsData

	status := sys.hostStatistics(sys.machHostSelf(), hostVMInfo,
		uintptr(unsafe.Pointer(&vmstat)), &count)

	if status != kernSuccess {
		return nil, fmt.Errorf("host_statistics error=%d", status)
	}

	pageSizeAddr, _ := sys.Dlsym("vm_kernel_page_size")
	pageSize := **(**uint64)(unsafe.Pointer(&pageSizeAddr))
	total, err := getHwMemsize()
	if err != nil {
		return nil, err
	}
	totalCount := uint32(total / pageSize)

	availableCount := vmstat.inactiveCount + vmstat.freeCount
	usedPercent := 100 * float64(totalCount-availableCount) / float64(totalCount)

	usedCount := totalCount - availableCount

	return &VirtualMemoryStat{
		Total:       total,
		Available:   pageSize * uint64(availableCount),
		Used:        pageSize * uint64(usedCount),
		UsedPercent: usedPercent,
		Free:        pageSize * uint64(vmstat.freeCount),
		Active:      pageSize * uint64(vmstat.activeCount),
		Inactive:    pageSize * uint64(vmstat.inactiveCount),
		Wired:       pageSize * uint64(vmstat.wireCount),
	}, nil
}
