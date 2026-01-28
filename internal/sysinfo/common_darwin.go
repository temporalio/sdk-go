// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build darwin

package sysinfo

import (
	"github.com/ebitengine/purego"
)

const (
	systemLibPath = "/usr/lib/libSystem.B.dylib"

	// mach/processor_info.h
	processorCpuLoadInfo = 2

	// mach/host_info.h
	hostVMInfo      = 2
	hostCpuLoadInfo = 3
	hostVMInfoCount = 0xf

	// Status codes
	kernSuccess = 0
)

type systemLib struct {
	handle uintptr

	hostProcessorInfo func(host uint32, flavor int32, outProcessorCount *uint32,
		outProcessorInfo uintptr, outProcessorInfoCnt *uint32) int32
	hostStatistics func(host uint32, flavor int32, hostInfoOut uintptr, hostInfoOutCnt *uint32) int32
	machHostSelf   func() uint32
	machTaskSelf   func() uint32
	vmDeallocate   func(targetTask uint32, vmAddress, vmSize uintptr) int32
}

func newSystemLib() (*systemLib, error) {
	handle, err := purego.Dlopen(systemLibPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}

	sys := &systemLib{handle: handle}

	purego.RegisterLibFunc(&sys.hostProcessorInfo, handle, "host_processor_info")
	purego.RegisterLibFunc(&sys.hostStatistics, handle, "host_statistics")
	purego.RegisterLibFunc(&sys.machHostSelf, handle, "mach_host_self")
	purego.RegisterLibFunc(&sys.machTaskSelf, handle, "mach_task_self")
	purego.RegisterLibFunc(&sys.vmDeallocate, handle, "vm_deallocate")

	return sys, nil
}

func (s *systemLib) Dlsym(symbol string) (uintptr, error) {
	return purego.Dlsym(s.handle, symbol)
}

func (s *systemLib) close() {
	purego.Dlclose(s.handle)
}
