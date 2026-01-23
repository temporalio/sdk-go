// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build windows

package sysinfo

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type memoryStatusEx struct {
	cbSize                  uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

func VirtualMemory() (*VirtualMemoryStat, error) {
	return VirtualMemoryWithContext(context.Background())
}

func VirtualMemoryWithContext(ctx context.Context) (*VirtualMemoryStat, error) {
	var memInfo memoryStatusEx
	memInfo.cbSize = uint32(unsafe.Sizeof(memInfo))
	mem, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&memInfo)))
	if mem == 0 {
		return nil, err
	}

	ret := &VirtualMemoryStat{
		Total:       memInfo.ullTotalPhys,
		Available:   memInfo.ullAvailPhys,
		Free:        memInfo.ullAvailPhys,
		UsedPercent: float64(memInfo.dwMemoryLoad),
	}

	ret.Used = ret.Total - ret.Available
	return ret, nil
}
