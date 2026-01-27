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
	InterruptCount uint64
}

const (
	ClocksPerSec = 10000000.0

	// systemProcessorPerformanceInformationClass information class to query with NTQuerySystemInformation
	// https://processhacker.sourceforge.io/doc/ntexapi_8h.html#ad5d815b48e8f4da1ef2eb7a2f18a54e0
	win32_SystemProcessorPerformanceInformationClass = 8

	// size of systemProcessorPerformanceInfoSize in memory
	win32_SystemProcessorPerformanceInfoSize = uint32(unsafe.Sizeof(win32_SystemProcessorPerformanceInformation{}))
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

func TimesWithContext(_ context.Context, percpu bool) ([]TimesStat, error) {
	if percpu {
		return perCPUTimes()
	}

	var ret []TimesStat
	var lpIdleTime fileTime
	var lpKernelTime fileTime
	var lpUserTime fileTime
	r, _, err := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&lpIdleTime)),
		uintptr(unsafe.Pointer(&lpKernelTime)),
		uintptr(unsafe.Pointer(&lpUserTime)))
	if r == 0 {
		return nil, err
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

// makes call to Windows API function to retrieve performance information for each core
func perfInfo() ([]win32_SystemProcessorPerformanceInformation, error) {
	// Make maxResults large for safety.
	// We can't invoke the api call with a results array that's too small.
	// If we have more than 2056 cores on a single host, then it's probably the future.
	maxBuffer := 2056
	// buffer for results from the windows proc
	resultBuffer := make([]win32_SystemProcessorPerformanceInformation, maxBuffer)
	// size of the buffer in memory
	bufferSize := uintptr(win32_SystemProcessorPerformanceInfoSize) * uintptr(maxBuffer)
	// size of the returned response
	var retSize uint32

	// Invoke windows api proc.
	// The returned err from the windows dll proc will always be non-nil even when successful.
	// See https://godoc.org/golang.org/x/sys/windows#LazyProc.Call for more information
	retCode, _, err := procNtQuerySystemInformation.Call(
		win32_SystemProcessorPerformanceInformationClass, // System Information Class -> SystemProcessorPerformanceInformation
		uintptr(unsafe.Pointer(&resultBuffer[0])),        // pointer to first element in result buffer
		bufferSize,                        // size of the buffer in memory
		uintptr(unsafe.Pointer(&retSize)), // pointer to the size of the returned results the windows proc will set this
	)

	// check return code for errors
	if retCode != 0 {
		return nil, fmt.Errorf("call to NtQuerySystemInformation returned %d. err: %s", retCode, err.Error())
	}

	// calculate the number of returned elements based on the returned size
	numReturnedElements := retSize / win32_SystemProcessorPerformanceInfoSize

	// trim results to the number of returned elements
	resultBuffer = resultBuffer[:numReturnedElements]

	return resultBuffer, nil
}
