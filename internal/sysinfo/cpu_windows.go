// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build windows

package sysinfo

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION
// https://docs.microsoft.com/en-us/windows/desktop/api/winternl/nf-winternl-ntquerysysteminformation#system_processor_performance_information
type win32_SystemProcessorPerformanceInformation struct {
	IdleTime       int64
	KernelTime     int64
	UserTime       int64
	DpcTime        int64
	InterruptTime  int64
	InterruptCount uint32
}

const (
	// ClocksPerSec is 100ns units (10 million per second)
	ClocksPerSec = 10000000.0

	win32_SystemProcessorPerformanceInformationClass = 8
	win32_SystemProcessorPerformanceInfoSize         = uint32(unsafe.Sizeof(win32_SystemProcessorPerformanceInformation{}))
)

var (
	modkernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	modNt                        = windows.NewLazySystemDLL("ntdll.dll")
	procGetSystemTimes           = modkernel32.NewProc("GetSystemTimes")
	procNtQuerySystemInformation = modNt.NewProc("NtQuerySystemInformation")
)

type fileTime struct {
	dwLowDateTime  uint32
	dwHighDateTime uint32
}

func Times(percpu bool) ([]TimesStat, error) {
	return TimesWithContext(context.Background(), percpu)
}

func TimesWithContext(ctx context.Context, percpu bool) ([]TimesStat, error) {
	if percpu {
		return perCPUTimes()
	}

	var ret []TimesStat
	var lpIdleTime fileTime
	var lpKernelTime fileTime
	var lpUserTime fileTime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&lpIdleTime)),
		uintptr(unsafe.Pointer(&lpKernelTime)),
		uintptr(unsafe.Pointer(&lpUserTime)))
	if r == 0 {
		return ret, windows.GetLastError()
	}

	LOT := float64(0.0000001)
	HIT := (LOT * 4294967296.0)
	idle := ((HIT * float64(lpIdleTime.dwHighDateTime)) + (LOT * float64(lpIdleTime.dwLowDateTime)))
	user := ((HIT * float64(lpUserTime.dwHighDateTime)) + (LOT * float64(lpUserTime.dwLowDateTime)))
	kernel := ((HIT * float64(lpKernelTime.dwHighDateTime)) + (LOT * float64(lpKernelTime.dwLowDateTime)))
	system := (kernel - idle)

	ret = append(ret, TimesStat{
		CPU:    "cpu-total",
		Idle:   idle,
		User:   user,
		System: system,
	})
	return ret, nil
}

func perCPUTimes() ([]TimesStat, error) {
	var ret []TimesStat
	stats, err := perfInfo()
	if err != nil {
		return nil, err
	}
	for core, v := range stats {
		c := TimesStat{
			CPU:    fmt.Sprintf("cpu%d", core),
			User:   float64(v.UserTime) / ClocksPerSec,
			System: float64(v.KernelTime-v.IdleTime) / ClocksPerSec,
			Idle:   float64(v.IdleTime) / ClocksPerSec,
			Irq:    float64(v.InterruptTime) / ClocksPerSec,
		}
		ret = append(ret, c)
	}
	return ret, nil
}

func perfInfo() ([]win32_SystemProcessorPerformanceInformation, error) {
	maxBuffer := 2056
	resultBuffer := make([]win32_SystemProcessorPerformanceInformation, maxBuffer)
	bufferSize := uintptr(win32_SystemProcessorPerformanceInfoSize) * uintptr(maxBuffer)
	var retSize uint32

	retCode, _, err := procNtQuerySystemInformation.Call(
		win32_SystemProcessorPerformanceInformationClass,
		uintptr(unsafe.Pointer(&resultBuffer[0])),
		bufferSize,
		uintptr(unsafe.Pointer(&retSize)),
	)

	if retCode != 0 {
		return nil, fmt.Errorf("call to NtQuerySystemInformation returned %d. err: %s", retCode, err.Error())
	}

	numReturnedElements := retSize / win32_SystemProcessorPerformanceInfoSize
	resultBuffer = resultBuffer[:numReturnedElements]

	return resultBuffer, nil
}
